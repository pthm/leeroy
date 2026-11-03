// Package trajectory turns an agent's transcript, in its runtime's own format (Claude Code's stream-json, Codex's
// exec --json, opencode's run --format json), into an ATIF trajectory (the Agent Trajectory Interchange Format,
// harbor-framework/harbor rfcs/0001), agents/<agent>/trajectory.json, with golang.org/x/exp/eval/trajectory/atif's
// types. Step 1 is the task prompt (source user); then a step per model turn (source agent): its message, reasoning,
// tool calls and their results (observation, by source_call_id), and its tokens and cost (metrics).
//
// A turn is a model response where the runtime says so: Claude Code's message id, opencode's step_start to
// step_finish. Codex's stream doesn't mark its model calls, so each of its commands, messages and edits is a step, its
// reasoning going to the next, and its tokens (in its own session files, agents/<agent>/usage.json) are in
// final_metrics only; Claude Code's stream has each response's tokens as they stood mid-stream (its steps' metrics),
// so its final_metrics are its session log's totals. What ATIF has no field for is in extra: a tool call's ok, duration_ms and exit_code (on its
// observation result), cache_write_tokens and reasoning_tokens (metrics).
package trajectory

import (
	"bufio"
	"cmp"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/pthm/leeroy/internal/bench/judge"
	"golang.org/x/exp/eval/trajectory/atif"
)

// Version is the ATIF schema the trajectories declare: the latest the reference implementation (Harbor) reads.
const Version = "ATIF-v1.7"

// Info is who the transcript is from.
type Info struct {
	Agent, Char     string
	Runtime         string // claude, codex, opencode
	Version         string // the runtime's version (its --version); else what the transcript says (Claude Code's)
	Model, Provider string
	Effort          string
	Prompt          string // the task prompt: step 1
	Usage           string // agents/<agent>/usage.json: Codex's totals
}

var runtimeNames = map[string]string{"claude": "claude-code", "codex": "codex", "opencode": "opencode"}

// Write converts a transcript and writes the trajectory to out (replacing it).
func Write(transcript, out string, info Info) error {
	t, err := Read(transcript, info)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(out, append(b, '\n'), 0o644)
}

// line is the union of the three runtimes' transcript lines, as far as a trajectory reads them.
type line struct {
	Type      string        `json:"type"`
	Timestamp string        `json:"timestamp"`
	SessionID string        `json:"session_id"`
	ThreadID  string        `json:"thread_id"`
	Version   string        `json:"claude_code_version"`
	Message   *claudeMsg    `json:"message"`
	Item      *codexItem    `json:"item"`
	Part      *opencodePart `json:"part"`
}

type claudeMsg struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *struct {
		Input      int `json:"input_tokens"`
		Output     int `json:"output_tokens"`
		CacheRead  int `json:"cache_read_input_tokens"`
		CacheWrite int `json:"cache_creation_input_tokens"`
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
		Kind string `json:"kind"`
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
		Input     int `json:"input"`
		Output    int `json:"output"`
		Reasoning int `json:"reasoning"`
		Cache     struct{ Read, Write int }
	} `json:"tokens"`
}

func iso(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano) }

