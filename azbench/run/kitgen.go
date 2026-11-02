package run

// azbench kit gen: a starting kit made from items a character of that class, race and level could get by playing:
// gear up to a quality (green, uncommon, by default; rare: blues too) and an item level (default level + 10) that drops from a creature or chest in the world, is a quest reward the character can take,
// or is made by a profession its trainers teach; bags and ammunition from vendors. Test, deprecated, seasonal,
// random-enchantment and profession- or reputation-locked items are left out. Each slot gets the item that scores
// best for the spec's stats (weights below), armour of the best type the class wears at that level, weapons the class
// can learn (the kit includes the proficiency, as a weapon master would teach it).

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/pthm/leeroy/azbench/config"
	"github.com/pthm/leeroy/azworld/world"
)

// obtainable: every item a character of a level, race and class can come by in play, with how (drop, quest, crafted,
// vendor). Quests it can take at its level, of at most 2 levels above it; items it can use, from 10 item levels below it to a cap (some quest rewards
// have no required level of their own: the quest's level is the gate). Drops: loot of creatures and chests that are spawned, through reference loot. Vendors: of spawned
// creatures. Quest givers and vendors are only those not hostile to the character's side. Not raids (notMaps).
var obtainable = fmt.Sprintf(`WITH RECURSIVE
loot AS (SELECT l.Item, l.Reference FROM creature_loot_template l JOIN creature_template ct ON ct.lootid = l.Entry
    WHERE EXISTS (SELECT 1 FROM creature c WHERE c.id = ct.entry AND c.map NOT IN (%[1]s) AND c.map <> ?)
  UNION SELECT l.Item, l.Reference FROM gameobject_loot_template l JOIN gameobject_template gt ON gt.type = 3 AND gt.Data1 = l.Entry
    WHERE EXISTS (SELECT 1 FROM gameobject g WHERE g.id = gt.entry AND g.map NOT IN (%[1]s) AND g.map <> ?)),
ref (Item, Reference, depth) AS (SELECT Item, Reference, 0 FROM loot
  UNION ALL SELECT r.Item, r.Reference, ref.depth + 1 FROM reference_loot_template r JOIN ref ON ref.Reference = r.Entry WHERE ref.depth < 4),
friendly AS (SELECT ct.entry FROM creature_template ct JOIN wowbench.reaction r ON r.faction_template = ct.faction
  WHERE IF(?, r.to_alliance, r.to_horde) <> 'hostile' AND EXISTS (SELECT 1 FROM creature c WHERE c.id = ct.entry)),
quests AS (SELECT q.* FROM quest_template q LEFT JOIN quest_template_addon a ON a.ID = q.ID
  WHERE q.QuestSortID NOT IN (%[2]s) AND q.QuestSortID NOT IN (SELECT id FROM wowbench.area WHERE map = ? AND zone = 0) AND q.MinLevel <= ? AND q.QuestLevel <= ? + 2 AND (q.AllowableRaces = 0 OR q.AllowableRaces & ?) AND (COALESCE(a.AllowableClasses, 0) = 0 OR a.AllowableClasses & ?)
    AND (EXISTS (SELECT 1 FROM creature_queststarter s JOIN friendly f ON f.entry = s.id WHERE s.quest = q.ID)
      OR EXISTS (SELECT 1 FROM gameobject_queststarter s WHERE s.quest = q.ID))),
src (e, how) AS (SELECT Item, 'drop' FROM ref WHERE Reference = 0
  UNION SELECT RewardItem1, 'quest' FROM quests UNION SELECT RewardItem2, 'quest' FROM quests
  UNION SELECT RewardItem3, 'quest' FROM quests UNION SELECT RewardItem4, 'quest' FROM quests
  UNION SELECT RewardChoiceItemID1, 'quest' FROM quests UNION SELECT RewardChoiceItemID2, 'quest' FROM quests
  UNION SELECT RewardChoiceItemID3, 'quest' FROM quests UNION SELECT RewardChoiceItemID4, 'quest' FROM quests
  UNION SELECT RewardChoiceItemID5, 'quest' FROM quests UNION SELECT RewardChoiceItemID6, 'quest' FROM quests
  UNION SELECT s.create_item, 'crafted' FROM trainer_spell ts JOIN trainer t ON t.Id = ts.TrainerId AND t.Type = 2
    JOIN wowbench.spell s ON s.id = ts.SpellId WHERE s.create_item > 0
  UNION SELECT v.item, 'vendor' FROM npc_vendor v JOIN friendly f ON f.entry = v.entry WHERE v.ExtendedCost = 0)
SELECT it.entry, it.name, it.class, it.subclass, it.InventoryType, it.ItemLevel, it.RequiredLevel, it.Quality,
  it.armor, it.dmg_min1, it.dmg_max1, it.delay, it.ContainerSlots, it.ammo_type, it.maxcount,
  CONCAT_WS(' ', it.stat_type1, it.stat_value1, it.stat_type2, it.stat_value2, it.stat_type3, it.stat_value3,
    it.stat_type4, it.stat_value4, it.stat_type5, it.stat_value5, it.stat_type6, it.stat_value6, it.stat_type7,
    it.stat_value7, it.stat_type8, it.stat_value8, it.stat_type9, it.stat_value9, it.stat_type10, it.stat_value10),
  GROUP_CONCAT(DISTINCT src.how ORDER BY src.how)
FROM item_template it JOIN src ON src.e = it.entry
WHERE it.RequiredLevel <= ? AND it.ItemLevel BETWEEN ? - 10 AND ? AND (it.AllowableClass <= 0 OR it.AllowableClass & ?) AND (it.AllowableRace <= 0 OR it.AllowableRace & ?)
  AND it.RandomProperty = 0 AND it.RandomSuffix = 0 AND it.RequiredSkill = 0 AND it.requiredspell = 0
  AND it.RequiredReputationFaction = 0 AND it.requiredhonorrank = 0 AND it.HolidayId = 0 AND it.Flags & 16 = 0
  AND it.name NOT REGEXP '(^|[^a-z])(test|qa|deprecated|monster|unused|old|ph)([^a-z]|$)'
GROUP BY it.entry`, notMaps, raidZones)

