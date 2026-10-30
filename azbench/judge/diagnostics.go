package judge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/pthm/leeroy/azbench/diagnostics"
)

// intervalPlayed excludes whole sample intervals crossing a pause/stop: attributing their progress to
// either side would require inventing when it occurred. With no lifecycle evidence the existing sample-gap
// check remains the fallback, and diagnostic coverage states that the harness artifact is missing.
func intervalPlayed(dir string) func(int64, int64) bool {
	type change struct {
		t       int64
		playing bool
	}
	var changes []change
	for _, ev := range jsonl(filepath.Join(dir, "harness.jsonl")) {
		if ev["kind"] != "runtime_state" || get(ev["details"], "component") != "judge" {
			continue
		}
		state := get(ev["details"], "state")
		if state == "playing" || state == "paused" || state == "done" || state == "failed" {
			changes = append(changes, change{int64(fnum(ev["t"])), state == "playing"})
		}
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].t < changes[j].t })
	return func(a, b int64) bool {
		playing := true
		for _, c := range changes {
			if c.t <= a {
				playing = c.playing
				continue
			}
			if c.t >= b {
				break
			}
			if !playing || !c.playing {
				return false
			}
		}
		return playing
	}
}

// savedJudgeState never turns a provisional board value into a final goal result.
func savedJudgeState(dir string) string {
	if len(readJSON(filepath.Join(dir, "judge.json"))) > 0 {
		return "completed"
	}
	state := "unknown"
	for _, e := range jsonl(filepath.Join(dir, "harness.jsonl")) {
		if e["kind"] == "runtime_state" && get(e["details"], "component") == "judge" {
			switch get(e["details"], "state") {
			case "playing":
				state = "running"
			case "paused":
				state = "paused"
			case "failed":
				state = "failed"
			}
		}
	}
	if state != "unknown" {
		return state
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "paused")); err == nil {
		return "paused"
	}
	if _, err := os.Stat(filepath.Join(dir, "paused")); err == nil {
		return "paused"
	}
	return state
}

func savedRunState(dir string, cards []map[string]any) string {
	if _, err := os.Stat(filepath.Join(dir, "paused")); err == nil {
		return "paused"
	}
	all := len(cards) > 0
	failed := false
	for _, c := range cards {
		all = all && c["judge_state"] == "completed"
		failed = failed || c["judge_state"] == "failed"
	}
	if all {
		return "completed"
	}
	if failed {
		return "failed"
	}
	for _, c := range cards {
		if c["judge_state"] == "running" {
			return "running"
		}
	}
	// Artifacts that don't establish a state must not be labeled as a completed or failed run.
	return "unknown"
}

func teamDiagnostics(cards []map[string]any) map[string]any {
	agents := []map[string]any{}
	for _, c := range cards {
		var d diagnostics.Summary
		b, _ := json.Marshal(c["diagnostics"])
		json.Unmarshal(b, &d)
		actions, failures := 0, 0
		for _, a := range d.ByOperation {
			actions += a.Count
			if a.Outcome == "failed" {
				failures += a.Count
			}
		}
		agents = append(agents, map[string]any{"agent": c["agent"], "coverage": d.Coverage.Status, "actions": actions,
			"failures": failures, "episodes": len(d.Episodes), "cost_status": d.Coverage.CostStatus,
			"tool_coverage": d.Tools.Status, "tool_timeouts": d.Tools.ObservedTimeouts, "tool_denials": d.Tools.ObservedDenials})
	}
	return map[string]any{"schema": 1, "agents": agents}
}

type usageSnapshot struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

func savedUsage(dir, runtime, id string) (in, out, cr, cw float64, ok bool) {
	if id == "" {
		return
	}
	var snapshots map[string]usageSnapshot
	b, err := os.ReadFile(filepath.Join(dir, "usage.json"))
	if err != nil || json.Unmarshal(b, &snapshots) != nil {
		return
	}
	u, ok := snapshots[runtime+":"+id]
	return u.Input, u.Output, u.CacheRead, u.CacheWrite, ok
}

// SaveUsage snapshots provider-local usage into run artifacts. Reporting itself never consults home logs.
// Called by the supervisor after a runtime attempt ends; previous sessions are preserved on resume.
func SaveUsage(dir string) error {
	snapshots := map[string]usageSnapshot{}
	path := filepath.Join(dir, "usage.json")
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &snapshots); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, e := range jsonl(filepath.Join(dir, "agent.jsonl")) {
		runtime, id := "claude", str(e["session_id"])
		if e["type"] == "thread.started" {
			runtime, id = "codex", str(e["thread_id"])
		}
		key := runtime + ":" + id
		if id == "" || seen[key] {
			continue
		}
		seen[key] = true
		// IDs are filenames in runtime-owned stores; reject path/glob syntax from malformed artifacts.
		valid := true
		for _, c := range id {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				valid = false
			}
		}
		if !valid {
			continue
		}
		var in, out, cr, cw float64
		var ok bool
		if runtime == "codex" {
			in, out, cr, ok = codexLogUsage(id)
		} else {
			in, out, cr, cw, ok = claudeLogUsage(id)
		}
		if ok {
			snapshots[key] = usageSnapshot{in, out, cr, cw}
		}
	}
	b, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".usage-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
