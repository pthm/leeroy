package session

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/pthm/leeroy/azsdk/client"
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
	me := &client.WorldObject{Values: map[uint16]uint32{unitFieldBaseMana: 100}}
	if got := spellCost(fb, me, nil); got != fb.costPct || got == 0 {
		t.Errorf("Fireball cost with 100 base mana = %d (cost %d, %d%% of base)", got, fb.Cost, fb.costPct)
	}
	if got := spellCost(&spellRec{Cost: 10, costPct: 5}, &client.WorldObject{Values: map[uint16]uint32{unitFieldBaseMana: 1000}}, nil); got != 60 {
		t.Errorf("flat cost plus 5%% of 1000 base mana: %d", got)
	}

	// Improved Sinister Strike rank 2: the server sends -5 on Sinister Strike's family bit 1 (talent mask 0x2), and
	// the cast costs 40 energy, not 45
	ss, s := spellInfo(1752), &agentState{mods: map[spellMod]int32{}}
	s.onPacket(1, smsgSetFlatSpellMod, []byte{1, spellModCost, 0xFB, 0xFF, 0xFF, 0xFF})
	if got := spellCost(ss, nil, s); got != 40 {
		t.Errorf("Sinister Strike with Improved Sinister Strike: %d, want 40", got)
	}
	if got := spellCost(spellInfo(78), nil, s); got != 150 {
		t.Errorf("a rogue's mod on bit 1 changed Heroic Strike (bit 6): %d", got)
	}
	// a percent mod (Clearcasting, -100%) and the school's cost fields
	s.onPacket(1, smsgSetPctSpellMod, []byte{23, spellModCost, 0x9C, 0xFF, 0xFF, 0xFF})
	if got := spellCost(ss, nil, s); got != 0 {
		t.Errorf("-100%%: %d", got)
	}
	s.mods = map[spellMod]int32{}
	me = &client.WorldObject{Values: map[uint16]uint32{unitFieldPowerCostMod: 0xFFFFFFFE, unitFieldPowerCostMult: math.Float32bits(-0.5)}}
	if got := spellCost(ss, me, s); got != 21 { // (45 - 2) x 0.5, physical school
		t.Errorf("school modifier and multiplier: %d, want 21", got)
	}
}

// With the game data: spells.check asks for facing only of spells whose FacingCasterFlags say so, as the server does
func TestSpellFacingFlag(t *testing.T) {
	dir, _ := filepath.Abs("../../data")
	if _, err := os.Stat(filepath.Join(dir, "dbc", "Spell.dbc")); err != nil {
		t.Skip("no game data in ../../data")
	}
	spellsDir = dir
	spellsOnce = sync.Once{}
	for id, want := range map[uint32]uint32{2050: 0, 635: 0, 355: 0, 1243: 0, 585: 1, 78: 1, 133: 1} { // Lesser Heal, Holy Light, Taunt, Fortitude; Smite, Heroic Strike, Fireball
		if got := spellInfo(id).facing & 1; got != want {
			t.Errorf("spell %d: facing %d, want %d", id, got, want)
		}
	}
}

// With the game data: control auras name the spell's mechanic and whether damage breaks them (AuraInterruptFlags)
func TestControls(t *testing.T) {
	dir, _ := filepath.Abs("../../data")
	if _, err := os.Stat(filepath.Join(dir, "dbc", "Spell.dbc")); err != nil {
		t.Skip("no game data in ../../data")
	}
	spellsDir, spellsOnce = dir, sync.Once{}
	now := time.Now()
	auras := []client.AuraInfo{{Spell: 118, Caster: 7, Flags: 0x20, MaxDurationMS: 20000, DurationMS: 15000, At: now}, // Polymorph
		{Spell: 6770}, {Spell: 122}, {Spell: 853}, {Spell: 1243}} // Sap, Frost Nova, Hammer of Justice; Power Word: Fortitude isn't one
	cs := controls(auras)
	got := make([]string, 0, len(cs))
	for _, c := range cs {
		got = append(got, fmt.Sprintf("%d %s %v %v %d %v", c.Spell, c.Mechanic, c.BreaksOnDamage, c.DamageLimit, c.CasterGUID, c.RemainingMS != nil))
	}
	want := []string{"118 polymorph true false 7 true", "6770 sap true true 0 false", "122 root false true 0 false", "853 stun false false 0 false"}
	if !slices.Equal(got, want) {
		t.Fatalf("controls: %q, want %q", got, want)
	}
}
