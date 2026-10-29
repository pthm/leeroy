package world

// azworld: structured, read-only lookups over the world DB + the wowbench schema (azworld setup), aowow-style: each
// entity comes back with its relations resolved (where an item drops, where a quest giver stands...).
// Usage: azworld <command> [args] [--flags]; prints JSON. `azworld help` lists commands.

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
)

const limit = "15"

var npcFlagNames = []struct {
	bit  int
	name string
}{{0x1, "gossip"}, {0x2, "questgiver"}, {0x10, "trainer"}, {0x80, "vendor"}, {0x1000, "repair"},
	{0x2000, "flightmaster"}, {0x4000, "spirithealer"}, {0x10000, "innkeeper"}, {0x20000, "banker"},
	{0x200000, "auctioneer"}, {0x400000, "stablemaster"}}

func npcFlags(f int) []string {
	out := []string{}
	for _, n := range npcFlagNames {
		if f&n.bit != 0 {
			out = append(out, n.name)
		}
	}
	return out
}

var (
	rankNames    = []string{"normal", "elite", "rare elite", "boss", "rare"}
	qualityNames = []string{"poor", "common", "uncommon", "rare", "epic", "legendary", "artifact", "heirloom"}
	slotNames    = []string{"none", "head", "neck", "shoulder", "shirt", "chest", "waist", "legs", "feet", "wrists", "hands",
		"finger", "trinket", "one-hand", "shield", "ranged", "back", "two-hand", "bag", "tabard", "robe", "main hand",
		"off hand", "held in off-hand", "ammo", "thrown", "ranged (right)", "quiver", "relic"}
	statNames = map[int]string{0: "mana", 1: "health", 3: "agility", 4: "strength", 5: "intellect", 6: "spirit",
		7: "stamina", 31: "hit", 32: "crit", 35: "resilience", 36: "haste", 38: "attack power", 45: "spell power", 43: "mp5"}
)

// pick is list[n], or nil out of range (a JS array lookup).
func pick(list []string, n float64) any {
	if n >= 0 && int(n) < len(list) && float64(int(n)) == n {
		return list[int(n)]
	}
	return nil
}

func (o *obj) n(k string) float64 { return jsNum(o.get(k)) }

// spawns: count, zones, and the first few points (x, y, z, area). table = creature_spawn | gameobject_spawn
func spawns(table string, id float64, near []float64, lim int) *obj {
	order := "ORDER BY s.guid"
	if near != nil {
		order = fmt.Sprintf("ORDER BY POW(s.x - %s, 2) + POW(s.y - %s, 2)", jsStr(near[0]), jsStr(at(near, 1)))
	}
	zones := q(fmt.Sprintf(`SELECT z.name zone, COUNT(*) n FROM wowbench.%s s LEFT JOIN wowbench.area z ON z.id = s.zone
    WHERE s.id = %s GROUP BY s.zone ORDER BY n DESC LIMIT 5`, table, jsStr(id)))
	points := q(fmt.Sprintf(`SELECT s.map, ROUND(s.x) x, ROUND(s.y) y, ROUND(s.z) z, a.name area FROM wowbench.%s s
    LEFT JOIN wowbench.area a ON a.id = s.area WHERE s.id = %s %s LIMIT %d`, table, jsStr(id), order, lim))
	count := 0.0
	for _, z := range zones {
		count += z.n("n")
	}
	return newObj("count", count, "zones", zones, "points", points)
}

// at is a[i], NaN past the end (Number(undefined)).
func at(a []float64, i int) float64 {
	if i < len(a) {
		return a[i]
	}
	return math.NaN()
}

type loot struct {
	direct, viaRef string
	refs           []float64
}

// lootEntries: loot entries that contain item, following reference_loot_template up to 3 levels (shared drop tables)
func lootEntries(table string, item float64) loot {
	refs := idsOf(q(fmt.Sprintf("SELECT DISTINCT Entry FROM reference_loot_template WHERE Item = %s AND Reference = 0", jsStr(item))), "Entry")
	frontier := refs
	for depth := 0; depth < 2 && len(frontier) > 0; depth++ {
		frontier = idsOf(q(fmt.Sprintf("SELECT DISTINCT Entry FROM reference_loot_template WHERE Reference IN (%s)", inList(frontier))), "Entry")
		refs = append(append([]float64{}, refs...), frontier...)
	}
	return loot{direct: fmt.Sprintf("%s.Item = %s AND %s.Reference = 0", table, jsStr(item), table),
		viaRef: fmt.Sprintf("%s.Reference IN (%s)", table, inList(refs)), refs: refs}
}

func withSpawns(rows []*obj, table string, lim int) []*obj {
	for _, r := range rows {
		r.set("spawns", spawns(table, r.n("entry"), nil, lim))
	}
	return rows
}

func numList(s string) []float64 {
	parts := strings.Split(s, ",")
	out := make([]float64, len(parts))
	for i, p := range parts {
		out[i] = jsNum(p)
	}
	return out
}

