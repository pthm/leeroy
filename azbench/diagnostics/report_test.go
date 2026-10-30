package diagnostics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func writeEvents(t *testing.T, path string, events []types.Diagnostic) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, ev := range events {
		if err := json.NewEncoder(f).Encode(ev); err != nil {
			t.Fatal(err)
		}
	}
}
func failure(seq uint64, at int64) types.Diagnostic {
	return types.Diagnostic{Schema: 1, Seq: seq, T: at, Agent: "a1", Session: "s1", Task: "main", TaskID: 1,
		Kind: "action_finished", Op: "act.go_to", Outcome: "failed", Code: "unreachable", DurationMS: 100,
		Details: types.DiagnosticDetails{Target: "42"}}
}

func TestEpisodesRequireThreeFailuresWithinWindow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		times []int64
		want  int
	}{
		{"three", []int64{1000, 11000, 21000}, 1},
		{"two", []int64{1000, 11000}, 0},
		{"slow repeats", []int64{1000, 21000, 41000}, 0},
		{"sliding window", []int64{1000, 26000, 41000, 51000}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "record.jsonl")
			var events []types.Diagnostic
			for i, at := range tc.times {
				events = append(events, failure(uint64(i+1), at))
			}
			writeEvents(t, path, events)
			s := Read("a1", path)
			if len(s.Episodes) != tc.want {
				t.Fatalf("episodes: %+v", s.Episodes)
			}
			if len(s.Episodes) > 0 {
				ep := s.Episodes[0]
				if ep.Count != 3 || ep.ActionMS != 300 || ep.Last.Line != len(events) || ep.DurationMS <= 0 {
					t.Fatalf("episode evidence: %+v", ep)
				}
			}
		})
	}
}

func TestEpisodeBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.jsonl")
	events := []types.Diagnostic{failure(1, 1000), failure(2, 2000), failure(3, 3000), failure(4, 4000), failure(5, 5000), failure(6, 6000), failure(7, 7000)}
	events[3].Outcome, events[3].Code = "succeeded", ""
	writeEvents(t, path, events)
	s := Read("a1", path)
	if len(s.Episodes) != 2 || s.Episodes[0].Active || !s.Episodes[1].Active {
		t.Fatalf("success did not close episode: %+v", s.Episodes)
	}
	if len(s.TopActive(8000, 3)) != 1 || len(s.TopActive(40000, 3)) != 0 {
		t.Fatal("active episode expiry")
	}
	for _, change := range []func(*types.Diagnostic){
		func(e *types.Diagnostic) { e.Session = "s2" }, func(e *types.Diagnostic) { e.TaskID = 2 },
		func(e *types.Diagnostic) { e.Details.Target = "43" }, func(e *types.Diagnostic) { e.Details.QuestID = 7 },
	} {
		es := []types.Diagnostic{failure(1, 1000), failure(2, 2000), failure(3, 3000)}
		change(&es[2])
		writeEvents(t, path, es)
		if s := Read("a1", path); len(s.Episodes) != 0 {
			t.Fatalf("unrelated failures combined: %+v", s.Episodes)
		}
	}
}

func TestCoverageAndClaimEvidence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "record.jsonl")
	events := []types.Diagnostic{failure(1, 1000), failure(3, 2000), failure(4, 3000)}
	events[2].Kind = "task_status"
	events[2].Details = types.DiagnosticDetails{Status: "quest accepted", AgentClaim: true}
	writeEvents(t, path, events)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString("{\"schema\":1")
	f.Close()
	s := Read("a1", path, filepath.Join(dir, "harness.jsonl"))
	if s.Coverage.Status != "partial" || s.Coverage.MalformedLines != 1 || len(s.Coverage.Gaps) != 1 || s.Coverage.Gaps[0].Missing != 1 || len(s.Coverage.MissingFiles) != 1 {
		t.Fatalf("coverage: %+v", s.Coverage)
	}
	if !s.Evidence[2].Details.AgentClaim || len(s.Episodes) != 0 {
		t.Fatalf("claim was interpreted as success: %+v", s)
	}
	if !reflect.DeepEqual(s, Read("a1", path, filepath.Join(dir, "harness.jsonl"))) {
		t.Fatal("saved report is not reproducible")
	}
	os.WriteFile(path, []byte("{\"t\":1000,\"ev\":\"score\"}\n"), 0600)
	if old := Read("a1", path); old.Coverage.Status != "unavailable" || old.Coverage.FirstMS != nil || old.Coverage.CostStatus != "unavailable" {
		t.Fatalf("legacy artifacts: %+v", old)
	}
}

