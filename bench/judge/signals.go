package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"regexp"
	"strconv"
	"strings"

	"github.com/pthm/leeroy/azworld/world"
)

// Progress signals for one character, read from the character DB (the server's record, not the client's).
// `judge signals <character>` prints them; the judge samples them every poll and scores `score:<n>` goals as the
// weighted sum of each signal's change since it started (weights: score.json).

var floor = math.Floor

var nameRe = regexp.MustCompile(`^[A-Za-z]+$`)

// sql runs a query on acore_characters and returns its rows tab-joined, like `mysql -N -B` (trimmed).
func sql(q string) string {
	rows := world.MustRows(world.DB(world.Admin, "acore_characters"), q)
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = strings.Join(r, "\t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// num is JS Number(s) || 0 for the values the DB returns.
func num(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(f) {
		return 0
	}
	return f
}

// Profession skill lines: first aid, cooking, fishing + the primary professions.
const professions = "129,185,356,164,165,171,182,186,197,202,333,393,755,773"

const (
	statDeaths = 111 // achievement criteria counters ("Total deaths" statistic)
	statKills  = 107 // "Creatures killed" statistic achievement (sum of its criteria)
)

// popcount of a space-separated list of uint32 masks (explored zones, taxi nodes)
func countBits(s string) float64 {
	n := 0
	for _, w := range strings.Fields(s) {
		v, _ := strconv.ParseUint(w, 10, 32)
		n += bits.OnesCount32(uint32(v))
	}
	return float64(n)
}

func signals(char string) obj {
	if !nameRe.MatchString(char) {
		fail("bad character name %s", char)
	}
	g := fmt.Sprintf("(SELECT guid FROM characters WHERE name = '%s')", char)
	crit := func(ids string) string {
		return fmt.Sprintf("(SELECT COALESCE(SUM(counter), 0) FROM character_achievement_progress WHERE guid = %s AND criteria IN (%s))", g, ids)
	}
	row := sql(`SELECT c.level, c.xp, COALESCE(x.Experience, 0), c.money, c.totalHonorPoints, c.arenaPoints, c.totalKills,
      c.totaltime, c.exploredZones, c.taximask,
      (SELECT COALESCE(SUM(a.points), 0) FROM character_achievement ca JOIN wowbench.achievement a ON a.id = ca.achievement WHERE ca.guid = c.guid),
      (SELECT COUNT(*) FROM character_queststatus_rewarded WHERE guid = c.guid),
      (SELECT COALESCE(SUM(standing), 0) FROM character_reputation WHERE guid = c.guid),
      (SELECT COALESCE(SUM(value), 0) FROM character_skills WHERE guid = c.guid AND skill IN (` + professions + `)),
      (SELECT COALESCE(SUM(it.ItemLevel), 0) FROM character_inventory ci JOIN item_instance ii ON ii.guid = ci.item
         JOIN acore_world.item_template it ON it.entry = ii.itemEntry WHERE ci.guid = c.guid AND ci.bag = 0 AND ci.slot < 19 AND ci.slot NOT IN (3, 18)),
      (SELECT COALESCE(SUM(it.SellPrice * ii.count), 0) FROM character_inventory ci JOIN item_instance ii ON ii.guid = ci.item
         JOIN acore_world.item_template it ON it.entry = ii.itemEntry WHERE ci.guid = c.guid),
      ` + crit(strconv.Itoa(statDeaths)) + `,
      ` + crit(fmt.Sprintf("SELECT id FROM wowbench.achievement_criteria WHERE achievement = %d", statKills)) + `,
      (SELECT COALESCE(SUM(Experience), 0) FROM acore_world.player_xp_for_level WHERE Level < c.level)
    FROM characters c LEFT JOIN acore_world.player_xp_for_level x ON x.Level = c.level WHERE c.name = '` + char + `'`)
	if row == "" {
		fail("no character %s", char)
	}
	f := strings.Split(row, "\t")
	n := func(i int) float64 {
		if i >= len(f) {
			return 0
		}
		return num(f[i])
	}
	field := func(i int) string {
		if i >= len(f) {
			return ""
		}
		return f[i]
	}
	level := n(0)
	if n(2) != 0 {
		level += n(1) / n(2) // fractional: 10.5 = halfway to 11
	}
	return obj{
		{"level", level},
		{"xp_earned", n(18) + n(1)}, // total XP to reach your level, plus XP into it
		{"achievement_points", n(10)},
		// ponytail: mean over the 17 gear slots (shirt and tabard excluded), empty = 0; no two-hander/off-hand adjustment
		{"avg_item_level", round1(n(14) / 17)},
		{"wealth_copper", n(3) + n(15)}, // money + vendor value of everything carried, worn or banked
		{"money_copper", n(3)},
		{"honor", n(4)},
		{"arena_points", n(5)},
		{"honorable_kills", n(6)},
		{"quests", n(11)},
		{"reputation", n(12)},
		{"profession_skill", n(13)},
		{"explored_areas", countBits(field(8))},
		{"flight_paths", countBits(field(9))},
		{"deaths", n(16)},
		{"creatures_killed", n(17)},
		{"played_s", n(7)},
	}
}

//go:embed score.json
var scoreJSON []byte

func weights() map[string]float64 {
	var s struct{ Weights map[string]float64 }
	if err := json.Unmarshal(scoreJSON, &s); err != nil {
		fail("score.json: %v", err)
	}
	return s.Weights
}

// composite is the weighted sum of each signal's change since base.
func composite(now obj, base map[string]float64, w map[string]float64) float64 {
	s := 0.0
	for k, wt := range w {
		s += wt * (now.get(k) - base[k])
	}
	return round1(s)
}
