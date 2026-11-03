package session

import (
	"fmt"
	"math"
	"time"

	"github.com/pthm/leeroy/azsdk/script"
	"github.com/pthm/leeroy/azsdk/types"
)

const maxMovementLease = 600 * time.Second

type movementLease struct {
	owner    types.MovementOwner
	deadline time.Time
	duration time.Duration
	depth    int
	failure  *script.Outcome
}

func movementPriority(p string) int {
	switch p {
	case "normal":
		return 1
	case "urgent":
		return 2
	case "recovery":
		return 3
	}
	return 0
}

func (m *movementRequests) expireLeaseLocked() {
	if m.lease != nil && !m.now().Before(m.lease.deadline) {
		m.invalidateLeaseLocked(script.Fail("lease_timeout", "movement control lease expired", m.ownerLocked()))
		m.advanceLocked()
	}
}

func (m *movementRequests) ownerLocked() *types.MovementOwner {
	var o types.MovementOwner
	if m.lease != nil {
		o = m.lease.owner
	}
	if m.active != nil {
		if m.lease == nil {
			o = m.active.owner
		} else {
			o.RequestID = m.active.owner.RequestID
			o.Destination = m.active.owner.Destination
			o.Phase = m.active.owner.Phase
		}
	}
	if m.active == nil && m.lease == nil {
		return nil
	}
	return &o
}

func (m *movementRequests) enqueueLocked(r *movementRequest) {
	// Higher priorities go first; equal priority remains FIFO.
	i := len(m.queue)
	for i > 0 && movementPriority(m.queue[i-1].owner.Priority) < movementPriority(r.owner.Priority) {
		i--
	}
	m.queue = append(m.queue, nil)
	copy(m.queue[i+1:], m.queue[i:])
	m.queue[i] = r
}

func (m *movementRequests) invalidateLeaseLocked(reason *script.Outcome) {
	l := m.lease
	if l == nil {
		return
	}
	m.ownerEventLocked(l.owner, "released", reason.Code, 0)
	l.failure = reason
	m.inactive = append(m.inactive, movementInactive{l.owner.TaskID, reason.Code})
	if m.active != nil {
		m.finishLocked(m.active, reason)
	}
	m.lease = nil
	// Keep the failed lease attached to its task until its wrappers unwind. A
	// callback returning after preemption cannot silently acquire movement again.
}

func (m *movementRequests) grantLeaseLocked(r *movementRequest) {
	l := r.lease
	l.owner.RequestID = 0
	l.owner.Phase = "held"
	l.deadline = m.now().Add(l.duration)
	l.depth = 1
	m.lease = l
	m.ownerEventLocked(l.owner, "acquired", "", m.now().Sub(r.queued).Milliseconds())
	m.leases[l.owner.TaskID] = l
	m.completions = append(m.completions, movementCompletion{r.owner.RequestID, script.Ok(map[string]any{"lease_id": l.owner.LeaseID})})
}

