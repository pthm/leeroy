package main

import (
	"testing"
	"time"

	"golang.org/x/exp/eval/trajectory/atif"
)

func TestPlayOf(t *testing.T) {
	call := func(id, cmd string) atif.ToolCall {
		return atif.ToolCall{ToolCallID: id, FunctionName: "shell", Arguments: map[string]any{"command": cmd}}
	}
	res := func(id string, ok bool, ms float64) atif.ObservationResult {
		return atif.ObservationResult{SourceCallID: id, Extra: map[string]any{"ok": ok, "duration_ms": ms}}
	}
	tr := &atif.Trajectory{Steps: []atif.Step{
		{Source: "user"},
		{
			Source: "agent", Timestamp: "2026-11-02T10:00:10Z", Metrics: &atif.Metrics{CompletionTokens: 100, CostUSD: 0.5},
			ToolCalls:   []atif.ToolCall{call("a", `bash -lc 'wow "return me.level()"'`), call("b", "wow wait 30"), call("c", "rg foo docs")},
			Observation: &atif.Observation{Results: []atif.ObservationResult{res("a", true, 400), res("b", true, 30000), res("c", false, 20)}},
		},
		{
			Source: "agent", Timestamp: "2026-11-02T10:01:10Z", Metrics: &atif.Metrics{CompletionTokens: 300, CostUSD: 0.25},
			ToolCalls: []atif.ToolCall{{ToolCallID: "d", FunctionName: "Read", Arguments: map[string]any{"file_path": "x"}}},
		},
	}}
	p := playOf(tr)
	names := map[string]toolUse{}
	for _, u := range p.Tools {
		names[u.Name] = u
	}
	if p.Calls != 4 || names["wow eval"].Calls != 1 || names["wow wait"].MedianS != 30 || names["rg"].Failed != 1 || names["read"].Calls != 1 ||
		p.FailedPct != 25 || p.ModelCalls != 2 || p.OutputPerCall != 200 {
		t.Fatalf("%+v", p)
	}
	start, _ := time.Parse(time.RFC3339, "2026-11-02T10:00:00Z")
	if c := trajectoryCost(tr, start); len(c) != 2 || c[0] != [2]float64{10, 0.5} || c[1] != [2]float64{70, 0.75} {
		t.Fatalf("cost %v", c)
	}
}

func TestScrub(t *testing.T) {
	for in, want := range map[string]string{
		`{"file_path": "/Users/pat/leeroy/var/workspaces/a/x.lua"}`: `{"file_path": "~/leeroy/var/workspaces/a/x.lua"}`,
		`dofile(\"/home/pat/leeroy/x.lua\")`:                        `dofile(\"~/leeroy/x.lua\")`,
		"cd /Users/pat && ls":                                       "cd ~ && ls",
		`"Found 2 matches\n/Users/pat/leeroy/a.lua"`:                `"Found 2 matches\n~/leeroy/a.lua"`,
		`"\u003e/home/pat/leeroy"`:                                  `"\u003e~/leeroy"`,
		"/home/pat/.claude/settings.json":                           "~/.claude/settings.json",
		"https://example.com/home/page and /usr/local/bin":          "https://example.com/home/page and /usr/local/bin",
	} {
		if got := string(scrub([]byte(in))); got != want {
			t.Errorf("scrub(%q) = %q, want %q", in, got, want)
		}
	}
}