func TestAgentExitDoesNotCloseGameEpisode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "record.jsonl")
	harness := filepath.Join(dir, "harness.jsonl")
	writeEvents(t, path, []types.Diagnostic{failure(1, 1000), failure(2, 2000), failure(3, 3000)})
	stop := types.Diagnostic{Schema: 1, Seq: 1, T: 4000, Agent: "a1", Kind: "runtime_state", Details: types.DiagnosticDetails{Component: "agent", State: "stop"}}
	writeEvents(t, harness, []types.Diagnostic{stop})
	if !Read("a1", path, harness).Episodes[0].Active {
		t.Fatal("agent exit incorrectly ended game activity")
	}
	stop.Details.Component, stop.Details.State = "judge", "paused"
	writeEvents(t, harness, []types.Diagnostic{stop})
	if Read("a1", path, harness).Episodes[0].Active {
		t.Fatal("paused judge did not close episode")
	}
}

func TestToolSummaryPreservesUnknowns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.jsonl")
	known := true
	events := []types.Diagnostic{
		{Schema: 1, Seq: 1, T: 1000, Agent: "a1", Kind: "runtime_state", Details: types.DiagnosticDetails{Component: "agent", State: "start", ToolCoverage: true}},
		{Schema: 1, Seq: 2, T: 2000, Agent: "a1", Kind: "tool_call", Op: "bash", Outcome: "failed", Code: "tool_timeout", DurationMS: 500, Details: types.DiagnosticDetails{Runtime: "opencode", CommandFamily: "wow", DurationKnown: &known}},
		{Schema: 1, Seq: 3, T: 3000, Agent: "a1", Kind: "tool_call", Op: "bash", Outcome: "unknown", Code: "outcome_unavailable"},
	}
	writeEvents(t, path, events)
	s := Read("a1", path)
	if s.Tools.Status != "partial" || *s.Tools.ObservedTimeouts != 1 || *s.Tools.ObservedDenials != 0 || s.Tools.UnknownOutcomes != 1 || len(s.Tools.ByTool) != 2 || len(s.ByOperation) != 0 {
		t.Fatalf("tool summary: %+v", s)
	}
	for _, a := range s.Tools.ByTool {
		if a.Outcome == "unknown" && a.UnknownDurations != 1 {
			t.Fatal("missing duration treated as known")
		}
		if a.Outcome == "failed" && (a.DurationMS != 500 || a.First.Line != 2) {
			t.Fatalf("missing evidence: %+v", a)
		}
	}
	writeEvents(t, path, events[:1])
	s = Read("a1", path)
	if s.Tools.Status != "available" || s.Tools.ObservedTimeouts == nil || *s.Tools.ObservedTimeouts != 0 {
		t.Fatal("enabled collector with zero observed calls")
	}
	events[0].Details.ToolCoverage = false
	writeEvents(t, path, events[:1])
	s = Read("a1", path)
	if s.Tools.Status != "unavailable" || s.Tools.ObservedTimeouts != nil {
		t.Fatal("absent collector reported zero")
	}
	events[0].Details.State = "coverage_gap"
	writeEvents(t, path, events[:1])
	if s := Read("a1", path); s.Coverage.Status != "partial" || s.Coverage.ReportedGaps != 1 {
		t.Fatalf("lost coverage gap: %+v", s.Coverage)
	}
}

func TestObservedStateAndAgentClaimStaySeparate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.jsonl")
	events := []types.Diagnostic{failure(1, 1000), failure(2, 2000), failure(3, 3000), failure(4, 4000), failure(5, 5000)}
	for i := range events {
		events[i].Op = "act.accept"
		events[i].Details.QuestID = 62
	}
	events[1].Kind = "task_status"
	events[1].Details = types.DiagnosticDetails{Status: "quest accepted", AgentClaim: true}
	events[2].Kind = "quest_state"
	events[2].Details = types.DiagnosticDetails{QuestLog: &types.DiagnosticQuestLog{Available: true, Quests: []types.DiagnosticQuest{}}}
	events[3].Kind = "group_state"
	events[3].Details = types.DiagnosticDetails{Group: &types.DiagnosticGroup{InGroup: false, Members: []types.GUID{}}}
	writeEvents(t, path, events)
	s := Read("a1", path)
	if len(s.Evidence) != 5 || !s.Evidence[1].Details.AgentClaim || s.Evidence[2].Details.AgentClaim || s.Evidence[2].Details.QuestLog == nil || s.Evidence[3].Details.Group == nil {
		t.Fatalf("evidence: %+v", s.Evidence)
	}
	if len(s.ByOperation) != 1 || s.ByOperation[0].Outcome != "failed" || s.ByOperation[0].Count != 2 || len(s.Episodes) != 0 {
		t.Fatalf("observation or claim treated as action outcome: %+v", s)
	}
}
