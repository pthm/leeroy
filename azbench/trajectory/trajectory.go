// Package trajectory turns an agent's transcript, in its runtime's own format (Claude Code's stream-json, Codex's
// exec --json, opencode's run --format json), into one schema: agents/<agent>/trajectory.jsonl, a line per step, so
// runs on different runtimes compare with one reader.
//
//	{"t": unix ms, "agent", "runtime", "kind", ...}
//	kind "session":   id (the runtime's session or thread)
//	kind "message":   text (what the agent wrote)
//	kind "reasoning": text (its visible reasoning; empty or encrypted reasoning is left out)
//	kind "tool":      id, tool, input, output (cut to 2000 characters), ok, ms (call to result); t is the call
//	kind "edit":      paths (files the runtime's own edit tool changed: Codex's file_change)
//	kind "usage":     tokens {input, output, cache_read, cache_write}: one model call's (Claude, opencode; Codex
//	                  keeps its usage in its session files, agents/<agent>/codex-usage.jsonl)
package trajectory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Step is one line of a trajectory.
type Step struct {
	T       int64    `json:"t"`
	Agent   string   `json:"agent"`
	Runtime string   `json:"runtime"`
	Kind    string   `json:"kind"`
	ID      string   `json:"id,omitempty"`
	Text    string   `json:"text,omitempty"`
	Tool    string   `json:"tool,omitempty"`
	Input   string   `json:"input,omitempty"`
	Output  string   `json:"output,omitempty"`
	OK      *bool    `json:"ok,omitempty"`
	MS      int64    `json:"ms,omitempty"`
	Paths   []string `json:"paths,omitempty"`
	Tokens  *Tokens  `json:"tokens,omitempty"`
}

// Tokens is one model call's usage.
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

const cutAt = 2000

func cut(s string) string {
	if len(s) <= cutAt {
		return s
	}
	return s[:cutAt] + fmt.Sprintf("… (%d more bytes)", len(s)-cutAt)
}

