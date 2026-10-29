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
// Conventions (docs/lua-api.md): queries are nouns and return one value or one table (nil when unknown); actions
// are verbs and return a request (script/requests.go) whose op is the function's name; every value is one of the
// structs in package types (built in model.go), so a creature or an item has the same fields everywhere. Domain files register their own namespaces (items.go, trade.go, ...);
// this file has the core ones. Every registration carries its signature and description: help() and the
// generated reference come from them.

import (
	"bytes"
	"encoding/binary"
	"math"
	"time"

	"github.com/pthm/wowbench/azeroth-sdk/client"
	"github.com/pthm/wowbench/azeroth-sdk/script"
	"github.com/pthm/wowbench/azeroth-sdk/types"
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
	10: "goober", 11: "elevator", 15: "ship", 17: "fishing_hole", 19: "mailbox", 22: "portal", 23: "meeting_stone", 25: "fishing_node"}

// game object names by entry, from SMSG_GAMEOBJECT_QUERY_RESPONSE
var goNames = struct {
	m     map[uint32]string
	asked map[uint32]time.Time
}{m: map[uint32]string{}, asked: map[uint32]time.Time{}}

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
	if !known && time.Since(goNames.asked[entry]) > 5*time.Second {
		goNames.asked[entry] = time.Now()
		body := binary.LittleEndian.AppendUint32(nil, entry)
		go b.world.SendPacketRaw(cmsgGameObjectQuery, binary.LittleEndian.AppendUint64(body, o.GUID))
	}
	creatures.Unlock()
	return types.Object{GUID: types.GUID(o.GUID), Entry: entry, Name: goName, Type: name(goTypes, (o.Value(fieldGOBytes1)>>8)&0xFF),
		X: round1(x), Y: round1(y), Z: round1(z), Distance: round1(float32(math.Sqrt(float64((x-mx)*(x-mx) + (y-my)*(y-my) + (z-mz)*(z-mz)))))}
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
		creatures.Lock()
		goNames.m[entry] = readCString(r)
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

	e.Func("me.status", "() → Status", "a one-call snapshot: level, xp, health, power, money, where you are, state, target, attackers, nearby mobs and players, group, quests, broken gear, bag upgrades, your last minute of fighting, corpse, tasks",
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
		func(d float32) []types.Creature { return b.creatures(b.GetNearbyUnits(dist(d))) })
	e.Func("units.players", "([dist = 30]) → Player[]", "players within dist yards, nearest first", func(d float32) []types.Player {
		return b.players(b.GetNearbyPlayers(dist(d)))
	})
	e.Func("units.get", "(guid) → (Creature | Player)?", "one unit by GUID; nil if it's out of sight", func(g uint64) any {
		if u := b.GetUnitInfo(g); u != nil {
			if u.IsPlayer {
				return b.player(*u)
			}
			return b.creature(*u)
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
			return out
		})
	e.Func("objects.find", "(entry [, dist = 80]) → Object?", "the nearest game object with that entry; nil if none", func(entry uint32, d float32) any {
		if d <= 0 {
			d = 80
		}
		if g := w.FindGameObjectByEntry(entry, d); g != 0 {
			if o := w.GetObject(g); o != nil {
				return b.object(o)
			}
		}
		return nil
	})

	// ---------- move ----------
	e.Func("move.face", "(guid) → request", "turn to face a unit (melee and most spells need it)", func(g uint64) map[string]any {
		args := map[string]any{"unit": gs(g)}
		if !b.FaceTarget(g) {
			return b.refuse("move.face", args, "unit_not_found", "no such unit in sight")
		}
		return b.done("move.face", args, nil)
	})
	e.Func("move.turn", "(o) → request", "face an angle (radians, as me.position().o)", func(o float32) map[string]any {
		return b.sent("move.turn", map[string]any{"o": o}, b.SetFacing(o))
	})

	// ---------- combat ----------
	e.Func("combat.target", "(guid) → request", "select a unit (some spells and abilities use your target)", func(g uint64) map[string]any {
		return b.sent("combat.target", map[string]any{"unit": gs(g)}, b.SetTarget(g))
	})
	e.Func("combat.attack", "(guid) → request<Attacking>", "start auto-attacking (selects it too): ok when the swing starts, else not_in_range, bad_facing, dead_target, cant_attack",
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
	e.Func("combat.stop", "() → request", "stop auto-attacking", func() map[string]any { return b.sent("combat.stop", nil, b.StopAttack()) })
	e.Func("combat.pet_attack", "(guid) → request", "send your pet at a unit", func(g uint64) map[string]any {
		b.PetAttack(g)
		return b.done("combat.pet_attack", map[string]any{"unit": gs(g)}, nil)
	})
	e.Func("combat.sheath", "(state) → request", "0 = weapons out, 1 = melee sheathed, 2 = ranged", func(st uint32) map[string]any {
		return b.sent("combat.sheath", map[string]any{"state": st}, b.SetSheathed(st))
	})

	// ---------- spells ----------
	e.Func("spells.known", "(id) → bool", "whether you know the spell (that rank)", w.KnowsSpell)
	e.Func("spells.usable", "(id [, guid]) → bool", "known, off cooldown and affordable now (on that target)", func(id uint32, g uint64) bool {
		if g == 0 {
			g = w.CharGUID()
		}
		return b.CanCast(id, g)
	})
	e.Func("spells.cast_at", "(id, x, y, z) → request<CastDone>", "cast a ground-targeted spell (Blizzard, Flare...)", func(id uint32, x, y, z float32) map[string]any {
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
	say := func(op string, typ uint32, to string) func(string) map[string]any {
		return func(msg string) map[string]any {
			b.sendChat(typ, to, msg)
			return b.done(op, nil, nil)
		}
	}
	e.Func("chat.say", "(msg) → request", "players within 40 yards hear it", func(msg string) map[string]any {
		return b.sent("chat.say", nil, b.SendChat(msg))
	})
	e.Func("chat.yell", "(msg) → request", "players within 300 yards hear it", say("chat.yell", 6, ""))
	e.Func("chat.party", "(msg) → request", "your party, at any distance", say("chat.party", 2, ""))
	e.Func("chat.whisper", "(name, msg) → request", "one player anywhere, by exact character name", func(to, msg string) map[string]any {
		return say("chat.whisper", 7, to)(msg)
	})
	e.Func("chat.channel", "(name, msg) → request", "everyone in a channel you've joined", func(ch, msg string) map[string]any {
		return say("chat.channel", 0x11, ch)(msg)
	})
	e.Func("chat.join", "(name [, id = 0]) → request", "join a channel. Zone channels need their id: 1 General, 2 Trade, 22 LocalDefense (\"General - Elwynn Forest\"); custom channels use 0 and any name",
		func(name string, id uint32) map[string]any {
			var buf bytes.Buffer
			binary.Write(&buf, binary.LittleEndian, id)
			buf.Write([]byte{0, 0})
			buf.WriteString(name + "\x00\x00") // name, empty password
			w.SendPacketRaw(cmsgJoinChannel, buf.Bytes())
			return b.done("chat.join", map[string]any{"name": name}, nil)
		})
	e.Func("chat.command", "(text) → request", "a server command (\".help\"); GM commands only work on GM accounts", func(cmd string) map[string]any {
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

// snapshot builds me.status() (called with the engine lock held: Lua is calling it).
func (b *Bot) snapshot() types.Status {
	w := b.world
	hc, hm := b.GetHealth()
	pc, pm := b.GetPower()
	x, y, z, o, m := w.Position()
	st := types.Status{Name: b.config.CharacterName, Class: name(classNames, b.GetClass()), Race: name(raceNames, b.GetRace()),
		Level: b.GetLevel(), Health: types.Resource{Current: hc, Max: hm},
		Power: types.Resource{Current: pc, Max: pm, Type: name(powerNames, b.GetPowerType())},
		Money: w.Money(), Position: types.Position{X: round1(x), Y: round1(y), Z: round1(z), O: math.Round(float64(o)*1000) / 1000, Map: m},
		Alive: b.IsAlive(), Ghost: b.IsGhost(), InCombat: b.InCombat(), FreeSlots: freeSlots(w),
		Quests: b.questLog(), Attackers: []types.Creature{}, Broken: []types.Item{}, BagUpgrades: []types.Comparison{},
		Tasks: b.lua.TasksHeld()}
	if me := w.GetObject(w.CharGUID()); me != nil {
		st.XP = types.XP{Current: me.Value(0x027A), Next: me.Value(0x027B)}
		st.Resting = me.Value(fieldPlayerFlags)&0x20 != 0
	}
	e := b.lua
	st.Location, _ = e.Native("me.location").(types.Location)
	st.Movement, _ = e.Native("me.movement").(types.Movement)
	st.Gear, st.Spellbook, st.Talents = b.gear(), b.spellbook(), unspentTalents(w)
	st.Auras = b.aurasOf(w.CharGUID())
	if t := b.GetTargetGUID(); t != 0 {
		st.TargetAuras = b.aurasOf(t)
	}
	if c, ok := e.Native("me.casting").(types.Casting); ok {
		st.Casting = &c
	}
	if v, ok := e.Native("me.stats").(types.Stats); ok {
		st.Stats = &v
	}
	if c, ok := e.Native("death.corpse").(types.Corpse); ok {
		st.Corpse = &c
	}
	if g, ok := e.Native("group.info").(types.Group); ok {
		st.Group = &g
	}
	if t := b.GetTargetGUID(); t != 0 {
		if u := b.GetUnitInfo(t); u != nil {
			bu := b.baseUnit(*u)
			st.Target = &bu
		}
	}
	for _, c := range b.creatures(b.GetNearbyUnits(30)) {
		if !c.Alive || !c.Attackable {
			continue
		}
		if uint64(c.Target) == w.CharGUID() {
			st.Attackers = append(st.Attackers, c)
		}
		if c.Reaction == "hostile" && c.Level+5 >= st.Level {
			st.Mobs.Count++
			st.Mobs.MaxLevel = max(st.Mobs.MaxLevel, c.Level)
			if c.Elite {
				st.Mobs.Elites++
			}
		}
	}
	st.Players = b.players(b.GetNearbyPlayers(60))
	for _, it := range bagItems(w) {
		if it.Equipped && it.Broken {
			st.Broken = append(st.Broken, it)
		}
		if inBags(it) && it.Usable && it.Slot != "" {
			if c := b.compareItem(uint64(it.GUID)); c != nil && c.ItemLevel > 0 {
				st.BagUpgrades = append(st.BagUpgrades, *c)
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
