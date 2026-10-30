package session

import (
	"math"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

type recoveryAttempt struct {
	id, task int
	attempts int
	deadline time.Time
}
type recoveryCoordinator struct {
	mu             sync.Mutex
	reqs           *script.Requests
	active         *recoveryAttempt
	now            func() time.Time
	cancelMovement func(int, string)
	closed         bool
	terminated     func(int, int, string)
}

func recoveryFailure(code string, attempts int) *script.Outcome {
	return script.Fail(code, "recovery stopped: "+code, map[string]any{"state": "attention", "attempts": attempts})
}
func (c *recoveryCoordinator) begin(task int, seconds float64) map[string]any {
	c.poll()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || task == 0 || math.IsNaN(seconds) || seconds <= 0 || seconds > 600 {
		return map[string]any{"error": "recovery requires a task and timeout_s >0..600 in an open session"}
	}
	owner := false
	if c.active == nil {
		h := c.reqs.Start(script.Spec{Op: "act.recover", Timeout: time.Hour})
		c.active = &recoveryAttempt{id: h["id"].(int), task: task, deadline: c.now().Add(time.Duration(seconds * float64(time.Second)))}
		owner = true
	} else if c.active.task == task {
		return map[string]any{"error": "recovery cannot join itself"}
	}
	return map[string]any{"owner": owner, "id": c.active.id, "request": map[string]any{"id": c.active.id, "op": "act.recover", "pending": true}}
}
func (c *recoveryCoordinator) finish(task, id int, out *script.Outcome) {
	c.poll()
	c.mu.Lock()
	if c.active == nil || c.active.id != id || c.active.task != task {
		c.mu.Unlock()
		return
	}
	c.active = nil
	c.mu.Unlock()
	c.reqs.Resolve(id, out)
}
func (c *recoveryCoordinator) check(task, id int) map[string]any {
	c.poll()
	c.mu.Lock()
	active := c.active != nil && c.active.id == id && c.active.task == task
	c.mu.Unlock()
	if active {
		return map[string]any{"ok": true, "op": "act.recover"}
	}
	r := c.reqs.Result(id)
	if r != nil && r["ok"] == false {
		return r
	}
	return map[string]any{"ok": false, "op": "act.recover", "error": map[string]any{"code": "recovery_cancelled", "message": "recovery attempt is no longer active"}, "data": map[string]any{"state": "attention"}}
}
func (c *recoveryCoordinator) progress(task, attempts int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != nil && c.active.task == task {
		c.active.attempts = attempts
	}
}
func (c *recoveryCoordinator) abort(task int, reason string) {
	c.mu.Lock()
	a := c.active
	if a == nil || (task != 0 && a.task != task) {
		c.mu.Unlock()
		return
	}
	c.active = nil
	c.mu.Unlock()
	if c.terminated != nil {
		c.terminated(a.task, a.attempts, reason)
	}
	c.reqs.Resolve(a.id, recoveryFailure(reason, a.attempts))
}
func (c *recoveryCoordinator) poll() {
	c.mu.Lock()
	a := c.active
	if a == nil || c.now().Before(a.deadline) {
		c.mu.Unlock()
		return
	}
	c.active = nil
	c.mu.Unlock()
	c.cancelMovement(a.task, "recovery_timeout")
	if c.terminated != nil {
		c.terminated(a.task, a.attempts, "recovery_timeout")
	}
	c.reqs.Resolve(a.id, recoveryFailure("recovery_timeout", a.attempts))
}
func (c *recoveryCoordinator) close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.abort(0, "session_closed")
}
func (b *Bot) setupRecovery() {
	e := b.lua
	c := &recoveryCoordinator{reqs: e.Reqs, now: time.Now, cancelMovement: func(id int, reason string) {
		b.moves.cancelTask(id, reason)
		if b.routes != nil {
			b.routes.cancelTask(id, reason)
		}
	}}
	c.terminated = func(task, attempts int, code string) {
		e.RecordObservationEvent(types.Diagnostic{Kind: "recovery_transition", TaskID: task, Code: code, Details: types.DiagnosticDetails{Component: "recovery", State: "attention", Attempts: attempts}})
	}
	c.attach(e)
	c.register(e)
	e.InternalFunc("death._recovery_state", "(state, attempts) → nil", func(state string, attempts int) {
		switch state {
		case "dead", "releasing", "ghost_travel", "reclaim_wait", "reclaiming", "spirit_healer", "resting", "needs_repair", "done", "attention":
		default:
			panic("invalid recovery state")
		}
		if attempts < 0 || attempts > 2 {
			panic("invalid recovery attempts")
		}
		task, _, _ := e.CurrentTaskIdentity()
		c.progress(task, attempts)
		e.RecordTaskObservation("recovery_transition", types.DiagnosticDetails{Component: "recovery", State: state, Attempts: attempts})
		if state == "needs_repair" || state == "done" || state == "resting" {
			g := b.gear()
			e.RecordTaskObservation("gear_state", types.DiagnosticDetails{State: state, Gear: &types.DiagnosticGear{Money: b.world.Money(), MainHandBroken: g.Weapon != nil && g.Weapon.Broken, BrokenSlots: g.Broken}})
		}
	})
}
func (c *recoveryCoordinator) attach(e *script.Engine) {
	inactive, closeSession, poll := e.OnTaskInactive, e.OnClose, e.OnPoll
	e.OnTaskInactive = func(id int, reason string) {
		if inactive != nil {
			inactive(id, reason)
		}
		c.abort(id, reason)
	}
	e.OnClose = func() {
		if closeSession != nil {
			closeSession()
		}
		c.close()
	}
	e.OnPoll = func() {
		if poll != nil {
			poll()
		}
		c.poll()
	}
}
func (c *recoveryCoordinator) register(e *script.Engine) {
	e.InternalFunc("death._recovery_begin", "(timeout_s) → RecoveryJoin", func(seconds float64) map[string]any { id, _, _ := e.CurrentTaskIdentity(); return c.begin(id, seconds) })
	e.InternalFunc("death._recovery_check", "(id) → ActResult", func(id int) map[string]any { task, _, _ := e.CurrentTaskIdentity(); return c.check(task, id) })
	e.InternalFunc("death._recovery_finish", "(id, result) → ActResult", func(id int, r map[string]any) map[string]any {
		task, _, _ := e.CurrentTaskIdentity()
		out := script.Ok(r["data"])
		if r["ok"] != true {
			err, _ := r["error"].(map[string]any)
			code, _ := err["code"].(string)
			message, _ := err["message"].(string)
			if code == "" {
				code = "script_error"
			}
			out = script.Fail(code, message, r["data"])
		}
		c.finish(task, id, out)
		if done := c.reqs.Result(id); done != nil && done["ok"] != nil {
			return done
		}
		return c.check(task, id)
	})
}
