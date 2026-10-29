package session

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// With the game data (../../data, from the setup): a level-1 mage's starting spells count as class spells, a
// talent's effect (Chilled) doesn't, and a spell's level and mana cost come from Spell.dbc.
func TestClassSpellsAndCost(t *testing.T) {
	dir, _ := filepath.Abs("../../data")
	if _, err := os.Stat(filepath.Join(dir, "dbc", "Spell.dbc")); err != nil {
		t.Skip("no game data in ../../data")
	}
	spellsDir = dir
	// another test may have loaded the tables without the data: load them again from it
	spellsOnce, talentOnce, classSpells.once, classSpells.list = sync.Once{}, sync.Once{}, sync.Once{}, nil
	loadClassSpells()
	has := map[uint32]bool{}
	for _, c := range classSpells.list {
		if c.classMask&(1<<7) != 0 { // mage
			has[c.id] = true
		}
	}
	for id, want := range map[uint32]bool{133: true, 168: true, 116: true, 12484: false} { // Fireball, Frost Armor, Frostbolt, Chilled
		if has[id] != want {
			t.Errorf("mage class spell %d: %v, want %v", id, has[id], want)
		}
	}
	fb := spellInfo(133)
	if fb.Level != 1 || spellInfo(116).Level != 4 {
		t.Errorf("levels: Fireball %d, Frostbolt %d", fb.Level, spellInfo(116).Level)
	}
	if got := spellCost(fb, 100); got != fb.costPct || got == 0 {
		t.Errorf("Fireball cost with 100 base mana = %d (cost %d, %d%% of base)", got, fb.Cost, fb.costPct)
	}
	if got := spellCost(&spellRec{Cost: 10, costPct: 5}, 1000); got != 10 {
		t.Errorf("a flat cost wins: %d", got)
	}
}
