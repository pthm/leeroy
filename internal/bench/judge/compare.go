package judge

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// MeanSE is mean ± standard error; nil for no values, se nil for one.
func MeanSE(v []float64) any {
	if len(v) == 0 {
		return nil
	}
	mean := 0.0
	for _, x := range v {
		mean += x
	}
	mean /= float64(len(v))
	out := map[string]any{"mean": mean, "se": nil}
	if len(v) > 1 {
		ss := 0.0
		for _, x := range v {
			ss += (x - mean) * (x - mean)
		}
		out["se"] = math.Sqrt(ss / float64(len(v)-1) / float64(len(v)))
	}
	return out
}

// byModel groups report cards by runtime and model, the unit of comparison (the same model on two harnesses is two
// rows): players, score mean ± standard error, best, total, and cost (null if any player's is unknown), also per
// agent-hour.
func byModel(cards []map[string]any) []obj {
	groups := map[[2]string][]map[string]any{}
	var keys [][2]string
	for _, c := range cards {
		k := [2]string{str(c["runtime"]), str(c["model"])}
		if groups[k] == nil {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], c)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	var out []obj
	for _, k := range keys {
		var scores []float64
		best, total, usd, hours, costKnown := 0.0, 0.0, 0.0, 0.0, true
		for _, c := range groups[k] {
			s := fnum(get(c["score"], "score"))
			scores, best, total = append(scores, s), math.Max(best, s), total+s
			if v, ok := c["cost_usd"].(float64); ok {
				usd += v
			} else {
				costKnown = false
			}
			hours += fnum(c["hours"])
		}
		var cost, perHour any
		if costKnown {
			cost = round1(usd)
			if hours > 0 {
				perHour = round1(usd / hours)
			}
		}
		out = append(out, obj{{"runtime", k[0]}, {"model", k[1]}, {"players", len(scores)}, {"score", MeanSE(scores)},
			{"best", best}, {"total", round1(total)}, {"cost_usd", cost}, {"cost_per_agent_hour", perHour}})
	}
	return out
}

// Compare pools the players of several runs (their report.json) by runtime and model: a table, then the groups as
// JSON.
func Compare(dirs []string) {
	var cards []map[string]any
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(d, "report.json"))
		if err != nil {
			fail("%s: %v (azbench report it first)", d, err)
		}
		var r struct{ Agents []map[string]any }
		if err := json.Unmarshal(b, &r); err != nil {
			fail("%s/report.json: %v", d, err)
		}
		cards = append(cards, r.Agents...)
	}
	groups := byModel(cards)
	fmt.Printf("%-9s %-40s %3s %9s %7s %9s %8s %7s\n", "runtime", "model", "n", "score", "±se", "best", "cost", "$/h")
	for _, g := range groups {
		m := map[string]any{}
		for _, e := range g {
			m[e.k] = e.v
		}
		mean, se := "-", "-"
		if s, ok := m["score"].(map[string]any); ok {
			mean = fmt.Sprintf("%.0f", s["mean"])
			if s["se"] != nil {
				se = fmt.Sprintf("%.0f", s["se"])
			}
		}
		dash := func(v any) string {
			if v == nil {
				return "-"
			}
			return fmt.Sprint(v)
		}
		fmt.Printf("%-9s %-40s %3d %9s %7s %9.0f %8s %7s\n", m["runtime"], m["model"], m["players"], mean, se, m["best"],
			dash(m["cost_usd"]), dash(m["cost_per_agent_hour"]))
	}
	j, _ := json.MarshalIndent(groups, "", "  ")
	fmt.Println(string(j))
}
