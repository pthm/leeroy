package session

// Combat log, meters and inspect: what a real client's combat log, damage meters (Recount/Skada) and the inspect
// window show. Only what the server sends this client: fights within visibility range (~100 yd), any source and
// target, party members included.

import (
	"bytes"
	"encoding/binary"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

const (
	smsgAttackerState = 0x14A
	smsgHealLog       = 0x150
	smsgPartyKillLog  = 0x1F5
	smsgPeriodicAura  = 0x24E
	smsgSpellDmgLog   = 0x250

	fieldUnitBytes0   = 0x17  // UNIT_FIELD_BYTES_0: race, class, gender, power type
	fieldVisibleItem1 = 0x11B // PLAYER_VISIBLE_ITEM_1_ENTRYID; 19 slots, 2 fields each
	combatLogCap      = 5000  // ponytail: ring buffer; ~10-20 min of a busy fight nearby
)

type combatEvent struct {
	T      int64  // ms
	Src    uint64 // 0 for environment
	Dst    uint64
	Spell  uint32 // 0 = melee swing
	Amount uint32 // damage or healing done (after absorbs)
	Over   uint32 // overkill / overheal
	Kind   string // melee, spell, dot, heal, hot, kill
	Crit   bool
}

type combatLog struct {
	mu  sync.Mutex
	evs []combatEvent
}

func (c *combatLog) add(e combatEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.evs) >= combatLogCap {
		c.evs = c.evs[1:]
	}
	c.evs = append(c.evs, e)
}

func (c *combatLog) since(t int64) []combatEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := sort.Search(len(c.evs), func(i int) bool { return c.evs[i].T >= t })
	return append([]combatEvent{}, c.evs[i:]...)
}

func (c *combatLog) onPacket(op uint16, data []byte) {
	r := bytes.NewReader(data)
	u32 := func() (v uint32) { binary.Read(r, binary.LittleEndian, &v); return }
	now := time.Now().UnixMilli()
	switch op {
	case smsgAttackerState: // hitInfo, attacker, target, total damage, overkill, ...
		hit := u32()
		src, dst := readPackedGUID(r), readPackedGUID(r)
		dmg, over := u32(), u32()
		if int32(over) < 0 {
			over = 0
		}
		c.add(combatEvent{T: now, Src: src, Dst: dst, Amount: dmg, Over: over, Kind: "melee", Crit: hit&0x200 != 0}) // HITINFO_CRITICALHIT
	case smsgSpellDmgLog: // target, caster, spell, damage, overkill, school, absorb, resist, physical, unused, blocked, flags
		dst, src := readPackedGUID(r), readPackedGUID(r)
		spell, dmg, over := u32(), u32(), u32()
		r.ReadByte()
		u32()
		u32()
		r.ReadByte()
		r.ReadByte()
		u32()
		flags := u32()
		c.add(combatEvent{T: now, Src: src, Dst: dst, Spell: spell, Amount: dmg, Over: over, Kind: "spell", Crit: flags&0x2 != 0})
	case smsgHealLog: // target, caster, spell, heal, overheal, absorb, crit
		dst, src := readPackedGUID(r), readPackedGUID(r)
		spell, heal, over := u32(), u32(), u32()
		u32()
		crit, _ := r.ReadByte()
		c.add(combatEvent{T: now, Src: src, Dst: dst, Spell: spell, Amount: heal - min(heal, over), Over: over, Kind: "heal", Crit: crit != 0})
	case smsgPeriodicAura: // target, caster, spell, count, then per entry: aura type + payload
		dst, src := readPackedGUID(r), readPackedGUID(r)
		spell := u32()
		for n := u32(); n > 0 && r.Len() > 0; n-- {
			switch u32() {
			case 3, 89: // periodic damage (+ percent): damage, overkill, school, absorb, resist, crit
				dmg, over := u32(), u32()
				u32()
				u32()
				u32()
				crit, _ := r.ReadByte()
				c.add(combatEvent{T: now, Src: src, Dst: dst, Spell: spell, Amount: dmg, Over: over, Kind: "dot", Crit: crit != 0})
			case 8, 20: // periodic heal / obs mod health: heal, overheal, absorb, crit
				heal, over := u32(), u32()
				u32()
				crit, _ := r.ReadByte()
				c.add(combatEvent{T: now, Src: src, Dst: dst, Spell: spell, Amount: heal - min(heal, over), Over: over, Kind: "hot", Crit: crit != 0})
			default:
				return // energize, leech...: not tracked, and their payload sizes differ
			}
		}
	case smsgPartyKillLog: // killer, victim (sent to the killer's party)
		var killer, victim uint64
		binary.Read(r, binary.LittleEndian, &killer)
		binary.Read(r, binary.LittleEndian, &victim)
		c.add(combatEvent{T: now, Src: killer, Dst: victim, Kind: "kill"})
	}
}

