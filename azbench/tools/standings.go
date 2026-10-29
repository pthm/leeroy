package tools

import (
	"fmt"
	"github.com/pthm/leeroy/azeroth-sdk/jqjson"
	"math"
	"os"
	"path/filepath"
	"sort"
)

const standingsUsage = `standings: where everyone in this run stands, best score first (score = (overall rate + best 15-min rate) / 2,
in progress points per hour), updated every 15s by the judges. "you": true marks your character. The last line is
the faction: the sum over all players. role, damage_dealt, damage_taken and healing_done (this run) show how each
plays their role; assist_share (how much of your useful hits and heals also paid a teammate: shared kills, heals
on others) and contested_hits (hits on mobs whose XP went to someone outside your party) how you cooperate. All
reported, not scored. minutes_left on the faction line is the time left in the run.
`

// Standings prints every judge's latest board entry, best score first, then the faction line.
func Standings(args []string) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Print(standingsUsage)
		return 0
	}
	b := os.Getenv("WOWBENCH_BOARD")
	if b == "" {
		fmt.Fprintln(os.Stderr, "standings: WOWBENCH_BOARD: standings: not available in this run")
		return 1
	}
	me := os.Getenv("WOWBENCH_CHAR")
	files, _ := filepath.Glob(filepath.Join(b, "*.json"))
	var all []byte
	for _, f := range files {
		d, _ := os.ReadFile(f)
		all = append(all, d...)
	}
	vs, err := jqjson.ParseAll(all)
	if err != nil {
		fmt.Fprintf(os.Stderr, "standings: %v\n", err)
		return 2
	}
	sort.SliceStable(vs, func(i, j int) bool {
		return jqjson.Num(jqjson.Field(vs[i], "score")) > jqjson.Num(jqjson.Field(vs[j], "score"))
	})
	var left any
	for _, v := range vs {
		if m := jqjson.Field(v, "minutes_left"); jqjson.Truthy(m) && (left == nil || jqjson.Num(m) > jqjson.Num(left)) {
			left = m
		}
	}
	sum := func(k string) any { // map(.k) | add * 10 | round / 10
		var s float64
		any := false
		for _, v := range vs {
			if x := jqjson.Field(v, k); x != nil {
				s += jqjson.Num(x)
				any = true
			}
		}
		if !any {
			return nil
		}
		return math.Round(s*10) / 10
	}
	for i, v := range vs {
		o := jqjson.NewObj()
		o.Set("rank", i+1)
		o.Set("char", jqjson.Field(v, "char"))
		o.Set("you", jqjson.Field(v, "char") == me)
		for _, k := range []string{"score", "rate", "best_window", "recent_rate", "progress", "level", "role",
			"damage_dealt", "damage_taken", "healing_done", "assist_share", "contested_hits", "avg_item_level", "money"} {
			o.Set(k, jqjson.Field(v, k))
		}
		o.Set("minutes", math.Floor(jqjson.Num(jqjson.Field(v, "elapsed_s"))/60))
		fmt.Println(jqjson.Compact(o))
	}
	f := jqjson.NewObj()
	f.Set("faction", "Alliance")
	f.Set("players", len(vs))
	f.Set("minutes_left", left)
	f.Set("score", sum("score"))
	f.Set("progress", sum("progress"))
	f.Set("recent_rate", sum("recent_rate"))
	fmt.Println(jqjson.Compact(f))
	return 0
}
