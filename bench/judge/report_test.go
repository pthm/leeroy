package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// judge report on a made-up 10-minute solo run with a known shape (was report.test.ts)
func TestReportCard(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC).UnixMilli()
	const min = int64(60_000)
	lines := func(f string, xs []any) {
		var b []byte
		for _, x := range xs {
			j, _ := json.Marshal(x)
			b = append(append(b, j...), '\n')
		}
		os.WriteFile(filepath.Join(dir, f), b, 0o644)
	}
	iso := func(t int64) string { return time.UnixMilli(t).UTC().Format(time.RFC3339Nano) }
	os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"agent":"a1","char":"Tester","runtime":"claude"}`), 0o644)
	// 10 min of samples, 100 XP per 15 s while the script runs (0-8 min), nothing after it stops
	var rec []any
	for i := int64(0); i <= 40; i++ {
		rec = append(rec, map[string]any{"t": t0 + i*15_000, "ev": "score", "n": "Tester", "value": float64(min64(i, 32) * 100)})
	}
	lines("record.jsonl", rec)
	lines("Tester.events.jsonl", []any{
		map[string]any{"t": t0, "type": "task_started", "data": map[string]any{"task": 1}},
		map[string]any{"t": t0 + 3*min, "type": "chat", "data": map[string]any{"kind": "npc_say", "text": "You no take candle!"}},
		map[string]any{"t": t0 + 4*min, "type": "death", "data": map[string]any{}},
		map[string]any{"t": t0 + 8*min, "type": "task_finished", "data": map[string]any{"task": 1, "state": "failed"}},
	})
	call := func(id, cmd string, a, b int64) []any {
		return []any{
			map[string]any{"type": "assistant", "timestamp": iso(a), "message": map[string]any{"id": id,
				"content": []any{map[string]any{"type": "tool_use", "id": id, "input": map[string]any{"command": cmd}}}}},
			map[string]any{"type": "user", "timestamp": iso(b), "message": map[string]any{
				"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id}}}},
		}
	}
	var tr []any
	tr = append(tr, call("1", "wow wait 300 >/dev/null", t0+1*min, t0+3*min)...)        // woken early by the NPC line
	tr = append(tr, call("2", "wow -f grind.lua", t0+3*min+12_000, t0+3*min+18_000)...) // and restarts
	tr = append(tr, call("3", "wow wait 120", t0+4*min, t0+6*min)...)                   // times out
	lines("agent.jsonl", tr)

	runReport(dir)
	var rep struct{ Agents []map[string]any }
	b, _ := os.ReadFile(filepath.Join(dir, "report.json"))
	json.Unmarshal(b, &rep)
	c := rep.Agents[0]
	check := func(what string, got, want any) {
		if g, _ := json.Marshal(got); string(g) != want {
			t.Errorf("%s = %s, want %s", what, g, want)
		}
	}
	check("duty_cycle_pct", c["duty_cycle_pct"], "80") // 8 of 10 minutes
	ig := c["idle_gaps"].(map[string]any)
	check("idle_gaps.n", ig["n"], "1") // idle from 8 min to the end
	check("idle_gaps.median_s", ig["median_s"], "120")
	check("no_script", c["progress_per_hour_by_state"].(map[string]any)["no_script"], "0")
	w := c["wakes"].(map[string]any)
	check("waits", w["waits"], "2")
	check("early", w["early"], "1")
	check("by_trigger", w["by_trigger"], `{"chat:npc_say":{"n":1,"restart":1}}`)
	check("restarts", c["restarts"].(map[string]any)["agent_calls"], "1")
	check("deaths", c["deaths"], "1")
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
