// Package diagnostics summarizes saved structured evidence without interpreting model prose or strategy.
package diagnostics

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

type Ref struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Session string `json:"session,omitempty"`
	Seq     uint64 `json:"seq,omitempty"`
}
type Gap struct {
	Session string `json:"session"`
	After   Ref    `json:"after"`
	Before  Ref    `json:"before"`
	Missing uint64 `json:"missing_events"`
}
type Coverage struct {
	Status            string   `json:"status"`
	Files             []string `json:"files"`
	MissingFiles      []string `json:"missing_files"`
	Sessions          []string `json:"sessions"`
	Events            int      `json:"events"`
	FirstMS           *int64   `json:"first_ms"`
	LastMS            *int64   `json:"last_ms"`
	MalformedLines    int      `json:"malformed_lines"`
	UnsupportedEvents int      `json:"unsupported_events"`
	ReportedGaps      int      `json:"reported_gaps"`
	Duplicates        int      `json:"duplicate_events"`
	ReadErrors        []string `json:"read_errors"`
	Gaps              []Gap    `json:"sequence_gaps"`
	CostStatus        string   `json:"cost_status"`
}
type Aggregate struct {
	Op         string `json:"op"`
	Code       string `json:"code,omitempty"`
	Outcome    string `json:"outcome"`
	Count      int    `json:"count"`
	DurationMS int64  `json:"duration_ms"` // inclusive per operation; never summed across nested operations
	First      Ref    `json:"first"`
	Last       Ref    `json:"last"`
}
type ToolAggregate struct {
	Aggregate
	Runtime          string `json:"runtime"`
	CommandFamily    string `json:"command_family"`
	UnknownDurations int    `json:"unknown_durations"`
}

// ToolSummary counts observed structured outcomes only. A runtime may not expose a cause for every failure.
type ToolSummary struct {
	Status           string          `json:"status"`
	ByTool           []ToolAggregate `json:"by_tool"`
	ObservedTimeouts *int            `json:"observed_timeouts"`
	ObservedDenials  *int            `json:"observed_denials"`
	UnknownOutcomes  int             `json:"unknown_outcomes"`
}
type Episode struct {
	Session    string `json:"session"`
	Task       string `json:"task"`
	TaskID     int    `json:"task_id,omitempty"`
	Op         string `json:"op"`
	Code       string `json:"code"`
	Target     string `json:"target,omitempty"`
	Count      int    `json:"count"`
	StartMS    int64  `json:"start_ms"`
	EndMS      int64  `json:"end_ms"`
	DurationMS int64  `json:"duration_ms"`
	ActionMS   int64  `json:"action_ms"`
	Active     bool   `json:"active"` // as of coverage.last_ms, not a prediction of current gameplay
	First      Ref    `json:"first"`
	Last       Ref    `json:"last"`
}
type Evidence struct {
	Ref        Ref                     `json:"event"`
	T          int64                   `json:"t"`
	Kind       string                  `json:"kind"`
	TaskID     int                     `json:"task_id,omitempty"`
	Task       string                  `json:"task,omitempty"`
	Call       string                  `json:"call,omitempty"`
	ParentCall string                  `json:"parent_call,omitempty"`
	Op         string                  `json:"op,omitempty"`
	Outcome    string                  `json:"outcome,omitempty"`
	Code       string                  `json:"code,omitempty"`
	Details    types.DiagnosticDetails `json:"details"`
}
type Summary struct {
	Schema          int         `json:"schema"`
	Reliability     Reliability `json:"reliability"`
	Coverage        Coverage    `json:"coverage"`
	ByOperation     []Aggregate `json:"by_operation"`
	Tools           ToolSummary `json:"tools"`
	Episodes        []Episode   `json:"episodes"`
	Evidence        []Evidence  `json:"evidence"`
	EvidenceOmitted int         `json:"evidence_omitted"`
	Unavailable     []string    `json:"unavailable"`
}
type recorded struct {
	types.Diagnostic
	ref Ref
}

