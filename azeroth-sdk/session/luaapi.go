package session

// The Lua API agents script against. One namespace per domain:
//
//	me        your character (queries)          units    creatures and players around you
//	objects   game objects (chests, doors...)   world    terrain: height, paths, line of sight
//	move      walking and facing                combat   targeting, melee, combat log, meters
//	spells    casting and spell data            items    bags, gear, using and looting
//	vendor    buying, selling, repairing        trainer  learning spells
//	quests    quest log, accepting, turning in  death    releasing, corpse runs, spirit healers
//	chat      talking                           group    parties
//	trade     trading with players              talents  talent points
//	client    raw protocol (outside the stable API)
//
// Conventions (docs/sdk.md): queries are nouns and return one value or one table (nil when unknown); actions
// are verbs and return a request (script/requests.go) whose op is the function's name; every value is one of the
// structs in package types (built in model.go), so a creature or an item has the same fields everywhere. Domain files register their own namespaces (items.go, trade.go, ...);
// this file has the core ones. Every registration carries its signature and description: help() and the
// generated reference come from them.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// UnitInfo is a creature or player as the client tracks it.
type UnitInfo struct {
	GUID      uint64
	Entry     uint32
	Health    uint32
	MaxHealth uint32
	Level     uint32
	PosX      float32
	PosY      float32
	PosZ      float32
	IsAlive   bool
	IsPlayer  bool
	Distance  float32
	Name      string
	Faction   uint32
	NPCFlags  uint32
	Flags     uint32
	DynFlags  uint32
	Lootable  bool
	Target    uint64 // who this unit is targeting (0 = none)
}

// gs: a GUID for request args (a string; nil for none)
func gs(g uint64) any { return types.GUID(g) }

var goTypes = map[uint32]string{0: "door", 1: "button", 2: "questgiver", 3: "chest", 5: "generic", 6: "trap", 8: "spell_focus",
	10: "goober", 11: "elevator", 15: "ship", 17: "fishing_bobber", 19: "mailbox", 22: "portal", 23: "meeting_stone", 25: "fishing_pool"}

// game object names by entry, from SMSG_GAMEOBJECT_QUERY_RESPONSE
var goNames = struct {
	m     map[uint32]string
	t     map[uint32]*goTemplate
	asked map[uint32]time.Time
}{m: map[uint32]string{}, t: map[uint32]*goTemplate{}, asked: map[uint32]time.Time{}}

type goTemplate struct {
	typ  uint32
	data [24]uint32 // per-type fields (lock, loot, quest...), see GameObjectTemplate
}

const (
	cmsgGameObjectQuery     = 0x05E
	smsgGameObjectQueryResp = 0x05F
)

func (b *Bot) object(o *client.WorldObject) types.Object {
	x, y, z := o.InterpolatedPosition()
	mx, my, mz := b.myPos()
	entry := o.Entry
	if entry == 0 {
		entry = uint32(o.GUID>>24) & 0xFFFFFF
	}
	creatures.Lock()
	goName, known := goNames.m[entry]
	tmpl := goNames.t[entry]
	if !known && time.Since(goNames.asked[entry]) > 5*time.Second {
		goNames.asked[entry] = time.Now()
		body := binary.LittleEndian.AppendUint32(nil, entry)
		go b.world.SendPacketRaw(cmsgGameObjectQuery, binary.LittleEndian.AppendUint64(body, o.GUID))
	}
	creatures.Unlock()
	ob := types.Object{GUID: types.GUID(o.GUID), Entry: entry, Name: goName, Type: name(goTypes, (o.Value(fieldGOBytes1)>>8)&0xFF),
		Place: types.Place{X: round1(x), Y: round1(y), Z: round1(z), Distance: round1(float32(math.Sqrt(float64((x-mx)*(x-mx) + (y-my)*(y-my) + (z-mz)*(z-mz)))))}}
	m := b.me3()
	relate(m.x, m.y, m.z, m.o, &ob.Place)
	objectNeeds(b.world, o, &ob, tmpl)
	return ob
}

