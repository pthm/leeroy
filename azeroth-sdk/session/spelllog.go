package session

// Spell and combat log packets parsed once, as AzerothCore writes them, for the events, the combat log and the
// cast state alike.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

const (
	smsgSpellEnergizeLog = 0x151
	smsgSpellDelayed     = 0x1E2
	smsgDamageShield     = 0x24F
	smsgProcResist       = 0x260
	smsgSpellImmune      = 0x263 // SMSG_SPELLORDAMAGE_IMMUNE
	smsgDispelLog        = 0x27B
	smsgInstakillLog     = 0x32F
	smsgStealLog         = 0x333
)

// SpellMissInfo
var missReasons = map[uint8]string{1: "miss", 2: "resist", 3: "dodge", 4: "parry", 5: "block", 6: "evade", 7: "immune",
	8: "immune", 9: "deflect", 10: "absorb", 11: "reflect"}

// meleeOutcome: SMSG_ATTACKERSTATEUPDATE's HITINFO_* flags and VICTIMSTATE_* (Unit::CalculateMeleeDamage).
func meleeOutcome(hitInfo uint32, state uint8) string {
	switch state {
	case 0:
		return "miss"
	case 1: // a hit, possibly partly blocked, glancing, crushing, or wholly absorbed or resisted
		switch {
		case hitInfo&0x20 != 0: // HITINFO_FULL_ABSORB
			return "absorb"
		case hitInfo&0x80 != 0: // HITINFO_FULL_RESIST
			return "resist"
		case hitInfo&0x2000 != 0: // HITINFO_BLOCK
			return "block"
		case hitInfo&0x200 != 0:
			return "crit"
		case hitInfo&0x10000 != 0:
			return "glancing"
		case hitInfo&0x20000 != 0:
			return "crushing"
		}
		return "hit"
	}
	return map[uint8]string{2: "dodge", 3: "parry", 4: "interrupt", 5: "block", 6: "evade", 7: "immune", 8: "deflect"}[state]
}

type spellMiss struct {
	guid   uint64
	reason uint8
}

type spellGo struct {
	caster uint64
	spell  uint32
	hits   []uint64
	misses []spellMiss
}

// parseSpellGo: Spell::SendSpellGo: caster item (the caster when no item), caster, cast count, spell, flags, time,
// then WriteSpellGoTargets: hit count + guids, miss count + (guid, SpellMissInfo [, reflect result]).
func parseSpellGo(data []byte) (g spellGo) {
	r := bytes.NewReader(data)
	readPackedGUID(r)
	g.caster = readPackedGUID(r)
	r.ReadByte()
	var v [3]uint32 // spell, flags, time
	binary.Read(r, binary.LittleEndian, &v)
	g.spell = v[0]
	n, _ := r.ReadByte()
	for ; n > 0 && r.Len() >= 8; n-- {
		var t uint64
		binary.Read(r, binary.LittleEndian, &t)
		g.hits = append(g.hits, t)
	}
	n, _ = r.ReadByte()
	for ; n > 0 && r.Len() >= 9; n-- {
		var m spellMiss
		binary.Read(r, binary.LittleEndian, &m.guid)
		m.reason, _ = r.ReadByte()
		if m.reason == 11 { // SPELL_MISS_REFLECT: what the reflected spell did to its caster
			r.ReadByte()
		}
		g.misses = append(g.misses, m)
	}
	return
}

type periodicTick struct {
	kind                string // dot, hot, energize
	amount, over, power uint32
	absorb, resist      uint32
	crit                bool
}

// parsePeriodic: Unit::SendPeriodicAuraLog: target, caster (packed), spell, count, then per entry the aura type and
// its payload. Leech and unknown types end it (their payloads aren't tracked).
func parsePeriodic(data []byte) (dst, src uint64, spell uint32, ticks []periodicTick) {
	r := bytes.NewReader(data)
	u32 := func() (v uint32) { binary.Read(r, binary.LittleEndian, &v); return }
	dst, src = readPackedGUID(r), readPackedGUID(r)
	spell = u32()
	for n := u32(); n > 0 && r.Len() > 0; n-- {
		var t periodicTick
		switch u32() {
		case 3, 89: // periodic damage (+ percent): damage, overkill, school, absorb, resist, crit
			t.kind, t.amount, t.over = "dot", u32(), u32()
			u32()
			t.absorb, t.resist = u32(), u32()
		case 8, 20: // periodic heal / obs mod health: heal, overheal, absorb, crit
			t.kind, t.amount, t.over = "hot", u32(), u32()
			t.absorb = u32()
		case 21, 24: // obs mod power / periodic energize: power type, amount
			t.kind, t.power, t.amount = "energize", u32(), u32()
			ticks = append(ticks, t)
			continue
		default:
			return
		}
		c, _ := r.ReadByte()
		t.crit = c != 0
		ticks = append(ticks, t)
	}
	return
}

// castFailDetail: the extra data Spell::WriteCastResultInfo appends to SMSG_CAST_FAILED for some results.
func castFailDetail(code uint8, r *bytes.Reader, item func(uint32) string) string {
	var v []uint32
	for r.Len() >= 4 {
		var x uint32
		binary.Read(r, binary.LittleEndian, &x)
		v = append(v, x)
	}
	if len(v) == 0 {
		return ""
	}
	items := func() string {
		var s []string
		for _, e := range v {
			s = append(s, item(e))
		}
		return strings.Join(s, ", ")
	}
	switch castResultNames[code] {
	case "requires_spell_focus":
		return fmt.Sprintf("needs a spell focus nearby (SpellFocusObject %d: an anvil, forge, campfire...)", v[0])
	case "requires_area": // 0 but for a few spells (Spell::WriteCastResultInfo)
		if v[0] == 0 {
			return ""
		}
		if n := areaNames()[v[0]]; n != "" {
			return "only in " + n
		}
		return fmt.Sprintf("only in area %d", v[0])
	case "totems":
		return "needs in your bags: " + items()
	case "totem_category":
		return fmt.Sprintf("needs a tool in your bags (totem category %v)", v)
	case "equipped_item_class", "equipped_item_class_mainhand", "equipped_item_class_offhand":
		if len(v) == 2 {
			return fmt.Sprintf("needs an equipped %s (subclass mask 0x%x)", name(itemClassNames, v[0]), v[1])
		}
	case "reagents":
		if v[0] != 0 {
			return "missing reagent: " + items()
		}
	case "too_many_of_item":
		return fmt.Sprintf("you carry the most allowed (item limit category %d)", v[0])
	case "prevented_by_mechanic":
		return fmt.Sprintf("prevented by mechanic %d", v[0])
	case "custom_error":
		return fmt.Sprintf("custom error %d", v[0])
	case "fishing_too_low":
		if v[0] == 0 { // what AzerothCore always sends
			return ""
		}
		return fmt.Sprintf("needs fishing skill %d", v[0])
	}
	return ""
}