// maps whose loot doesn't count: test and unused maps (13 ... 451), the death knights' starting zone (609), and raids, which aren't levelling (classic,
// The Burning Crusade, Wrath); raidZones: the raids' zones, as quests are filed under them (Zul'Gurub: 1977, and 19)
const (
	notMaps = "13, 25, 29, 35, 37, 42, 44, 169, 451, 609, 249, 309, 409, 469, 509, 531, 533, 532, 534, 544, 548, 550, 564, " +
		"565, 568, 580, 603, 615, 616, 624, 631, 649, 724"
	raidZones = "2159, 1977, 19, 2717, 2677, 3429, 3428, 3456, 3457, 3606, 3836, 3607, 3842, 3959, 3923, 3805, 4075, 4273, " +
		"4500, 4493, 4603, 4722, 4812, 4987"
)

type kitItem struct {
	entry, class, sub, inv, ilvl, req, quality, slots, ammo, maxcount int
	name, how                                                         string
	armor, dps                                                        float64
	stats                                                             map[int]float64
	score                                                             float64
}

// weapon subclasses each class can learn, and the proficiency spell and skill for each
var (
	classWeapons = map[int][]int{1: {0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 13, 15, 16, 18}, 2: {0, 1, 4, 5, 6, 7, 8},
		3: {0, 1, 2, 3, 6, 7, 8, 10, 13, 15, 16, 18}, 4: {0, 2, 3, 4, 7, 13, 15, 16, 18}, 5: {4, 10, 15, 19},
		6: {0, 1, 4, 5, 6, 7, 8}, 7: {0, 1, 4, 5, 10, 13, 15}, 8: {7, 10, 15, 19}, 9: {7, 10, 15, 19}, 11: {4, 5, 6, 10, 13, 15}}
	weaponSpell = map[int]int{0: 196, 1: 197, 2: 264, 3: 266, 4: 198, 5: 199, 6: 200, 7: 201, 8: 202, 10: 227, 13: 15590,
		15: 1180, 16: 2567, 18: 5011, 19: 5009}
)