func (b *Bot) setupCoreAPI() {
	e, w := b.lua, b.world
	w.AddPacketHook(func(op uint16, data []byte) {
		if op != smsgGameObjectQueryResp || len(data) < 12 {
			return
		}
		r := bytes.NewReader(data)
		var entry, typ, display uint32
		binary.Read(r, binary.LittleEndian, &entry)
		if entry&0x80000000 != 0 {
			return
		}
		binary.Read(r, binary.LittleEndian, &typ)
		binary.Read(r, binary.LittleEndian, &display)
		name := readCString(r)
		t := &goTemplate{typ: typ}
		r.Seek(3, 1)   // name2-4
		readCString(r) // icon
		readCString(r) // cast bar caption
		readCString(r) // unk1
		binary.Read(r, binary.LittleEndian, &t.data)
		creatures.Lock()
		goNames.m[entry], goNames.t[entry] = name, t
		creatures.Unlock()
	})
	w.AddPacketHook(func(op uint16, data []byte) {
		if op == smsgQuestQueryResp {
			parseQuestQuery(data)
		}
	})

	// ---------- me ----------
	e.Func("me.name", "() → string", "your character's name", func() string { return b.config.CharacterName })
	e.Func("me.guid", "() → GUID", "your GUID", func() types.GUID { return types.GUID(w.CharGUID()) })
	e.Func("me.level", "() → number", "your level", b.GetLevel)
	e.Func("me.class", "() → string", "warrior, paladin, hunter, rogue, priest, death_knight, shaman, mage, warlock, druid", func() string { return name(classNames, b.GetClass()) })
	e.Func("me.race", "() → string", "human, dwarf, night_elf, gnome, draenei, orc, undead, tauren, troll, blood_elf", func() string { return name(raceNames, b.GetRace()) })
	e.Func("me.xp", "() → XP", "XP into this level, and the XP this level needs", func() types.XP {
		var x types.XP
		if o := w.GetObject(w.CharGUID()); o != nil {
			x.Current, x.Next = o.Value(0x027A), o.Value(0x027B)
		}
		return x
	})
	e.Func("me.health", "() → Resource", "your health", func() types.Resource {
		c, m := b.GetHealth()
		return types.Resource{Current: c, Max: m}
	})
	e.Func("me.power", "() → Resource", "your mana, rage, energy or runic power", func() types.Resource {
		c, m := b.GetPower()
		return types.Resource{Current: c, Max: m, Type: name(powerNames, b.GetPowerType())}
	})
	e.Func("me.position", "() → Position", "where you are and which way you face", func() types.Position {
		x, y, z, o, m := w.Position()
		return types.Position{X: round1(x), Y: round1(y), Z: round1(z), O: math.Round(float64(o)*1000) / 1000, Map: m}
	})
	e.Func("me.money", "() → copper", "your money", func() uint32 { return w.Money() })
	e.Func("me.alive", "() → bool", "false while dead or a ghost", b.IsAlive)
	e.Func("me.ghost", "() → bool", "released and walking back as a ghost", b.IsGhost)
	e.Func("me.in_combat", "() → bool", "the server's combat flag", b.InCombat)
	e.Func("me.moving", "() → bool", "walking somewhere (move.to)", b.IsMoving)
	e.Func("me.target", "() → GUID?", "your selected target, nil for none", func() types.GUID { return types.GUID(b.GetTargetGUID()) })
	e.Func("me.stance", "() → number", "warrior stance / druid form index (0 = none)", b.GetStance)
	e.Func("me.pet", "() → GUID?", "your pet, nil for none", func() types.GUID { return types.GUID(b.GetPetGUID()) })

	e.Func("me.status", "() → Status", "what you'd see on screen, in one call: you (health, power, auras, cast, breath), your target and every mob in your fight, the NPCs, objects, players and mobs around you, your group, quests, what to improve (gear, untrained spells, talents, bag upgrades), your last minute of fighting, your tasks. Each part has a detail call",
		func() types.Status { return b.snapshot() })

	e.Func("me.gear", "() → Gear", "your gear graded against your level: average item level, a 0-100 score, the weakest slots, empty and broken ones", b.gear)
	e.Func("me.spellbook", "() → Spellbook", "the class spells you could know at your level against the ones you do, and what's missing (learn them at your trainer)", b.spellbook)

	// ---------- units ----------
	dist := func(d float32) float32 {
		if d <= 0 {
			return 30
		}
		return d
	}
	e.Func("units.nearby", "([dist = 30]) → Creature[]", "creatures within dist yards (alive and dead), nearest first",
		func(d float32) []types.Creature {
			var us []UnitInfo
			for _, u := range b.GetNearbyUnits(dist(d)) {
				if u.Flags&0x2000000 == 0 { // UNIT_FLAG_NOT_SELECTABLE: invisible triggers (event and script helpers) a player never sees
					us = append(us, u)
				}
			}
			cs := b.creatures(us)
			b.annotateNearby(cs)
			return cs
		})
	e.Func("world.look", "([radius = 40]) → string", "a top-down text map of what's around you, north up: the ground (higher, lower, no floor), hostile creatures by letter with their aggro range shaded, other creatures, players and lootable corpses, then a line for each: level, distance, direction, o'clock, height above or below you, aggro radius and whether you're inside it, line of sight. wow look prints it",
		func(r float64) string {
			if r <= 0 {
				r = 40
			}
			return b.look(math.Min(r, 100))
		})
	e.Func("units.players", "([dist = 30]) → Player[]", "players within dist yards, nearest first", func(d float32) []types.Player {
		ps := b.players(b.GetNearbyPlayers(dist(d)))
		b.annotatePlayers(ps)
		return ps
	})
	e.Func("units.get", "(guid) → (Creature | Player)?", "one unit by GUID; nil if it's out of sight", func(g uint64) any {
		if u := b.GetUnitInfo(g); u != nil {
			r := b.newReach()
			if u.IsPlayer {
				p := b.player(*u)
				r.place(g, &p.Place, 1.5)
				return p
			}
			c := b.creature(*u)
			r.place(g, &c.Place, 1.5)
			return c
		}
		return nil
	})
	e.Func("units.attackers", "([dist = 30]) → Creature[]", "living creatures within dist that are targeting you", func(d float32) []types.Creature {
		out := []types.Creature{}
		for _, c := range b.creatures(b.GetNearbyUnits(dist(d))) {
			if uint64(c.Target) == w.CharGUID() && c.Alive {
				out = append(out, c)
			}
		}
		b.annotateNearby(out)
		return out
	})
	e.Func("units.has_aura", "(guid, spell) → bool", "whether the unit has that aura (buff or debuff)", b.HasAuraOn)
	e.Func("units.behind", "(guid) → bool", "whether you stand behind the unit (backstab and similar need it)", b.IsBehindTarget)

	// ---------- objects ----------
	e.Func("objects.nearby", "([dist = 30]) → Object[]", "game objects within dist yards (chests, doors, quest objects, herbs...), nearest first",
		func(d float32) []types.Object {
			out := []types.Object{}
			for _, o := range w.GameObjects() {
				if ob := b.object(o); ob.Distance <= float64(dist(d)) {
					out = append(out, ob)
				}
			}
			sortBy(out, func(o types.Object) float64 { return o.Distance })
			b.annotateObjects(out)
			return out
		})
	e.Func("objects.find", "(entry [, dist = 80]) → Object?", "the nearest game object with that entry; nil if none", func(entry uint32, d float32) any {
		if d <= 0 {
			d = 80
		}
		if g := w.FindGameObjectByEntry(entry, d); g != 0 {
			if o := w.GetObject(g); o != nil {
				ob := []types.Object{b.object(o)}
				b.annotateObjects(ob)
				return ob[0]
			}
		}
		return nil
	})

	// ---------- move ----------
	e.Func("move.face", "(guid) → Request", "turn to face a unit; movement_busy if another task owns movement", func(g uint64) map[string]any {
		args := map[string]any{"unit": gs(g)}
		return b.steeringRequest("move.face", args, func() *script.Outcome {
			if !b.FaceTarget(g) {
				return script.Fail("unit_not_found", "no such unit in sight", nil)
			}
			return script.Ok(nil)
		})
	})
	e.Func("move.turn", "(o) → Request", "face an angle (radians, as me.position().o); movement_busy if another task owns movement", func(o float32) map[string]any {
		return b.steeringRequest("move.turn", map[string]any{"o": o}, func() *script.Outcome {
			if err := b.SetFacing(o); err != nil {
				return script.Fail("failed", err.Error(), nil)
			}
			return script.Ok(nil)
		})
	})

	// ---------- combat ----------
	e.Func("combat.target", "(guid) → Request", "select a unit (some spells and abilities use your target)", func(g uint64) map[string]any {
		args := map[string]any{"unit": gs(g)}
		if err := b.SetTarget(g); err != nil {
			return b.refuse("combat.target", args, "failed", err.Error())
		}
		return b.request("combat.target", args, 2*time.Second, func() *script.Outcome {
			if w.TargetGUID() == g {
				return script.Ok(nil)
			}
			return nil
		}, nil)
	})
	e.Func("combat.attack", "(guid) → Request<Attacking>", "start auto-attacking (selects it too): ok when the swing starts, else not_in_range, bad_facing, dead_target, cant_attack",
		func(g uint64) map[string]any {
			args := map[string]any{"unit": gs(g)}
			if w.AttackingGUID() == g {
				b.AttackTarget(g) // keeps the swing alive
				return b.done("combat.attack", args, types.Attacking{Already: true})
			}
			if err := b.AttackTarget(g); err != nil {
				return b.refuse("combat.attack", args, "failed", err.Error())
			}
			return b.request("combat.attack", args, 4*time.Second, nil, func(ev script.Event) *script.Outcome {
				switch {
				case ev.Type == "attack_start" && ev.Data.(types.AttackStart).Target == types.GUID(g):
					return script.Ok(types.Attacking{})
				case ev.Type == "attack_failed" && ev.Data.(types.AttackFailed).Target == types.GUID(g):
					c := ev.Data.(types.AttackFailed).Code
					return script.Fail(c, c, nil)
				}
				return nil
			})
		})
	e.Func("combat.stop", "() → Request", "stop auto-attacking", func() map[string]any {
		if err := b.StopAttack(); err != nil {
			return b.refuse("combat.stop", nil, "failed", err.Error())
		}
		return b.request("combat.stop", nil, 2*time.Second, func() *script.Outcome {
			if w.AttackingGUID() == 0 {
				return script.Ok(nil)
			}
			return nil
		}, nil)
	})
	e.Func("combat.pet_attack", "(guid) → Request", "send your pet at a unit", func(g uint64) map[string]any {
		args := map[string]any{"unit": gs(g)}
		pet := b.GetPetGUID()
		if pet == 0 {
			return b.refuse("combat.pet_attack", args, "no_pet", "no controlled pet is observed")
		}
		if b.GetUnitInfo(g) == nil {
			return b.refuse("combat.pet_attack", args, "unit_gone", "target is not observed")
		}
		if err := w.PetAction(pet, 7<<24|2, g); err != nil {
			return b.refuse("combat.pet_attack", args, "failed", err.Error())
		}
		return b.request("combat.pet_attack", args, 5*time.Second, func() *script.Outcome {
			u := b.GetUnitInfo(pet)
			if u != nil && u.Target == g {
				return script.Ok(nil)
			}
			return nil
		}, nil)
	})
	e.Func("combat.sheath", "(state) → Request", "0 = weapons out, 1 = melee sheathed, 2 = ranged", func(st uint32) map[string]any {
		args := map[string]any{"state": st}
		if st > 2 {
			return b.refuse("combat.sheath", args, "invalid_arguments", "state must be 0, 1 or 2")
		}
		if err := b.SetSheathed(st); err != nil {
			return b.refuse("combat.sheath", args, "failed", err.Error())
		}
		return b.request("combat.sheath", args, 3*time.Second, func() *script.Outcome {
			obj := w.GetObject(w.CharGUID())
			if obj != nil && obj.Value(client.UnitFieldBytes2)&0xFF == st {
				return script.Ok(nil)
			}
			return nil
		}, nil)
	})

	// ---------- spells ----------
	e.Func("spells.known", "(id) → bool", "whether you know the spell (that rank)", w.KnowsSpell)
	e.Func("spells.check", "(id [, guid]) → SpellCheck", "local spell readiness with specific refusal reasons and unavailable checks; omitted guid means self. Range is nominal, target_valid means observed; the server remains authoritative", b.spellCheck)
	e.Func("spells.usable", "(id [, guid]) → bool", "convenience for spells.check(id,guid).usable; unknown details do not certify server acceptance", func(id uint32, g uint64) bool { return b.spellCheck(id, g).Usable })
	e.Func("spells.cast_at", "(id, x, y, z) → Request<CastDone>", "cast a ground-targeted spell (Blizzard, Flare...)", func(id uint32, x, y, z float32) map[string]any {
		args := map[string]any{"spell": id, "x": x, "y": y, "z": z}
		if !w.KnowsSpell(id) {
			return b.refuse("spells.cast_at", args, "not_known", "you don't know this spell (train it, or check the rank id)")
		}
		if err := w.CastSpellAtPosition(id, x, y, z); err != nil {
			return b.refuse("spells.cast_at", args, "failed", err.Error())
		}
		return b.request("spells.cast_at", args, castTimeout(id), nil, castMatch(id))
	})

	// ---------- chat ----------
	// The server drops chat over 255 bytes without a reply, so it'd look sent.
	tooLong := func(op, msg string) map[string]any {
		if len(msg) <= 255 {
			return nil
		}
		return b.refuse(op, nil, "message_too_long", strconv.Itoa(len(msg))+" bytes: the server drops chat over 255 without telling you. Split it")
	}
	// whispers and channel messages resolve on the server's echo (or its refusal)
	echoed := func(op string, typ uint32, to, msg, key string) map[string]any {
		if r := tooLong(op, msg); r != nil {
			return r
		}
		if msg = chatText(msg); msg == "" {
			return b.refuse(op, map[string]any{"to": to}, "empty_message", "the server drops a message that's empty or starts with a newline")
		}
		key += msg
		b.acked(key) // drop a stale one
		if err := b.sendChat(typ, to, msg); err != nil {
			return b.refuse(op, nil, "failed", err.Error())
		}
		return b.request(op, map[string]any{"to": to}, 3*time.Second, func() *script.Outcome {
			if b.acked(key) {
				return script.Ok(nil)
			}
			return nil
		}, func(ev script.Event) *script.Outcome {
			if e, ok := ev.Data.(types.ErrorEvent); ok && e.Code == "player_not_found" && !strings.Contains(strings.ToLower(e.Message), strings.ToLower(strconv.Quote(to))) {
				return nil // another whisper's
			}
			return failOn(ev, op)
		})
	}
	for _, c := range []struct {
		name string
		kind uint32
	}{{"say", 1}, {"yell", 6}, {"party", 2}} {
		op, kind := "chat."+c.name, c.kind
		e.Func(op, "(msg) → Request", "send chat; success requires the server echo, timeout is unconfirmed delivery", func(msg string) map[string]any { return echoed(op, kind, "", msg, fmt.Sprintf("chat:%d\x00", kind)) })
	}
	e.Func("chat.whisper", "(name, msg) → Request", "one player anywhere, by exact character name: player_not_found when no one by that name is online", func(to, msg string) map[string]any {
		return echoed("chat.whisper", 7, to, msg, "whisper\x00")
	})
	e.Func("chat.channel", "(name, msg) → Request", "everyone in a channel you've joined (not_member, muted, throttled, not_in_area). Every chat call takes at most 255 bytes (message_too_long)", func(ch, msg string) map[string]any {
		return echoed("chat.channel", 0x11, ch, msg, "channel\x00"+strings.ToLower(ch)+"\x00")
	})
	e.Func("chat.join", "(name [, id = 0]) → Request", "join a channel. Zone channels need their id: 1 General, 2 Trade, 22 LocalDefense (\"General - Elwynn Forest\"); custom channels use 0 and any name (wrong_password, banned)",
		func(name string, id uint32) map[string]any {
			key := "join\x00" + strings.ToLower(name)
			b.acked(key)
			var buf bytes.Buffer
			binary.Write(&buf, binary.LittleEndian, id)
			buf.Write([]byte{0, 0})
			buf.WriteString(name + "\x00\x00") // name, empty password
			w.SendPacketRaw(cmsgJoinChannel, buf.Bytes())
			return b.request("chat.join", map[string]any{"name": name}, 3*time.Second, func() *script.Outcome {
				if b.acked(key) {
					return script.Ok(nil)
				}
				return nil
			}, func(ev script.Event) *script.Outcome { return failOn(ev, "chat.join") })
		})
	e.Func("chat.command", "(text) → Request", "a server command (\".help\"); GM commands only work on GM accounts", func(cmd string) map[string]any {
		return b.sent("chat.command", nil, b.SendCommand(cmd))
	})

	// ---------- client: the raw protocol ----------
	e.Methods("client.call", "(method, ...) → results", "call any exported method of the underlying WorldClient (client/world.go); errors come back as strings. Outside the stable API", w)
}

