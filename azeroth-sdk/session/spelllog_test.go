package session

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// Packets laid out as AzerothCore writes them (Spell::SendSpellGo, Unit::SendPeriodicAuraLog,
// Spell::WriteCastResultInfo, ThreatManager::SendThreatListToClients, Unit::Kill).
func TestSpellGo(t *testing.T) {
	le := binary.LittleEndian
	const item, me, wolf, bear, imp = 0x4000000000000123, 0x2A, 0xF130000000000042, 0xF130000000000043, 0xF130000000000044
	p := append(packGUID(item), packGUID(me)...) // an item cast: the item's guid first
	p = append(p, 1)                             // cast count
	p = le.AppendUint32(p, 133)                  // spell
	p = le.AppendUint32(p, 0x100)                // flags
	p = le.AppendUint32(p, 123456)               // time
	p = append(p, 1)
	p = le.AppendUint64(p, wolf)
	p = append(p, 3)
	p = append(le.AppendUint64(p, bear), 2)    // resist
	p = append(le.AppendUint64(p, imp), 11, 0) // reflect, then what the reflection did
	p = append(le.AppendUint64(p, wolf+9), 7)  // immune
	p = le.AppendUint32(p, 2)                  // target mask (m_targets.Write)
	g := parseSpellGo(p)
	if g.caster != me || g.spell != 133 || len(g.hits) != 1 || g.hits[0] != wolf || len(g.misses) != 3 ||
		g.misses[0] != (spellMiss{bear, 2}) || g.misses[1] != (spellMiss{imp, 11}) || g.misses[2] != (spellMiss{wolf + 9, 7}) {
		t.Fatalf("spell go: %+v", g)
	}

	match := castMatch(133)
	miss := types.Cast{Spell: 133, Misses: []types.SpellMiss{{Outcome: "resist"}}}
	if o := match(script.Event{Type: "cast_done", Data: miss}); o == nil || o.OK || o.Code != "resist" {
		t.Fatalf("all missed: %+v", o)
	}
	miss.Hits = 1
	if o := match(script.Event{Type: "cast_done", Data: miss}); o == nil || !o.OK {
		t.Fatalf("one landed: %+v", o)
	}
}

func TestMeleeOutcome(t *testing.T) {
	for _, c := range []struct {
		hit   uint32
		state uint8
		want  string
	}{
		{0x10, 0, "miss"}, {0x2, 1, "hit"}, {0x202, 1, "crit"}, {0x12002, 1, "block"}, {0x10002, 1, "glancing"},
		{0x20002, 1, "crushing"}, {0x22, 1, "absorb"}, {0x82, 1, "resist"}, {0x3002, 5, "block"}, {0, 2, "dodge"},
		{0, 3, "parry"}, {0, 6, "evade"}, {0, 7, "immune"},
	} {
		if got := meleeOutcome(c.hit, c.state); got != c.want {
			t.Errorf("hitInfo %#x state %d: %s, want %s", c.hit, c.state, got, c.want)
		}
	}
}

func TestPeriodicLog(t *testing.T) {
	le := binary.LittleEndian
	head := func(aura uint32) []byte {
		p := append(packGUID(0x2A), packGUID(0xF130000000000042)...)
		p = le.AppendUint32(p, 348) // Immolate
		p = le.AppendUint32(p, 1)
		return le.AppendUint32(p, aura)
	}
	dot := head(3)
	for _, v := range []uint32{120, 5, 4, 10, 0} { // damage, overkill, school, absorb, resist
		dot = le.AppendUint32(dot, v)
	}
	dot = append(dot, 1)
	dst, src, spell, ticks := parsePeriodic(dot)
	if dst != 0x2A || src != 0xF130000000000042 || spell != 348 || len(ticks) != 1 ||
		ticks[0] != (periodicTick{kind: "dot", amount: 120, over: 5, absorb: 10, crit: true}) {
		t.Fatalf("dot: %x %x %d %+v", dst, src, spell, ticks)
	}
	en := le.AppendUint32(le.AppendUint32(head(24), 0), 300) // power type, amount
	if _, _, _, ticks := parsePeriodic(en); len(ticks) != 1 || ticks[0] != (periodicTick{kind: "energize", amount: 300}) {
		t.Fatalf("energize: %+v", ticks)
	}
}