func orMap(f map[string]string, k, def string) string {
	if v, ok := f[k]; ok {
		return v
	}
	return def
}

func items(n int, f func(i int) string) string {
	s := make([]string, n)
	for i := range s {
		s[i] = f(i + 1)
	}
	return strings.Join(s, " OR ")
}

type command struct {
	name, usage string
	run         func(a []string, has []bool, f map[string]string) any
}

func arg(a []string, i int) (string, bool) {
	if i < len(a) {
		return a[i], true
	}
	return "", false
}

var digits = regexp.MustCompile(`^\d+$`)

var commands = []command{
	{"search", "search <text> [--type item|quest|npc|object|spell|zone|achievement]  name search (15 per type)", func(a []string, _ []bool, f map[string]string) any {
		text, _ := arg(a, 0)
		if text == "" {
			panic(qerr{fmt.Errorf("search: give some text")})
		}
		like := sqlStr("%" + text + "%")
		t, hasT := f["type"]
		parts := []struct{ k, sql string }{
			{"item", "SELECT entry id, name, ItemLevel level, Quality quality FROM item_template WHERE name LIKE " + like + " ORDER BY ItemLevel"},
			{"quest", "SELECT ID id, LogTitle name, QuestLevel level FROM quest_template WHERE LogTitle LIKE " + like + " ORDER BY QuestLevel"},
			{"npc", "SELECT entry id, name, subname, minlevel level FROM creature_template WHERE name LIKE " + like + " ORDER BY minlevel"},
			{"object", "SELECT entry id, name, type FROM gameobject_template WHERE name LIKE " + like},
			{"spell", "SELECT id, name, `rank` FROM wowbench.spell WHERE name LIKE " + like + " ORDER BY level"},
			{"zone", "SELECT id, name, zone parent_zone, level FROM wowbench.area WHERE name LIKE " + like},
			{"achievement", "SELECT id, name, points FROM wowbench.achievement WHERE name LIKE " + like + " AND flags & 1 = 0"},
		}
		out := newObj()
		for _, p := range parts {
			if !hasT || t == "" || p.k == t {
				out.set(p.k, q(p.sql+" LIMIT "+limit))
			}
		}
		return out
	}},

	{"item", "item <id>  stats and every source: drops, containers, vendors, quest rewards, crafting; quests that need it", func(a []string, _ []bool, _ map[string]string) any {
		s, ok := arg(a, 0)
		id := num(s, "item", ok)
		ids := jsStr(id)
		it := q1("SELECT * FROM item_template WHERE entry = " + ids)
		if it == nil {
			panic(qerr{fmt.Errorf("no item %s", ids)})
		}
		stats := newObj()
		for i := 1; i <= 10; i++ {
			k, v := it.get(fmt.Sprintf("stat_type%d", i)), it.get(fmt.Sprintf("stat_value%d", i))
			if !truthy(v) {
				continue
			}
			name, ok := statNames[int(jsNum(k))]
			if !ok || float64(int(jsNum(k))) != jsNum(k) {
				name = "stat" + jsStr(k)
			}
			stats.set(name, v)
		}
		cl := lootEntries("l", id)
		drops := q(fmt.Sprintf("SELECT c.entry, c.name, c.minlevel, c.maxlevel, c.`rank`, l.Chance chance, l.QuestRequired quest_only,\n"+
			"          r.to_alliance, r.to_horde FROM creature_loot_template l JOIN creature_template c ON c.lootid = l.Entry\n"+
			"          LEFT JOIN wowbench.reaction r ON r.faction_template = c.faction WHERE %s ORDER BY l.Chance DESC LIMIT %s", cl.direct, limit))
		for _, d := range drops {
			d.set("rank", pick(rankNames, d.n("rank")))
			d.set("spawns", spawns("creature_spawn", d.n("entry"), nil, 3))
		}
		var dropsTotal any
		if r := q1("SELECT COUNT(DISTINCT c.entry) n FROM creature_loot_template l JOIN creature_template c ON c.lootid = l.Entry WHERE " + cl.direct); r != nil {
			dropsTotal = r.get("n")
		}
		var shared any
		if len(cl.refs) > 0 {
			shared = q1("SELECT COUNT(DISTINCT c.entry) creatures, MIN(c.minlevel) min_level, MAX(c.maxlevel) max_level\n" +
				"          FROM creature_loot_template l JOIN creature_template c ON c.lootid = l.Entry WHERE " + cl.viaRef)
		}
		gl := lootEntries("l", id)
		objects := withSpawns(q(fmt.Sprintf("SELECT g.entry, g.name, l.Chance chance FROM gameobject_loot_template l\n"+
			"          JOIN gameobject_template g ON g.Data1 = l.Entry AND g.type IN (3, 25) WHERE %s OR %s LIMIT %s", gl.direct, gl.viaRef, limit)), "gameobject_spawn", 3)
		vendors := withSpawns(q("SELECT c.entry, c.name, c.subname, v.maxcount stock FROM npc_vendor v JOIN creature_template c ON c.entry = v.entry\n"+
			"          WHERE v.item = "+ids+" LIMIT "+limit), "creature_spawn", 2)
		rewardCols := []string{}
		for i := 1; i <= 4; i++ {
			rewardCols = append(rewardCols, fmt.Sprintf("RewardItem%d = %s", i, ids))
		}
		for i := 1; i <= 6; i++ {
			rewardCols = append(rewardCols, fmt.Sprintf("RewardChoiceItemID%d = %s", i, ids))
		}
		rewardOf := q("SELECT ID id, LogTitle name, QuestLevel level FROM quest_template WHERE " + strings.Join(rewardCols, " OR ") + " LIMIT " + limit)
		neededBy := q("SELECT ID id, LogTitle name, QuestLevel level FROM quest_template WHERE " +
			items(6, func(i int) string { return fmt.Sprintf("RequiredItemId%d = %s", i, ids) }) + " LIMIT " + limit)
		craftedBy := q("SELECT id spell, name, `rank` FROM wowbench.spell WHERE create_item = " + ids + " LIMIT " + limit)
		containers := q("SELECT i.entry, i.name, l.Chance chance FROM item_loot_template l JOIN item_template i ON i.entry = l.Entry\n" +
			"          WHERE l.Item = " + ids + " LIMIT " + limit)
		var damage any
		if truthy(it.get("dmg_max1")) {
			damage = newObj("min", it.get("dmg_min1"), "max", it.get("dmg_max1"), "speed_ms", it.get("delay"))
		}
		return newObj("id", id, "name", it.get("name"), "quality", pick(qualityNames, it.n("Quality")), "item_level", it.get("ItemLevel"),
			"required_level", it.get("RequiredLevel"), "slot", pick(slotNames, it.n("InventoryType")), "class", it.get("class"),
			"subclass", it.get("subclass"), "stackable", it.get("stackable"), "buy_price", it.get("BuyPrice"), "sell_price", it.get("SellPrice"),
			"armor", or(it.get("armor")), "damage", damage, "stats", stats, "allowable_class", it.get("AllowableClass"),
			"allowable_race", it.get("AllowableRace"), "quest_item", it.get("class") == 12.0,
			"sources", newObj("drops_total", dropsTotal, "drops", drops, "shared_drop", shared, "objects", objects, "vendors", vendors,
				"reward_of", rewardOf, "crafted_by", craftedBy, "containers", containers), "needed_by", neededBy)
	}},

	{"quest", "quest <id>  requirements, giver/turn-in with locations, objectives with where to find them, rewards, chain", func(a []string, _ []bool, _ map[string]string) any {
		s, ok := arg(a, 0)
		id := num(s, "quest", ok)
		ids := jsStr(id)
		qt := q1("SELECT * FROM quest_template q LEFT JOIN quest_template_addon x ON x.ID = q.ID WHERE q.ID = " + ids)
		if qt == nil {
			panic(qerr{fmt.Errorf("no quest %s", ids)})
		}
		who := func(t, g string) []*obj {
			npcs := withSpawns(q(fmt.Sprintf("SELECT c.entry, c.name, 'npc' kind FROM %s s JOIN creature_template c ON c.entry = s.id WHERE s.quest = %s", t, ids)), "creature_spawn", 2)
			objs := withSpawns(q(fmt.Sprintf("SELECT o.entry, o.name, 'object' kind FROM %s s JOIN gameobject_template o ON o.entry = s.id WHERE s.quest = %s", g, ids)), "gameobject_spawn", 2)
			return append(npcs, objs...)
		}
		f := func(k string, i int) any { return qt.get(fmt.Sprintf(k, i)) }
		objectives := []*obj{}
		for i := 1; i <= 4; i++ {
			if !truthy(f("RequiredNpcOrGo%d", i)) {
				continue
			}
			t := jsNum(f("RequiredNpcOrGo%d", i))
			count, text := f("RequiredNpcOrGoCount%d", i), or(f("ObjectiveText%d", i))
			if t > 0 {
				c := q1(`SELECT c.name, c.minlevel, c.maxlevel, r.to_alliance, r.to_horde FROM creature_template c
            LEFT JOIN wowbench.reaction r ON r.faction_template = c.faction WHERE c.entry = ` + jsStr(t))
				objectives = append(objectives, newObj("kill_or_interact", newObj("npc", t).merge(c), "count", count, "text", text,
					"spawns", spawns("creature_spawn", t, nil, 5)))
				continue
			}
			objectives = append(objectives, newObj("use_object", newObj("object", -t, "name", q1("SELECT name FROM gameobject_template WHERE entry = "+jsStr(-t)).get("name")),
				"count", count, "text", text, "spawns", spawns("gameobject_spawn", -t, nil, 5)))
		}
		needItems := []*obj{}
		for i := 1; i <= 6; i++ {
			if !truthy(f("RequiredItemId%d", i)) {
				continue
			}
			it := jsNum(f("RequiredItemId%d", i))
			cl := lootEntries("l", it)
			from := withSpawns(q(fmt.Sprintf(`SELECT c.entry, c.name, c.minlevel, c.maxlevel, l.Chance chance FROM creature_loot_template l
            JOIN creature_template c ON c.lootid = l.Entry WHERE %s OR %s ORDER BY l.Chance DESC LIMIT 5`, cl.direct, cl.viaRef)), "creature_spawn", 3)
			objs := withSpawns(q(fmt.Sprintf(`SELECT g.entry, g.name, l.Chance chance FROM gameobject_loot_template l JOIN gameobject_template g
            ON g.Data1 = l.Entry AND g.type IN (3, 25) WHERE %s LIMIT 5`, cl.direct)), "gameobject_spawn", 3)
			needItems = append(needItems, newObj("item", it, "name", q1("SELECT name FROM item_template WHERE entry = "+jsStr(it)).get("name"),
				"count", f("RequiredItemCount%d", i), "dropped_by", from, "from_objects", objs))
		}
		named := func(list []*obj) []*obj {
			for _, r := range list {
				r.merge(q1("SELECT name, ItemLevel item_level, InventoryType slot FROM item_template WHERE entry = " + jsStr(r.get("item"))))
			}
			return list
		}
		rewardItems, choice := []*obj{}, []*obj{}
		for i := 1; i <= 4; i++ {
			if truthy(f("RewardItem%d", i)) {
				rewardItems = append(rewardItems, newObj("item", f("RewardItem%d", i), "count", f("RewardAmount%d", i)))
			}
		}
		for i := 1; i <= 6; i++ {
			if truthy(f("RewardChoiceItemID%d", i)) {
				choice = append(choice, newObj("item", f("RewardChoiceItemID%d", i), "count", f("RewardChoiceItemQuantity%d", i)))
			}
		}
		rep := []*obj{}
		for i := 1; i <= 5; i++ {
			if truthy(f("RewardFactionID%d", i)) {
				rep = append(rep, newObj("faction", q1("SELECT name FROM wowbench.faction WHERE id = "+jsStr(f("RewardFactionID%d", i))).get("name"),
					"value", f("RewardFactionValue%d", i)))
			}
		}
		next := or(qt.get("NextQuestID"))
		if next == nil {
			next = or(qt.get("RewardNextQuest"))
		}
		return newObj("id", id, "name", qt.get("LogTitle"), "level", qt.get("QuestLevel"), "min_level", qt.get("MinLevel"),
			"allowable_races", qt.get("AllowableRaces"), "allowable_classes", qt.get("AllowableClasses"),
			"objective_summary", qt.get("LogDescription"), "details", qt.get("QuestDescription"),
			"starts_at", who("creature_queststarter", "gameobject_queststarter"), "ends_at", who("creature_questender", "gameobject_questender"),
			"objectives", objectives, "items", needItems, "required_kills_players", or(qt.get("RequiredPlayerKills")),
			"rewards", newObj("money", qt.get("RewardMoney"), "xp_difficulty", qt.get("RewardXPDifficulty"), "spell", or(qt.get("RewardSpell")),
				"items", named(rewardItems), "choose_one", named(choice), "reputation", rep),
			"chain", newObj("previous", or(qt.get("PrevQuestID")), "next", next, "exclusive_group", or(qt.get("ExclusiveGroup"))))
	}},

	{"npc", "npc <id> [--near x,y]  level, rank, hostility, spawns, loot, vendor stock, trainer spells, quests", func(a []string, _ []bool, f map[string]string) any {
		s, ok := arg(a, 0)
		id := num(s, "npc", ok)
		ids := jsStr(id)
		c := q1(`SELECT c.*, r.to_alliance, r.to_horde, fa.name faction_name FROM creature_template c
        LEFT JOIN wowbench.reaction r ON r.faction_template = c.faction LEFT JOIN wowbench.faction fa ON fa.id = r.faction WHERE c.entry = ` + ids)
		if c == nil {
			panic(qerr{fmt.Errorf("no npc %s", ids)})
		}
		var near []float64
		if truthy(f["near"]) {
			near = numList(f["near"])
		}
		lootID := c.n("lootid")
		if !truthy(lootID) {
			lootID = -1
		}
		return newObj("id", id, "name", c.get("name"), "subname", or(c.get("subname")), "level", []any{c.get("minlevel"), c.get("maxlevel")},
			"rank", pick(rankNames, c.n("rank")), "faction", c.get("faction_name"),
			"reaction", newObj("alliance", c.get("to_alliance"), "horde", c.get("to_horde")), "services", npcFlags(int(c.n("npcflag"))),
			"spawns", spawns("creature_spawn", id, near, 5),
			"loot", q(fmt.Sprintf(`SELECT i.entry, i.name, l.Chance chance, l.QuestRequired quest_only FROM creature_loot_template l
            JOIN item_template i ON i.entry = l.Item WHERE l.Entry = %s AND l.Reference = 0 ORDER BY l.Chance DESC LIMIT %s`, jsStr(lootID), limit)),
			"sells", q(`SELECT i.entry, i.name, i.BuyPrice price, i.ItemLevel item_level, i.RequiredLevel required_level FROM npc_vendor v
            JOIN item_template i ON i.entry = v.item WHERE v.entry = `+ids+` LIMIT 60`),
			"teaches", q("SELECT ts.SpellId spell, s.name, s.`rank`, ts.MoneyCost cost, ts.ReqLevel level FROM creature_default_trainer d\n"+
				"            JOIN trainer_spell ts ON ts.TrainerId = d.TrainerId LEFT JOIN wowbench.spell s ON s.id = ts.SpellId\n"+
				"            WHERE d.CreatureId = "+ids+" ORDER BY ts.ReqLevel LIMIT 80"),
			"starts_quests", q("SELECT q.ID id, q.LogTitle name, q.QuestLevel level FROM creature_queststarter s JOIN quest_template q ON q.ID = s.quest WHERE s.id = "+ids),
			"ends_quests", q("SELECT q.ID id, q.LogTitle name FROM creature_questender s JOIN quest_template q ON q.ID = s.quest WHERE s.id = "+ids))
	}},

	{"spell", "spell <id>  name, rank, description, cost, cast time, range, cooldowns, item made, trainers that teach it", func(a []string, _ []bool, _ map[string]string) any {
		s, ok := arg(a, 0)
		id := num(s, "spell", ok)
		ids := jsStr(id)
		sp := q1("SELECT * FROM wowbench.spell WHERE id = " + ids)
		if sp == nil {
			panic(qerr{fmt.Errorf("no spell %s", ids)})
		}
		pt := sp.n("power_type")
		power := map[float64]string{0: "mana", 1: "rage", 3: "energy", 6: "runic power", -2: "health"}
		var pv, note, created any
		if v, ok := power[pt]; ok {
			pv = v
		}
		if pt == 1 || pt == 6 {
			note = "x10, as get_power()"
		}
		if truthy(sp.get("create_item")) {
			created = newObj("id", sp.get("create_item"), "name", q1("SELECT name FROM item_template WHERE entry = "+jsStr(sp.get("create_item"))).get("name"))
		}
		taught := withSpawns(q(`SELECT c.entry, c.name, c.subname, ts.MoneyCost cost, ts.ReqLevel level FROM trainer_spell ts
            JOIN creature_default_trainer d ON d.TrainerId = ts.TrainerId JOIN creature_template c ON c.entry = d.CreatureId
            WHERE ts.SpellId = `+ids+` LIMIT `+limit), "creature_spawn", 1)
		return sp.set("power_type", pv).set("cost_note", note).set("create_item", created).set("taught_by", taught)
	}},

	{"object", "object <id>  type, spawns, loot (chests, herbs, ore), quests", func(a []string, _ []bool, _ map[string]string) any {
		s, ok := arg(a, 0)
		id := num(s, "object", ok)
		ids := jsStr(id)
		o := q1("SELECT * FROM gameobject_template WHERE entry = " + ids)
		if o == nil {
			panic(qerr{fmt.Errorf("no object %s", ids)})
		}
		var lootRows any
		if t := o.n("type"); t == 3 || t == 25 {
			lootRows = q(fmt.Sprintf(`SELECT i.entry, i.name, l.Chance chance FROM gameobject_loot_template l
            JOIN item_template i ON i.entry = l.Item WHERE l.Entry = %s ORDER BY l.Chance DESC LIMIT %s`, jsStr(o.get("Data1")), limit))
		}
		return newObj("id", id, "name", o.get("name"), "type", o.get("type"), "spawns", spawns("gameobject_spawn", id, nil, 5), "loot", lootRows,
			"starts_quests", q("SELECT q.ID id, q.LogTitle name FROM gameobject_queststarter s JOIN quest_template q ON q.ID = s.quest WHERE s.id = "+ids),
			"ends_quests", q("SELECT q.ID id, q.LogTitle name FROM gameobject_questender s JOIN quest_template q ON q.ID = s.quest WHERE s.id = "+ids))
	}},

	{"zone", "zone <id|name>  sub-areas with locations, mobs by level, graveyards, services (flight master, inn, vendors, trainers)", func(a []string, _ []bool, _ map[string]string) any {
		s, ok := arg(a, 0)
		var z *obj
		if digits.MatchString(s) {
			z = q1("SELECT * FROM wowbench.area WHERE id = " + s)
		} else {
			z = q1("SELECT * FROM wowbench.area WHERE name = " + sqlStr(s) + " ORDER BY zone = 0 DESC LIMIT 1")
		}
		if z == nil {
			if !ok {
				s = "undefined"
			}
			panic(qerr{fmt.Errorf("no zone %s", s)})
		}
		id := z.n("zone")
		if !truthy(id) {
			id = z.n("id")
		}
		ids := jsStr(id)
		svc := func(bit int) []*obj {
			return q(fmt.Sprintf(`SELECT c.entry, c.name, c.subname, ROUND(s.x) x, ROUND(s.y) y, ROUND(s.z) z, r.to_alliance, r.to_horde
          FROM wowbench.creature_spawn s JOIN creature_template c ON c.entry = s.id LEFT JOIN wowbench.reaction r ON r.faction_template = c.faction
          WHERE s.zone = %s AND c.npcflag & %d LIMIT %s`, ids, bit, limit))
		}
		mobs := q("SELECT c.entry, c.name, c.minlevel, c.maxlevel, c.`rank`, r.to_alliance, r.to_horde, COUNT(*) spawns,\n" +
			"            ROUND(AVG(s.x)) x, ROUND(AVG(s.y)) y FROM wowbench.creature_spawn s JOIN creature_template c ON c.entry = s.id\n" +
			"            LEFT JOIN wowbench.reaction r ON r.faction_template = c.faction WHERE s.zone = " + ids + " AND c.npcflag = 0\n" +
			"            GROUP BY c.entry ORDER BY c.minlevel, spawns DESC LIMIT 60")
		for _, m := range mobs {
			m.set("rank", pick(rankNames, m.n("rank")))
		}
		return newObj("id", id, "name", q1("SELECT name FROM wowbench.area WHERE id = "+ids).get("name"), "map", z.get("map"),
			"areas", q(`SELECT a.id, a.name, l.x, l.y FROM wowbench.area a LEFT JOIN wowbench.area_location l ON l.area = a.id AND l.map = a.map
            WHERE a.zone = `+ids+` ORDER BY a.name`),
			"mobs", mobs,
			"graveyards", q(`SELECT g.ID id, ROUND(g.x) x, ROUND(g.y) y, ROUND(g.z) z, g.Comment name,
            CASE gz.Faction WHEN 469 THEN 'alliance' WHEN 67 THEN 'horde' ELSE 'any' END faction
            FROM graveyard_zone gz JOIN game_graveyard g ON g.ID = gz.ID WHERE gz.GhostZone = `+ids),
			"flight_masters", svc(0x2000), "innkeepers", svc(0x10000), "vendors", svc(0x80), "trainers", svc(0x10), "quest_givers", svc(0x2))
	}},

	{"near", "near <x> <y> [--map 0] [--kind vendor|trainer|questgiver|flightmaster|innkeeper|repair|banker|mob|graveyard|object]\n" +
		"     [--level 8-12] [--hostile] [--faction alliance|horde] [--limit 10]  nearest things to a point", func(a []string, _ []bool, f map[string]string) any {
		sx, okx := arg(a, 0)
		sy, oky := arg(a, 1)
		x, y := num(sx, "x", okx), num(sy, "y", oky)
		m := jsStr(jsNum(orMap(f, "map", "0")))
		lim := jsStr(math.Min(jsNum(orMap(f, "limit", "10")), 50))
		side := "to_alliance"
		if f["faction"] == "horde" {
			side = "to_horde"
		}
		kind := orMap(f, "kind", "mob")
		dist := fmt.Sprintf("ROUND(SQRT(POW(s.x - %s, 2) + POW(s.y - %s, 2)))", jsStr(x), jsStr(y))
		box := fmt.Sprintf("s.map = %s AND s.x BETWEEN %s AND %s AND s.y BETWEEN %s AND %s", m, jsStr(x-3000), jsStr(x+3000), jsStr(y-3000), jsStr(y+3000))
		if kind == "graveyard" {
			return q(fmt.Sprintf(`SELECT s.ID id, s.Comment name, ROUND(s.x) x, ROUND(s.y) y, ROUND(s.z) z, %s distance FROM game_graveyard s
          WHERE s.Map = %s ORDER BY distance LIMIT %s`, dist, m, lim))
		}
		if kind == "object" {
			return q(fmt.Sprintf(`SELECT o.entry, o.name, o.type, ROUND(s.x) x, ROUND(s.y) y, ROUND(s.z) z, %s distance FROM wowbench.gameobject_spawn s
          JOIN gameobject_template o ON o.entry = s.id WHERE %s ORDER BY distance LIMIT %s`, dist, box, lim))
		}
		bit := -1
		for _, n := range npcFlagNames {
			if n.name == kind {
				bit = n.bit
				break
			}
		}
		if kind != "mob" && bit < 0 {
			panic(qerr{fmt.Errorf("near: unknown --kind %s", kind)})
		}
		var where string
		if kind == "mob" {
			where = "c.npcflag = 0"
			if _, ok := f["hostile"]; ok {
				where += " AND r." + side + " = 'hostile'"
			}
		} else {
			where = fmt.Sprintf("c.npcflag & %d AND r.%s IN ('friendly', 'neutral')", bit, side)
		}
		lv := ""
		if l, ok := f["level"]; ok {
			p := strings.Split(l, "-")
			lo := jsNum(p[0])
			hi := lo
			if len(p) > 1 {
				hi = jsNum(p[1])
			}
			lv = fmt.Sprintf(" AND c.maxlevel >= %s AND c.minlevel <= %s", jsStr(lo), jsStr(hi))
		}
		return q(fmt.Sprintf(`SELECT c.entry, c.name, c.subname, c.minlevel, c.maxlevel, r.%s reaction, ROUND(s.x) x, ROUND(s.y) y, ROUND(s.z) z,
          a.name area, %s distance FROM wowbench.creature_spawn s JOIN creature_template c ON c.entry = s.id
          LEFT JOIN wowbench.reaction r ON r.faction_template = c.faction LEFT JOIN wowbench.area a ON a.id = s.area
          WHERE %s AND %s AND s.guid NOT IN (SELECT guid FROM game_event_creature)%s
          ORDER BY distance LIMIT %s`, side, dist, box, where, lv, lim))
	}},

	{"quests", "quests --level <n> --race <id> --class <id> [--near x,y] [--map 0] [--limit 25]  quests you can pick up, nearest givers first", func(_ []string, _ []bool, f map[string]string) any {
		fl := func(k string) float64 { v, ok := f[k]; return num(v, "--"+k, ok) }
		level, race, cls := fl("level"), fl("race"), fl("class")
		lim := jsStr(math.Min(jsNum(orMap(f, "limit", "25")), 100))
		dist, order := "0", "q.QuestLevel DESC"
		if n, ok := f["near"]; ok {
			near := numList(n)
			dist = fmt.Sprintf("MIN(SQRT(POW(s.x - %s, 2) + POW(s.y - %s, 2)))", jsStr(near[0]), jsStr(at(near, 1)))
			order = "distance"
		}
		return q(fmt.Sprintf(`SELECT q.ID id, q.LogTitle name, q.QuestLevel level, q.MinLevel min_level, x.PrevQuestID requires_previous,
          c.entry giver, c.name giver_name, ROUND(MIN(s.x)) x, ROUND(MIN(s.y)) y, ROUND(%s) distance
        FROM quest_template q LEFT JOIN quest_template_addon x ON x.ID = q.ID
        JOIN creature_queststarter qs ON qs.quest = q.ID JOIN creature_template c ON c.entry = qs.id
        JOIN wowbench.creature_spawn s ON s.id = c.entry AND s.map = %s
        WHERE q.MinLevel <= %s AND (q.QuestLevel = -1 OR q.QuestLevel BETWEEN %s AND %s)
          AND (q.AllowableRaces = 0 OR q.AllowableRaces & %d)
          AND (x.AllowableClasses IS NULL OR x.AllowableClasses = 0 OR x.AllowableClasses & %d)
          AND (x.RequiredSkillID IS NULL OR x.RequiredSkillID = 0)
        GROUP BY q.ID, c.entry ORDER BY %s LIMIT %s`, dist, jsStr(jsNum(orMap(f, "map", "0"))), jsStr(level), jsStr(level-6), jsStr(level+4),
			jsShl(race), jsShl(cls), order, lim))
	}},

	{"area", "area <x> <y> [--map 0]  which area and zone a point is in", func(a []string, _ []bool, f map[string]string) any {
		sx, okx := arg(a, 0)
		sy, oky := arg(a, 1)
		x, y := num(sx, "x", okx), num(sy, "y", oky)
		r := q1(fmt.Sprintf(`SELECT a.id area, a.name, z.id zone, z.name zone_name, a.level FROM wowbench.area_cell g JOIN wowbench.area a ON a.id = g.area
        LEFT JOIN wowbench.area z ON z.id = IF(a.zone = 0, a.id, a.zone)
        WHERE g.map = %s AND g.ix = %s AND g.iy = %s`, jsStr(jsNum(orMap(f, "map", "0"))),
			jsStr(math.Floor(16*(32-x/GridSize))), jsStr(math.Floor(16*(32-y/GridSize)))))
		if r == nil {
			return nil
		}
		return r
	}},

	{"upgrades", "upgrades --class <name|id> --level <n> [--slot chest] [--near x,y [--radius 3000]] [--min-ilvl n]  gear you can use and where to get it (quest rewards, vendors, drops), best first", func(_ []string, _ []bool, f map[string]string) any {
		cls, ok := classes[strings.ToLower(f["class"])]
		if !ok {
			v, has := f["class"]
			cls = num(v, "--class", has)
		}
		lv, has := f["level"]
		o := upgradeOpts{cls: cls, level: num(lv, "--level", has), mapID: jsNum(orMap(f, "map", "0"))}
		o.slot = f["slot"]
		o.hasSlot = o.slot != ""
		if n, ok := f["near"]; ok {
			o.near = numList(n)
		}
		o.radius, o.minIlvl, o.limit = flagNum(f, "radius"), flagNum(f, "min-ilvl"), flagNum(f, "limit")
		return upgrades(o)
	}},

	{"roads", "roads <x> <y> [--radius 500] [--map 0]  road polylines near a point (from guard/patroller routes)", func(a []string, _ []bool, f map[string]string) any {
		sx, okx := arg(a, 0)
		sy, oky := arg(a, 1)
		cx, cy := num(sx, "x", okx), num(sy, "y", oky)
		r, m := jsNum(orMap(f, "radius", "500")), jsNum(orMap(f, "map", "0"))
		out := [][][2]float64{}
		for _, p := range roadPaths(m, cx-r, cx+r, cy-r, cy+r) {
			var thin [][2]float64
			for i, v := range p {
				if i%3 == 0 || i == len(p)-1 {
					thin = append(thin, v)
				}
			}
			out = append(out, thin)
		}
		return out
	}},

	{"route", "route <x1> <y1> <x2> <y2> --level <your level> [--map 0] [--horde]  walking waypoints that prefer roads and avoid hostile mobs near or above your level", func(a []string, _ []bool, f map[string]string) any {
		n := func(i int, what string) float64 { s, ok := arg(a, i); return num(s, what, ok) }
		from, to := [2]float64{n(0, "x1"), n(1, "y1")}, [2]float64{n(2, "x2"), n(3, "y2")}
		lv, has := f["level"]
		_, horde := f["horde"]
		return route(jsNum(orMap(f, "map", "0")), from, to, num(lv, "--level", has), horde)
	}},

	{"map", "map <x> <y> --level <your level> [--radius 300] [--to <x2>,<y2>] [--out map.png] [--map 0]  PNG of terrain, roads and hostile mobs (open it with your Read tool)", func(a []string, _ []bool, f map[string]string) any {
		sx, okx := arg(a, 0)
		sy, oky := arg(a, 1)
		cx, cy := num(sx, "x", okx), num(sy, "y", oky)
		lv, has := f["level"]
		lvl, m := num(lv, "--level", has), jsNum(orMap(f, "map", "0"))
		var r *obj
		var to []float64
		if t, ok := f["to"]; ok && t != "" {
			to = numList(t)
			r = route(m, [2]float64{cx, cy}, [2]float64{to[0], at(to, 1)}, lvl, false)
		}
		radius := 300.0
		if v, ok := f["radius"]; ok {
			radius = jsNum(v)
		} else if to != nil {
			radius = math.Max(200, float64(math.Hypot(at(to, 0)-cx, at(to, 1)-cy)*0.7))
		}
		out := f["out"]
		if out == "" {
			out = "map.png"
		}
		var path [][3]float64
		if r != nil {
			if w, ok := r.get("waypoints").([][3]float64); ok {
				path = w
			}
		}
		img := mapImage(m, cx, cy, radius, lvl, out, path)
		if r != nil {
			img.set("route", r)
		}
		return img
	}},

	{"sql", "sql <query>  raw read-only SQL (acore_world; wowbench.* for the derived tables), tab-separated, at most 300 rows", nil},
}

