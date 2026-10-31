package session

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// movementBackend keeps arbitration testable without a server. plan must check
// valid at publication under the same lock used by stop. It must not publish a
// cancelled path or stop a newer path when its own computation finishes late.
type movementBackend struct {
	plan     func(context.Context, types.Position, func() bool) bool
	stop     func()
	position func() types.Position
	moving   func() bool
	lock     func() string
	inactive func(int, string)
	observe  func(types.Diagnostic)
}
type movementRequest struct {
	lease                     *movementLease
	queueTimeout              time.Duration
	owner                     types.MovementOwner
	cancelled                 atomic.Bool
	ctx                       context.Context
	cancel                    context.CancelFunc
	queued, started, deadline time.Time
	planning                  bool
	noPath                    bool
}
type movementInactive struct {
	task   int
	reason string
}
type movementCompletion struct {
	id     int
	result *script.Outcome
}
type movementRequests struct {
	mu                    sync.Mutex
	reqs                  *script.Requests
	backend               movementBackend
	now                   func() time.Time
	active                *movementRequest
	lease                 *movementLease
	leases                map[int]*movementLease
	queue                 []*movementRequest
	events                []types.Diagnostic
	completions           []movementCompletion
	inactive              []movementInactive
	wake                  chan struct{}
	done                  chan struct{}
	closed                bool
	transferSerial        uint64
	expectedTransferLease *movementLease
	expectedTransferUntil time.Time
}

