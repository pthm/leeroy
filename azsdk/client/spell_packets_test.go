package client

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

// Player low guids pack to 2-3 bytes, so these packets are shorter than the old length checks allowed.
func TestPowerUpdateAndSpellFailurePackedGUID(t *testing.T) {
	le := binary.LittleEndian
	w := NewWorldClient("t", nil, nil)
	w.charGUID = 5
	guid := new(bytes.Buffer)
	writePackedGUID(guid, w.charGUID)

	w.handlePowerUpdate(le.AppendUint32(append(guid.Bytes(), 0), 321)) // Unit::SetPower: guid, mana, value
	if cur, _ := w.Power(); cur != 321 {
		t.Fatalf("power=%d", cur)
	}

	var got []uint8
	w.AddSpellCastResultHook(func(spell uint32, ok bool, reason uint8) {
		if spell == 133 && !ok {
			got = append(got, reason)
		}
	})
	// Spell::SendInterrupted: guid, cast count, spell, reason (40 = INTERRUPTED)
	w.handleSpellFailure(append(le.AppendUint32(append(guid.Bytes(), 1), 133), 40))
	if len(got) != 1 || got[0] != 40 {
		t.Fatalf("interrupt hooks=%v", got)
	}
}

func TestCooldownPackets(t *testing.T) {
	le := binary.LittleEndian
	w := NewWorldClient("t", nil, nil)
	w.charGUID = 5
	left := func(spell uint32) time.Duration {
		cd, ok := w.cooldowns[spell]
		if !ok {
			return 0
		}
		return time.Until(cd.ExpiresAt).Round(time.Second)
	}

	// Player::SendInitialSpells: spec, count, (spell, 0), cooldown count, (spell, item, category, rec, catrec)
	p := le.AppendUint16([]byte{0}, 1)
	p = le.AppendUint16(le.AppendUint32(p, 2061), 0)
	p = le.AppendUint16(p, 3) // counts an entry it skipped
	cd := func(spell uint32, item, cat uint16, rec, catrec uint32) {
		p = le.AppendUint32(le.AppendUint32(le.AppendUint16(le.AppendUint16(le.AppendUint32(p, spell), item), cat), rec), catrec)
	}
	cd(2061, 0, 0, 6000, 0)
	cd(441, 118, 4, 0, 0x80000000) // potion used in combat: infinite until the event
	w.knownSpells[2061] = &KnownSpell{SpellID: 2061, Active: true}
	w.handleInitialSpells(p)
	if left(2061) != 6*time.Second || left(441) < time.Hour || w.IsSpellReady(2061) {
		t.Fatalf("initial: %v %v", left(2061), left(441))
	}

	// SMSG_MODIFY_COOLDOWN: spell, player, -2000 ms
	w.handleModifyCooldown(le.AppendUint32(le.AppendUint64(le.AppendUint32(nil, 2061), 5), uint32(0xFFFFFFFF-1999)))
	if left(2061) != 4*time.Second {
		t.Fatalf("modified: %v", left(2061))
	}
	w.handleModifyCooldown(le.AppendUint32(le.AppendUint64(le.AppendUint32(nil, 2061), 6), 10000)) // not ours
	if left(2061) != 4*time.Second {
		t.Fatalf("other player's modify applied: %v", left(2061))
	}

	// SMSG_ITEM_COOLDOWN: item guid, spell
	w.handleItemCooldown(le.AppendUint32(le.AppendUint64(nil, 0x4000000000000123), 33017))
	if left(33017) != 30*time.Second {
		t.Fatalf("item: %v", left(33017))
	}
}
