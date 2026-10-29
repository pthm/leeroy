package session

// What a real client's UI shows in a fight, beyond health: what units are casting, the game's aura states,
// combo points, how attacks landed (dodge, parry, evade...), who is attacking your group; plus name lookups for
// spells and items, and getting unstuck (jump, and the client's "Stuck" spell as a failsafe).

import (
	"bytes"
	"encoding/binary"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

const (
	fieldAuraState    = 0x3D // UNIT_FIELD_AURASTATE
	unitFlagSkinnable = 0x04000000
	spellStuck        = 7355 // what the client's "Character stuck" casts: hearths you home
	smsgHighestThreat = 0x482
	smsgThreatUpdate  = 0x483
	smsgThreatRemove  = 0x484
	smsgThreatClear   = 0x485
)

var auraStateNames = map[uint32]string{1: "defense", 2: "health_below_20", 3: "berserking", 4: "frozen", 5: "judgement",
	7: "hunter_parry", 8: "banished", 10: "victory_rush", 12: "faerie_fire", 13: "health_below_35", 14: "conflagrate",
	15: "swiftmend", 16: "deadly_poison", 17: "enrage", 18: "bleeding", 23: "health_above_75"}
var creatureTypes = map[uint32]string{1: "beast", 2: "dragonkin", 3: "demon", 4: "elemental", 5: "giant", 6: "undead",
	7: "humanoid", 8: "critter", 9: "mechanical", 10: "not_specified", 11: "totem", 12: "non_combat_pet", 13: "gas_cloud"}
var creatureRanks = map[uint32]string{0: "normal", 1: "elite", 2: "rare_elite", 3: "boss", 4: "rare"}
var dispelTypes = map[uint32]string{1: "magic", 2: "curse", 3: "disease", 4: "poison"}

type unitCast struct {
	spell uint32
	until time.Time
}

type combatInfo struct {
	mu     sync.Mutex
	casts  map[uint64]unitCast  // others' casts with a cast time (SMSG_SPELL_START)
	evaded map[uint64]time.Time // when each unit last evaded
	combo  types.ComboPoints
	threat map[uint64][]threatRef // a creature's threat list, highest first (SMSG_THREAT_UPDATE: sent to everyone near it)
}

type threatRef struct {
	guid   uint64
	threat float64
}

func hasAuraState(o *client.WorldObject, state uint32) bool {
	return o != nil && state <= 32 && o.Value(fieldAuraState)&(1<<(state-1)) != 0
}

func auraStates(o *client.WorldObject) []string {
	if o == nil {
		return nil
	}
	v := o.Value(fieldAuraState)
	var out []string
	for bit := uint32(1); bit <= 32; bit++ {
		if v&(1<<(bit-1)) != 0 {
			if n, ok := auraStateNames[bit]; ok {
				out = append(out, n)
			}
		}
	}
	return out
}

// unitExtras fills what the combat UI shows about a unit: its cast and its aura states.
func (b *Bot) unitExtras(u *types.Unit, guid uint64) {
	o := b.world.GetObject(guid)
	u.States = auraStates(o)
	if ci := b.cinfo; ci != nil {
		ci.mu.Lock()
		c, ok := ci.casts[guid]
		ci.mu.Unlock()
		if ok && time.Now().Before(c.until) {
			u.Casting = &types.Casting{Spell: c.spell, Name: spellName(c.spell), MSLeft: uint32(time.Until(c.until).Milliseconds())}
			return
		}
	}
	if o != nil {
		if ch := o.Value(client.UnitChannelSpell); ch != 0 {
			u.Casting = &types.Casting{Spell: ch, Name: spellName(ch)}
		}
	}
}

// creatureExtras: rank, type and title from the creature query; skinnable; evading.
func (b *Bot) creatureExtras(c *types.Creature, u UnitInfo) {
	creatures.Lock()
	info, known := creatures.info[u.Entry]
	creatures.Unlock()
	if known {
		c.Rank, c.Type, c.Subname = name(creatureRanks, info.rank), name(creatureTypes, info.typ), info.subname
		c.Elite = info.rank == 1 || info.rank == 2 || info.rank == 3
	}
	c.Skinnable = !u.IsAlive && u.Flags&unitFlagSkinnable != 0
	if b.npc != nil {
		c.QuestStatus = b.npc.questStatus(u.GUID)
	}
	if ci := b.cinfo; ci != nil {
		ci.mu.Lock()
		c.Evading = time.Since(ci.evaded[u.GUID]) < 6*time.Second
		if l := ci.threat[u.GUID]; len(l) > 0 && l[0].threat > 0 {
			for _, t := range l {
				if t.guid == b.world.CharGUID() {
					pct := math.Round(t.threat / l[0].threat * 100)
					c.ThreatPct = &pct
				}
			}
		}
		ci.mu.Unlock()
	}
}

// attackOutcome records how an attack landed (from the melee and spell-miss logs) and passes it on as a damage event.
func (b *Bot) attackOutcome(src, dst uint64, amount, spell uint32, outcome string, hit func(src, dst uint64, amount, spell uint32, kind string, outcome ...string)) {
	if outcome == "evade" && b.cinfo != nil {
		b.cinfo.mu.Lock()
		b.cinfo.evaded[dst] = time.Now()
		b.cinfo.mu.Unlock()
	}
	hit(src, dst, amount, spell, "damage", outcome)
}

// threats: hostile living creatures attacking you, your pet or a party member.
func (b *Bot) threats() []types.Creature {
	w := b.world
	ours := map[types.GUID]bool{types.GUID(w.CharGUID()): true}
	if p := b.GetPetGUID(); p != 0 {
		ours[types.GUID(p)] = true
	}
	for _, m := range w.GroupState().Members {
		ours[types.GUID(m.GUID)] = true
	}
	out := []types.Creature{}
	for _, c := range b.creatures(b.GetNearbyUnits(60)) {
		if c.Alive && c.Target != 0 && ours[c.Target] && c.Reaction != "friendly" {
			out = append(out, c)
		}
	}
	return out
}

func (b *Bot) setupCombatInfo() {
	ci := &combatInfo{casts: map[uint64]unitCast{}, evaded: map[uint64]time.Time{}, threat: map[uint64][]threatRef{}}
	b.cinfo = ci
	w, e := b.world, b.lua
	me := w.CharGUID()
	w.AddPacketHook(func(op uint16, data []byte) {
		switch op {
		case smsgSpellStart: // caster item, caster, cast count, spell, flags, cast time
			r := newReader(data)
			readPackedGUID(r.Reader)
			caster := readPackedGUID(r.Reader)
			if caster == me {
				return
			}
			r.ReadByte()
			spell, _, castMS := r.u32(), r.u32(), r.u32()
			if castMS == 0 {
				return
			}
			ci.mu.Lock()
			ci.casts[caster] = unitCast{spell: spell, until: time.Now().Add(time.Duration(castMS) * time.Millisecond)}
			ci.mu.Unlock()
			// at you or your group: tell the agent (an interrupt or a stun stops it)
			o := w.GetObject(caster)
			if o == nil || caster>>48 == 0 {
				return
			}
			target := uint64(o.Value(client.UnitFieldTarget)) | uint64(o.Value(client.UnitFieldTarget+1))<<32
			ours := target == me
			for _, m := range w.GroupState().Members {
				ours = ours || target == m.GUID
			}
			if ours {
				b.emit("unit_cast_start", types.UnitCast{Unit: b.ref(caster), Spell: spell, Name: spellName(spell), CastMS: castMS, Target: b.ref(target)})
			}
		case smsgSpellGo, smsgSpellFailure, smsgSpellFailedOther:
			r := newReader(data)
			if op == smsgSpellGo {
				readPackedGUID(r.Reader)
			}
			caster := readPackedGUID(r.Reader)
			ci.mu.Lock()
			delete(ci.casts, caster)
			ci.mu.Unlock()
		case smsgHighestThreat, smsgThreatUpdate: // ThreatManager::SendThreatListToClients: unit, [new top], count, (victim, threat*100)...
			r := newReader(data)
			unit := readPackedGUID(r.Reader)
			if op == smsgHighestThreat {
				readPackedGUID(r.Reader)
			}
			list := []threatRef{}
			for n := r.u32(); n > 0 && r.Len() > 0; n-- {
				v := readPackedGUID(r.Reader)
				list = append(list, threatRef{v, float64(r.u32()) / 100})
			}
			sort.SliceStable(list, func(i, j int) bool { return list[i].threat > list[j].threat })
			ci.mu.Lock()
			ci.threat[unit] = list
			ci.mu.Unlock()
		case smsgThreatRemove: // unit, victim
			r := newReader(data)
			unit, v := readPackedGUID(r.Reader), readPackedGUID(r.Reader)
			ci.mu.Lock()
			ci.threat[unit] = slices.DeleteFunc(ci.threat[unit], func(t threatRef) bool { return t.guid == v })
			ci.mu.Unlock()
		case smsgThreatClear: // unit
			unit := readPackedGUID(newReader(data).Reader)
			ci.mu.Lock()
			delete(ci.threat, unit)
			ci.mu.Unlock()
		case smsgUpdateComboPoints: // target, points
			r := newReader(data)
			t := readPackedGUID(r.Reader)
			n, _ := r.ReadByte()
			ci.mu.Lock()
			ci.combo = types.ComboPoints{Target: types.GUID(t), Points: n}
			ci.mu.Unlock()
		}
	})

	e.Func("me.combo_points", "() → ComboPoints?", "combo points and the unit they're on (rogues; for warriors, Overpower is ready on that unit after it dodged); nil when none",
		func() any {
			ci.mu.Lock()
			defer ci.mu.Unlock()
			if ci.combo.Points == 0 {
				return nil
			}
			return ci.combo
		})
	e.Func("me.states", "() → string[]", "your aura states: defense (you dodged, parried or blocked: Revenge), health_below_20, ... (some abilities need one)",
		func() []string {
			s := auraStates(w.GetObject(me))
			if s == nil {
				s = []string{}
			}
			return s
		})
	e.Func("units.threats", "() → Creature[]", "hostile creatures attacking you, your pet or anyone in your group, nearest first: what a tank has to pick up and a healer has to watch",
		func() []types.Creature { cs := b.threats(); b.annotateNearby(cs); return cs })
	e.Func("combat.threat", "(guid) → Threat[]", "a creature's threat table as the server sends it to everyone near: who it would attack, highest first, with each one's share of the top (pct = 100 is the one it attacks). Empty when it isn't fighting or out of sight",
		func(g uint64) []types.Threat {
			ci.mu.Lock()
			l := ci.threat[g]
			ci.mu.Unlock()
			out := []types.Threat{}
			for _, t := range l {
				out = append(out, types.Threat{Unit: b.ref(t.guid), Threat: math.Round(t.threat), Pct: math.Round(t.threat / max(l[0].threat, 1) * 100)})
			}
			return out
		})
	e.Func("spells.find", "(name [, rank]) → Spell?", "a spell by name (any case): the highest rank you know, or that rank (a number, or \"Rank 2\"); nil if you know none",
		func(n string, rank any) any {
			want := ""
			switch v := rank.(type) {
			case float64:
				want = "Rank " + strconv.FormatFloat(v, 'f', -1, 64)
			case string:
				want = v
			}
			var best *types.Spell
			for _, id := range spellsNamed(n) {
				sp := spellOf(w, id)
				if sp == nil || !sp.Known || (want != "" && !strings.EqualFold(sp.Rank, want)) {
					continue
				}
				if best == nil || spellInfo(sp.ID).Level > spellInfo(best.ID).Level || (spellInfo(sp.ID).Level == spellInfo(best.ID).Level && sp.ID > best.ID) {
					best = sp
				}
			}
			if best == nil {
				return nil
			}
			return *best
		})
	e.Func("items.find", "(name) → Item?", "an item you carry, by name (any case); count is how many you have in all", func(n string) any {
		var found *types.Item
		total := uint32(0)
		for _, it := range bagItems(w) {
			if strings.EqualFold(it.Name, n) {
				total += it.Count
				if found == nil {
					it := it
					found = &it
				}
			}
		}
		if found == nil {
			return nil
		}
		found.Count = total
		return *found
	})
	e.Func("move.jump", "() → request", "jump (over a low obstacle; part of getting unstuck)", func() map[string]any {
		return b.sent("move.jump", nil, w.Jump())
	})
	e.Func("move.unstuck", "() → request<Teleport>", "the failsafe when you're truly stuck: what the real client's \"Character stuck\" does: a 10 s cast that hearths you home (me.home()). Not in combat or on a flight; shares the hearthstone's cooldown",
		func() map[string]any {
			if b.InCombat() {
				return b.refuse("move.unstuck", nil, "in_combat", "not while in combat")
			}
			if lock := w.MovementLock(); lock == "in_flight" || lock == "teleporting" {
				return b.refuse("move.unstuck", nil, lock, "not while "+lock)
			}
			b.StopMoving()
			if err := w.CastSpell(spellStuck, 0); err != nil {
				return b.refuse("move.unstuck", nil, "failed", err.Error())
			}
			return b.request("move.unstuck", nil, 20*time.Second, nil, func(ev script.Event) *script.Outcome {
				switch d := ev.Data.(type) {
				case types.Teleport:
					return script.Ok(d)
				case types.Cast:
					if ev.Type == "cast_failed" && (d.Spell == spellStuck || d.Spell == 8690) {
						return script.Fail(d.Code, "the Stuck spell failed: "+d.Code+" (the hearthstone's cooldown is shared)", nil)
					}
				}
				return nil
			})
		})
}

type pktReader struct{ *bytes.Reader }

func newReader(b []byte) pktReader { return pktReader{bytes.NewReader(b)} }

func (r pktReader) u32() (v uint32) { binary.Read(r.Reader, binary.LittleEndian, &v); return }

var spellNames = struct {
	once sync.Once
	m    map[string][]uint32
}{}

// spellsNamed: every spell id with that name (lower-cased), all ranks
func spellsNamed(n string) []uint32 {
	spellNames.once.Do(func() {
		loadSpells()
		spellNames.m = map[string][]uint32{}
		for id, sp := range spells {
			k := strings.ToLower(sp.Name)
			spellNames.m[k] = append(spellNames.m[k], id)
		}
	})
	return spellNames.m[strings.ToLower(n)]
}