func (m *movementRequests) acquire(taskID int, task, call, priority string, queueSeconds, leaseSeconds float64) map[string]any {
	m.mu.Lock()
	defer m.unlock()
	m.expireLeaseLocked()
	h := m.reqs.Start(script.Spec{Op: "move._acquire", Timeout: time.Hour, Check: func() *script.Outcome { m.poll(); return nil }})
	id := h["id"].(int)
	complete := func(o *script.Outcome) map[string]any {
		m.completions = append(m.completions, movementCompletion{id, o})
		return h
	}
	if taskID == 0 {
		return complete(script.Fail("task_required", "movement leases require a task", nil))
	}
	if movementPriority(priority) == 0 || math.IsNaN(queueSeconds) || queueSeconds < 0 || queueSeconds > 60 || math.IsNaN(leaseSeconds) || leaseSeconds <= 0 || leaseSeconds > maxMovementLease.Seconds() {
		return complete(script.Fail("invalid_options", "priority must be normal, urgent or recovery; queue_timeout_s must be 0..60; max_s must be >0 and <=600", nil))
	}
	if m.closed {
		return complete(script.Fail("session_closed", "session closed", nil))
	}
	if l := m.leases[taskID]; l != nil {
		if l.failure != nil {
			return complete(l.failure)
		}
		l.depth++
		if movementPriority(priority) > movementPriority(l.owner.Priority) {
			l.owner.Priority = priority
		}
		return complete(script.Ok(map[string]any{"lease_id": l.owner.LeaseID}))
	}
	l := &movementLease{owner: types.MovementOwner{TaskID: taskID, Task: task, Call: call, LeaseID: id, Priority: priority, Phase: "queued"}, duration: time.Duration(leaseSeconds * float64(time.Second))}
	r := &movementRequest{owner: l.owner, lease: l, queued: m.now(), queueTimeout: time.Duration(queueSeconds * float64(time.Second))}
	r.owner.RequestID = id
	owner := m.ownerLocked()
	if owner != nil && owner.TaskID == taskID {
		// Adopt this task's raw walk into the critical section.
		m.grantLeaseLocked(r)
		if m.active != nil {
			m.active.owner.LeaseID = id
			m.active.owner.Priority = priority
		}
	} else if owner == nil {
		m.grantLeaseLocked(r)
	} else if movementPriority(priority) > movementPriority(owner.Priority) {
		newOwner := l.owner
		newOwner.Phase = "held"
		reason := script.Fail("preempted", "higher-priority movement acquired control", newOwner)
		if m.lease != nil {
			m.invalidateLeaseLocked(reason)
		} else {
			m.finishLocked(m.active, reason)
		}
		m.grantLeaseLocked(r)
	} else if queueSeconds == 0 {
		return complete(script.Fail("movement_busy", "another task owns movement", owner))
	} else {
		m.enqueueLocked(r)
	}
	return h
}

func (m *movementRequests) release(taskID, leaseID int) *script.Outcome {
	m.mu.Lock()
	defer m.unlock()
	m.expireLeaseLocked()
	l := m.leases[taskID]
	if l == nil || l.owner.LeaseID != leaseID {
		return script.Fail("invalid_lease", "no matching lease belongs to this task", nil)
	}
	out := script.Ok(nil)
	if l.failure != nil {
		out = l.failure
	}
	l.depth--
	if l.depth == 0 {
		delete(m.leases, taskID)
		if m.lease == l {
			m.ownerEventLocked(l.owner, "released", "", 0)
			if m.active != nil {
				m.finishLocked(m.active, script.Fail("cancelled", "movement control scope ended", l.owner))
			}
			m.lease = nil
			m.advanceLocked()
		}
	}
	return out
}

// permissionLocked guards instantaneous steering too; failed scopes remain
// invalid even after the preempting task has finished.
func (m *movementRequests) permissionLocked(taskID int) *script.Outcome {
	if l := m.leases[taskID]; l != nil && l.failure != nil {
		return l.failure
	}
	if o := m.ownerLocked(); o != nil && (taskID == 0 || o.TaskID != taskID) {
		return script.Fail("movement_busy", "another task owns movement", o)
	}
	return nil
}

func (m *movementRequests) steer(taskID int, fn func() *script.Outcome) *script.Outcome {
	m.mu.Lock()
	defer m.unlock()
	m.expireLeaseLocked()
	if out := m.permissionLocked(taskID); out != nil {
		return out
	}
	return fn()
}

// jump: a jump by the task that's walking ends its walk (which fails cancelled), else the walk, still going, turns
// the character back to its path when it lands (off a ledge: back up onto it). The jump goes first, so it carries
// the walk's speed.
func (m *movementRequests) jump(taskID int, jump func() *script.Outcome) *script.Outcome {
	m.mu.Lock()
	defer m.unlock()
	m.expireLeaseLocked()
	if out := m.permissionLocked(taskID); out != nil {
		return out
	}
	out := jump()
	if out.OK && m.active != nil && m.active.lease == nil {
		m.finishLocked(m.active, script.Fail("cancelled", "a jump (move.jump) ended the walk", m.active.owner))
		m.advanceLocked()
	}
	return out
}

