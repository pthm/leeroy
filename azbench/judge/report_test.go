package judge

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
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

	Report(dir)
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

// an opencode transcript: tool calls from tool_use events, tokens from step_finish, cost at the models.dev price
func TestReportOpencode(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC).UnixMilli()
	iso := func(t int64) string { return time.UnixMilli(t).UTC().Format(time.RFC3339Nano) }
	model := "openrouter/moonshotai/kimi-k2"
	os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"agent":"a1","char":"Tester","runtime":"opencode","model":"`+model+`","provider":"openrouter"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "record.jsonl"), []byte(`{"t":`+fmt.Sprint(t0)+`,"ev":"score","n":"Tester","value":0}`+"\n"), 0o644)
	var b []byte
	for _, x := range []any{
		map[string]any{"type": "tool_use", "timestamp": iso(t0 + 1000), "part": map[string]any{"tool": "bash",
			"state": map[string]any{"input": map[string]any{"command": "wow wait 60"}, "time": map[string]any{"start": t0, "end": t0 + 60_000}}}},
		map[string]any{"type": "step_finish", "timestamp": iso(t0 + 61_000), "part": map[string]any{
			"tokens": map[string]any{"input": 10_000, "output": 100_000, "reasoning": 50_000, "cache": map[string]any{"read": 20_000_000, "write": 1_000_000}}}},
		map[string]any{"type": "step_finish", "timestamp": iso(t0 + 62_000), "part": map[string]any{
			"tokens": map[string]any{"input": 5_000, "output": 50_000, "reasoning": 0, "cache": map[string]any{"read": 21_000_000, "write": 0}}}},
	} {
		j, _ := json.Marshal(x)
		b = append(append(b, j...), '\n')
	}
	os.WriteFile(filepath.Join(dir, "agent.jsonl"), b, 0o644)
	// deaths come from the judge's samples (the character database), not the death events: 2 -> 5 is 3, although
	// the event stream has one more (a death after the clock)
	os.WriteFile(filepath.Join(dir, "samples.jsonl"), []byte(`{"elapsed_s":0,"signals":{"deaths":2}}`+"\n"+`{"elapsed_s":60,"signals":{"deaths":5}}`+"\n"), 0o644)
	ev := ""
	for i := 0; i < 4; i++ {
		ev += `{"t":` + fmt.Sprint(t0+int64(i)*1000) + `,"type":"death","data":{}}` + "\n"
	}
	os.WriteFile(filepath.Join(dir, "Tester.events.jsonl"), []byte(ev), 0o644)

	Report(dir)
	var rep struct{ Agents []map[string]any }
	j, _ := os.ReadFile(filepath.Join(dir, "report.json"))
	json.Unmarshal(j, &rep)
	c := rep.Agents[0]
	if c["deaths"] != 3.0 {
		t.Errorf("deaths = %v, want 3 from the samples", c["deaths"])
	}
	if c["calls"] != 1.0 {
		t.Errorf("calls = %v, want 1", c["calls"])
	}
	if g, _ := json.Marshal(c["tokens"]); string(g) != `{"cache_read":41000000,"cache_write":1000000,"input":15000,"output":200000}` {
		t.Errorf("tokens = %s", g)
	}
	var table map[string]Price
	json.Unmarshal(pricesJSON, &table)
	p, ok := table[model]
	if !ok {
		t.Fatalf("%s not in prices.json", model)
	}
	want := (0.015*p.Input + 0.2*p.Output + 41*p.CacheRead + cmp.Or(p.CacheWrite, p.Input))
	if got, _ := c["cost_usd"].(float64); math.Abs(got-want) > 0.051 { // r1: dollars to 1 decimal
		t.Errorf("cost_usd = %v, want %.4f", c["cost_usd"], want)
	}
}

// players grouped by runtime and model: the same model on two harnesses is two rows; cost null if any is unknown
func TestByModel(t *testing.T) {
	card := func(rt, model string, score float64, cost any) map[string]any {
		return map[string]any{"runtime": rt, "model": model, "score": map[string]any{"score": score}, "cost_usd": cost, "hours": 1.0}
	}
	g, _ := json.Marshal(byModel([]map[string]any{card("claude", "m", 100, 2.0), card("opencode", "m", 50, nil),
		card("claude", "m", 300, 4.0)}))
	want := `[{"runtime":"claude","model":"m","players":2,"score":{"mean":200,"se":100},"best":300,"total":400,"cost_usd":6,"cost_per_agent_hour":3},` +
		`{"runtime":"opencode","model":"m","players":1,"score":{"mean":50,"se":null},"best":50,"total":50,"cost_usd":null,"cost_per_agent_hour":null}]`
	if string(g) != want {
		t.Errorf("byModel = %s", g)
	}
}

// team.json shows what OpenRouter billed for the run (openrouter.json, start to finish) next to the estimate for the
// agents that went through it
func TestTeamOpenrouterBilled(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "openrouter.json"), []byte(`{"start":10.5,"end":14.25}`), 0o644)
	cards := []map[string]any{{"provider": "openrouter", "cost_usd": 3.0}, {"provider": "", "cost_usd": 9.0}}
	j, _ := json.Marshal(teamResults(dir, 0, cards))
	var team struct{ Openrouter map[string]float64 }
	json.Unmarshal(j, &team)
	if team.Openrouter["billed_usd"] != 3.75 || team.Openrouter["estimated_usd"] != 3 {
		t.Fatalf("openrouter = %v, want billed 3.75, estimated 3", team.Openrouter)
	}
}
