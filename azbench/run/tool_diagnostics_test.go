package run

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

type toolTestBuffer struct{ bytes.Buffer }

func (*toolTestBuffer) Close() error { return nil }

func TestToolDiagnosticsRuntimeOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, transcript, tool, family, outcome, code string
		ms                                            int64
	}{
		{"claude", `{"type":"assistant","timestamp":"2026-10-30T00:00:00Z","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"bin/wow secret-argument"}}]}}
{"type":"user","timestamp":"2026-10-30T00:00:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"error_code":"permission_denied","content":"secret-error"}]}}`, "Bash", "wow", "failed", "tool_denied", 2000},
		{"codex", `{"type":"item.started","timestamp":"2026-10-30T00:00:00Z","item":{"type":"command_execution","id":"i1","command":"azworld route secret-argument"}}
{"type":"item.completed","timestamp":"2026-10-30T00:00:03Z","item":{"type":"command_execution","id":"i1","status":"completed","exit_code":124,"aggregated_output":"timeout permission denied secret-error"}}`, "exec_command", "azworld", "failed", "tool_error", 3000},
		{"opencode", `{"type":"tool_use","part":{"callID":"c1","tool":"bash","state":{"status":"error","input":{"command":"sleep secret-argument"},"time":{"start":1000,"end":5000},"error_code":"ETIMEDOUT","error":"secret-error"}}}`, "bash", "sleep", "failed", "tool_timeout", 4000},
		{"opencode exit", `{"type":"tool_use","part":{"callID":"c1","tool":"bash","state":{"status":"completed","input":{"command":"rg secret"},"metadata":{"exit":2},"time":{"start":1000,"end":5000}}}}`, "bash", "rg", "failed", "tool_error", 4000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []types.Diagnostic
			d := newToolDiagnostics(tc.name, func(e types.Diagnostic) { events = append(events, e) })
			for _, line := range strings.Split(tc.transcript, "\n") {
				decodeToolLine([]byte(line), d.observe)
			}
			d.finish(100000)
			if len(events) != 1 {
				t.Fatalf("events: %+v", events)
			}
			e := events[0]
			if e.Op != tc.tool || e.Details.CommandFamily != tc.family || e.Outcome != tc.outcome || e.Code != tc.code || e.DurationMS != tc.ms || e.Details.DurationKnown == nil || !*e.Details.DurationKnown {
				t.Fatalf("outcome: %+v", e)
			}
			b, _ := json.Marshal(events)
			if bytes.Contains(b, []byte("secret")) {
				t.Fatalf("private data copied: %s", b)
			}
		})
	}
}

func TestCommandFamilyShellWrapper(t *testing.T) {
	for _, command := range []string{"/bin/zsh -lc 'wow help'", `/bin/bash -lc "wow help"`, "wow help"} {
		if commandFamily(command) != "wow" {
			t.Fatal(command)
		}
	}
}

func TestToolDiagnosticsIncompleteEvidence(t *testing.T) {
	var events []types.Diagnostic
	d := newToolDiagnostics("codex", func(e types.Diagnostic) { events = append(events, e) })
	d.begin("pending", "exec_command", "unknown-command secret", 1000)
	d.end("missing-start", 1500, false, nil, "", true)
	d.finish(2000)
	if len(events) != 2 || events[0].Op != "unknown" || *events[0].Details.DurationKnown || events[1].Outcome != "unknown" || events[1].Code != "outcome_unavailable" {
		t.Fatalf("incomplete evidence: %+v", events)
	}
	// Runtime error prose cannot establish quota exhaustion or an exit cause.
	decodeToolLine([]byte(`{"type":"error","message":"usage limit exceeded"}`), d.observe)
	if len(events) != 2 {
		t.Fatal("classified prose")
	}
	decodeToolLine([]byte(`{"type":"error","error":{"code":"usage_limit_exceeded"}}`), d.observe)
	d.finish(3000)
	if len(events) != 3 || events[2].Details.State != "usage_limit" {
		t.Fatalf("quota evidence: %+v", events)
	}
}

func TestToolDiagnosticsOpenCodeRunning(t *testing.T) {
	var events []types.Diagnostic
	d := newToolDiagnostics("opencode", func(e types.Diagnostic) { events = append(events, e) })
	decodeToolLine([]byte(`{"type":"tool_use","part":{"callID":"c1","tool":"bash","state":{"status":"running","time":{"start":1000}}}}`), d.observe)
	if len(events) != 0 {
		t.Fatal("running tool marked complete")
	}
	d.finish(2000)
	if len(events) != 1 || events[0].Outcome != "unknown" {
		t.Fatalf("exit evidence: %+v", events)
	}
}

func TestStampPreservesRuntimeTimestamps(t *testing.T) {
	input := `{"type":"assistant","timestamp":"2026-10-30T00:00:00.123Z"}
{"type":"tool_use","timestamp":1793318400123}
{"type":"turn.completed"}
runtime stderr
`
	var out toolTestBuffer
	var events []map[string]any
	stampLinesObserved(strings.NewReader(input), &out, func(e map[string]any) { events = append(events, e) })
	if len(events) != 4 || events[0]["timestamp"] != "2026-10-30T00:00:00.123Z" || events[1]["timestamp"] != "2026-10-30T00:00:00.123Z" || events[2]["timestamp"] == nil || events[3]["type"] != "stderr" {
		t.Fatalf("timestamps: %s", out.String())
	}
	if strings.Count(out.String(), `"timestamp"`) != 4 {
		t.Fatal("duplicate timestamp fields")
	}
}

func TestClaudeBackgroundLaunchWaitsForCompletionNotification(t *testing.T) {
	for _, status := range []string{"completed", "failed"} {
		t.Run(status, func(t *testing.T) {
			var out []types.Diagnostic
			d := newToolDiagnostics("claude", func(v types.Diagnostic) { out = append(out, v) })
			for _, line := range []string{
				`{"type":"assistant","timestamp":"2026-10-30T00:00:00Z","message":{"content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"sleep 12","run_in_background":true}}]}}`,
				`{"type":"user","timestamp":"2026-10-30T00:00:00.1Z","message":{"content":[{"type":"tool_result","tool_use_id":"b1","is_error":false,"content":"background launch"}]}}`,
			} {
				decodeToolLine([]byte(line), d.observe)
			}
			if len(out) != 0 {
				t.Fatal("launch counted as completion")
			}
			decodeToolLine([]byte(`{"type":"system","subtype":"task_notification","tool_use_id":"b1","status":"`+status+`","timestamp":"2026-10-30T00:00:12Z"}`), d.observe)
			if len(out) != 1 || out[0].DurationMS != 12000 || out[0].Details.CommandFamily != "sleep" {
				t.Fatalf("%+v", out)
			}
			if (out[0].Outcome == "succeeded") != (status == "completed") {
				t.Fatalf("%+v", out[0])
			}
		})
	}
}
