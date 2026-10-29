package session

import (
	"encoding/binary"
	"testing"
	"time"
)

// Cooldown packets as AzerothCore writes them (Player::SendInitialSpells, SendCooldownEvent, ModifySpellCooldown,
// SendClearCooldown, Unit::BuildCooldownPacket, Player::ApplyEquipCooldown).
func TestCooldowns(t *testing.T) {
	le := binary.LittleEndian
	const me, potion, hearth = uint64(0x2A), uint64(0x4000000000000077), uint32(8690)
	s := &agentState{cd: map[uint32]time.Time{}, catCD: map[uint32]time.Time{}, onEvent: map[uint32]cdRule{},
		pushed: map[uint32]time.Time{}}
	s.itemCD = func(item uint64, entry, spell uint32) (cdRule, bool) {
		return cdRule{rec: -1, cat: 4, catrec: 120000, onEvent: true}, (item == potion || entry == 858) && spell == 439
	}
	near := func(what string, got time.Duration, want float64) {
		t.Helper()
		if ms := cdMS(got); ms != want && (want <= 0 || ms < want-50 || ms > want) {
			t.Errorf("%s: %v ms, want %v", what, ms, want)
		}
	}
	ms := func(spell uint32) time.Duration { return s.itemCooldownLeft(spell, 0) }

	// login: one spell, then cooldowns, counted with one the server skipped
	p := le.AppendUint16([]byte{0}, 1)
	p = le.AppendUint16(le.AppendUint32(p, 6603), 0)
	p = le.AppendUint16(p, 3)
	p = le.AppendUint16(le.AppendUint16(le.AppendUint32(p, hearth), 6948), 0)
	p = le.AppendUint32(le.AppendUint32(p, 900000), 0)
	p = le.AppendUint16(le.AppendUint16(le.AppendUint32(p, 2370), 858), 4) // a potion used in combat: infinite
	p = le.AppendUint32(le.AppendUint32(p, 1), 0x80000000)
	s.onPacket(me, smsgInitialSpells, p)
	near("hearthstone after relog", ms(hearth), 900000)
	near("potion waiting since login", ms(2370), -1)
	s.onPacket(me, smsgCooldownEvent, le.AppendUint64(le.AppendUint32(nil, 2370), me))
	near("event after login (no Spell.dbc)", ms(2370), 0)

	// a potion: blocked until the event, then its item category cooldown
	go1 := append(append(packGUID(potion), packGUID(me)...), 1)
	go1 = le.AppendUint32(le.AppendUint32(go1, 439), 0)
	s.onPacket(me, smsgSpellGo, go1)
	near("potion in combat", s.itemCooldownLeft(439, 4), -1)
	s.onPacket(me, smsgCooldownEvent, le.AppendUint64(le.AppendUint32(nil, 9999), me))
	near("event for nothing waiting", ms(9999), 0)
	s.onPacket(me, smsgCooldownEvent, le.AppendUint64(le.AppendUint32(nil, 439), me))
	near("potion out of combat", s.itemCooldownLeft(439, 4), 120000)
	near("another potion (same category)", s.itemCooldownLeft(441, 4), 120000)

	// SMSG_SPELL_COOLDOWN comes before the SPELL_GO and wins; then modified and cleared
	p = append(le.AppendUint64(nil, me), 0)
	s.onPacket(me, smsgSpellCooldown, le.AppendUint32(le.AppendUint32(p, 100), 9000))
	go2 := append(append(packGUID(me), packGUID(me)...), 1)
	s.onPacket(me, smsgSpellGo, le.AppendUint32(le.AppendUint32(go2, 100), 0))
	near("pushed cooldown", ms(100), 9000)
	p = le.AppendUint64(le.AppendUint32(nil, 100), me)
	s.onPacket(me, smsgModifyCooldown, le.AppendUint32(p, uint32(0xFFFFEC78))) // -5000
	near("modified", ms(100), 4000)
	s.onPacket(me, smsgClearCooldown, le.AppendUint64(le.AppendUint32(nil, 100), me))
	near("cleared", ms(100), 0)

	s.onPacket(me, smsgItemCooldown, le.AppendUint32(le.AppendUint64(nil, 0x4000000000000088), 23723))
	near("trinket just equipped", ms(23723), 30000)

	// casting: a second spell refused while casting doesn't end the cast; the cast's own failure does
	s.castSpell, s.castUntil = 133, time.Now().Add(time.Second)
	s.onPacket(me, smsgCastFailed, append(le.AppendUint32([]byte{2}, 5176), 0x18)) // spell_in_progress
	if s.castSpell != 133 {
		t.Errorf("cast ended by another spell's failure")
	}
	s.onPacket(me, smsgSpellFailure, append(le.AppendUint32(append(packGUID(me), 1), 133), 0x28))
	if s.castSpell != 0 {
		t.Errorf("interrupted cast still casting")
	}
}

