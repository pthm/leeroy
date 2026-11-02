package diagnostics

// Reliability is the tooling's: movement, routes and recovery. Time dead or with a broken weapon is the character's,
// in the report's time_use (from the game log's state records).
type Reliability struct {
	MovementPreemptions *int64 `json:"movement_preemptions"`
	MovementWaitMS      *int64 `json:"movement_wait_ms"`
	RouteTimeouts       *int64 `json:"route_timeouts"`
	RecoveryAttempts    *int64 `json:"recovery_attempts"`
}

func (s *Summary) summarizeReliability(events []recorded) {
	var preempt, wait, timeouts, attempts int64
	instrumented := false
	for _, e := range events {
		switch {
		case e.Kind == "runtime_state" && e.Details.Component == "tooling" && e.Details.State == "instrumented_v1":
			instrumented = true
		case e.Kind == "movement_owner":
			if e.Code == "preempted" {
				preempt++
			}
			if e.Details.State == "acquired" {
				wait += max(0, e.DurationMS)
			}
		case e.Kind == "route_finished" && e.Code == "route_timeout":
			timeouts++
		case e.Kind == "action_finished" && (e.Op == "death.reclaim" || e.Op == "death.spirit_res"):
			attempts++
		}
	}
	s.Unavailable = []string{}
	if instrumented {
		s.Reliability = Reliability{MovementPreemptions: &preempt, MovementWaitMS: &wait, RouteTimeouts: &timeouts, RecoveryAttempts: &attempts}
	} else {
		s.Unavailable = append(s.Unavailable, "movement_preemptions", "movement_wait_ms", "route_timeouts", "recovery_attempts")
	}
}
