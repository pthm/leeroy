package session

import (
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
	"testing"
)

func TestObjectOpeningSpellMatchesLock(t *testing.T) {
	for kind, want := range map[string]uint32{"": spellOpening, "herbalism": 2366, "mining": 2575, "lockpicking": 1804} {
		if got, ok := openingSpell(kind); !ok || got != want {
			t.Fatal(kind, got, ok)
		}
	}
	if _, ok := openingSpell("fishing"); ok {
		t.Fatal("fishing pool treated as ordinary chest")
	}
	match := objectCastMatch(42, 2366, 0)
	if match(script.Event{Type: "cast_done", Data: types.Cast{Spell: 133}}) != nil {
		t.Fatal("unrelated cast matched")
	}
	out := match(script.Event{Type: "cast_done", Data: types.Cast{Spell: 2366}})
	d, ok := out.Data.(types.ObjectActivation)
	if !out.OK || !ok || d.Kind != "cast" || d.Object != 42 || d.Cast.Spell != 2366 {
		t.Fatal(out)
	}
}