func ms(iso string) int64 {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

func yes(b bool) *bool { return &b }

// Write reads a transcript and writes its trajectory to out (replacing it).
func Write(runtime, agent, transcript, out string) error {
	steps, err := Read(runtime, agent, transcript)
	if err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, s := range steps {
		enc.Encode(s)
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// raw is the union of the three runtimes' transcript lines, as far as a trajectory reads them.
type raw struct {
	Type      string         `json:"type"`
	Timestamp string         `json:"timestamp"`
	SessionID string         `json:"session_id"`
	ThreadID  string         `json:"thread_id"`
	Message   *claudeMessage `json:"message"`
	Item      *codexItem     `json:"item"`
	Part      *opencodePart  `json:"part"`
}

type claudeMessage struct {
	ID      string          `json:"id"`
	Content json.RawMessage `json:"content"`
	Usage   *struct {
		Input      int64 `json:"input_tokens"`
		Output     int64 `json:"output_tokens"`
		CacheRead  int64 `json:"cache_read_input_tokens"`
		CacheWrite int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type codexItem struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Text     string `json:"text"`
	Command  string `json:"command"`
	Output   string `json:"aggregated_output"`
	ExitCode *int   `json:"exit_code"`
	Changes  []struct {
		Path string `json:"path"`
	} `json:"changes"`
}

type opencodePart struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Tool   string `json:"tool"`
	CallID string `json:"callID"`
	State  *struct {
		Status string          `json:"status"`
		Input  json.RawMessage `json:"input"`
		Output string          `json:"output"`
		Error  string          `json:"error"`
		Time   struct{ Start, End int64 }
	} `json:"state"`
	Tokens *struct {
		Input     int64 `json:"input"`
		Output    int64 `json:"output"`
		Reasoning int64 `json:"reasoning"`
		Cache     struct{ Read, Write int64 }
	} `json:"tokens"`
}

// command: a tool's input as one string: its command (a shell's), else its JSON
func command(in json.RawMessage) string {
	var c struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(in, &c) == nil && c.Command != "" {
		return c.Command
	}
	return string(in)
}

// text: a Claude tool result's content (a string, or text blocks)
func text(c json.RawMessage) string {
	var s string
	if json.Unmarshal(c, &s) == nil {
		return s
	}
	var blocks []claudeBlock
	json.Unmarshal(c, &blocks)
	var b strings.Builder
	for _, x := range blocks {
		b.WriteString(x.Text)
	}
	return b.String()
}

// Read reads a transcript as trajectory steps, in order.
func Read(runtime, agent, transcript string) ([]Step, error) {
	f, err := os.Open(transcript)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Step
	add := func(s Step) {
		s.Agent, s.Runtime = agent, runtime
		out = append(out, s)
	}
	calls := map[string]int{}     // a tool call's id -> its step, until its result
	usage := map[string]int{}     // a Claude message's id -> its usage step (each content block repeats the usage)
	started := map[string]int64{} // a Codex item's id -> when it started
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var r raw
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		t := ms(r.Timestamp)
		switch {
		case r.Type == "thread.started" || r.Type == "system" && r.SessionID != "" && len(out) == 0:
			add(Step{T: t, Kind: "session", ID: r.ThreadID + r.SessionID})
		case r.Message != nil && (r.Type == "assistant" || r.Type == "user"): // Claude
			var blocks []claudeBlock
			json.Unmarshal(r.Message.Content, &blocks)
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if r.Type == "assistant" && b.Text != "" {
						add(Step{T: t, Kind: "message", Text: b.Text})
					}
				case "thinking":
					if b.Thinking != "" {
						add(Step{T: t, Kind: "reasoning", Text: b.Thinking})
					}
				case "tool_use":
					calls[b.ID] = len(out)
					add(Step{T: t, Kind: "tool", ID: b.ID, Tool: b.Name, Input: command(b.Input)})
				case "tool_result":
					if i, ok := calls[b.ToolUseID]; ok {
						out[i].Output, out[i].OK, out[i].MS = cut(text(b.Content)), yes(!b.IsError), max(0, t-out[i].T)
						delete(calls, b.ToolUseID)
					}
				}
			}
			if u := r.Message.Usage; r.Type == "assistant" && u != nil && r.Message.ID != "" {
				tok := &Tokens{u.Input, u.Output, u.CacheRead, u.CacheWrite}
				if i, ok := usage[r.Message.ID]; ok {
					out[i].Tokens = tok // the message's last line has its final usage
				} else {
					usage[r.Message.ID] = len(out)
					add(Step{T: t, Kind: "usage", ID: r.Message.ID, Tokens: tok})
				}
			}
		case r.Item != nil: // Codex
			it := r.Item
			switch {
			case r.Type == "item.started":
				started[it.ID] = t
			case it.Type == "agent_message" && it.Text != "":
				add(Step{T: t, Kind: "message", Text: it.Text})
			case it.Type == "reasoning" && it.Text != "":
				add(Step{T: t, Kind: "reasoning", Text: it.Text})
			case it.Type == "command_execution":
				at := started[it.ID]
				if at == 0 {
					at = t
				}
				add(Step{T: at, Kind: "tool", ID: it.ID, Tool: "shell", Input: it.Command, Output: cut(it.Output),
					OK: yes(it.ExitCode != nil && *it.ExitCode == 0), MS: max(0, t-at)})
			case it.Type == "file_change":
				var paths []string
				for _, c := range it.Changes {
					paths = append(paths, c.Path)
				}
				add(Step{T: t, Kind: "edit", ID: it.ID, Paths: paths})
			}
		case r.Part != nil: // opencode
			p := r.Part
			switch {
			case p.Type == "text" && p.Text != "":
				add(Step{T: t, Kind: "message", Text: p.Text})
			case p.Type == "reasoning" && p.Text != "":
				add(Step{T: t, Kind: "reasoning", Text: p.Text})
			case p.Type == "tool" && p.State != nil:
				s := Step{T: t, Kind: "tool", ID: p.CallID, Tool: p.Tool, Input: command(p.State.Input),
					Output: cut(p.State.Output + p.State.Error), OK: yes(p.State.Status == "completed")}
				if p.State.Time.Start > 0 {
					s.T, s.MS = p.State.Time.Start, max(0, p.State.Time.End-p.State.Time.Start)
				}
				add(s)
			case p.Type == "step-finish" && p.Tokens != nil:
				add(Step{T: t, Kind: "usage", Tokens: &Tokens{p.Tokens.Input, p.Tokens.Output + p.Tokens.Reasoning, p.Tokens.Cache.Read, p.Tokens.Cache.Write}})
			}
		}
	}
	return out, sc.Err()
}
