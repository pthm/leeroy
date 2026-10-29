package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pthm/leeroy/azbench/config"
	"github.com/pthm/leeroy/azbench/judge"
)

// cellName: a character name from a number unique to the cell: consonant-vowel syllables (letters only, at most 12).
func cellName(n int64) string {
	const cons, vow = "bcdfghjklmnprstvwxyz", "aeiou"
	var s []byte
	for ; n > 0; n /= 100 {
		s = append(s, cons[n%20], vow[n/20%5])
	}
	if len(s) == 0 {
		return "Ba"
	}
	s[0] -= 'a' - 'A'
	return string(s)
}

// Matrix runs a matrix: every cell is a solo run with a new character and an empty workspace, parallel at a time
// (ports 8090 and up). Writes runs/<time>-matrix-<name>/: cells.jsonl, results.jsonl (each cell's judge result plus
// its cell), failed.jsonl (cells that never got a result), summary.json (n, goals met, mean time to goal, value and
// score as mean ± standard error; per cell, per task × class and per task), and each cell's run dir.
func Matrix(b *config.Bench, file, repo string, dry bool) error {
	allowed, err := allowedPairs()
	if err != nil {
		return fmt.Errorf("can't read playercreateinfo (is the server up?): %v", err)
	}
	m, cells, err := b.LoadMatrix(file, allowed)
	if err != nil {
		return err
	}
	mins := 0
	for _, c := range cells {
		mins += c.Minutes
	}
	fmt.Fprintf(os.Stderr, "%d cells, %d agent-minutes, about %d h at %d in parallel\n", len(cells), mins, mins/m.Parallel/60+1, m.Parallel)
	if dry {
		for _, c := range cells {
			j, _ := json.Marshal(cellJSON(c))
			fmt.Println(string(j))
		}
		return nil
	}
	dir := filepath.Join(repo, "bench/runs", time.Now().Format("20060102-150405")+"-matrix-"+m.Name)
	os.MkdirAll(dir, 0o755)
	src, _ := os.ReadFile(file)
	os.WriteFile(filepath.Join(dir, "matrix.yaml"), src, 0o644)
	appendJSON := func(f string, v any) {
		j, _ := json.Marshal(v)
		fh, _ := os.OpenFile(filepath.Join(dir, f), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		fh.Write(append(j, '\n'))
		fh.Close()
	}
	for _, c := range cells {
		appendJSON("cells.jsonl", cellJSON(c))
	}
	base := time.Now().Unix() % 100000000 * 1000
	slots := make(chan int, m.Parallel)
	for i := range m.Parallel {
		slots <- i
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, c := range cells {
		slot := <-slots
		wg.Add(1)
		go func() {
			defer func() { slots <- slot; wg.Done() }()
			char := cellName(base + int64(i))
			s, err := b.CellScenario(m, c, "m"+strings.ToLower(char), char)
			var line []byte
			if err == nil {
				var r *Runner
				r, err = New(b, s, Options{Repo: repo, BasePort: 8090 + slot, Dir: filepath.Join(dir, char)})
				if err == nil {
					err = r.Run(true)
					line = r.players[0].result
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if line == nil {
				cj := cellJSON(c)
				cj["char"] = char
				if err != nil {
					cj["error"] = err.Error()
				}
				appendJSON("failed.jsonl", cj)
				return
			}
			var res map[string]any
			json.Unmarshal(line, &res)
			res["cell"] = cellJSON(c)
			appendJSON("results.jsonl", res)
		}()
		time.Sleep(10 * time.Second) // concurrent logins time out on the auth server
	}
	wg.Wait()
	sum, err := summary(filepath.Join(dir, "results.jsonl"))
	if err != nil {
		return fmt.Errorf("no results, see %s: %v", dir, err)
	}
	j, _ := json.MarshalIndent(sum, "", "  ")
	os.WriteFile(filepath.Join(dir, "summary.json"), j, 0o644)
	for _, t := range sum["by_task"].([]map[string]any) {
		tj, _ := json.Marshal(t)
		fmt.Println(string(tj))
	}
	fmt.Println(dir)
	return nil
}

func cellJSON(c config.Cell) map[string]any {
	m := map[string]any{"key": c.Key, "goal": c.Goal, "minutes": c.Minutes, "race": c.Race, "class": c.Class, "rep": c.Rep}
	t := c.Task
	if t.Level > 0 {
		m["level"] = t.Level
	}
	if t.Money > 0 {
		m["money"] = t.Money
	}
	if t.Start != nil {
		m["start"] = fmt.Sprintf("%g %g %g %d %d", t.Start.X, t.Start.Y, t.Start.Z, t.Start.Map, t.Start.Zone)
	}
	if t.Hint != "" {
		m["hint"] = t.Hint
	}
	if t.Channel != "" {
		m["channel"] = t.Channel
	}
	return m
}

// summary groups a matrix's results by task, by task × class and by cell: n, goals met, time to goal for the cells
// that met it, and the judge's value and score as mean ± standard error.
func summary(results string) (map[string]any, error) {
	b, err := os.ReadFile(results)
	if err != nil {
		return nil, err
	}
	type res struct {
		Met      bool
		ElapsedS float64 `json:"elapsed_s"`
		Value    float64
		Score    float64
		Cell     map[string]any
	}
	var rs []res
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var r res
		if json.Unmarshal([]byte(l), &r) == nil {
			rs = append(rs, r)
		}
	}
	group := func(keys ...string) []map[string]any {
		groups := map[string][]res{}
		var order []string
		gk := func(r res) map[string]any {
			g := map[string]any{}
			for _, k := range keys {
				g[k] = r.Cell[k]
			}
			return g
		}
		for _, r := range rs {
			j, _ := json.Marshal(gk(r))
			if _, ok := groups[string(j)]; !ok {
				order = append(order, string(j))
			}
			groups[string(j)] = append(groups[string(j)], r)
		}
		sort.Strings(order)
		var out []map[string]any
		for _, k := range order {
			g := groups[k]
			var times, values, scores []float64
			met := 0
			for _, r := range g {
				if r.Met {
					met++
					times = append(times, r.ElapsedS)
				}
				values, scores = append(values, r.Value), append(scores, r.Score)
			}
			out = append(out, map[string]any{"group": gk(g[0]), "goal": g[0].Cell["goal"], "n": len(g), "met": met,
				"time_to_goal_s": judge.MeanSE(times), "value": judge.MeanSE(values), "score": judge.MeanSE(scores)})
		}
		return out
	}
	return map[string]any{"by_task": group("key", "minutes"), "by_class": group("key", "minutes", "class"),
		"by_cell": group("key", "minutes", "race", "class")}, nil
}
