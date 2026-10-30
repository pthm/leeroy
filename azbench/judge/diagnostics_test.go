package judge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPausedReportPreservesProvisionalScores(t *testing.T) {
	dir := t.TempDir()
	run := filepath.Join(dir, "a1")
	os.MkdirAll(run, 0755)
	os.WriteFile(filepath.Join(dir, "go"), nil, 0600)
	os.WriteFile(filepath.Join(dir, "paused"), []byte("paused"), 0600)
	os.WriteFile(filepath.Join(run, "meta.json"), []byte(`{"agent":"a1","char":"Tester"}`), 0600)
	os.WriteFile(filepath.Join(dir, "a1.record.jsonl"), []byte(`{"t":1000,"ev":"score","n":"Tester","value":10,"score":10}`+"\n"+
		`{"schema":1,"seq":1,"t":1000,"agent":"a1","session":"s1","kind":"action_finished","op":"act.accept","outcome":"failed","code":"timeout","details":{}}`+"\n"), 0600)
	Report(dir) // includes a player whose agent never wrote a transcript
	r := readJSON(filepath.Join(dir, "report.json"))
	agents := r["agents"].([]any)
	a := agents[0].(map[string]any)
	if a["judge_state"] != "paused" || a["score_provisional"] != true || a["cost_usd"] != nil || get(a["score"], "score") != 10.0 {
		t.Fatalf("card: %+v", a)
	}
	if _, err := os.Stat(filepath.Join(run, "judge.json")); !os.IsNotExist(err) {
		t.Fatal("report created judge result")
	}
	team := readJSON(filepath.Join(dir, "team.json"))
	if team["state"] != "paused" || team["team_s"] != nil {
		t.Fatalf("team: %+v", team)
	}
	if get(a["diagnostics"], "schema") != 1.0 || get(team["diagnostics"], "schema") != 1.0 {
		t.Fatal("diagnostics missing")
	}
	before, _ := os.ReadFile(filepath.Join(dir, "report.json"))
	Report(dir)
	after, _ := os.ReadFile(filepath.Join(dir, "report.json"))
	if string(before) != string(after) {
		t.Fatal("saved report changes without new artifacts")
	}
}

func TestSavedUsageIndependentOfRuntimeHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	logs := filepath.Join(home, ".codex/sessions/2026/10/30")
	os.MkdirAll(logs, 0755)
	os.WriteFile(filepath.Join(dir, "agent.jsonl"), []byte(`{"type":"thread.started","thread_id":"abc-123"}`+"\n"), 0600)
	os.WriteFile(filepath.Join(logs, "rollout-test-abc-123.jsonl"), []byte(`{"payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":30}}}}`+"\n"), 0600)
	if err := SaveUsage(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	in, out, cr, cw, ok := savedUsage(dir, "codex", "abc-123")
	if !ok || in != 80 || out != 30 || cr != 20 || cw != 0 {
		t.Fatalf("saved usage: %v %v %v %v %v", in, out, cr, cw, ok)
	}
	if err := SaveUsage(dir); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, ok := savedUsage(dir, "codex", "abc-123"); !ok {
		t.Fatal("missing runtime logs erased saved usage")
	}
}

func TestMissingUsageNeverBecomesFreeCost(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"agent":"a1","runtime":"claude","model":"claude-sonnet-4-5"}`), 0600)
	os.WriteFile(filepath.Join(dir, "agent.jsonl"), []byte(`{"type":"result","session_id":"s1","total_cost_usd":3}`+"\n"), 0600)
	c := card(agentRun{"a1", dir, filepath.Join(dir, "record.jsonl"), dir}, nil, nil, nil)
	b, _ := json.Marshal(c)
	var m map[string]any
	json.Unmarshal(b, &m)
	if m["cost_usd"] != nil || get(get(m["diagnostics"], "coverage"), "cost_status") != "unavailable" {
		t.Fatalf("missing token counts: %s", b)
	}
	os.WriteFile(filepath.Join(dir, "openrouter.json"), []byte(`{"start":10,"end":12}`), 0600)
	b, _ = json.Marshal(teamResults(dir, 1, []map[string]any{{"provider": "openrouter", "cost_usd": nil}}))
	json.Unmarshal(b, &m)
	if get(m["openrouter"], "estimated_usd") != nil {
		t.Fatal("missing estimate became zero")
	}
}

func TestShortPauseExcludedFromSampledTime(t *testing.T) {
	dir := t.TempDir()
	var lines []byte
	for _, v := range []struct {
		t     int
		state string
	}{{1000, "playing"}, {11000, "paused"}, {21000, "playing"}, {51000, "done"}} {
		b, _ := json.Marshal(map[string]any{"kind": "runtime_state", "t": v.t, "details": map[string]any{"component": "judge", "state": v.state}})
		lines = append(append(lines, b...), '\n')
	}
	os.WriteFile(filepath.Join(dir, "harness.jsonl"), lines, 0600)
	played := intervalPlayed(dir)
	for _, v := range []struct {
		a, b int64
		want bool
	}{{1000, 10000, true}, {1000, 16000, false}, {16000, 31000, false}, {31000, 46000, true}, {51000, 66000, false}} {
		if got := played(v.a, v.b); got != v.want {
			t.Fatalf("interval %d..%d=%v, want %v", v.a, v.b, got, v.want)
		}
	}
}
