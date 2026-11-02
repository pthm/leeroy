package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	if err != nil { // older runs didn't keep a copy
		if s, err = b.LoadScenario(filepath.Join(root, "bench/scenarios", name+".yaml")); err != nil {
			return "", err
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
	for _, r := range report.Agents {
		char := fmt.Sprint(r["char"])
		p := pick(r, "char", "runtime", "model", "hours", "deaths", "cost_usd", "tokens", "calls")
		a := agents[char]
		p["class"], p["spec"], p["race"] = a.Class, a.Spec, a.Race
		p["met"] = false
		for k, v := range pick(judged[char], "met", "time_to_goal_s", "value", "score", "level", "gave_up", "died") {
			p[k] = v
		}
		if _, ok := p["time_to_goal_s"]; !ok && p["met"] == true { // older judges: the clock stopped at the goal
			p["time_to_goal_s"] = judged[char]["elapsed_s"]
		}
		players = append(players, p)
	}
	if task == "" {
		task = name
	}
	doc := map[string]any{
		"id": id, "task": task, "scenario": name, "description": strings.TrimSpace(s.Description), "goal": s.Goal,
		"minutes": s.Minutes, "hardcore": s.Hardcore, "level": s.Level, "started": started.Format(time.RFC3339),
		"players": players,
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	f := filepath.Join(out, id+".json")
	data, _ := json.MarshalIndent(doc, "", "  ")
	return f, os.WriteFile(f, append(data, '\n'), 0o644)
}

func readJSON(f string, v any) error {
	data, err := os.ReadFile(f)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
