package run

// A character's kit as the run left it: equipped gear, talents and trained abilities, from the characters database.
// The harness saves it to <run>/<agent>/kit.json when a player's run ends (characters can be deleted or reused by
// later runs); azbench publish puts it on the website.

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/pthm/leeroy/azworld/world"
)

// passiveSpells: the spells Spell.dbc marks passive (SPELL_ATTR0_PASSIVE), read once.
var passiveSpells = sync.OnceValue(func() map[int]bool {
	out := map[int]bool{}
	b, err := os.ReadFile(filepath.Join(world.DataDir(), "dbc", "Spell.dbc"))
	if err != nil || len(b) < 20 || string(b[:4]) != "WDBC" {
		return out
	}
	n, size := int(binary.LittleEndian.Uint32(b[4:])), int(binary.LittleEndian.Uint32(b[12:]))
	for i := 0; i < n && 20+(i+1)*size <= len(b); i++ {
		rec := b[20+i*size:]
		if binary.LittleEndian.Uint32(rec[16:])&0x40 != 0 { // field 4: Attributes
			out[int(binary.LittleEndian.Uint32(rec))] = true
		}
	}
	return out
})

// Kit is a character's gear, talents and abilities.
type Kit struct {
	Gear      []KitItem  `json:"gear"`
	Talents   []KitSpell `json:"talents"`
	Abilities []KitSpell `json:"abilities"` // trained spells that aren't passive
}

// KitItem is an equipped item.
type KitItem struct {
	Slot    int    `json:"slot"` // 0 head ... 18 tabard
	Entry   int    `json:"entry"`
	Name    string `json:"name"`
	Quality int    `json:"quality"`        // 0 poor ... 4 epic
	Level   int    `json:"level"`          // item level
	Icon    string `json:"icon,omitempty"` // added by azbench publish
}

// KitSpell is a talent or an ability.
type KitSpell struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Rank string `json:"rank,omitempty"`
	Icon string `json:"icon,omitempty"` // added by azbench publish
}

// CharKit reads char's kit from the characters database.
func CharKit(char string) (*Kit, error) {
	db := world.DB(world.Admin, "acore_characters")
	guid := "(SELECT guid FROM characters WHERE name = ?)"
	k := &Kit{Gear: []KitItem{}, Talents: []KitSpell{}, Abilities: []KitSpell{}}
	rows, err := world.Rows(db, `SELECT ci.slot, it.entry, it.name, it.Quality, it.ItemLevel FROM character_inventory ci
  JOIN item_instance ii ON ii.guid = ci.item JOIN acore_world.item_template it ON it.entry = ii.itemEntry
  WHERE ci.guid = `+guid+` AND ci.bag = 0 AND ci.slot < 19 ORDER BY ci.slot`, char)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		k.Gear = append(k.Gear, KitItem{Slot: atoi(r[0]), Entry: atoi(r[1]), Name: r[2], Quality: atoi(r[3]), Level: atoi(r[4])})
	}
	spells := func(q string) ([]KitSpell, error) {
		rows, err := world.Rows(db, q, char)
		if err != nil {
			return nil, err
		}
		out := []KitSpell{}
		for _, r := range rows {
			out = append(out, KitSpell{ID: atoi(r[0]), Name: r[1], Rank: r[2]})
		}
		return out, nil
	}
	if k.Talents, err = spells(`SELECT s.id, s.name, COALESCE(s.rank, '') FROM character_talent t JOIN wowbench.spell s ON s.id = t.spell
  WHERE t.guid = ` + guid + ` ORDER BY s.name`); err != nil {
		return nil, err
	}
	all, err := spells(`SELECT s.id, s.name, COALESCE(s.rank, '') FROM character_spell c JOIN wowbench.spell s ON s.id = c.spell
  WHERE c.guid = ` + guid + ` ORDER BY s.name`)
	if err != nil {
		return nil, err
	}
	passive := passiveSpells()
	for _, sp := range all { // the ones you cast: not passive (proficiencies, languages, auras that are always on)
		if !passive[sp.ID] && sp.Name != "" {
			k.Abilities = append(k.Abilities, sp)
		}
	}
	return k, nil
}

// saveKit writes the character's kit into the run, for the website (a failure only loses the kit).
func (p *player) saveKit() {
	k, err := CharKit(p.a.Char)
	if err != nil {
		p.logf("kit: %v", err)
		return
	}
	b, _ := json.Marshal(k)
	os.WriteFile(filepath.Join(p.dir, "kit.json"), b, 0o644)
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
