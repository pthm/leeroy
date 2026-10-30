package diagnostics

import (
	"github.com/pthm/leeroy/azeroth-sdk/types"
	"testing"
)

func TestReliabilityObservedIntervalsAndBoundaries(t *testing.T) {
	yes, no := true, false
	sample := func(ms int64, session string, alive bool) recorded {
		return recorded{Diagnostic: types.Diagnostic{T: ms, Session: session, Kind: "character_state", Details: types.DiagnosticDetails{SampleAvailable: &yes, Alive: &alive, Gear: &types.DiagnosticGear{MainHandBroken: true}}}}
	}
	state := func(ms int64, state string) recorded {
		return recorded{Diagnostic: types.Diagnostic{T: ms, Kind: "runtime_state", Details: types.DiagnosticDetails{Component: "judge", State: state}}}
	}
	events := []recorded{
		{Diagnostic: types.Diagnostic{Kind: "runtime_state", Details: types.DiagnosticDetails{Component: "tooling", State: "instrumented_v1"}}},
		{Diagnostic: types.Diagnostic{Kind: "movement_owner", Code: "preempted"}},
		{Diagnostic: types.Diagnostic{Kind: "movement_owner", DurationMS: 42, Details: types.DiagnosticDetails{State: "acquired"}}},
		{Diagnostic: types.Diagnostic{Kind: "route_finished", Code: "route_timeout"}},
		{Diagnostic: types.Diagnostic{Kind: "action_finished", Op: "death.reclaim"}},
		sample(1000, "a", false), sample(2000, "a", false), state(2500, "paused"), sample(3000, "a", false), state(4000, "running"), sample(5000, "a", false), sample(6000, "a", true), sample(7000, "b", false), sample(8000, "b", false), sample(12000, "b", false),
	}
	s := Summary{}
	s.summarizeReliability(events)
	r := s.Reliability
	if *r.DeadMS != 2000 || *r.BrokenWeaponMS != 3000 || r.ObservedMS != 3000 || *r.MovementPreemptions != 1 || *r.MovementWaitMS != 42 || *r.RouteTimeouts != 1 || *r.RecoveryAttempts != 1 {
		t.Fatalf("%+v dead=%d broken=%d", r, *r.DeadMS, *r.BrokenWeaponMS)
	}
	s = Summary{}
	bad := sample(1000, "a", false)
	bad.Details.SampleAvailable = &no
	s.summarizeReliability([]recorded{bad})
	if s.Reliability.DeadMS != nil || len(s.Unavailable) != 6 {
		t.Fatal("unknown evidence became zero", s)
	}
}

func TestReliabilitySequenceGapIsNotObservedTime(t *testing.T) {
	yes, no := true, false
	events := []recorded{}
	for i, seq := range []uint64{1, 3, 4} {
		events = append(events, recorded{Diagnostic: types.Diagnostic{Session: "s", Seq: seq, T: int64(i+1) * 1000, Kind: "character_state", Details: types.DiagnosticDetails{SampleAvailable: &yes, Alive: &no, Gear: &types.DiagnosticGear{}}}})
	}
	s := Summary{}
	s.summarizeReliability(events)
	if s.Reliability.ObservedMS != 1000 || *s.Reliability.DeadMS != 1000 {
		t.Fatal(s.Reliability)
	}
	s = Summary{}
	s.summarizeReliability([]recorded{{Diagnostic: types.Diagnostic{Kind: "movement_owner"}}})
	if s.Reliability.RouteTimeouts != nil {
		t.Fatal("one movement event implied complete instrumentation")
	}
}
