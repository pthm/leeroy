package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pthm/leeroy/azbench/config"
)

// publish writes a run's results for the website (site/src/data/runs/<run>.json): the scenario, then each player's
// model, outcome, time, cost and tokens, from the run's scenario.yaml, report.json and team.json. Small and committed:
// the site builds from these, never from bench/runs.
func publish(root, runDir, task, out string) (string, error) {
	id := filepath.Base(filepath.Clean(runDir))
	started, err := time.ParseInLocation("20060102-150405", id[:min(len(id), 15)], time.Local)
	if err != nil {
		return "", fmt.Errorf("%s: not a run directory (<time>-<scenario>)", runDir)
	}
	name := strings.TrimPrefix(id[15:], "-")
	b, err := config.LoadBench(filepath.Join(root, "bench"))
	if err != nil {
		return "", err
	}
	s, err := b.LoadScenario(filepath.Join(runDir, "scenario.yaml"))
	if err != nil { // older runs didn't keep a copy: the scenario of that name, else what the run's own files say
		if s, err = b.LoadScenario(filepath.Join(root, "bench/scenarios", name+".yaml")); err != nil {
			s = &config.Scenario{Name: name}
		}
	}
	var report struct {
		Agents []map[string]any `json:"agents"`
	}
	var team struct {
		Agents []map[string]any `json:"agents"`
	}
	if err := readJSON(filepath.Join(runDir, "report.json"), &report); err != nil {
		return "", err
	}
	if err := readJSON(filepath.Join(runDir, "team.json"), &team); err != nil {
		return "", err
	}
	judged := map[string]map[string]any{}
	for _, a := range team.Agents {
		judged[fmt.Sprint(a["char"])] = a
	}
	agents := map[string]config.Agent{}
	for _, a := range s.Agents {
		agents[a.Char] = a
	}
	for _, r := range report.Agents { // agents the scenario doesn't list (no scenario kept): from the report
		if c := fmt.Sprint(r["char"]); agents[c].Name == "" {
			agents[c] = config.Agent{Name: fmt.Sprint(r["agent"]), Char: c}
		}
	}
	if s.Goal == "" {
		for _, a := range team.Agents {
			if g, ok := a["goal"].(string); ok && g != "" {
				s.Goal = g
			}
		}
	}
	pick := func(m map[string]any, keys ...string) map[string]any {
		o := map[string]any{}
		for _, k := range keys {
			if v, ok := m[k]; ok && v != nil {
				o[k] = v
			}
		}
		return o
	}
	var players []map[string]any
	chat := map[string]chatMsg{}
	var recs []*recording
	recOf := map[string]*recording{}
	for _, r := range report.Agents {
		char := fmt.Sprint(r["char"])
		p := pick(r, "char", "runtime", "model", "hours", "deaths", "cost_usd", "tokens", "calls", "time_pct_by_state", "time_use", "combat")
		if th, ok := r["think_s"].(map[string]any); ok {
			p["think_median_s"] = th["median"]
		}
		p["failures"] = failures(r)
		a := agents[char]
		p["class"], p["spec"], p["race"] = a.Class, a.Spec, a.Race
		p["met"] = false
		for k, v := range pick(judged[char], "met", "time_to_goal_s", "value", "score", "level", "gave_up", "died") {
			p[k] = v
		}
		if _, ok := p["time_to_goal_s"]; !ok && p["met"] == true { // older judges: the clock stopped at the goal
			p["time_to_goal_s"] = judged[char]["elapsed_s"]
		}
		if judged[char] == nil { // the judge's final word never written (a paused or stopped run): its last score
			if sc, ok := r["score"].(map[string]any); ok {
				p["value"], p["score"] = sc["value"], sc["score"]
				if t, ok := sc["time_to_goal_s"].(float64); ok {
					p["met"], p["time_to_goal_s"] = true, t
				}
			}
		}
		if a.Name != "" {
			adir := agentDir(runDir, id, a.Name)
			var meta struct{ Spec, Role string }
			if readJSON(filepath.Join(adir, "meta.json"), &meta) == nil {
				p["spec_name"], p["role"] = meta.Spec, meta.Role
				if a.Class == "" { // "Warrior - Protection"
					p["class"] = strings.ToLower(strings.TrimSpace(strings.Split(meta.Spec, " - ")[0]))
				}
			}
			p["series"], p["deaths_at"] = series(filepath.Join(adir, "samples.jsonl"))
			if rec, err := readRecording(filepath.Join(runDir, a.Name+".record.jsonl"), char, started.UnixMilli(), chat); err == nil {
				recs, recOf[char] = append(recs, rec), rec
			}
		}
		players = append(players, p)
	}
	nameDeaths(recs)
	var tracks, thin [][][5]float64
	end := 0.0
	for _, rec := range recs {
		if n := len(rec.track); n > 0 {
			end = math.Max(end, rec.track[n-1][0])
		}
	}
	for _, p := range players {
		rec := recOf[p["char"].(string)]
		if rec == nil {
			continue
		}
		t := simplify(rec.raw)
		p["track"], p["death_list"] = t, rec.deaths
		tracks, thin = append(tracks, rec.track), append(thin, t)
	}
	maps, err := mapImages(id, thin, filepath.Join(out, "../../assets/runs"))
	if err != nil {
		return "", err
	}
	if task == "" {
		task = name
	}
	if s.Minutes == 0 { // no scenario: the longest anyone played
		for _, p := range players {
			if h, ok := p["hours"].(float64); ok {
				s.Minutes = max(s.Minutes, int(math.Round(h*60)))
			}
		}
	}
	doc := map[string]any{
		"id": id, "task": task, "scenario": name, "description": strings.TrimSpace(s.Description), "goal": s.Goal,
		"minutes": s.Minutes, "hardcore": s.Hardcore, "level": s.Level, "started": started.Format(time.RFC3339),
		"players": players, "maps": maps, "together": together(tracks, end), "chat_per_hour": chatPerHour(chat),
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	f := filepath.Join(out, id+".json")
	data, _ := json.Marshal(doc) // compact: routes and series are thousands of numbers
	return f, os.WriteFile(f, append(data, '\n'), 0o644)
}

// benign: failures that are the SDK working as meant (a script replacing its own move, a swing that missed, a spell
// still on the global cooldown), not mistakes worth showing
var benign = map[string]bool{"replaced_by_owner": true, "cancelled": true, "task_stopped": true, "miss": true,
	"dodge": true, "parry": true, "not_ready": true}

// failures: a player's most frequent failed calls, [{op, code, count}], at most 6.
func failures(r map[string]any) []map[string]any {
	diag, _ := r["diagnostics"].(map[string]any)
	ops, _ := diag["by_operation"].([]any)
	var out []map[string]any
	for _, o := range ops {
		m, _ := o.(map[string]any)
		if m["outcome"] == "failed" && !benign[fmt.Sprint(m["code"])] {
			out = append(out, map[string]any{"op": m["op"], "code": m["code"], "count": m["count"]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["count"].(float64) > out[j]["count"].(float64) })
	return out[:min(6, len(out))]
}

// seriesPoints: about how many points a player's progress keeps for the charts
const seriesPoints = 240

// series: a player's progress from the judge's samples (every 15 s), thinned to about seriesPoints points of
// [elapsed s, level (fractional), XP earned, deaths, quests, kills], the last always kept; and the elapsed seconds of
// every death, from each sample where the count rose.
func series(f string) ([][6]float64, []float64) {
	data, err := os.ReadFile(f)
	if err != nil {
		return nil, nil
	}
	var all [][6]float64
	deaths := []float64{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var s struct {
			Elapsed float64 `json:"elapsed_s"`
			Signals struct {
				Level, Deaths, Quests float64
				XP                    float64 `json:"xp_earned"`
				Kills                 float64 `json:"creatures_killed"`
			} `json:"signals"`
		}
		if json.Unmarshal([]byte(line), &s) != nil {
			continue
		}
		g := s.Signals
		if n := len(all); n > 0 && g.Deaths > all[n-1][3] {
			deaths = append(deaths, s.Elapsed)
		}
		all = append(all, [6]float64{s.Elapsed, math.Round(g.Level*100) / 100, g.XP, g.Deaths, g.Quests, g.Kills})
	}
	step := max(1, len(all)/seriesPoints)
	var out [][6]float64
	for i := 0; i < len(all); i += step {
		out = append(out, all[i])
	}
	if len(all) > 0 && out[len(out)-1] != all[len(all)-1] {
		out = append(out, all[len(all)-1])
	}
	return out, deaths
}

// agentDir: an agent's files, inside the run (<run>/<agent>), or for the first team runs beside it
// (<time>-<agent>, started seconds after the run's own <time>-team): the first such after the run's time.
func agentDir(runDir, id, name string) string {
	in := filepath.Join(runDir, name)
	if _, err := os.Stat(in); err == nil {
		return in
	}
	ms, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Clean(runDir)), id[:9]+"*-"+name))
	sort.Strings(ms)
	for _, m := range ms {
		if filepath.Base(m) >= id[:15] {
			return m
		}
	}
	return in
}

func readJSON(f string, v any) error {
	data, err := os.ReadFile(f)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