// armorType: the best armour a class wears at a level (1 cloth, 2 leather, 3 mail, 4 plate)
func armorType(class, level int) int {
	switch class {
	case 1, 2, 6:
		return map[bool]int{true: 4, false: 3}[level >= 40]
	case 3, 7:
		return map[bool]int{true: 3, false: 2}[level >= 40]
	case 4, 11:
		return 2
	}
	return 1
}

// style: how a spec holds its weapons; weights: what its stats are worth (item stat ids: 3 agility, 4 strength,
// 5 intellect, 6 spirit, 7 stamina, 12 defense, 13 dodge, 14 parry, 15 block, 31 hit, 32 crit, 36 haste, 38 attack
// power, 43 mana per 5, 45 spell power, 48 block value)
func gearPlan(class int, spec string, level int) (style string, w map[int]float64) {
	if spec == "" {
		spec = map[int]string{1: "arms", 2: "ret", 3: "hunter", 4: "combat", 5: "holy", 6: "arms", 7: "caster", 8: "frost", 9: "caster", 11: "caster"}[class]
	}
	melee := map[int]float64{4: 2, 3: 1, 7: 1, 38: 0.5, 31: 1, 32: 1}
	caster := map[int]float64{5: 2, 6: 0.5, 7: 0.8, 45: 2, 31: 1, 32: 1, 36: 1}
	switch spec {
	case "arms", "ret":
		return "two_hand", melee
	case "fury":
		return map[bool]string{true: "dual", false: "two_hand"}[level >= 20], melee
	case "protwarrior", "protpaladin":
		return "shield", map[int]float64{7: 2, 4: 1.5, 3: 1, 12: 1, 13: 1, 14: 1, 15: 0.5, 48: 0.5, 5: 0.3 * b2f(class == 2)}
	case "holypaladin":
		return "shield", map[int]float64{5: 2, 7: 0.7, 45: 1.5, 43: 1.5, 32: 1}
	case "combat", "assassination", "subtlety":
		return "dual", map[int]float64{3: 2, 4: 0.5, 7: 1, 38: 0.5, 31: 1, 32: 1}
	case "holy", "discipline":
		return "caster", map[int]float64{5: 2, 6: 1, 7: 0.7, 45: 1.5, 43: 1.5}
	case "hunter":
		return "two_hand", map[int]float64{3: 2, 5: 0.5, 7: 1, 38: 0.5, 31: 1, 32: 1}
	}
	return "caster", caster
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// GenKit makes a kit for a class, race, spec (optional) and level, of items up to a quality (2 uncommon, 3 rare, 4
// epic) and item level, none from the map notFrom (a dungeon the kit is for: its loot and its quests; 0: none); notes:
// what each item is, for the file's comments.
func GenKit(class, race int, spec string, level, quality, maxIlvl, notFrom int) (*config.Kit, map[int]string, error) {
	rows, err := world.Rows(world.DB(world.Admin, "acore_world"), obtainable, notFrom, notFrom, slices.Contains([]int{1, 3, 4, 7, 11}, race), notFrom, level, level, 1<<(race-1), 1<<(class-1), level, level, maxIlvl, 1<<(class-1), 1<<(race-1))
	if err != nil {
		return nil, nil, err
	}
	style, w := gearPlan(class, spec, level)
	can := classWeapons[class]
	armor := armorType(class, level)
	var items []*kitItem
	for _, r := range rows {
		it := &kitItem{entry: atoi(r[0]), name: r[1], class: atoi(r[2]), sub: atoi(r[3]), inv: atoi(r[4]), ilvl: atoi(r[5]),
			req: atoi(r[6]), quality: atoi(r[7]), slots: atoi(r[12]), ammo: atoi(r[13]), maxcount: atoi(r[14]), how: r[16], stats: map[int]float64{}}
		fmt.Sscan(r[8], &it.armor)
		var lo, hi, delay float64
		fmt.Sscan(r[9], &lo)
		fmt.Sscan(r[10], &hi)
		fmt.Sscan(r[11], &delay)
		if delay > 0 {
			it.dps = (lo + hi) / 2 / (delay / 1000)
		}
		f := strings.Fields(r[15])
		for i := 0; i+1 < len(f); i += 2 {
			var v float64
			fmt.Sscan(f[i+1], &v)
			it.stats[atoi(f[i])] += v
		}
		items = append(items, it)
	}
	score := func(it *kitItem, dpsWeight float64) float64 {
		s := 0.0
		for k, v := range it.stats {
			s += w[k] * v
		}
		return s + it.armor*0.02*(1+w[12]*3) + it.dps*dpsWeight
	}
	melee := style != "caster"
	weaponDPS := map[bool]float64{true: 4, false: 0}[melee]
	best := func(dpsWeight float64, ok func(*kitItem) bool, not ...int) *kitItem {
		var b *kitItem
		for _, it := range items {
			if it.quality < 2 || it.quality > quality || !ok(it) || slices.Contains(not, it.entry) {
				continue
			}
			if it.score = score(it, dpsWeight); it.score <= 0 { // nothing for the spec: better an empty slot
				continue
			}
			if b == nil || it.score > b.score || it.score == b.score && it.ilvl > b.ilvl {
				b = it
			}
		}
		return b
	}
	isWeapon := func(it *kitItem, invs ...int) bool {
		return it.class == 2 && slices.Contains(can, it.sub) && slices.Contains(invs, it.inv)
	}
	k := &config.Kit{Level: level, Money: level * level * 25, Trained: true, Gear: map[string]int{}}
	notes := map[int]string{}
	put := func(slot string, it *kitItem) {
		if it == nil {
			return
		}
		k.Gear[slot] = it.entry
		notes[it.entry] = fmt.Sprintf("%s (item level %d, %s)", it.name, it.ilvl, it.how)
		if it.class == 2 && !slices.Contains(k.Spells, weaponSpell[it.sub]) {
			k.Spells = append(k.Spells, weaponSpell[it.sub])
		}
	}
	body := map[string][]int{"head": {1}, "shoulders": {3}, "chest": {5, 20}, "waist": {6}, "legs": {7}, "feet": {8}, "wrists": {9}, "hands": {10}}
	for slot, invs := range body {
		put(slot, best(0, func(it *kitItem) bool { return it.class == 4 && it.sub == armor && slices.Contains(invs, it.inv) }))
	}
	put("neck", best(0, func(it *kitItem) bool { return it.inv == 2 }))
	put("back", best(0, func(it *kitItem) bool { return it.inv == 16 }))
	for _, pair := range [][2]string{{"finger1", "finger2"}, {"trinket1", "trinket2"}} {
		inv := map[string]int{"finger1": 11, "trinket1": 12}[pair[0]]
		a := best(0, func(it *kitItem) bool { return it.inv == inv })
		put(pair[0], a)
		if a != nil {
			put(pair[1], best(0, func(it *kitItem) bool { return it.inv == inv }, a.entry))
		}
	}
	switch style {
	case "two_hand":
		put("main_hand", best(weaponDPS, func(it *kitItem) bool { return isWeapon(it, 17) }))
	case "dual":
		mh := best(weaponDPS, func(it *kitItem) bool { return isWeapon(it, 13, 21) })
		put("main_hand", mh)
		if mh != nil {
			put("off_hand", best(weaponDPS/2, func(it *kitItem) bool { return isWeapon(it, 13, 22) }, mh.entry))
		}
	case "shield":
		put("main_hand", best(weaponDPS, func(it *kitItem) bool { return isWeapon(it, 13, 21) }))
		put("off_hand", best(0, func(it *kitItem) bool { return it.class == 4 && it.sub == 6 && it.inv == 14 }))
	case "caster": // a staff, or a one-hander and something held in the off hand, whichever scores more
		staff := best(0, func(it *kitItem) bool { return isWeapon(it, 17) })
		mh := best(0, func(it *kitItem) bool { return isWeapon(it, 13, 21) })
		held := best(0, func(it *kitItem) bool { return it.inv == 23 })
		pair := 0.0
		if mh != nil {
			pair += mh.score
		}
		if held != nil {
			pair += held.score
		}
		if staff != nil && staff.score >= pair {
			put("main_hand", staff)
		} else {
			put("main_hand", mh)
			put("off_hand", held)
		}
	}
	switch class {
	case 5, 8, 9: // wands
		put("ranged", best(2, func(it *kitItem) bool { return isWeapon(it, 26) && it.sub == 19 }))
	case 1, 3, 4:
		dw := map[bool]float64{true: 4, false: 0.5}[class == 3]
		r := best(dw, func(it *kitItem) bool { return isWeapon(it, 15, 25, 26) && it.sub != 19 })
		put("ranged", r)
		if class == 3 && r != nil && r.sub != 16 { // a hunter's ammunition: the best a vendor sells for it
			kind := map[bool]int{true: 3, false: 2}[r.sub == 3]
			var ammo *kitItem
			for _, it := range items {
				if it.class == 6 && it.sub == kind && strings.Contains(it.how, "vendor") && (ammo == nil || it.ilvl > ammo.ilvl) {
					ammo = it
				}
			}
			if ammo != nil {
				k.Items = append(k.Items, config.Stack{Entry: ammo.entry, Count: 1000})
				notes[ammo.entry] = ammo.name + " (vendor)"
			}
		}
	}
	var bag *kitItem // four of the roomiest bag a vendor sells
	for _, it := range items {
		if it.class == 1 && it.sub == 0 && it.quality <= 1 && strings.Contains(it.how, "vendor") && (bag == nil || it.slots > bag.slots) {
			bag = it
		}
	}
	if bag != nil {
		k.Bags = []int{bag.entry, bag.entry, bag.entry, bag.entry}
		notes[bag.entry] = fmt.Sprintf("%s (%d slots, vendor)", bag.name, bag.slots)
	}
	sort.Ints(k.Spells)
	return k, notes, nil
}

// KitYAML writes a kit as bench/kits keeps it, each item named in a comment.
func KitYAML(k *config.Kit, notes map[int]string, header string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimSpace(header), "\n") {
		fmt.Fprintf(&b, "# %s\n", l)
	}
	fmt.Fprintf(&b, "level: %d\nmoney: %d # copper\ntrained: %v # every spell the class trainers teach and class quests reward up to the level\n", k.Level, k.Money, k.Trained)
	if len(k.Spells) > 0 {
		fmt.Fprintf(&b, "spells: %s\n", strings.ReplaceAll(fmt.Sprint(k.Spells), " ", ", "))
	}
	b.WriteString("gear:\n")
	for _, s := range config.Slots {
		if e, ok := k.Gear[s]; ok {
			fmt.Fprintf(&b, "  %s: %d # %s\n", s, e, notes[e])
		}
	}
	if len(k.Bags) > 0 {
		fmt.Fprintf(&b, "bags: %s # %s\n", strings.ReplaceAll(fmt.Sprint(k.Bags), " ", ", "), notes[k.Bags[0]])
	}
	if len(k.Items) > 0 {
		b.WriteString("items:\n")
		for _, s := range k.Items {
			fmt.Fprintf(&b, "  - {entry: %d, count: %d} # %s\n", s.Entry, s.Count, notes[s.Entry])
		}
	}
	return b.String()
}
