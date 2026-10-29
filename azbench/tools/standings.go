package tools

import (
	"fmt"
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
	vs, err := parseAll(all)
	if err != nil {
		fmt.Fprintf(os.Stderr, "standings: %v\n", err)
		return 2
	}
	sort.SliceStable(vs, func(i, j int) bool { return num(field(vs[i], "score")) > num(field(vs[j], "score")) })
	var left any
	for _, v := range vs {
		if m := field(v, "minutes_left"); truthy(m) && (left == nil || num(m) > num(left)) {
			left = m
		}
	}
	sum := func(k string) any { // map(.k) | add * 10 | round / 10
		var s float64
		any := false
		for _, v := range vs {
			if x := field(v, k); x != nil {
				s += num(x)
				any = true
			}
		}
		if !any {
			return nil
		}
		return math.Round(s*10) / 10
	}
	for i, v := range vs {
		o := newObj()
		o.set("rank", i+1)
		o.set("char", field(v, "char"))
		o.set("you", field(v, "char") == me)
		for _, k := range []string{"score", "rate", "best_window", "recent_rate", "progress", "level", "role",
			"damage_dealt", "damage_taken", "healing_done", "assist_share", "contested_hits", "avg_item_level", "money"} {
			o.set(k, field(v, k))
		}
		o.set("minutes", math.Floor(num(field(v, "elapsed_s"))/60))
		fmt.Println(compact(o))
	}
	f := newObj()
	f.set("faction", "Alliance")
	f.set("players", len(vs))
	f.set("minutes_left", left)
	f.set("score", sum("score"))
	f.set("progress", sum("progress"))
	f.set("recent_rate", sum("recent_rate"))
	fmt.Println(compact(f))
	return 0
}
