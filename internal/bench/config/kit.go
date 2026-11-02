package config

import (
	"fmt"
	"path/filepath"
	"slices"
)

// Kit is a character's starting state beyond level 1: its level, money, trained spells, gear and bags
// (bench/kits/<name>.yaml; azbench kit gen makes one from the game's own items, azbench kit save copies a character).
// A party member takes one with kit: <name>; the harness applies it while the character is offline, and the run's
// scenario.yaml keeps it as applied. Talents stay unspent: the server gives the points for the level.
type Kit struct {
	Level   int            `yaml:"level"`
	Money   int            `yaml:"money"`   // copper
	Trained bool           `yaml:"trained"` // every spell the class trainers teach and class quests reward up to the level, and riding
	Spells  []int          `yaml:"spells"`  // more spells, by id: weapon proficiencies, or every spell for a saved kit
	Gear    map[string]int `yaml:"gear"`    // slot (Slots) -> item entry
	Bags    []int          `yaml:"bags"`    // up to 4, in the bag slots
	Items   []Stack        `yaml:"items"`   // in the backpack, then the bags
	Pet     *Pet           `yaml:"pet"`     // a hunter's tamed beast, at the kit's level
}

// Pet is a tamed beast: a creature a hunter can tame (creature_template entry), and its name.
type Pet struct {
	Entry int    `yaml:"entry"`
	Name  string `yaml:"name"`
}

// Stack is a number of one item.
type Stack struct {
	Entry int `yaml:"entry"`
	Count int `yaml:"count"`
}

// Slots: the equipment slots, in the game's order (slot 0 is head).
var Slots = []string{"head", "neck", "shoulders", "shirt", "chest", "waist", "legs", "feet", "wrists", "hands",
	"finger1", "finger2", "trinket1", "trinket2", "back", "main_hand", "off_hand", "ranged", "tabard"}

// LoadKit reads bench/kits/<name>.yaml.
func (b *Bench) LoadKit(name string) (*Kit, error) {
	var k Kit
	if err := readYAML(filepath.Join(b.Dir, "kits", name+".yaml"), &k); err != nil {
		return nil, err
	}
	if k.Level < 1 || k.Level > 80 {
		return nil, fmt.Errorf("kit %s: level %d: 1 to 80", name, k.Level)
	}
	for s := range k.Gear {
		if !slices.Contains(Slots, s) {
			return nil, fmt.Errorf("kit %s: slot %q (%v)", name, s, Slots)
		}
	}
	if len(k.Bags) > 4 {
		return nil, fmt.Errorf("kit %s: %d bags, 4 at most", name, len(k.Bags))
	}
	return &k, nil
}
