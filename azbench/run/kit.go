package run

// A character's kit as the run left it: equipped gear, talents and trained abilities, from the characters database.
// The harness saves it to <run>/<agent>/kit.json when a player's run ends (characters can be deleted or reused by
// later runs); azbench publish puts it on the website.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/pthm/leeroy/azbench/config"

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

// learnedBy: what each spell teaches (Spell.dbc effects 71-73 of type 36, learn spell, the taught spell in fields
// 116-118), read once. Class quests reward a spell like this: the warrior's level-10 quest teaches Defensive Stance,
// Sunder Armor and Taunt.
var learnedBy = sync.OnceValue(func() map[int][]int {
	out := map[int][]int{}
	b, err := os.ReadFile(filepath.Join(world.DataDir(), "dbc", "Spell.dbc"))
	if err != nil || len(b) < 20 || string(b[:4]) != "WDBC" {
		return out
	}
	n, size := int(binary.LittleEndian.Uint32(b[4:])), int(binary.LittleEndian.Uint32(b[12:]))
	field := func(rec []byte, i int) int { return int(binary.LittleEndian.Uint32(rec[i*4:])) }
	for i := 0; i < n && 20+(i+1)*size <= len(b); i++ {
		rec := b[20+i*size:]
		for e := range 3 {
			if field(rec, 71+e) == 36 && field(rec, 116+e) > 0 {
				out[field(rec, 0)] = append(out[field(rec, 0)], field(rec, 116+e))
			}
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

// skills that rise with level (5 per level): the weapon skills and Defense; a kit puts them at their cap, as playing
// to that level would
var levelSkills = []int{43, 44, 45, 46, 54, 55, 95, 136, 160, 162, 172, 173, 176, 226, 228, 229, 473}

// skills the proficiency spells teach: weapons (5 per level) and armour (1)
var spellSkill = map[int][2]int{196: {44, 5}, 197: {172, 5}, 264: {45, 5}, 266: {46, 5}, 198: {54, 5}, 199: {160, 5},
	200: {229, 5}, 201: {43, 5}, 202: {55, 5}, 227: {136, 5}, 15590: {473, 5}, 1180: {173, 5}, 2567: {176, 5},
	5011: {226, 5}, 5009: {228, 5}, 9078: {415, 0}, 9077: {414, 0}, 8737: {413, 0}, 750: {293, 0}, 9116: {433, 0}}

// applyKit puts a kit on an offline character: level, money, spells (with trained, those the class trainers teach
// and the class quests reward up to the level; and the kit's own), the weapon skills and Defense at their cap, gear, bags and items. Items it replaces are deleted. New items
// take guids 100000 above the highest in the database: the worldserver counts item guids up from the highest it saw
// at startup, so items written while it runs must stay clear of the ones it will make.
func applyKit(k *config.Kit, char string, class int) error {
	db := chars()
	r, err := world.Rows(db, "SELECT guid FROM characters WHERE name = ?", char)
	if err != nil || len(r) == 0 {
		return fmt.Errorf("kit: no character %s", char)
	}
	g := atoi(r[0][0])
	exec := func(q string, a ...any) {
		if err == nil {
			_, err = db.Exec(q, a...)
		}
	}
	exec("UPDATE characters SET level = ?, xp = 0 WHERE guid = ?", k.Level, g)
	if k.Money > 0 {
		exec("UPDATE characters SET money = ? WHERE guid = ?", k.Money, g)
	}
	spells := slices.Clone(k.Spells)
	if k.Trained {
		rows, e := world.Rows(db, `SELECT DISTINCT ts.SpellId FROM acore_world.trainer_spell ts JOIN acore_world.trainer t ON t.Id = ts.TrainerId
  WHERE t.Type = 0 AND t.Requirement = ? AND ts.ReqLevel <= ?`, class, k.Level)
		if e != nil {
			return e
		}
		for _, r := range rows {
			spells = append(spells, atoi(r[0]))
		}
		rows, e = world.Rows(db, `SELECT DISTINCT q.RewardSpell FROM acore_world.quest_template q JOIN acore_world.quest_template_addon a ON a.ID = q.ID
  WHERE a.AllowableClasses & ? AND q.MinLevel <= ? AND q.RewardSpell > 0`, 1<<(class-1), k.Level)
		if e != nil {
			return e
		}
		for _, r := range rows { // class quests: the spells they teach
			spells = append(spells, learnedBy()[atoi(r[0])]...)
		}
	}
	for i := 0; i < len(spells); i++ { // and what each spell teaches, as learning it would (Judgement: Judgement of Light)
		for _, t := range learnedBy()[spells[i]] {
			if !slices.Contains(spells, t) {
				spells = append(spells, t)
			}
		}
	}
	cap := 5 * k.Level
	for _, s := range spells {
		exec("INSERT IGNORE INTO character_spell (guid, spell, specMask) VALUES (?, ?, 255)", g, s)
		if sk, ok := spellSkill[s]; ok {
			v := max(1, sk[1]*k.Level)
			exec("INSERT INTO character_skills (guid, skill, value, max) VALUES (?, ?, ?, ?) ON DUPLICATE KEY UPDATE value = VALUES(value), max = VALUES(max)", g, sk[0], v, v)
		}
	}
	exec(`UPDATE character_skills SET value = ?, max = ? WHERE guid = ? AND skill IN (`+
		strings.Join(strings.Fields(strings.Trim(fmt.Sprint(levelSkills), "[]")), ",")+`)`, cap, cap, g)
	if err != nil {
		return fmt.Errorf("kit: %v", err)
	}
	// items: what goes where (bag 0: slots 0-18 equipped, 19-22 bags, 23-38 the backpack; then inside each bag)
	type place struct{ slot, entry, count int }
	var equip []place
	for i, s := range config.Slots {
		if e, ok := k.Gear[s]; ok {
			equip = append(equip, place{i, e, 1})
		}
	}
	for i, e := range k.Bags {
		equip = append(equip, place{19 + i, e, 1})
	}
	if len(equip) == 0 && len(k.Items) == 0 {
		return nil
	}
	tmpl := func(e int) (stack, dur, slots, bonding int, charges string, err error) {
		r, err := world.Rows(db, `SELECT stackable, MaxDurability, ContainerSlots, bonding,
  CONCAT_WS(' ', spellcharges_1, spellcharges_2, spellcharges_3, spellcharges_4, spellcharges_5) FROM acore_world.item_template WHERE entry = ?`, e)
		if err == nil && len(r) == 0 {
			err = fmt.Errorf("kit: no item %d", e)
		}
		if err != nil {
			return
		}
		return max(1, atoi(r[0][0])), atoi(r[0][1]), atoi(r[0][2]), atoi(r[0][3]), r[0][4] + " ", nil
	}
	top, err := world.Rows(db, "SELECT COALESCE(MAX(guid), 0) FROM item_instance")
	if err != nil {
		return err
	}
	next := atoi(top[0][0]) + 100000 // ponytail: 100000 a kit, so a 32-bit guid lasts some 40000 kits
	newItem := func(e, count, bag, slot int, equipped bool) (int, int, error) {
		_, dur, slots, bonding, charges, err := tmpl(e)
		if err != nil {
			return 0, 0, err
		}
		flags := 0
		if bonding == 1 || bonding == 2 && equipped { // bound on pickup, or on equip
			flags = 1
		}
		next++
		exec(`INSERT INTO item_instance (guid, itemEntry, owner_guid, count, charges, flags, enchantments, durability, text)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, '')`, next, e, g, count, charges, flags, strings.Repeat("0 ", 36), dur)
		exec("INSERT INTO character_inventory (guid, bag, slot, item) VALUES (?, ?, ?, ?)", g, bag, slot, next)
		return next, slots, err
	}
	clear := map[int]bool{}
	for _, p := range equip { // what's in the slot now goes; both hands go together (a two-hander and an off-hand don't fit)
		clear[p.slot] = true
		if p.slot == 15 || p.slot == 16 {
			clear[15], clear[16] = true, true
		}
	}
	for slot := range clear {
		exec(`DELETE ii, ci FROM character_inventory ci JOIN item_instance ii ON ii.guid = ci.item
  WHERE ci.guid = ? AND ci.bag = 0 AND ci.slot = ?`, g, slot)
	}
	type space struct{ bag, slot int }
	var free []space
	used, err := world.Rows(db, "SELECT slot FROM character_inventory WHERE guid = ? AND bag = 0 AND slot BETWEEN 23 AND 38", g)
	for s := 23; s <= 38; s++ {
		if !slices.ContainsFunc(used, func(r []string) bool { return atoi(r[0]) == s }) {
			free = append(free, space{0, s})
		}
	}
	for _, p := range equip {
		guid, slots, e := newItem(p.entry, 1, 0, p.slot, p.slot < 19)
		if e != nil {
			return e
		}
		for s := range slots {
			free = append(free, space{guid, s})
		}
	}
	for _, st := range k.Items {
		stack, _, _, _, _, e := tmpl(st.Entry)
		if e != nil {
			return e
		}
		for left := st.Count; left > 0; left -= stack {
			if len(free) == 0 {
				return fmt.Errorf("kit: no room for %d more of item %d", left, st.Entry)
			}
			if _, _, e := newItem(st.Entry, min(left, stack), free[0].bag, free[0].slot, false); e != nil {
				return e
			}
			free = free[1:]
		}
	}
	if err != nil {
		return fmt.Errorf("kit: %v", err)
	}
	return nil
}

// ApplyKit puts a kit on an offline character (azbench kit apply, for testing a kit).
func ApplyKit(k *config.Kit, char string) error {
	if charOnline(char) {
		return fmt.Errorf("%s is online", char)
	}
	r, err := world.Rows(chars(), "SELECT class FROM characters WHERE name = ?", char)
	if err != nil || len(r) == 0 {
		return fmt.Errorf("no character %s", char)
	}
	return applyKit(k, char, atoi(r[0][0]))
}

// SaveKit reads a character as a kit: level, money, every spell, gear, bags and the items it carries; notes: item names.
func SaveKit(char string) (*config.Kit, map[int]string, error) {
	db := chars()
	r, err := world.Rows(db, "SELECT guid, level, money FROM characters WHERE name = ?", char)
	if err != nil || len(r) == 0 {
		return nil, nil, fmt.Errorf("no character %s", char)
	}
	g := r[0][0]
	k := &config.Kit{Level: atoi(r[0][1]), Money: atoi(r[0][2]), Gear: map[string]int{}}
	notes := map[int]string{}
	spells, err := world.Rows(db, "SELECT spell FROM character_spell WHERE guid = ? ORDER BY spell", g)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range spells {
		k.Spells = append(k.Spells, atoi(s[0]))
	}
	inv, err := world.Rows(db, `SELECT ci.bag, ci.slot, ii.itemEntry, ii.count, it.name FROM character_inventory ci
  JOIN item_instance ii ON ii.guid = ci.item JOIN acore_world.item_template it ON it.entry = ii.itemEntry
  WHERE ci.guid = ? ORDER BY ci.bag, ci.slot`, g)
	if err != nil {
		return nil, nil, err
	}
	count := map[int]int{}
	var order []int
	for _, x := range inv {
		bag, slot, e := atoi(x[0]), atoi(x[1]), atoi(x[2])
		notes[e] = x[4]
		switch {
		case bag == 0 && slot < 19:
			k.Gear[config.Slots[slot]] = e
		case bag == 0 && slot < 23:
			k.Bags = append(k.Bags, e)
		case bag != 0 || slot < 39: // the backpack and the bags (not the bank)
			if count[e] == 0 {
				order = append(order, e)
			}
			count[e] += atoi(x[3])
		}
	}
	for _, e := range order {
		k.Items = append(k.Items, config.Stack{Entry: e, Count: count[e]})
	}
	return k, notes, nil
}