// jsShl is JS `1 << (n - 1)` (int32, shift count mod 32).
func jsShl(n float64) int32 { return int32(1) << (uint32(int32(n)-1) & 31) }

// opt is an optional number (a flag that may be absent).
type opt struct {
	v  float64
	ok bool
}

func (o opt) or(def float64) float64 {
	if o.ok {
		return o.v
	}
	return def
}

// flagNum is `f[k] ? Number(f[k]) : undefined`.
func flagNum(f map[string]string, k string) opt {
	if s := f[k]; s != "" {
		return opt{jsNum(s), true}
	}
	return opt{}
}

// Run runs one azworld query command line (without the program name), printing its JSON (or the help) to w; it returns
// the exit code.
func Run(argv []string, w io.Writer) (code int) {
	var cmd string
	var rest []string
	if len(argv) > 0 {
		cmd, rest = argv[0], argv[1:]
	}
	args, flags := []string{}, map[string]string{}
	for i := 0; i < len(rest); i++ {
		if strings.HasPrefix(rest[i], "--") {
			k := rest[i][2:]
			if i+1 < len(rest) && rest[i+1] != "" && !strings.HasPrefix(rest[i+1], "--") {
				i++
				flags[k] = rest[i]
			} else {
				flags[k] = ""
			}
		} else {
			args = append(args, rest[i])
		}
	}
	if cmd == "sql" { // a table, not JSON
		return SQL(rest)
	}
	var c *command
	for i := range commands {
		if commands[i].name == cmd {
			c = &commands[i]
		}
	}
	if c == nil {
		var b strings.Builder
		b.WriteString("azworld <command> ...  (JSON out)")
		for _, k := range commands {
			b.WriteString("\n  ")
			if !strings.HasPrefix(k.usage, k.name) {
				b.WriteString(k.name + " ")
			}
			b.WriteString(k.usage)
		}
		fmt.Fprintln(w, b.String())
		if cmd != "" && cmd != "help" {
			return 1
		}
		return 0
	}
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(qerr)
			if !ok {
				e = qerr{fmt.Errorf("%v", r)}
			}
			w.Write(toJSON(newObj("error", e.Error())))
			fmt.Fprintln(w)
			code = 1
		}
	}()
	v := c.run(args, nil, flags)
	if v == nil || v == "" { // JSON.stringify with the drop-empty replacer gives undefined for a top-level null
		fmt.Fprintln(w, "undefined")
		return 0
	}
	w.Write(toJSON(v))
	fmt.Fprintln(w)
	return 0
}
