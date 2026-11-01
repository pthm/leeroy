package session

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func TestObjectOpeningSpellMatchesLock(t *testing.T) {
	for kind, want := range map[string]uint32{"": spellOpening, "herbalism": 2366, "mining": 2575, "lockpicking": 1804} {
		if got, ok := openingSpell(kind, 0); !ok || got != want {
			t.Fatal(kind, got, ok)
		}
	}
	if _, ok := openingSpell("fishing", 0); ok {
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

// With the game data: the Opening spell follows the lock's type (Spell.dbc effect 33), as the client's does.
func TestOpeningSpellByLockType(t *testing.T) {
	dir, _ := filepath.Abs("../../data")
	if _, err := os.Stat(filepath.Join(dir, "dbc", "Lock.dbc")); err != nil {
		t.Skip("no game data in ../../data")
	}
	spellsDir = dir
	spellsOnce, locks.once = sync.Once{}, sync.Once{}
	// Furlbrow's Wardrobe (290, lock 93: type 12), Defias Gunpowder (17155, lock 57: types 5 and 6), no lock
	for lock, want := range map[uint32]uint32{93: 6477, 57: 3365, 0: spellOpening} {
		if got, ok := openingSpell("", lock); !ok || got != want {
			t.Errorf("lock %d: spell %d, want %d", lock, got, want)
		}
	}
}