func (b *Bot) setupCombatLog() {
	c := &combatLog{}
	b.clog = c
	w := b.world
	w.AddPacketHook(func(op uint16, data []byte) { c.onPacket(op, data) })
	e := b.lua

	e.Func("combat.log", "([since_ms]) → CombatEntry[]", "what a real client's combat log shows: every hit, heal and kill within ~100 yd (any source and target, party included) since then; default the last 30 s",
		func(since float64) []types.CombatEntry {
			t := int64(since)
			if t == 0 {
				t = time.Now().UnixMilli() - 30000
			}
			out := []types.CombatEntry{}
			for _, ev := range c.since(t) {
				ce := types.CombatEntry{T: ev.T, Kind: ev.Kind, Src: b.ref(ev.Src), Dst: b.ref(ev.Dst)}
				if ev.Kind != "kill" {
					ce.Amount, ce.Spell, ce.Over, ce.Crit = ev.Amount, ev.Spell, ev.Over, ev.Crit
				}
				out = append(out, ce)
			}
			return out
		})

	e.Func("combat.meter", "([window_s = 60]) → Meter[]", "per unit over the window: damage, healing, taken, healed, dps and hps (per second of its active time), kills; most damage first. Yours has by_spell: compare rotations, see what hurts you, judge a party",
		b.meter)
	e.Func("units.inspect", "(guid) → Inspect?", "what you'd see inspecting a player: class, race, level and the gear they wear with item levels (names fill in a moment after the first call); nil if out of sight",
		func(g uint64) any {
			o := w.GetObject(g)
			if o == nil || g>>48 != 0 {
				return nil
			}
			u := b.worldObjToUnitInfo(o)
			u.Name = b.unitName(g)
			out := types.Inspect{Player: b.player(u), Gear: []types.Item{}}
			sum := 0.0
			for slot := 0; slot < 19; slot++ {
				entry := o.Value(fieldVisibleItem1 + uint16(slot)*2)
				if entry == 0 {
					continue
				}
				it := itemTemplate(w, entry)
				it.Bag, it.BagSlot, it.Equipped = 255, uint8(slot), true
				if slot != 3 && slot != 18 { // shirt, tabard
					sum += float64(it.ItemLevel)
				}
				out.Gear = append(out.Gear, it)
			}
			out.AvgItemLevel = math.Round(sum/17*10) / 10 // over the 17 slots, empty = 0 (as the judge counts)
			return out
		})
}

// meter aggregates the combat log over the last window seconds (default 60).
func (b *Bot) meter(window float64) []types.Meter {
	c, w := b.clog, b.world
	if window <= 0 {
		window = 60
	}
	type agg struct {
		dmg, heal, taken, healed, hits, crits, kills uint32
		first, last                                  int64
		spells                                       map[uint32]*[3]uint32 // damage, healing, hits
	}
	me := w.CharGUID()
	units := map[uint64]*agg{}
	get := func(g uint64, t int64) *agg {
		a := units[g]
		if a == nil {
			a = &agg{first: t, spells: map[uint32]*[3]uint32{}}
			units[g] = a
		}
		a.last = t
		return a
	}
	for _, ev := range c.since(time.Now().UnixMilli() - int64(window*1000)) {
		if ev.Kind == "kill" {
			get(ev.Src, ev.T).kills++
			continue
		}
		if ev.Src != 0 {
			a := get(ev.Src, ev.T)
			a.hits++
			if ev.Crit {
				a.crits++
			}
			s := a.spells[ev.Spell]
			if s == nil {
				s = &[3]uint32{}
				a.spells[ev.Spell] = s
			}
			s[2]++
			if ev.Kind == "heal" || ev.Kind == "hot" {
				a.heal += ev.Amount
				s[1] += ev.Amount
			} else {
				a.dmg += ev.Amount
				s[0] += ev.Amount
			}
		}
		d := get(ev.Dst, ev.T)
		if ev.Kind == "heal" || ev.Kind == "hot" {
			d.healed += ev.Amount
		} else {
			d.taken += ev.Amount
		}
	}
	out := []types.Meter{}
	for g, a := range units {
		active := math.Max(1, float64(a.last-a.first)/1000)
		m := types.Meter{Unit: b.ref(g), Damage: a.dmg, Healing: a.heal, Taken: a.taken, Healed: a.healed, Kills: a.kills,
			DPS: math.Round(float64(a.dmg)/active*10) / 10, HPS: math.Round(float64(a.heal)/active*10) / 10, ActiveS: math.Round(active)}
		if a.hits > 0 {
			m.CritPct = math.Round(float64(a.crits) / float64(a.hits) * 100)
		}
		if g == me {
			for sp, s := range a.spells {
				n := "melee"
				if sp != 0 {
					n = spellName(sp)
				}
				m.BySpell = append(m.BySpell, types.SpellMeter{Spell: sp, Name: n, Damage: s[0], Healing: s[1], Hits: s[2]})
			}
			sort.Slice(m.BySpell, func(i, j int) bool {
				return m.BySpell[i].Damage+m.BySpell[i].Healing > m.BySpell[j].Damage+m.BySpell[j].Healing
			})
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Damage > out[j].Damage })
	return out
}
