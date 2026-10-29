package world

// azworld upgrades: Wowhead-style "what could I wear in this slot, and where do I get it". Items your class can use at
// your level, best item level first, each with how to obtain it: a quest reward (quest, giver, where), a vendor
// (price, where) or a creature drop (mob, level, chance, where). World drops from shared loot tables are left out
// (they drop anywhere at random); `azworld item <id>` has every source of one item.

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

var classes = map[string]float64{"warrior": 1, "paladin": 2, "hunter": 3, "rogue": 4, "priest": 5, "death knight": 6,
	"shaman": 7, "mage": 8, "warlock": 9, "druid": 11}

// what each class can wear and wield (weapons: what it can learn; some need a trainer first). Armor at levels below 40.
var armorOf = map[int][]int{1: {1, 2, 3, 6}, 2: {1, 2, 3, 6}, 3: {1, 2}, 4: {1, 2}, 5: {1}, 6: {1, 2, 3, 4}, 7: {1, 2, 6},
	8: {1}, 9: {1}, 11: {1, 2}}
var weaponsOf = map[int][]int{
	1: {0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 13, 15, 16, 18}, 2: {0, 1, 4, 5, 6, 7, 8}, 3: {0, 1, 2, 3, 6, 7, 8, 10, 13, 15, 16, 18},
	4: {0, 2, 3, 4, 7, 13, 15, 16, 18}, 5: {4, 10, 15, 19}, 6: {0, 1, 4, 5, 6, 7, 8}, 7: {0, 1, 4, 5, 10, 13, 15},
	8: {7, 10, 15, 19}, 9: {7, 10, 15, 19}, 11: {4, 5, 6, 10, 13, 15}}

// slot name -> inventory types, in this order
var gearSlots = []struct {
	name  string
	types []int
}{{"head", []int{1}}, {"neck", []int{2}}, {"shoulder", []int{3}}, {"chest", []int{5, 20}}, {"waist", []int{6}},
	{"legs", []int{7}}, {"feet", []int{8}}, {"wrists", []int{9}}, {"hands", []int{10}}, {"finger", []int{11}},
	{"trinket", []int{12}}, {"back", []int{16}}, {"main hand", []int{13, 17, 21}}, {"off hand", []int{13, 14, 22, 23}},
	{"ranged", []int{15, 25, 26, 28}}}

var gearStats = map[int]string{0: "mana", 1: "health", 3: "agility", 4: "strength", 5: "intellect", 6: "spirit", 7: "stamina",
	31: "hit", 32: "crit", 35: "resilience", 36: "haste", 38: "attack_power", 43: "mana_per_5s", 45: "spell_power"}

// slotOf: inventory type -> slot name (the last slot listing it wins, as Object.fromEntries does)
var slotOf = func() map[int]string {
	m := map[int]string{}
	for _, s := range gearSlots {
		for _, t := range s.types {
			m[t] = s.name
		}
	}
	return m
}()

func ints(a []int) string {
	s := make([]string, len(a))
	for i, v := range a {
		s[i] = fmt.Sprint(v)
	}
	return strings.Join(s, ",")
}

type upgradeOpts struct {
	cls, level, mapID      float64
	slot                   string
	hasSlot                bool
	near                   []float64
	radius, minIlvl, limit opt
}