func newMovementRequests(reqs *script.Requests, backend movementBackend) *movementRequests {
	m := &movementRequests{reqs: reqs, backend: backend, now: time.Now, leases: map[int]*movementLease{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go m.worker()
	return m
}

// Resolve outside mu: request observers and event listeners may inspect ownership.
func (m *movementRequests) unlock() {
	completed, inactive, events := m.completions, m.inactive, m.events
	m.events = nil
	m.completions, m.inactive = nil, nil
	m.mu.Unlock()
	if m.backend.observe != nil {
		for _, event := range events {
			m.backend.observe(event)
		}
	}
	if m.backend.inactive != nil {
		for _, event := range inactive {
			m.backend.inactive(event.task, event.reason)
		}
	}
	for _, c := range completed {
		m.reqs.Resolve(c.id, c.result)
	}
}
func (m *movementRequests) finishLocked(r *movementRequest, result *script.Outcome) {
	r.cancelled.Store(true)
	if r.cancel != nil {
		r.cancel()
	}
	m.completions = append(m.completions, movementCompletion{r.owner.RequestID, result})
	if m.active == r {
		if m.lease == nil {
			m.ownerEventLocked(r.owner, "released", result.Code, 0)
		}
		m.backend.stop()
		m.active = nil
	}
}
func (m *movementRequests) activateLocked(r *movementRequest) {
	if r.lease != nil {
		m.grantLeaseLocked(r)
		return
	}
	if lock := m.backend.lock(); lock != "" {
		m.finishLocked(r, script.Fail(lock, "server controls or blocks movement", r.owner))
		return
	}
	m.active = r
	if m.lease == nil {
		m.ownerEventLocked(r.owner, "acquired", "", m.now().Sub(r.queued).Milliseconds())
	}
	r.owner.Phase = "planning"
	r.deadline = m.now().Add(15 * time.Second)
	if m.lease != nil && m.lease.deadline.Before(r.deadline) {
		r.deadline = m.lease.deadline
	}
	r.ctx, r.cancel = context.WithDeadline(context.Background(), r.deadline)
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
func (m *movementRequests) advanceLocked() {
	if m.active != nil || m.lease != nil || m.closed {
		return
	}
	for len(m.queue) > 0 {
		r := m.queue[0]
		m.queue = m.queue[1:]
		if !m.now().Before(r.queued.Add(r.queueTimeout)) {
			m.finishLocked(r, script.Fail("movement_busy", "movement queue deadline expired", nil))
			continue
		}
		m.activateLocked(r)
		if m.active != nil || m.lease != nil {
			return
		}
	}
}
func (m *movementRequests) start(taskID int, task, call string, dest types.Position) map[string]any {
	m.mu.Lock()
	defer m.unlock()
	m.expireLeaseLocked()
	args := map[string]any{"x": dest.X, "y": dest.Y, "z": dest.Z}
	handle := m.reqs.Start(script.Spec{Op: "move.to", Args: args, Timeout: time.Hour, Check: func() *script.Outcome { m.poll(); return nil }})
	r := &movementRequest{owner: types.MovementOwner{TaskID: taskID, Task: task, Call: call, RequestID: handle["id"].(int), Priority: "normal", Phase: "queued", Destination: &dest}, queued: m.now(), queueTimeout: 5 * time.Second}
	if m.closed {
		m.finishLocked(r, script.Fail("session_closed", "session closed", nil))
		return handle
	}
	if lock := m.backend.lock(); lock != "" {
		m.finishLocked(r, script.Fail(lock, "server controls or blocks movement", nil))
		return handle
	}
	if l := m.leases[taskID]; l != nil {
		if l.failure != nil {
			m.finishLocked(r, l.failure)
			return handle
		}
		r.owner.Priority = l.owner.Priority
		r.owner.LeaseID = l.owner.LeaseID
	}
	if owner := m.ownerLocked(); owner != nil && taskID != 0 && owner.TaskID == taskID {
		if m.active != nil {
			m.finishLocked(m.active, script.Fail("replaced_by_owner", "the owning task replaced its destination", r.owner))
		}
		m.activateLocked(r)
	} else if m.active == nil && m.lease == nil && len(m.queue) == 0 {
		m.activateLocked(r)
	} else {
		m.enqueueLocked(r)
	}
	return handle
}
func (m *movementRequests) worker() {
	for {
		select {
		case <-m.done:
			return
		case <-m.wake:
		}
		m.mu.Lock()
		r := m.active
		if m.closed || r == nil || r.planning || r.owner.Phase != "planning" {
			m.mu.Unlock()
			continue
		}
		r.planning = true
		deadline := r.deadline
		m.mu.Unlock()
		noPath := m.backend.plan(r.ctx, *r.owner.Destination, func() bool { return !r.cancelled.Load() && r.ctx.Err() == nil && m.now().Before(deadline) })
		m.mu.Lock()
		m.expireLeaseLocked()
		if m.active == r && !r.cancelled.Load() && (r.ctx.Err() != nil || !m.now().Before(deadline)) {
			m.finishLocked(r, script.Fail("route_timeout", "planning deadline expired", r.owner))
			m.advanceLocked()
		} else if m.active == r && !r.cancelled.Load() {
			r.noPath = noPath
			r.started = m.now()
			r.deadline = r.started.Add(time.Duration(math.Min(m.distance(r)/5*1.5+10, 600) * float64(time.Second)))
			r.owner.Phase = "walking"
		}
		m.unlock()
	}
}
func (m *movementRequests) distance(r *movementRequest) float64 {
	p := m.backend.position()
	d := r.owner.Destination
	return math.Sqrt((p.X-d.X)*(p.X-d.X) + (p.Y-d.Y)*(p.Y-d.Y) + (p.Z-d.Z)*(p.Z-d.Z))
}
func (m *movementRequests) poll() {
	m.mu.Lock()
	defer m.unlock()
	now := m.now()
	if m.lease != nil && !now.Before(m.lease.deadline) {
		m.invalidateLeaseLocked(script.Fail("lease_timeout", "movement control lease expired", m.ownerLocked()))
	}
	for i := 0; i < len(m.queue); {
		r := m.queue[i]
		if !now.Before(r.queued.Add(r.queueTimeout)) {
			owner := m.ownerLocked()
			m.finishLocked(r, script.Fail("movement_busy", "another task owns movement; queue deadline expired", owner))
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
		} else {
			i++
		}
	}
	if r := m.active; r != nil {
		if lock := m.backend.lock(); lock != "" {
			m.finishLocked(r, script.Fail(lock, "server controls or blocks movement", r.owner))
		} else if !now.Before(r.deadline) {
			code := "timeout"
			if r.owner.Phase == "planning" {
				code = "route_timeout"
			}
			m.finishLocked(r, script.Fail(code, "movement deadline expired", r.owner))
		} else if r.owner.Phase == "walking" {
			d := m.distance(r)
			res := types.MoveDone{Distance: math.Round(d*10) / 10, NoPath: r.noPath}
			if d < 2.5 || (d < 5 && !m.backend.moving()) {
				m.finishLocked(r, script.Ok(res))
			} else if !m.backend.moving() && now.Sub(r.started) > time.Second {
				m.finishLocked(r, script.Fail("stuck", "movement stopped short of the destination", res))
			}
		}
	}
	m.advanceLocked()
}
func (m *movementRequests) cancelTask(taskID int, reason string) {
	m.mu.Lock()
	defer m.unlock()
	// Remove queued work before releasing the active request, so none can start.
	if l := m.leases[taskID]; l != nil {
		if m.lease == l {
			m.invalidateLeaseLocked(script.Fail(reason, "owning task became inactive", m.ownerLocked()))
		}
		if reason == "task_stopped" || reason == "task_finished" || reason == "script_error" || reason == "session_closed" {
			delete(m.leases, taskID)
		}
	}
	for i := 0; i < len(m.queue); {
		r := m.queue[i]
		if r.owner.TaskID == taskID {
			m.finishLocked(r, script.Fail(reason, "owning task became inactive", r.owner))
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
		} else {
			i++
		}
	}
	if r := m.active; r != nil && r.owner.TaskID == taskID {
		m.finishLocked(r, script.Fail(reason, "owning task became inactive", r.owner))
	}
	m.advanceLocked()
}
func (m *movementRequests) close() {
	m.mu.Lock()
	defer m.unlock()
	if m.closed {
		return
	}
	m.closed = true
	close(m.done)
	if m.lease != nil {
		m.invalidateLeaseLocked(script.Fail("session_closed", "session closed", nil))
	}
	m.leases = map[int]*movementLease{}
	if m.active != nil {
		m.finishLocked(m.active, script.Fail("session_closed", "session closed", nil))
	}
	for _, r := range m.queue {
		m.finishLocked(r, script.Fail("session_closed", "session closed", nil))
	}
	m.queue = nil
}
func (m *movementRequests) snapshot() (*types.MovementOwner, []types.MovementOwner) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owner := m.ownerLocked()
	queue := make([]types.MovementOwner, 0, len(m.queue))
	for _, r := range m.queue {
		queue = append(queue, r.owner)
	}
	return owner, queue
}
func (m *movementRequests) cancel(id int) *script.Outcome {
	m.mu.Lock()
	defer m.unlock()
	if r := m.active; r != nil && r.owner.RequestID == id {
		m.finishLocked(r, script.Fail("cancelled", "explicitly cancelled by request ID", r.owner))
		m.advanceLocked()
		return script.Ok(nil)
	}
	for i, r := range m.queue {
		if r.owner.RequestID == id {
			m.finishLocked(r, script.Fail("cancelled", "explicitly cancelled by request ID", r.owner))
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			return script.Ok(nil)
		}
	}
	return script.Fail("not_pending", "no pending movement with that request ID", nil)
}
func (m *movementRequests) stop(taskID int) *script.Outcome {
	m.mu.Lock()
	defer m.unlock()
	m.expireLeaseLocked()
	if l := m.leases[taskID]; l != nil && l.failure != nil {
		return l.failure
	}
	if owner := m.ownerLocked(); owner != nil && (taskID == 0 || owner.TaskID != taskID) {
		return script.Fail("movement_busy", "another caller owns movement; use move.cancel(request_id) for explicit cancellation", owner)
	}
	if m.active != nil {
		m.finishLocked(m.active, script.Fail("cancelled", "stopped by owning task", m.active.owner))
		m.advanceLocked()
	} else {
		m.backend.stop()
	}
	return script.Ok(types.MoveDone{Stopped: true})
}
func (b *Bot) setupMovementRequests() {
	e := b.lua
	m := newMovementRequests(e.Reqs, movementBackend{
		plan: func(ctx context.Context, p types.Position, valid func() bool) bool {
			return b.moveToPointContext(ctx, float32(p.X), float32(p.Y), float32(p.Z), valid)
		},
		stop: func() { b.StopMoving() },
		position: func() types.Position {
			x, y, z, _ := b.GetPosition()
			return types.Position{X: float64(x), Y: float64(y), Z: float64(z)}
		},
		moving: b.IsMoving, lock: b.world.MovementLock,
		observe: e.RecordObservationEvent,
		inactive: func(task int, reason string) {
			if b.routes != nil {
				b.routes.cancelTask(task, reason)
			}
		},
	})
	b.moves = m
	e.OnTaskInactive = m.cancelTask
	e.OnClose = m.close
	e.OnPoll = m.poll
	m.registerLeases(e)
	e.TaskMovement = func(id int) []types.MovementOwner {
		owner, queue := m.snapshot()
		var result []types.MovementOwner
		if owner != nil && owner.TaskID == id {
			result = append(result, *owner)
		}
		for _, q := range queue {
			if q.TaskID == id {
				result = append(result, q)
			}
		}
		return result
	}
	e.Func("move.to", "(x, y, z) → Request<MoveDone>", "walk along the navmesh; same-task replacement returns replaced_by_owner; other callers queue FIFO for at most 5 seconds then movement_busy. Planning waits at most 15 seconds; walking at most 600 seconds. Task pause/termination cancels its moves", func(x, y, z float32) map[string]any {
		id, name, call := e.CurrentTaskIdentity()
		_, _, _, _, mapID := b.world.Position()
		return m.start(id, name, call, types.Position{X: float64(x), Y: float64(y), Z: float64(z), Map: mapID})
	})
	e.Func("move.stop", "() → Request<MoveDone>", "stop this task's active movement; its walk fails cancelled. Refuses another caller's movement with movement_busy", func() map[string]any {
		id, _, _ := e.CurrentTaskIdentity()
		return e.Reqs.Now(script.Spec{Op: "move.stop"}, m.stop(id))
	})
	e.Func("move.cancel", "(request_id) → Request", "explicitly cancel active or queued movement by request ID, including another task's; the walk fails cancelled; not_pending if absent", func(id int) map[string]any {
		return e.Reqs.Now(script.Spec{Op: "move.cancel", Args: map[string]any{"request_id": id}}, m.cancel(id))
	})
	e.Func("move.owner", "() → MovementOwner?", "active local movement owner, or nil when idle; queued requests are in me.movement and task.list", func() *types.MovementOwner { o, _ := m.snapshot(); return o })
}

// invalidateWorld cancels all work tied to the old pose. Failed lease tombstones
// prevent sleeping callbacks from publishing a new path after the transfer.
func (m *movementRequests) invalidateWorld(reason string) {
	m.mu.Lock()
	defer m.unlock()
	m.transferSerial++
	preserve := reason == "teleported" && m.lease != nil && m.lease == m.expectedTransferLease && m.now().Before(m.expectedTransferUntil)
	m.expectedTransferLease = nil
	out := script.Fail(reason, "world position or connection changed; observe state before moving again", nil)
	for _, r := range m.queue {
		m.finishLocked(r, out)
	}
	m.queue = nil
	if m.lease != nil && !preserve {
		m.invalidateLeaseLocked(out)
	}
	if m.active != nil {
		m.finishLocked(m.active, out)
	}
}

// A release request authorizes one server transfer for its current recovery
// lease. Paths and queued owners still expire; only the bounded scope survives.
// The ticket cannot migrate to a replacement lease or survive a disconnect.
func (m *movementRequests) expectRecoveryTransfer(task int, timeout time.Duration) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lease != nil && m.lease.owner.TaskID == task && m.lease.owner.Priority == "recovery" {
		m.expectedTransferLease = m.lease
		m.expectedTransferUntil = m.now().Add(timeout)
	}
	return m.transferSerial
}
func (m *movementRequests) transferredSince(serial uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.transferSerial != serial
}

func (m *movementRequests) ownerEventLocked(owner types.MovementOwner, state, code string, wait int64) {
	m.events = append(m.events, types.Diagnostic{Kind: "movement_owner", TaskID: owner.TaskID, Task: owner.Task, Call: owner.Call, Request: owner.RequestID, Code: code, DurationMS: max(0, wait), Details: types.DiagnosticDetails{State: state, Movement: &owner}})
}