func unixMS(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

func args(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	if json.Unmarshal(raw, &m) != nil && len(raw) > 0 {
		m["input"] = string(raw)
	}
	return m
}

// claudeText: a Claude tool result's content, a string or text blocks
func claudeText(c json.RawMessage) string {
	var s string
	if json.Unmarshal(c, &s) == nil {
		return s
	}
	var blocks []claudeBlock
	_ = json.Unmarshal(c, &blocks) // neither a string nor blocks: no text
	var b strings.Builder
	for _, x := range blocks {
		b.WriteString(x.Text)
	}
	return b.String()
}

// builder accumulates steps.
type builder struct {
	info  Info
	steps []atif.Step
	open  int            // the agent step being added to (an index), -1 for none
	calls map[string]int // a tool call's id -> its step, until its result
	at    map[string]int64
}

func (b *builder) step(s atif.Step) int {
	s.StepID = len(b.steps) + 1
	b.steps = append(b.steps, s)
	return len(b.steps) - 1
}

// agentStep: the open agent step, or a new one at t
func (b *builder) agentStep(t int64) *atif.Step {
	if b.open < 0 {
		b.open = b.step(atif.Step{Timestamp: iso(t), Source: "agent", ModelName: b.info.Model})
	}
	return &b.steps[b.open]
}

func (b *builder) closeStep() { b.open = -1 }

func (b *builder) say(t int64, text string) {
	s := b.agentStep(t)
	if s.Message.Text != "" {
		s.Message.Text += "\n\n"
	}
	s.Message.Text += text
}

func (b *builder) think(t int64, text string) {
	s := b.agentStep(t)
	if s.ReasoningContent != "" {
		s.ReasoningContent += "\n\n"
	}
	s.ReasoningContent += text
}

func (b *builder) call(t int64, id, name string, arguments map[string]any) {
	s := b.agentStep(t)
	s.ToolCalls = append(s.ToolCalls, atif.ToolCall{ToolCallID: id, FunctionName: name, Arguments: arguments})
	b.calls[id], b.at[id] = b.open, t
}

// result: a tool call's outcome, on the step that made it
func (b *builder) result(t int64, id, content string, ok bool, extra map[string]any) {
	i, known := b.calls[id]
	if !known {
		return
	}
	delete(b.calls, id)
	s := &b.steps[i]
	if s.Observation == nil {
		s.Observation = &atif.Observation{}
	}
	if extra == nil {
		extra = map[string]any{}
	}
	extra["ok"] = ok
	if t >= b.at[id] && b.at[id] > 0 {
		extra["duration_ms"] = t - b.at[id]
	}
	s.Observation.Results = append(s.Observation.Results, atif.ObservationResult{
		SourceCallID: id,
		Content:      atif.MessageContent{Text: content}, Extra: extra,
	})
}

// metrics: a model call's tokens (input not cached, output, cache reads, cache writes) and their cost at list price
// (the step's model's, else the run's)
func (b *builder) metrics(s *atif.Step, in, out, cr, cw, reasoning int) {
	m := &atif.Metrics{PromptTokens: in + cr + cw, CompletionTokens: out, CachedTokens: cr, Extra: map[string]any{}}
	if cw > 0 {
		m.Extra["cache_write_tokens"] = cw
	}
	if reasoning > 0 {
		m.Extra["reasoning_tokens"] = reasoning
	}
	if usd, ok := judge.Cost(b.info.Runtime, b.info.Provider, cmp.Or(s.ModelName, b.info.Model), float64(in), float64(out+reasoning), float64(cr), float64(cw)); ok {
		m.CostUSD = usd
	}
	if len(m.Extra) == 0 {
		m.Extra = nil
	}
	s.Metrics = m
}

// Read converts a transcript to a trajectory.
func Read(transcript string, info Info) (*atif.Trajectory, error) {
	f, err := os.Open(transcript)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	t := &atif.Trajectory{
		SchemaVersion: Version, Agent: atif.Agent{
			Name: runtimeNames[info.Runtime], Version: cmp.Or(info.Version, "unknown"),
			ModelName: info.Model, Extra: map[string]any{"agent": info.Agent, "character": info.Char, "runtime": info.Runtime},
		},
		Notes: "Converted by azbench from the " + info.Runtime + " transcript (agents/<agent>/transcript.jsonl).",
	}
	if info.Effort != "" {
		t.Agent.Extra["reasoning_effort"] = info.Effort
	}
	if t.Agent.Name == "" {
		t.Agent.Name = info.Runtime
	}
	b := &builder{info: info, open: -1, calls: map[string]int{}, at: map[string]int64{}}
	var sessions []string
	msgStep := map[string]int{} // a Claude message id -> its step
	pendingReasoning := ""      // Codex: reasoning before the item it led to
	first := int64(0)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		at := unixMS(l.Timestamp)
		if first == 0 {
			first = at
		}
		switch {
		case l.Type == "thread.started" && l.ThreadID != "":
			sessions = append(sessions, l.ThreadID)
		case l.Type == "system" && l.SessionID != "":
			if len(sessions) == 0 || sessions[len(sessions)-1] != l.SessionID {
				sessions = append(sessions, l.SessionID)
			}
			if l.Version != "" && info.Version == "" {
				t.Agent.Version = l.Version
			}
		case l.Message != nil && l.Type == "assistant": // Claude: a model response, over one or more lines
			if i, ok := msgStep[l.Message.ID]; ok && l.Message.ID != "" {
				b.open = i
			} else {
				b.closeStep()
				b.agentStep(at)
				msgStep[l.Message.ID] = b.open
			}
			if l.Message.Model != "" { // Claude Code makes some calls on another model
				b.steps[b.open].ModelName = l.Message.Model
			}
			var blocks []claudeBlock
			_ = json.Unmarshal(l.Message.Content, &blocks) // content that isn't blocks has none of these
			for _, x := range blocks {
				switch x.Type {
				case "text":
					if x.Text != "" {
						b.say(at, x.Text)
					}
				case "thinking":
					if x.Thinking != "" {
						b.think(at, x.Thinking)
					}
				case "tool_use":
					b.call(at, x.ID, x.Name, args(x.Input))
				}
			}
			if u := l.Message.Usage; u != nil { // each line repeats the usage so far: the last is the response's
				b.metrics(&b.steps[b.open], u.Input, u.Output, u.CacheRead, u.CacheWrite, 0)
			}
		case l.Message != nil && l.Type == "user": // Claude: tool results, or a message to the agent
			var blocks []claudeBlock
			if json.Unmarshal(l.Message.Content, &blocks) != nil { // a plain string
				var s string
				if json.Unmarshal(l.Message.Content, &s) == nil && s != "" {
					b.closeStep()
					b.step(atif.Step{Timestamp: iso(at), Source: "user", Message: atif.MessageContent{Text: s}})
				}
				continue
			}
			for _, x := range blocks {
				switch x.Type {
				case "tool_result":
					b.result(at, x.ToolUseID, claudeText(x.Content), !x.IsError, nil)
				case "text":
					if x.Text != "" {
						b.closeStep()
						b.step(atif.Step{Timestamp: iso(at), Source: "user", Message: atif.MessageContent{Text: x.Text}})
					}
				}
			}
		case l.Item != nil: // Codex: each item its own step
			it := l.Item
			switch {
			case l.Type == "item.started":
				if it.Type == "command_execution" || it.Type == "file_change" {
					b.closeStep()
					s := b.agentStep(at)
					s.ReasoningContent, pendingReasoning = pendingReasoning, ""
					name, a := "shell", map[string]any{"command": it.Command}
					if it.Type == "file_change" {
						name, a = "apply_patch", map[string]any{"changes": it.Changes}
					}
					b.call(at, it.ID, name, a)
				}
			case it.Type == "reasoning" && it.Text != "":
				pendingReasoning = strings.TrimSpace(pendingReasoning + "\n\n" + it.Text)
			case it.Type == "agent_message" && it.Text != "":
				b.closeStep()
				s := b.agentStep(at)
				s.ReasoningContent, pendingReasoning = pendingReasoning, ""
				b.say(at, it.Text)
				b.closeStep()
			case it.Type == "command_execution":
				extra := map[string]any{}
				if it.ExitCode != nil {
					extra["exit_code"] = *it.ExitCode
				}
				b.result(at, it.ID, it.Output, it.ExitCode != nil && *it.ExitCode == 0, extra)
				b.closeStep()
			case it.Type == "file_change":
				b.result(at, it.ID, "", true, nil)
				b.closeStep()
			}
		case l.Part != nil: // opencode: a model call from step_start to step_finish
			p := l.Part
			switch {
			case p.Type == "step-start":
				b.closeStep()
				b.agentStep(at)
			case p.Type == "text" && p.Text != "":
				b.say(at, p.Text)
			case p.Type == "reasoning" && p.Text != "":
				b.think(at, p.Text)
			case p.Type == "tool" && p.State != nil:
				start, end := p.State.Time.Start, p.State.Time.End
				if start == 0 {
					start, end = at, at
				}
				b.call(start, p.CallID, p.Tool, args(p.State.Input))
				b.result(end, p.CallID, p.State.Output+p.State.Error, p.State.Status == "completed", nil)
			case p.Type == "step-finish" && p.Tokens != nil:
				s := b.agentStep(at)
				b.metrics(s, p.Tokens.Input, p.Tokens.Output, p.Tokens.Cache.Read, p.Tokens.Cache.Write, p.Tokens.Reasoning)
				b.closeStep()
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// step 1: the task prompt, before everything the agent did
	steps := []atif.Step{{StepID: 1, Timestamp: iso(first), Source: "user", Message: atif.MessageContent{Text: info.Prompt}}}
	for _, s := range b.steps {
		s.StepID = len(steps) + 1
		steps = append(steps, s)
	}
	t.Steps = steps
	if len(sessions) > 0 {
		t.SessionID = sessions[0]
		if len(sessions) > 1 {
			t.Extra = map[string]any{"sessions": sessions} // resumes: the runtime's later sessions
		}
	}
	t.FinalMetrics = finalMetrics(t, info)
	return t, nil
}

// finalMetrics: the totals the runtime's own session logs give (usage.json, which the report uses too) for Claude
// Code and Codex: Codex's steps have no tokens, and Claude Code's stream has a response's usage as it stood when each
// line went out, short of its final output. Else (opencode) the steps' totals.
func finalMetrics(t *atif.Trajectory, info Info) *atif.FinalMetrics {
	fm := &atif.FinalMetrics{TotalSteps: len(t.Steps)}
	for _, s := range t.Steps {
		if m := s.Metrics; m != nil {
			fm.TotalPromptTokens += m.PromptTokens
			fm.TotalCompletionTokens += m.CompletionTokens
			fm.TotalCachedTokens += m.CachedTokens
			fm.TotalCostUSD += m.CostUSD
		}
	}
	if (info.Runtime != "codex" && info.Runtime != "claude") || info.Usage == "" {
		return fm
	}
	var snaps map[string]struct {
		Input      int `json:"input"`
		Output     int `json:"output"`
		CacheRead  int `json:"cache_read"`
		CacheWrite int `json:"cache_write"`
	}
	b, err := os.ReadFile(info.Usage)
	if err != nil || json.Unmarshal(b, &snaps) != nil {
		return fm
	}
	var in, out, cr, cw int
	found := false
	for k, u := range snaps {
		if strings.HasPrefix(k, info.Runtime+":") {
			found = true
			in, out, cr, cw = in+u.Input, out+u.Output, cr+u.CacheRead, cw+u.CacheWrite
		}
	}
	if !found {
		return fm
	}
	fm.TotalPromptTokens, fm.TotalCompletionTokens, fm.TotalCachedTokens = in+cr+cw, out, cr
	if usd, ok := judge.Cost(info.Runtime, info.Provider, info.Model, float64(in), float64(out), float64(cr), float64(cw)); ok {
		fm.TotalCostUSD = usd
	}
	return fm
}