func (m *movementRequests) registerLeases(e *script.Engine) {
	e.InternalFunc("move._check", "() → ActResult", func() map[string]any {
		id, _, _ := e.CurrentTaskIdentity()
		o := m.steer(id, func() *script.Outcome { return script.Ok(nil) })
		r := map[string]any{"ok": o.OK, "op": "move.with_control", "data": o.Data}
		if !o.OK {
			r["error"] = map[string]any{"code": o.Code, "message": o.Message}
		}
		return r
	})
	e.InternalFunc("move._expect_transfer", "(seconds) → nil", func(seconds float64) {
		id, _, _ := e.CurrentTaskIdentity()
		m.expectTransfer(id, time.Duration(seconds*float64(time.Second)))
	})
	e.InternalFunc("move._acquire", "(priority, queue_timeout_s, max_s) → Request", func(priority string, queue, max float64) map[string]any {
		id, name, call := e.CurrentTaskIdentity()
		return m.acquire(id, name, call, priority, queue, max)
	})
	e.InternalFunc("move._release", "(lease_id) → ActResult", func(lease int) map[string]any {
		id, _, _ := e.CurrentTaskIdentity()
		o := m.release(id, lease)
		r := map[string]any{"op": "move.with_control", "ok": o.OK, "data": o.Data}
		if !o.OK {
			r["error"] = map[string]any{"code": o.Code, "message": o.Message}
		}
		return r
	})
}

func (b *Bot) steeringRequest(op string, args map[string]any, fn func() *script.Outcome) map[string]any {
	if b.moves == nil {
		return b.refuse(op, args, "not_available", "movement controller is not initialized")
	}
	id, _, _ := b.lua.CurrentTaskIdentity()
	out := b.moves.steer(id, fn)
	return b.lua.Reqs.Now(script.Spec{Op: op, Args: args}, out)
}

// faceTolerance: how far (radians) the observed facing may be from the one asked for
const faceTolerance = 0.1

// faceRequest turns you to want and completes on the next tick, when the facing observed then is within
// faceTolerance of it. A route walking you faces you along its path, so it fails at once with not_facing.
func (b *Bot) faceRequest(op string, args map[string]any, want float32) map[string]any {
	want = float32(math.Mod(math.Mod(float64(want), 2*math.Pi)+2*math.Pi, 2*math.Pi)) // the server's [0, 2π)
	off := func() (float32, float64) {
		_, _, _, o := b.GetPosition()
		return o, math.Abs(math.Remainder(float64(o-want), 2*math.Pi))
	}
	if b.moves == nil {
		return b.refuse(op, args, "not_available", "movement controller is not initialized")
	}
	id, _, _ := b.lua.CurrentTaskIdentity()
	out := b.moves.steer(id, func() *script.Outcome {
		if _, d := off(); d <= faceTolerance {
			return script.Ok(nil)
		}
		if b.movementActive() {
			return script.Fail("not_facing", "a route is walking you and faces you along its path (move.stop ends it)", nil)
		}
		if err := b.world.SetFacing(want); err != nil {
			return script.Fail("failed", err.Error(), nil)
		}
		return nil
	})
	if out != nil {
		return b.lua.Reqs.Now(script.Spec{Op: op, Args: args}, out)
	}
	return b.request(op, args, time.Second, func() *script.Outcome {
		if o, d := off(); d > faceTolerance {
			return script.Fail("not_facing", fmt.Sprintf("facing %.2f after the turn to %.2f", o, want), map[string]any{"o": o, "want": want})
		}
		return script.Ok(nil)
	}, nil)
}

func (m *movementRequests) leaseStatus(task, lease int) *script.Outcome {
	m.mu.Lock()
	defer m.unlock()
	m.expireLeaseLocked()
	l := m.leases[task]
	if l == nil || l.owner.LeaseID != lease {
		return script.Fail("cancelled", "movement owner is no longer active", nil)
	}
	return l.failure
}