// Read uses only the supplied artifacts. Old records without diagnostics are explicitly unavailable.
// Incomplete last lines are reported as malformed coverage, not silently interpreted as successful work.
func Read(agent string, paths ...string) Summary {
	s := Summary{Schema: 1, Coverage: Coverage{Status: "unavailable", CostStatus: "unavailable", Files: []string{}, MissingFiles: []string{}, Sessions: []string{}, ReadErrors: []string{}, Gaps: []Gap{}},
		ByOperation: []Aggregate{}, Episodes: []Episode{}, Evidence: []Evidence{}, Tools: ToolSummary{Status: "unavailable", ByTool: []ToolAggregate{}},
		Unavailable: []string{"movement_preemptions", "movement_wait_ms", "route_timeouts", "recovery_attempts", "dead_ms", "broken_weapon_ms"}}
	var events []recorded
	seen := map[string]bool{}
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				s.Coverage.MissingFiles = append(s.Coverage.MissingFiles, path)
			} else {
				s.Coverage.ReadErrors = append(s.Coverage.ReadErrors, path)
			}
			continue
		}
		s.Coverage.Files = append(s.Coverage.Files, path)
		sc, line := bufio.NewScanner(f), 0
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line++
			var ev types.Diagnostic
			if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
				s.Coverage.MalformedLines++
				continue
			}
			if ev.Kind == "" {
				continue
			} // existing gameplay records have ev instead of kind
			if ev.Schema != 1 {
				s.Coverage.UnsupportedEvents++
				continue
			}
			if ev.Agent != agent {
				continue
			}
			if ev.Seq == 0 || ev.T == 0 {
				s.Coverage.MalformedLines++
				continue
			}
			stream := ev.Session
			if stream == "" {
				stream = path
			}
			key := stream + ":" + strconv.FormatUint(ev.Seq, 10)
			if seen[key] {
				s.Coverage.Duplicates++
				continue
			}
			seen[key] = true
			events = append(events, recorded{ev, Ref{File: path, Line: line, Session: ev.Session, Seq: ev.Seq}})
		}
		if sc.Err() != nil {
			s.Coverage.ReadErrors = append(s.Coverage.ReadErrors, path)
		}
		f.Close()
	}
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if a.T != b.T {
			return a.T < b.T
		}
		if a.Session == b.Session && a.ref.File == b.ref.File {
			return a.Seq < b.Seq
		}
		return a.ref.File < b.ref.File
	})
	if len(events) > 0 {
		s.Coverage.Status, s.Coverage.Events = "available", len(events)
		s.Coverage.FirstMS, s.Coverage.LastMS = &events[0].T, &events[len(events)-1].T
	}
	sessions, streams, aggregates := map[string]bool{}, map[string][]recorded{}, map[string]*Aggregate{}
	toolAggregates := map[string]*ToolAggregate{}
	toolCoverage, timeouts, denials := false, 0, 0
	type pending struct {
		base    string
		recent  []recorded
		episode int
		last    int64
	}
	pendingByKey := map[string]*pending{}
	closeEpisodes := func(base, session string) {
		for key, p := range pendingByKey {
			if (base != "" && p.base == base) || (session != "" && strings.HasPrefix(p.base, session+"|")) || (base == "" && session == "") {
				if p.episode >= 0 {
					s.Episodes[p.episode].Active = false
				}
				delete(pendingByKey, key)
			}
		}
	}
	for _, ev := range events {
		stream := ev.Session
		if stream == "" {
			stream = ev.ref.File
		} else {
			sessions[stream] = true
		}
		streams[stream] = append(streams[stream], ev)
		if ev.Kind == "action_finished" || ev.Kind == "task_status" || ev.Kind == "runtime_state" || ev.Kind == "tool_call" || ev.Kind == "quest_state" || ev.Kind == "group_state" || ev.Kind == "recovery_transition" || ev.Kind == "gear_state" || ev.Kind == "movement_owner" || ev.Kind == "route_finished" || ev.Kind == "character_state" {
			s.Evidence = append(s.Evidence, Evidence{Ref: ev.ref, T: ev.T, Kind: ev.Kind, Task: ev.Task, TaskID: ev.TaskID, Call: ev.Call, ParentCall: ev.ParentCall, Op: ev.Op, Outcome: ev.Outcome, Code: ev.Code, Details: ev.Details})
			if len(s.Evidence) > 200 {
				s.Evidence = s.Evidence[1:]
				s.EvidenceOmitted++
			}
		}
		if ev.Kind == "runtime_state" {
			toolCoverage = toolCoverage || ev.Details.ToolCoverage
			if ev.Details.State == "coverage_gap" {
				s.Coverage.ReportedGaps++
			}
			if ev.Details.Component == "session" && ev.Details.State == "stop" && ev.Session != "" {
				closeEpisodes("", ev.Session)
			}
			if ev.Details.Component == "session" && ev.Details.State == "start" {
				closeEpisodes("", "")
			}
			if ev.Details.Component == "judge" && (ev.Details.State == "paused" || ev.Details.State == "done" || ev.Details.State == "failed") {
				closeEpisodes("", "")
			}
		}
		if ev.Kind == "tool_call" {
			if ev.Op == "" || (ev.Outcome != "failed" && ev.Outcome != "succeeded" && ev.Outcome != "unknown") {
				s.Coverage.MalformedLines++
				continue
			}
			toolCoverage = true
			key := ev.Details.Runtime + "|" + ev.Op + "|" + ev.Details.CommandFamily + "|" + ev.Outcome + "|" + ev.Code
			a := toolAggregates[key]
			if a == nil {
				a = &ToolAggregate{Aggregate: Aggregate{Op: ev.Op, Code: ev.Code, Outcome: ev.Outcome, First: ev.ref}, Runtime: ev.Details.Runtime, CommandFamily: ev.Details.CommandFamily}
				toolAggregates[key] = a
			}
			a.Count++
			if ev.Details.DurationKnown != nil && *ev.Details.DurationKnown {
				a.DurationMS += max(ev.DurationMS, 0)
			} else {
				a.UnknownDurations++
			}
			a.Last = ev.ref
			if ev.Outcome == "unknown" {
				s.Tools.UnknownOutcomes++
			}
			if ev.Code == "tool_timeout" {
				timeouts++
			}
			if ev.Code == "tool_denied" {
				denials++
			}
		}
		if ev.Kind != "action_finished" {
			continue
		}
		if ev.Op == "" || (ev.Outcome != "failed" && ev.Outcome != "succeeded") || (ev.Outcome == "failed" && ev.Code == "") {
			s.Coverage.MalformedLines++
			continue
		}
		key := ev.Op + "|" + ev.Outcome + "|" + ev.Code
		a := aggregates[key]
		if a == nil {
			a = &Aggregate{Op: ev.Op, Code: ev.Code, Outcome: ev.Outcome, First: ev.ref}
			aggregates[key] = a
		}
		a.Count++
		a.DurationMS += max(ev.DurationMS, 0)
		a.Last = ev.ref
		// Destination and all available target identifiers prevent unrelated objectives sharing an episode.
		d := ev.Details
		target, _ := json.Marshal(struct {
			Target                  string
			Destination             *types.DiagnosticDestination
			Spell, Quest, NPC, Item uint32
		}{d.Target, d.Destination, d.SpellID, d.QuestID, d.NPCEntry, d.ItemEntry})
		base := ev.Session + "|" + strconv.Itoa(ev.TaskID) + "|" + ev.Task + "|" + ev.Op + "|" + string(target)
		if ev.Outcome == "succeeded" {
			closeEpisodes(base, "")
			continue
		}
		key = base + "|" + ev.Code
		p := pendingByKey[key]
		if p == nil || ev.T-p.last > 30_000 {
			if p != nil && p.episode >= 0 {
				s.Episodes[p.episode].Active = false
			}
			p = &pending{base: base, episode: -1}
			pendingByKey[key] = p
		}
		p.last = ev.T
		if p.episode < 0 {
			p.recent = append(p.recent, ev)
			for len(p.recent) > 0 && ev.T-p.recent[0].T > 30_000 {
				p.recent = p.recent[1:]
			}
			if len(p.recent) < 3 {
				continue
			}
			first := p.recent[0]
			ep := Episode{Session: ev.Session, Task: ev.Task, TaskID: ev.TaskID, Op: ev.Op, Code: ev.Code, Target: string(target), StartMS: first.T, First: first.ref, Active: true}
			for _, item := range p.recent[:len(p.recent)-1] {
				ep.Count++
				ep.ActionMS += max(item.DurationMS, 0)
			}
			p.episode = len(s.Episodes)
			s.Episodes = append(s.Episodes, ep)
			p.recent = nil
		}
		ep := &s.Episodes[p.episode]
		ep.Count++
		ep.ActionMS += max(ev.DurationMS, 0)
		ep.EndMS, ep.Last = ev.T, ev.ref
		ep.DurationMS = ep.EndMS - ep.StartMS
	}
	for i := range s.Episodes {
		if s.Coverage.LastMS != nil && *s.Coverage.LastMS-s.Episodes[i].EndMS > 30_000 {
			s.Episodes[i].Active = false
		}
	}
	for session := range sessions {
		s.Coverage.Sessions = append(s.Coverage.Sessions, session)
	}
	sort.Strings(s.Coverage.Sessions)
	for stream, evs := range streams {
		sort.Slice(evs, func(i, j int) bool { return evs[i].Seq < evs[j].Seq })
		if len(evs) > 0 && evs[0].Seq > 1 {
			s.Coverage.Gaps = append(s.Coverage.Gaps, Gap{Session: stream, Before: evs[0].ref, Missing: evs[0].Seq - 1})
		}
		for i := 1; i < len(evs); i++ {
			if evs[i].Seq > evs[i-1].Seq+1 {
				s.Coverage.Gaps = append(s.Coverage.Gaps, Gap{Session: stream, After: evs[i-1].ref, Before: evs[i].ref, Missing: evs[i].Seq - evs[i-1].Seq - 1})
			}
		}
	}
	sort.Slice(s.Coverage.Gaps, func(i, j int) bool {
		a, b := s.Coverage.Gaps[i], s.Coverage.Gaps[j]
		if a.Session != b.Session {
			return a.Session < b.Session
		}
		return a.After.Seq < b.After.Seq
	})
	if s.Coverage.Status == "available" && (s.Coverage.MalformedLines+s.Coverage.UnsupportedEvents+s.Coverage.ReportedGaps+len(s.Coverage.Gaps)+len(s.Coverage.ReadErrors)+len(s.Coverage.MissingFiles) > 0) {
		s.Coverage.Status = "partial"
	}
	keys := make([]string, 0, len(aggregates))
	for k := range aggregates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		s.ByOperation = append(s.ByOperation, *aggregates[key])
	}
	keys = keys[:0]
	for key := range toolAggregates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		s.Tools.ByTool = append(s.Tools.ByTool, *toolAggregates[key])
	}
	if toolCoverage {
		s.Tools.Status, s.Tools.ObservedTimeouts, s.Tools.ObservedDenials = "available", &timeouts, &denials
		if s.Coverage.Status == "partial" || s.Tools.UnknownOutcomes > 0 {
			s.Tools.Status = "partial"
		}
	} else {
		s.Unavailable = append(s.Unavailable, "tool_timeouts", "tool_denials")
	}
	s.summarizeReliability(events)
	return s
}

// TopActive uses an explicit observation time so status can reject stale episodes without changing saved reports.
func (s Summary) TopActive(nowMS int64, limit int) []Episode {
	out := []Episode{}
	for _, e := range s.Episodes {
		if e.Active && nowMS-e.EndMS <= 30_000 {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ActionMS != out[j].ActionMS {
			return out[i].ActionMS > out[j].ActionMS
		}
		return out[i].DurationMS > out[j].DurationMS
	})
	return out[:min(max(limit, 0), len(out))]
}

// Paths names the two authoritative diagnostic artifacts for an agent in a benchmark run.
func Paths(dir, agent string) []string {
	return []string{filepath.Join(dir, agent+".record.jsonl"), filepath.Join(dir, agent, "harness.jsonl")}
}
