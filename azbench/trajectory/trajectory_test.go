package trajectory

import (
	"os"
	"path/filepath"
	"testing"
)

// one transcript per runtime, in its own format: the same steps out
func TestRead(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct{ runtime, lines string }{
		{"claude", `{"type":"system","session_id":"s1","timestamp":"2026-11-02T10:00:00Z"}
{"type":"assistant","timestamp":"2026-11-02T10:00:01Z","message":{"id":"m1","content":[{"type":"text","text":"looking"}],"usage":{"input_tokens":5,"output_tokens":1}}}
{"type":"assistant","timestamp":"2026-11-02T10:00:02Z","message":{"id":"m1","content":[{"type":"tool_use","id":"u1","name":"Bash","input":{"command":"wow status"}}],"usage":{"input_tokens":5,"output_tokens":9}}}
{"type":"user","timestamp":"2026-11-02T10:00:04Z","message":{"content":[{"type":"tool_result","tool_use_id":"u1","content":"ok"}]}}`},
		{"codex", `{"type":"thread.started","thread_id":"s1","timestamp":"2026-11-02T10:00:00Z"}
{"type":"item.completed","timestamp":"2026-11-02T10:00:01Z","item":{"id":"i0","type":"agent_message","text":"looking"}}
{"type":"item.started","timestamp":"2026-11-02T10:00:02Z","item":{"id":"i1","type":"command_execution","command":"wow status"}}
{"type":"item.completed","timestamp":"2026-11-02T10:00:04Z","item":{"id":"i1","type":"command_execution","command":"wow status","aggregated_output":"ok","exit_code":0}}`},
		{"opencode", `{"type":"text","timestamp":"2026-11-02T10:00:01Z","part":{"type":"text","text":"looking"}}
{"type":"tool_use","timestamp":"2026-11-02T10:00:04Z","part":{"type":"tool","tool":"bash","callID":"u1","state":{"status":"completed","input":{"command":"wow status"},"output":"ok","time":{"start":1793613602000,"end":1793613604000}}}}
{"type":"step_finish","timestamp":"2026-11-02T10:00:05Z","part":{"type":"step-finish","tokens":{"input":5,"output":9,"reasoning":0,"cache":{"read":0,"write":0}}}}`},
	} {
		path := filepath.Join(dir, c.runtime+".jsonl")
		os.WriteFile(path, []byte(c.lines+"\n"), 0o644)
		steps, err := Read(c.runtime, "a1", path)
		if err != nil {
			t.Fatal(err)
		}
		var msg, tool *Step
		for i := range steps {
			switch steps[i].Kind {
			case "message":
				msg = &steps[i]
			case "tool":
				tool = &steps[i]
			}
		}
		if msg == nil || msg.Text != "looking" || tool == nil || tool.Input != "wow status" || tool.Output != "ok" ||
			tool.OK == nil || !*tool.OK || tool.MS != 2000 || tool.Agent != "a1" || tool.Runtime != c.runtime {
			t.Fatalf("%s: %+v", c.runtime, steps)
		}
		if c.runtime == "claude" {
			for _, s := range steps {
				if s.Kind == "usage" && s.Tokens.Output != 9 {
					t.Fatalf("claude usage: the message's last line, got %+v", s.Tokens)
				}
			}
		}
	}
}