func upgrades(o upgradeOpts) any {
	var types []int
	if o.hasSlot {
		for _, s := range gearSlots {
			if s.name == o.slot {
				types = s.types
			}
		}
		if types == nil {
			names := make([]string, len(gearSlots))
			for i, s := range gearSlots {
				names[i] = s.name
			}
			panic(qerr{fmt.Errorf("unknown slot %s: one of %s", o.slot, strings.Join(names, ", "))})
		}
	} else {
		seen := map[int]bool{}
		for _, s := range gearSlots {
			for _, t := range s.types {
				if !seen[t] {
					seen[t] = true
					types = append(types, t)
				}
			}
		}
	}
	cls := int(o.cls)
	armor, ok1 := armorOf[cls]
	weapons, ok2 := weaponsOf[cls]
	if !ok1 || !ok2 {
		panic(qerr{fmt.Errorf("Cannot read properties of undefined (reading 'join')")})
	}
	level := jsStr(o.level)
	usable := fmt.Sprintf(`it.InventoryType IN (%s) AND it.RequiredLevel <= %s
    AND (it.AllowableClass = -1 OR it.AllowableClass & %d) AND it.Quality >= 1 AND it.ItemLevel >= %s
    AND ((it.class = 4 AND it.subclass IN (0, %s)) OR (it.class = 2 AND it.subclass IN (%s)))
    AND (it.armor > 0 OR it.dmg_max1 > 0 OR it.stat_value1 <> 0)`, ints(types), level, jsShl(o.cls), jsStr(o.minIlvl.or(1)),
		ints(armor), ints(weapons)) // not cosmetic clothing
	// a creature that spawns outside holiday events
	spawned := func(col string) string {
		return fmt.Sprintf("EXISTS (SELECT 1 FROM creature cr WHERE cr.id = %s AND cr.guid NOT IN (SELECT guid FROM game_event_creature))", col)
	}
	statCols := make([]string, 10)
	for i := range statCols {
		statCols[i] = fmt.Sprintf("it.stat_type%d, it.stat_value%d", i+1, i+1)
	}
	cols := `it.entry, it.name, it.Quality quality, it.ItemLevel item_level, it.RequiredLevel required_level, it.InventoryType inv,
    it.armor, it.dmg_min1, it.dmg_max1, it.delay, ` + strings.Join(statCols, ", ")
	alliance := "(qt.AllowableRaces = 0 OR qt.AllowableRaces & 1101)" // human, dwarf, night elf, gnome, draenei
	// quest rewards you could still get around your level
	var rewardCols []string
	for i := 1; i <= 4; i++ {
		rewardCols = append(rewardCols, fmt.Sprintf("qt.RewardItem%d", i))
	}
	for i := 1; i <= 6; i++ {
		rewardCols = append(rewardCols, fmt.Sprintf("qt.RewardChoiceItemID%d", i))
	}
	fromQuests := q(fmt.Sprintf(`SELECT %s, qt.ID quest, qt.LogTitle quest_name, qt.QuestLevel quest_level, qt.MinLevel min_level, st.id giver
    FROM quest_template qt JOIN item_template it ON it.entry IN (%s)
    LEFT JOIN creature_queststarter st ON st.quest = qt.ID
    WHERE %s AND %s AND qt.MinLevel <= %s AND qt.QuestLevel BETWEEN %s AND %s`, cols, strings.Join(rewardCols, ","), usable, alliance,
		jsStr(o.level+2), jsStr(o.level-4), jsStr(o.level+6)))
	fromVendors := q(fmt.Sprintf(`SELECT %s, v.entry vendor, c.name vendor_name, it.BuyPrice price FROM npc_vendor v
    JOIN item_template it ON it.entry = v.item JOIN creature_template c ON c.entry = v.entry WHERE %s AND %s`, cols, usable, spawned("v.entry")))
	fromDrops := q(fmt.Sprintf("SELECT %s, c.entry mob, c.name mob_name, c.minlevel mob_lo, c.maxlevel mob_hi, c.`rank` mob_rank, l.Chance chance\n"+
		"    FROM creature_loot_template l JOIN item_template it ON it.entry = l.Item AND l.Reference = 0\n"+
		"    JOIN creature_template c ON c.lootid = l.Entry\n"+
		"    WHERE %s AND l.Chance >= 0.5 AND c.maxlevel BETWEEN %s AND %s AND %s", cols, usable, jsStr(o.level-3), jsStr(o.level+5), spawned("c.entry")))

	type item struct {
		o       *obj
		sources []*obj
	}
	byID := map[float64]*item{}
	var order []*item
	where := func(table string, id float64) *obj {
		s := spawns(table, id, o.near, 1)
		if !truthy(s.get("count")) {
			return nil
		}
		p := s.get("points").([]*obj)[0]
		if o.near != nil && (p.n("map") != o.mapID || math.Hypot(p.n("x")-o.near[0], p.n("y")-at(o.near, 1)) > o.radius.or(3000)) {
			return nil
		}
		r := newObj("x", p.get("x"), "y", p.get("y"), "z", p.get("z"), "area", p.get("area"))
		if o.near != nil {
			r.set("distance", jsRound(math.Hypot(p.n("x")-o.near[0], p.n("y")-at(o.near, 1))))
		}
		return r
	}
	add := func(r *obj, src *obj) {
		id := r.n("entry")
		it := byID[id]
		if it == nil {
			stats := newObj()
			for i := 1; i <= 10; i++ {
				v := r.get(fmt.Sprintf("stat_value%d", i))
				if !truthy(v) {
					continue
				}
				t := r.get(fmt.Sprintf("stat_type%d", i))
				name, ok := gearStats[int(jsNum(t))]
				if !ok || float64(int(jsNum(t))) != jsNum(t) {
					name = "stat" + jsStr(t)
				}
				stats.set(name, jsNum(v))
			}
			var dps any
			if truthy(r.get("dmg_max1")) {
				dps = jsRound((r.n("dmg_min1")+r.n("dmg_max1"))/2/(r.n("delay")/1000)*10) / 10
			}
			var slot any
			if s, ok := slotOf[int(r.n("inv"))]; ok {
				slot = s
			}
			it = &item{o: newObj("entry", id, "name", r.get("name"), "slot", slot, "quality", r.get("quality"), "item_level", r.get("item_level"),
				"required_level", r.get("required_level"), "armor", or(r.get("armor")), "stats", stats, "dps", dps)}
			byID[id] = it
			order = append(order, it)
		}
		it.sources = append(it.sources, src)
	}
	nn := func(v any) any { return v } // `?? undefined` keeps everything but null, which is already nil
	for _, r := range fromQuests {
		add(r, newObj("type", "quest", "quest", r.get("quest"), "name", r.get("quest_name"), "quest_level", r.get("quest_level"),
			"min_level", r.get("min_level"), "giver", nn(r.get("giver")), "npc", nn(r.get("giver"))))
	}
	for _, r := range fromVendors {
		add(r, newObj("type", "vendor", "vendor", r.get("vendor"), "name", r.get("vendor_name"), "price", r.get("price"), "npc", r.get("vendor")))
	}
	for _, r := range fromDrops {
		var lvl any = jsStr(r.get("mob_lo")) + "-" + jsStr(r.get("mob_hi"))
		if r.get("mob_lo") == r.get("mob_hi") {
			lvl = r.get("mob_lo")
		}
		var elite any
		if r.n("mob_rank") > 0 {
			elite = true
		}
		add(r, newObj("type", "drop", "mob", r.get("mob"), "name", r.get("mob_name"), "npc", r.get("mob"), "level", lvl, "elite", elite,
			"chance", r.get("chance")))
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].o.n("item_level") > order[j].o.n("item_level") })
	lim := int(o.limit.or(3))
	if o.hasSlot {
		lim = int(o.limit.or(12))
	}
	picked := []*obj{}
	bySlot := newObj()
	// best first; locations are looked up only for candidates, and with --near only sources within --radius count
	d := func(s *obj) float64 {
		if a, ok := s.get("at").(*obj); ok && a != nil && a.get("distance") != nil {
			return a.n("distance")
		}
		return 1e9
	}
	for _, it := range order {
		s := "undefined"
		if v, ok := it.o.get("slot").(string); ok {
			s = v
		}
		if o.hasSlot {
			if len(picked) >= lim {
				continue
			}
		} else {
			if bySlot.get(s) == nil {
				bySlot.set(s, []*obj{})
			}
			if len(bySlot.get(s).([]*obj)) >= lim {
				continue
			}
		}
		for _, src := range it.sources {
			if truthy(src.get("npc")) {
				if w := where("creature_spawn", src.n("npc")); w != nil {
					src.set("at", w)
				} else {
					src.set("at", nil)
				}
			}
			src.del("npc")
		}
		var keep []*obj
		for _, x := range it.sources {
			if a, _ := x.get("at").(*obj); a != nil || (o.near == nil && x.get("type") == "quest") {
				keep = append(keep, x)
			}
		}
		sort.SliceStable(keep, func(i, j int) bool { return d(keep[i]) < d(keep[j]) })
		if len(keep) > 3 {
			keep = keep[:3]
		}
		it.sources = keep
		if len(keep) == 0 {
			continue
		}
		it.o.set("sources", keep)
		picked = append(picked, it.o)
		if !o.hasSlot {
			bySlot.set(s, append(bySlot.get(s).([]*obj), it.o))
		}
	}
	if o.hasSlot {
		return picked
	}
	return bySlot
}
