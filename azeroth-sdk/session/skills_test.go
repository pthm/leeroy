package session

import (
	"encoding/binary"
	"testing"

	"github.com/pthm/leeroy/azeroth-sdk/client"
)

func TestSkillFieldsExcludeBonusesAndEmptySlots(t *testing.T) {
	me := &client.WorldObject{Values: map[uint16]uint32{
		fieldSkillInfo: 186 | 2<<16, fieldSkillInfo + 1: 37 | 150<<16, fieldSkillInfo + 2: 5 | 10<<16,
		fieldSkillInfo + 3*127: 43, fieldSkillInfo + 3*127 + 1: 21 | 25<<16,
	}}
	got := skillsOf(me, map[uint32]skillLine{186: {"Mining", 11}})
	if len(got) != 2 || got[0].ID != 43 || got[0].MetadataKnown || got[1].Value != 37 || got[1].Max != 150 || got[1].Name != "Mining" || got[1].Category != 11 || !got[1].MetadataKnown {
		t.Fatalf("skills: %+v", got)
	}
	if got := skillsOf(nil, nil); got == nil || len(got) != 0 {
		t.Fatal("absent snapshot must return an empty array")
	}
}

func TestSkillMetadataBoundsAndLocales(t *testing.T) {
	le := binary.LittleEndian
	b := make([]byte, 20+19*4)
	copy(b, "WDBC")
	le.PutUint32(b[4:], 1)
	le.PutUint32(b[8:], 19)
	le.PutUint32(b[12:], 19*4)
	le.PutUint32(b[20:], 186)
	le.PutUint32(b[24:], 11)
	le.PutUint32(b[20+4*4:], 1) // fallback locale
	strings := []byte("\x00Mining\x00")
	le.PutUint32(b[16:], uint32(len(strings)))
	b = append(b, strings...)
	if got := parseSkillLines(b)[186]; got.name != "Mining" || got.category != 11 {
		t.Fatalf("metadata: %+v", got)
	}
	for _, bad := range [][]byte{nil, b[:19], b[:len(b)-1]} {
		if len(parseSkillLines(bad)) != 0 {
			t.Fatal("truncated DBC accepted")
		}
	}
	le.PutUint32(b[4:], 0xFFFFFFFF)
	if len(parseSkillLines(b)) != 0 {
		t.Fatal("overflowed record count accepted")
	}
}

// me.stance reads the form byte of UNIT_FIELD_BYTES_2 (sheath and PvP flags in the low bytes don't count)
func TestStanceFromBytes2(t *testing.T) {
	for v, want := range map[uint32]any{0: nil, 0x0101: nil, 18<<24 | 0x0101: "defensive_stance", 5 << 24: "bear", 99 << 24: "99"} {
		if got := stanceOf(&client.WorldObject{Values: map[uint16]uint32{client.UnitFieldBytes2: v}}); got != want {
			t.Errorf("%#x: %v, want %v", v, got, want)
		}
	}
	if stanceOf(nil) != nil {
		t.Error("no character: a form")
	}
}
