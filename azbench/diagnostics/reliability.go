package diagnostics

// Duration metrics are sampled observed intervals, not extrapolated wall time.
// Missing samples, pauses and session boundaries never become dead/broken time.
type Reliability struct {
	MovementPreemptions *int64 `json:"movement_preemptions"`
	MovementWaitMS      *int64 `json:"movement_wait_ms"`
	RouteTimeouts       *int64 `json:"route_timeouts"`
	RecoveryAttempts    *int64 `json:"recovery_attempts"`
	DeadMS              *int64 `json:"dead_ms"`
	BrokenWeaponMS      *int64 `json:"broken_weapon_ms"`
	ObservedMS          int64  `json:"observed_ms"`
}

func (s *Summary) summarizeReliability(events []recorded) {
	var preempt, wait, timeouts, attempts, dead, broken int64
	instrumented, sampled := false, false
	previous := map[string]recorded{}
	paused := false
	for _, e := range events {
		if e.Kind == "runtime_state" {
			if e.Details.Component == "tooling" && e.Details.State == "instrumented_v1" {
				instrumented = true
			}
			if e.Details.Component == "judge" || e.Details.Component == "supervisor" {
				switch e.Details.State {
				case "paused", "done", "failed", "stopped":
					paused = true
					clear(previous)
				case "running", "start", "started", "resumed":
					paused = false
					clear(previous)
				}
			}
			if e.Details.Component == "session" {
				delete(previous, e.Session)
			}
		}
		if e.Kind == "movement_owner" {
			instrumented = true
			if e.Code == "preempted" {
				preempt++
			}
			if e.Details.State == "acquired" {
				wait += max(0, e.DurationMS)
			}
		}
		if e.Kind == "route_finished" && e.Code == "route_timeout" {
			timeouts++
		}
		if e.Kind == "action_finished" && (e.Op == "death.reclaim" || e.Op == "death.spirit_res") {
			attempts++
		}
		if e.Kind != "character_state" {
			continue
		}
		valid := e.Details.SampleAvailable != nil && *e.Details.SampleAvailable && e.Details.Alive != nil && e.Details.Gear != nil
		if !valid || paused {
			delete(previous, e.Session)
			continue
		}
		sampled = true
		if p, ok := previous[e.Session]; ok {
			dt := e.T - p.T
			// At most 2.5 sampling intervals. Changed states make this boundary unknown.
			if dt > 0 && dt <= 2500 {
				s.Reliability.ObservedMS += dt
				if !*p.Details.Alive && !*e.Details.Alive {
					dead += dt
				}
				if p.Details.Gear.MainHandBroken && e.Details.Gear.MainHandBroken {
					broken += dt
				}
			}
		}
		previous[e.Session] = e
	}
	s.Unavailable = nil
	if instrumented {
		s.Reliability.MovementPreemptions = &preempt
		s.Reliability.MovementWaitMS = &wait
		s.Reliability.RouteTimeouts = &timeouts
		s.Reliability.RecoveryAttempts = &attempts
	} else {
		s.Unavailable = append(s.Unavailable, "movement_preemptions", "movement_wait_ms", "route_timeouts", "recovery_attempts")
	}
	if sampled {
		s.Reliability.DeadMS = &dead
		s.Reliability.BrokenWeaponMS = &broken
	} else {
		s.Unavailable = append(s.Unavailable, "dead_ms", "broken_weapon_ms")
	}
	if s.Unavailable == nil {
		s.Unavailable = []string{}
	}
}