// SMSG_INITIAL_SPELLS on login and on every far teleport: the cast spell comes with category 0, a category sibling
// with its own cooldown with its category (Player::AddSpellAndCategoryCooldowns); categories come back too.
func TestCooldownsRestored(t *testing.T) {
	le := binary.LittleEndian
	const me = uint64(0x2A)
	s := &agentState{cd: map[uint32]time.Time{}, catCD: map[uint32]time.Time{}, onEvent: map[uint32]cdRule{},
		pushed: map[uint32]time.Time{}}
	s.itemCD = func(_ uint64, entry, spell uint32) (cdRule, bool) {
		switch {
		case entry == 858 && spell == 439: // a potion
			return cdRule{rec: -1, cat: 4, catrec: 120000, onEvent: true}, true
		case entry == 5512 && spell == 6262: // a healthstone
			return cdRule{rec: -1, cat: 30, catrec: 120000}, true
		}
		return cdRule{}, false
	}
	near := func(what string, got time.Duration, want float64) {
		t.Helper()
		if ms := cdMS(got); ms != want && (want <= 0 || ms < want-50 || ms > want) {
			t.Errorf("%s: %v ms, want %v", what, ms, want)
		}
	}
	p := le.AppendUint16([]byte{0}, 1)
	p = le.AppendUint16(le.AppendUint32(p, 6603), 0)
	p = le.AppendUint16(p, 3)
	p = le.AppendUint16(le.AppendUint16(le.AppendUint32(p, 439), 858), 0) // used in combat: waiting
	p = le.AppendUint32(le.AppendUint32(p, 1), 0x80000000)
	p = le.AppendUint16(le.AppendUint16(le.AppendUint32(p, 6262), 5512), 0)
	p = le.AppendUint32(le.AppendUint32(p, 60000), 0)
	p = le.AppendUint16(le.AppendUint16(le.AppendUint32(p, 5000), 0), 77) // a sibling
	p = le.AppendUint32(le.AppendUint32(p, 0), 30000)
	s.onPacket(me, smsgInitialSpells, p)
	near("waiting potion", s.itemCooldownLeft(439, 4), -1)
	near("another potion while one waits", s.itemCooldownLeft(441, 4), -1)
	near("healthstone", s.itemCooldownLeft(6262, 30), 60000)
	near("another healthstone", s.itemCooldownLeft(23468, 30), 60000)
	near("sibling's category", s.itemCooldownLeft(5001, 77), 30000)

	s.itemCD = nil // a map change: the wait keeps its rule though the item isn't looked up
	s.onPacket(me, smsgInitialSpells, p)
	s.onPacket(me, smsgCooldownEvent, le.AppendUint64(le.AppendUint32(nil, 439), me))
	near("another potion after the event", s.itemCooldownLeft(441, 4), 120000)
}

// A potion's on-use spell in SMSG_ITEM_QUERY_SINGLE_RESPONSE: item_template's cooldown, category and category cooldown.
func TestItemQueryCooldown(t *testing.T) {
	le := binary.LittleEndian
	u := func(b []byte, v ...uint32) []byte {
		for _, x := range v {
			b = le.AppendUint32(b, x)
		}
		return b
	}
	p := u(nil, 90118, 0, 1, 0) // entry, consumable, potion, sound override
	p = append(p, "Minor Healing Potion\x00\x00\x00\x00"...)
	p = u(p, 1, 1, 0, 0, 25, 1, 0, 0xFFFFFFFF, 0xFFFFFFFF, 5, 1)
	p = u(p, 0, 0, 0, 0, 0, 0, 0, 0)
	p = u(p, 5, 0, 0, 0, 0)    // stack, container slots, no stats, scaling
	p = u(p, 0, 0, 0, 0, 0, 0) // damage
	p = u(p, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	p = u(p, 439, 0, 0xFFFFFFFF, 0xFFFFFFFF, 4, 120000) // on use, charges -1, no own cooldown, category 4: 2 min
	for i := 0; i < 4; i++ {
		p = u(p, 0, 0, 0, 0xFFFFFFFF, 0, 0xFFFFFFFF)
	}
	p = append(u(p, 0), 0)
	p = u(p, 0, 0, 0, 0, 0)
	parseItemQuery(p)
	items.Lock()
	it := items.proto[90118]
	items.Unlock()
	if it == nil || it.UseSpell != 439 || it.useCD != (cdRule{rec: -1, cat: 4, catrec: 120000, onEvent: true}) {
		t.Fatalf("%+v", it)
	}
}
