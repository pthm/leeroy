package diagnostics

import (
	"github.com/pthm/leeroy/azeroth-sdk/types"
	"testing"
)

func TestReliabilityCounts(t *testing.T) {
	events := []recorded{
		{Diagnostic: types.Diagnostic{Kind: "runtime_state", Details: types.DiagnosticDetails{Component: "tooling", State: "instrumented_v1"}}},
		{Diagnostic: types.Diagnostic{Kind: "movement_owner", Code: "preempted"}},
		{Diagnostic: types.Diagnostic{Kind: "movement_owner", DurationMS: 42, Details: types.DiagnosticDetails{State: "acquired"}}},
		{Diagnostic: types.Diagnostic{Kind: "route_finished", Code: "route_timeout"}},
		{Diagnostic: types.Diagnostic{Kind: "action_finished", Op: "death.reclaim"}},
	}
	s := Summary{}
	s.summarizeReliability(events)
	r := s.Reliability
	if *r.MovementPreemptions != 1 || *r.MovementWaitMS != 42 || *r.RouteTimeouts != 1 || *r.RecoveryAttempts != 1 || len(s.Unavailable) != 0 {
		t.Fatalf("%+v %v", r, s.Unavailable)
	}
	s = Summary{}
	s.summarizeReliability([]recorded{{Diagnostic: types.Diagnostic{Kind: "movement_owner"}}})
	if s.Reliability.RouteTimeouts != nil || len(s.Unavailable) != 4 {
		t.Fatal("one movement event implied complete instrumentation")
	}
}
