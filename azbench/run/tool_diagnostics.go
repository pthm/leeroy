package run

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

type pendingTool struct {
	start        int64
	tool, family string
}
type toolDiagnostics struct {
	runtime string
	pending map[string]pendingTool
	emit    func(types.Diagnostic)
}

func newToolDiagnostics(runtime string, emit func(types.Diagnostic)) *toolDiagnostics {
	return &toolDiagnostics{runtime: runtime, pending: map[string]pendingTool{}, emit: emit}
}
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func stringValue(v any) string    { s, _ := v.(string); return s }
func numberValue(v any) int64     { f, _ := v.(float64); return int64(f) }
func toolName(s string) string {
	if s == "" || len(s) > 64 {
		return "unknown"
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-') {
			return "unknown"
		}
	}
	return s
}
func commandFamily(command string) string {
	words := strings.Fields(command)
	if len(words) == 0 {
		return "unknown"
	}
	name := filepath.Base(words[0])
	switch name {
	case "wow", "azworld", "standings", "sleep", "tail", "cat", "ls", "grep", "mkdir", "head", "wc", "sort", "uniq", "cut":
		return name
	}
	return "unknown"
}
func (d *toolDiagnostics) begin(id, tool, command string, at int64) {
	if id == "" {
		return
	}
	if len(d.pending) >= 4096 {
		d.emit(types.Diagnostic{Kind: "runtime_state", Code: "diagnostic_overflow", Details: types.DiagnosticDetails{Component: "agent", State: "coverage_gap", Runtime: d.runtime}})
		return
	}
	d.pending[id] = pendingTool{at, toolName(tool), commandFamily(command)}
}
func (d *toolDiagnostics) end(id string, at int64, failed bool, exit *int, code string, known bool) {
	p, ok := d.pending[id]
	if !ok {
		p = pendingTool{start: at, tool: "unknown", family: "unknown"}
	}
	delete(d.pending, id)
	reportedCode := code
	code = ""
	outcome := "succeeded"
	if !known {
		outcome, code = "unknown", "outcome_unavailable"
	}
	if failed || (exit != nil && *exit != 0) {
		outcome, code = "failed", "tool_error"
	}
	// Classify only explicit structured codes. Error prose is not evidence of a timeout or permission denial.
	switch reportedCode {
	case "timeout", "tool_timeout", "ETIMEDOUT":
		outcome, code = "failed", "tool_timeout"
	case "permission_denied", "tool_denied", "EACCES":
		outcome, code = "failed", "tool_denied"
	}
	durationKnown := ok && p.start > 0 && at >= p.start
	d.emit(types.Diagnostic{Kind: "tool_call", Op: p.tool, Outcome: outcome, Code: code, DurationMS: max(at-p.start, 0),
		Details: types.DiagnosticDetails{Runtime: d.runtime, Tool: p.tool, CommandFamily: p.family, ExitCode: exit, DurationKnown: &durationKnown}})
}
func errorCode(m map[string]any) string {
	if code := stringValue(m["error_code"]); code != "" {
		return code
	}
	return stringValue(object(m["error"])["code"])
}
func (d *toolDiagnostics) observe(m map[string]any) {
	at := time.Now().UnixMilli()
	if s, ok := m["timestamp"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			at = t.UnixMilli()
		}
	}
	code := errorCode(m)
	if code == "usage_limit_exceeded" || code == "quota_exceeded" {
		// A quota error alone does not establish why the process eventually exits.
		d.emit(types.Diagnostic{Kind: "runtime_state", Code: code, Details: types.DiagnosticDetails{Component: "agent", State: "usage_limit", Runtime: d.runtime}})
	}
	switch stringValue(m["type"]) {
	case "assistant", "user":
		content, _ := object(m["message"])["content"].([]any)
		for _, v := range content {
			c := object(v)
			switch c["type"] {
			case "tool_use":
				d.begin(stringValue(c["id"]), stringValue(c["name"]), stringValue(object(c["input"])["command"]), at)
			case "tool_result":
				failed, _ := c["is_error"].(bool)
				d.end(stringValue(c["tool_use_id"]), at, failed, nil, errorCode(c), true)
			}
		}
	case "item.started":
		item := object(m["item"])
		if item["type"] == "command_execution" {
			d.begin(stringValue(item["id"]), "exec_command", stringValue(item["command"]), at)
		}
	case "item.completed":
		item := object(m["item"])
		if item["type"] != "command_execution" {
			return
		}
		var exit *int
		if v, ok := item["exit_code"].(float64); ok {
			n := int(v)
			exit = &n
		}
		d.end(stringValue(item["id"]), at, item["status"] == "failed", exit, errorCode(item), exit != nil || item["status"] == "completed" || item["status"] == "failed")
	case "tool_use":
		part := object(m["part"])
		state := object(part["state"])
		times := object(state["time"])
		start, end := numberValue(times["start"]), numberValue(times["end"])
		if start == 0 {
			start = at
		}
		if end == 0 {
			end = at
		}
		id := stringValue(part["callID"])
		if id == "" {
			id = "completed"
		}
		d.begin(id, stringValue(part["tool"]), stringValue(object(state["input"])["command"]), start)
		if state["status"] == "completed" || state["status"] == "error" {
			d.end(id, end, state["status"] == "error", nil, errorCode(state), true)
		}
	}
}
func (d *toolDiagnostics) finish(at int64) {
	ids := make([]string, 0, len(d.pending))
	for id := range d.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		d.end(id, at, false, nil, "", false)
	}
}

func decodeToolLine(line []byte, observe func(map[string]any)) {
	if observe == nil {
		return
	}
	var m map[string]any
	if json.Unmarshal(line, &m) == nil {
		observe(m)
	}
}