func TestCastFailDetail(t *testing.T) {
	le := binary.LittleEndian
	item := func(e uint32) string { return fmt.Sprintf("item %d", e) }
	pkt := func(code uint8, extra ...uint32) (uint8, *bytes.Reader) {
		p := le.AppendUint32([]byte{0}, 2018) // cast count, spell
		p = append(p, code)
		for _, v := range extra {
			p = le.AppendUint32(p, v)
		}
		r := bytes.NewReader(p)
		r.Seek(6, 0)
		return p[5], r
	}
	for _, c := range []struct {
		code  uint8
		extra []uint32
		want  string
	}{
		{100, []uint32{2449}, "missing reagent: item 2449"},
		{130, []uint32{14}, "needs a tool in your bags (totem category [14])"},
		{102, []uint32{3}, "needs a spell focus nearby (SpellFocusObject 3: an anvil, forge, campfire...)"},
		{29, []uint32{2, 0x2}, "needs an equipped weapon (subclass mask 0x2)"},
		{12, nil, ""},
		{101, []uint32{0}, ""}, // requires_area, fishing_too_low: AzerothCore mostly writes 0
		{181, []uint32{0}, ""},
	} {
		code, r := pkt(c.code, c.extra...)
		if got := castFailDetail(code, r, item); got != c.want {
			t.Errorf("code %d: %q, want %q", c.code, got, c.want)
		}
	}
}

func TestThreatPct(t *testing.T) {
	le := binary.LittleEndian
	const mob, tank, me = 0xF130000000000042, 0x10, 0x2A
	ci := &combatInfo{casts: map[uint64]unitCast{}, threat: map[uint64][]threatRef{}, victim: map[uint64]uint64{}}
	p := append(packGUID(mob), packGUID(tank)...) // the new victim
	p = le.AppendUint32(p, 2)
	p = le.AppendUint32(append(p, packGUID(tank)...), 100000)
	p = le.AppendUint32(append(p, packGUID(me)...), 105000)
	ci.onPacket(smsgHighestThreat, p)
	if pct := ci.threatPct(mob, 1050); pct != 105 {
		t.Fatalf("highest: %v", pct)
	}
	p = le.AppendUint32(packGUID(mob), 2) // a plain update keeps the victim
	p = le.AppendUint32(append(p, packGUID(tank)...), 100000)
	p = le.AppendUint32(append(p, packGUID(me)...), 120000)
	ci.onPacket(smsgThreatUpdate, p)
	if pct := ci.threatPct(mob, 1200); pct != 120 || ci.threat[mob][0].guid != me {
		t.Fatalf("update: %v %+v", pct, ci.threat[mob])
	}

	ci.casts[mob] = unitCast{spell: 133, until: time.Now().Add(time.Second)}
	ci.onPacket(smsgSpellDelayed, le.AppendUint32(packGUID(mob), 500))
	if left := time.Until(ci.casts[mob].until); math.Abs(left.Seconds()-1.5) > 0.1 {
		t.Fatalf("pushback: %v", left)
	}
	s := &agentState{castSpell: 133, castUntil: time.Now().Add(time.Second)}
	s.onPacket(me, smsgSpellDelayed, le.AppendUint32(packGUID(me), 500))
	if left := time.Until(s.castUntil); math.Abs(left.Seconds()-1.5) > 0.1 {
		t.Fatalf("own pushback: %v", left)
	}
}

func TestKillingBlow(t *testing.T) {
	le := binary.LittleEndian
	const tapper, killer, mob = 0x10, 0x11, 0xF130000000000042
	c := &combatLog{}
	now := time.Now().UnixMilli()
	c.add(combatEvent{T: now, Src: tapper, Dst: mob, Amount: 50, Kind: "melee"})
	c.add(combatEvent{T: now, Src: killer, Dst: mob, Amount: 30, Kind: "dot"})
	c.add(combatEvent{T: now, Src: 0x12, Dst: mob, Amount: 30, Kind: "heal"})
	c.onPacket(smsgPartyKillLog, le.AppendUint64(le.AppendUint64(nil, tapper), mob))
	if e := c.evs[len(c.evs)-1]; e.Kind != "kill" || e.Src != killer {
		t.Fatalf("kill: %+v", e)
	}
	if c.killingBlow(0xF130000000000099, tapper, nil) != tapper {
		t.Fatal("no hits seen: the credited player")
	}
	// party_kill: the last party member to hurt it, not a guard after them nor a member's 0-damage miss
	const mob2, guard = 0xF130000000000043, 0xF130000000000044
	c.add(combatEvent{T: now, Src: killer, Dst: mob2, Amount: 40, Kind: "spell"})
	c.add(combatEvent{T: now, Src: tapper, Dst: mob2, Kind: "melee"})
	c.add(combatEvent{T: now, Src: guard, Dst: mob2, Amount: 90, Kind: "melee"})
	party := func(g uint64) bool { return g == tapper || g == killer }
	if got := c.killingBlow(mob2, tapper, party); got != killer {
		t.Fatalf("party killing blow: %#x", got)
	}
}