func castTimeout(id uint32) time.Duration {
	if sp := spellInfo(id); sp != nil {
		return 5*time.Second + time.Duration(sp.CastMS)*time.Millisecond
	}
	return 5 * time.Second
}

// snapshot builds me.status() (called with the engine lock held: Lua is calling it): what a player sees on screen.
func (b *Bot) snapshot() types.Status {
	w := b.world
	me := w.CharGUID()
	hc, hm := b.GetHealth()
	pc, pm := b.GetPower()
	x, y, z, o, m := w.Position()
	st := types.Status{Name: b.config.CharacterName, Class: name(classNames, b.GetClass()), Race: name(raceNames, b.GetRace()),
		Level: b.GetLevel(), Health: types.Resource{Current: hc, Max: hm},
		Power: types.Resource{Current: pc, Max: pm, Type: name(powerNames, b.GetPowerType())},
		Money: w.Money(), Position: types.Position{X: round1(x), Y: round1(y), Z: round1(z), O: math.Round(float64(o)*1000) / 1000, Map: m},
		Alive: b.IsAlive(), Ghost: b.IsGhost(), InCombat: b.InCombat(), FreeSlots: freeSlots(w),
		Quests: b.questLog(), Threats: []types.Hostile{}, NPCs: []types.NearbyNPC{}, BagUpgrades: []types.Upgrade{},
		Tasks: b.lua.TasksHeld()}
	if o := w.GetObject(me); o != nil {
		st.XP = types.XP{Current: o.Value(0x027A), Next: o.Value(0x027B)}
		st.Resting = o.Value(fieldPlayerFlags)&0x20 != 0
	}
	e := b.lua
	st.Location, _ = e.Native("me.location").(types.Location)
	st.Movement, _ = e.Native("me.movement").(types.Movement)
	st.Auras, st.States = b.aurasOf(me), auraStates(w.GetObject(me))
	if c, ok := e.Native("me.casting").(types.Casting); ok {
		st.Casting = &c
	}
	if c, ok := e.Native("me.combo_points").(types.ComboPoints); ok {
		st.ComboPoints = &c
	}
	st.Timers, _ = e.Native("me.timers").([]types.MirrorTimer)
	st.LootRolls, _ = e.Native("items.rolls").([]types.LootRoll)
	var res uint64
	if b.agent != nil {
		b.agent.mu.Lock()
		res = b.agent.resFrom
		b.agent.mu.Unlock()
	}
	if res != 0 && (!st.Alive || st.Ghost) {
		r := b.ref(res)
		st.ResurrectOffer = &r
	}
	if c, ok := e.Native("death.corpse").(types.Corpse); ok {
		st.Corpse = &c
	}

	// target and fight
	if t := b.GetTargetGUID(); t != 0 {
		if u := b.GetUnitInfo(t); u != nil {
			bu := b.baseUnit(*u)
			st.Target = &bu
		}
		st.TargetAuras = b.aurasOf(t)
	}
	pet := b.GetPetGUID()
	for _, c := range b.threats() {
		h := types.Hostile{GUID: c.GUID, Name: c.Name, Level: c.Level, Place: c.Place, ThreatPct: c.ThreatPct,
			Elite: c.Elite, Evading: c.Evading, Casting: c.Casting}
		if c.MaxHealth > 0 {
			h.HealthPct = math.Round(float64(c.Health) / float64(c.MaxHealth) * 100)
		}
		switch uint64(c.Target) {
		case me:
			h.On = "you"
		case pet:
			h.On = "pet"
		default:
			h.On = b.unitName(uint64(c.Target))
		}
		st.Threats = append(st.Threats, h)
	}

	// the world around you (60 yd: the widest aggro range, 45 yd, and the 15 yd before it)
	var hostile []types.Creature
	for _, c := range b.creatures(b.GetNearbyUnits(60)) {
		if c.Alive && c.AggroRadius > 0 {
			hostile = append(hostile, c)
		}
		if c.Alive && c.Reaction == "hostile" && c.Attackable && c.Distance <= 30 && c.Level+5 >= st.Level {
			st.Mobs.Count++
			st.Mobs.MaxLevel = max(st.Mobs.MaxLevel, c.Level)
			if c.Elite {
				st.Mobs.Elites++
			}
		}
		if c.Lootable && c.Distance <= 30 {
			st.Mobs.Loot++
		}
		if c.Alive && c.Reaction != "hostile" && c.Distance <= 40 && (len(c.Roles) > 0 || c.QuestStatus != "") {
			st.NPCs = append(st.NPCs, types.NearbyNPC{GUID: c.GUID, Name: c.Name, Place: c.Place, Roles: c.Roles, QuestStatus: c.QuestStatus})
		}
	}
	sortBy(st.NPCs, func(n types.NearbyNPC) float64 { // quest givers with something for you first
		switch n.QuestStatus {
		case "available", "available_repeatable", "reward":
			return n.Distance
		}
		return 1000 + n.Distance
	})
	if len(st.NPCs) > 8 {
		st.NPCs = st.NPCs[:8]
	}
	st.Objects = []types.Object{}
	for _, o := range w.GameObjects() {
		ob := b.object(o)
		if ob.Distance <= 40 && (ob.Type == "chest" || ob.Type == "fishing_pool" || ob.Lit || ob.Gather != "") {
			st.Objects = append(st.Objects, ob)
		}
	}
	sortBy(st.Objects, func(o types.Object) float64 { return o.Distance })
	if len(st.Objects) > 5 {
		st.Objects = st.Objects[:5]
	}
	st.Players = b.players(b.GetNearbyPlayers(60))
	sortBy(st.Players, func(p types.Player) float64 { return p.Distance })
	if len(st.Players) > 10 {
		st.Players = st.Players[:10]
	}
	if g, ok := e.Native("group.info").(types.Group); ok {
		st.Group = &g
	}
	// sight and walking routes for everything around you, from one time budget: your fight first, then your target,
	// the NPCs, objects, players and your group
	r := b.newReach()
	st.Danger, st.Mobs.InAggro, st.Mobs.AggroMargin = danger(hostile, func(d *types.Danger) { r.place(uint64(d.GUID), &d.Place, 1.5) })
	for i := range st.Threats {
		r.place(uint64(st.Threats[i].GUID), &st.Threats[i].Place, 1.5)
	}
	if st.Target != nil {
		r.place(uint64(st.Target.GUID), &st.Target.Place, 1.5)
	}
	for i := range st.NPCs {
		r.place(uint64(st.NPCs[i].GUID), &st.NPCs[i].Place, 1.5)
	}
	for i := range st.Objects {
		r.place(uint64(st.Objects[i].GUID), &st.Objects[i].Place, 0.5)
	}
	for i := range st.Players {
		r.place(uint64(st.Players[i].GUID), &st.Players[i].Place, 1.5)
	}
	if st.Group != nil {
		for i := range st.Group.Members {
			if m := &st.Group.Members[i]; m.InRange && m.Position != nil {
				m.InSight, m.PathDistance, m.Reachable = r.of(uint64(m.GUID), m.Position.X, m.Position.Y, m.Position.Z, 1.5)
			}
		}
	}

	// your character: what to improve
	g := b.gear()
	st.Gear = types.GearSummary{Score: g.Score, Grade: g.Grade, AvgItemLevel: g.AvgItemLevel, Broken: g.Broken, Weakest: []string{}}
	for _, ws := range g.Weakest[:min(3, len(g.Weakest))] {
		st.Gear.Weakest = append(st.Gear.Weakest, ws.Slot)
	}
	for _, sp := range b.spellbook().Missing {
		if sp.Level <= st.Level {
			st.Untrained++
		}
	}
	st.Talents = unspentTalents(w)
	seen := map[uint32]bool{} // one per item: two copies of the same upgrade are one decision
	for _, it := range bagItems(w) {
		if inBags(it) && it.Usable && it.Slot != "" && !seen[it.Entry] {
			seen[it.Entry] = true
			if c := b.compareItem(uint64(it.GUID)); c != nil && c.ItemLevel > 0 {
				st.BagUpgrades = append(st.BagUpgrades, types.Upgrade{Item: types.ItemRef{GUID: it.GUID, Entry: it.Entry, Name: it.Name}, Slot: it.Slot, ItemLevel: c.ItemLevel})
			}
		}
	}
	for _, mt := range b.meter(60) {
		if mt.Unit.Me && (mt.Damage > 0 || mt.Healing > 0 || mt.Taken > 0) {
			mt := mt
			st.LastMinute = &mt
		}
	}
	return st
}

// chatText: a message as the server echoes it (ChatHandler: cut at the first newline, runs of spaces collapsed)
func chatText(s string) string {
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}
