package trajectory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/exp/eval/trajectory/atif"
)

// one transcript per runtime, each in its own format: the same ATIF trajectory out. Step 1 is the prompt, then an
// agent step with the message, the tool call and its result (by source_call_id), and the tokens.
func TestRead(t *testing.T) {
	dir := t.TempDir()
	usage := filepath.Join(dir, "usage.json")
	os.WriteFile(usage, []byte(`{"codex:t1":{"input":4,"output":9,"cache_read":1,"cache_write":0}}`), 0o644)
	for _, c := range []struct{ runtime, lines string }{
		{"claude", `{"type":"system","session_id":"s1","claude_code_version":"2.9.0","timestamp":"2026-11-02T10:00:00Z"}
{"type":"assistant","timestamp":"2026-11-02T10:00:01Z","message":{"id":"m1","content":[{"type":"text","text":"looking"}],"usage":{"input_tokens":4,"output_tokens":1,"cache_read_input_tokens":1}}}
{"type":"assistant","timestamp":"2026-11-02T10:00:02Z","message":{"id":"m1","content":[{"type":"tool_use","id":"u1","name":"Bash","input":{"command":"wow status"}}],"usage":{"input_tokens":4,"output_tokens":9,"cache_read_input_tokens":1}}}
{"type":"user","timestamp":"2026-11-02T10:00:04Z","message":{"content":[{"type":"tool_result","tool_use_id":"u1","content":"ok"}]}}`},
		{"codex", `{"type":"thread.started","thread_id":"t1","timestamp":"2026-11-02T10:00:00Z"}
{"type":"item.completed","timestamp":"2026-11-02T10:00:01Z","item":{"id":"i0","type":"agent_message","text":"looking"}}
{"type":"item.started","timestamp":"2026-11-02T10:00:02Z","item":{"id":"u1","type":"command_execution","command":"wow status"}}
{"type":"item.completed","timestamp":"2026-11-02T10:00:04Z","item":{"id":"u1","type":"command_execution","command":"wow status","aggregated_output":"ok","exit_code":0}}`},
		{"opencode", `{"type":"step_start","timestamp":"2026-11-02T10:00:00Z","part":{"type":"step-start"}}
{"type":"text","timestamp":"2026-11-02T10:00:01Z","part":{"type":"text","text":"looking"}}
{"type":"tool_use","timestamp":"2026-11-02T10:00:04Z","part":{"type":"tool","tool":"bash","callID":"u1","state":{"status":"completed","input":{"command":"wow status"},"output":"ok","time":{"start":1793613602000,"end":1793613604000}}}}
{"type":"step_finish","timestamp":"2026-11-02T10:00:05Z","part":{"type":"step-finish","tokens":{"input":4,"output":9,"reasoning":0,"cache":{"read":1,"write":0}}}}`},
	} {
		path := filepath.Join(dir, c.runtime+".jsonl")
		os.WriteFile(path, []byte(c.lines+"\n"), 0o644)
		tr, err := Read(path, Info{Agent: "a1", Runtime: c.runtime, Model: "m", Prompt: "play", Usage: usage})
		if err != nil {
			t.Fatal(err)
		}
		if tr.SchemaVersion != Version || tr.Agent.Name == "" || tr.Agent.Version == "" || tr.Steps[0].Source != "user" ||
			tr.Steps[0].Message.Text != "play" || tr.Steps[0].StepID != 1 {
			t.Fatalf("%s: header or step 1: %+v", c.runtime, tr)
		}
		var said bool
		var call *atif.Step
		for i, s := range tr.Steps {
			if s.StepID != i+1 {
				t.Fatalf("%s: step ids not 1, 2, ...: %+v", c.runtime, tr.Steps)
			}
			said = said || s.Source == "agent" && s.Message.Text == "looking"
			if len(s.ToolCalls) > 0 {
				call = &tr.Steps[i]
			}
		}
		if !said || call == nil || call.ToolCalls[0].Arguments["command"] != "wow status" || call.Observation == nil ||
			call.Observation.Results[0].SourceCallID != call.ToolCalls[0].ToolCallID || call.Observation.Results[0].Content.Text != "ok" ||
			call.Observation.Results[0].Extra["ok"] != true || call.Observation.Results[0].Extra["duration_ms"] != int64(2000) {
			t.Fatalf("%s: message or tool call: %+v", c.runtime, tr.Steps)
		}
		if fm := tr.FinalMetrics; fm.TotalPromptTokens != 5 || fm.TotalCompletionTokens != 9 || fm.TotalCachedTokens != 1 {
			t.Fatalf("%s: final metrics (prompt includes the cached): %+v", c.runtime, fm)
		}
		b, _ := json.Marshal(tr) // and it reads back with the ATIF types
		var back atif.Trajectory
		if err := json.Unmarshal(b, &back); err != nil || len(back.Steps) != len(tr.Steps) {
			t.Fatalf("%s: round trip: %v", c.runtime, err)
		}
	}
}
