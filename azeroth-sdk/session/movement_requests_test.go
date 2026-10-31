package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/navigation"
	"github.com/pthm/leeroy/azeroth-sdk/pathfinding"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

type movementFixture struct {
	mu        sync.Mutex
	position  types.Position
	moving    bool
	published []float64
	block     chan struct{}
	entered   chan struct{}
	once      sync.Once
	clock     atomic.Int64
	control   *movementRequests
}

func newMovementFixture(t *testing.T, block bool) *movementFixture {
	t.Helper()
	f := &movementFixture{entered: make(chan struct{})}
	if block {
		f.block = make(chan struct{})
	}
	f.clock.Store(time.Now().UnixNano())
	f.control = newMovementRequests(script.NewRequests(script.NewBus("")), movementBackend{
		plan: func(_ context.Context, p types.Position, valid func() bool) bool {
			f.once.Do(func() {
				close(f.entered)
				if f.block != nil {
					<-f.block
				}
			})
			f.mu.Lock()
			defer f.mu.Unlock()
			if valid() {
				f.published = append(f.published, p.X)
				f.moving = true
			}
			return false
		},
		stop:     func() { f.mu.Lock(); f.moving = false; f.mu.Unlock() },
		position: func() types.Position { f.mu.Lock(); defer f.mu.Unlock(); return f.position },
		moving:   func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.moving },
		lock:     func() string { return "" },
	})
	f.control.mu.Lock()
	f.control.now = func() time.Time { return time.Unix(0, f.clock.Load()) }
	f.control.mu.Unlock()
	t.Cleanup(func() {
		f.control.close()
		if f.block != nil {
			select {
			case <-f.block:
			default:
				close(f.block)
			}
		}
	})
	return f
}
func (f *movementFixture) start(task int, x float64) int {
	return f.control.start(task, "test", "c1", types.Position{X: x})["id"].(int)
}
func waitMovement(t *testing.T, predicate func() bool) {
	t.Helper()
	for until := time.Now().Add(time.Second); time.Now().Before(until); time.Sleep(time.Millisecond) {
		if predicate() {
			return
		}
	}
	t.Fatal("movement condition did not become true")
}
func (f *movementFixture) walking(t *testing.T, id int) {
	t.Helper()
	waitMovement(t, func() bool {
		o, _ := f.control.snapshot()
		return o != nil && o.RequestID == id && o.Phase == "walking"
	})
}
func (f *movementFixture) resultCode(t *testing.T, id int, want string) {
	t.Helper()
	r := f.control.reqs.Result(id)
	if r["ok"] != false {
		t.Fatalf("request %d = %v, want failure %s", id, r, want)
	}
	if got := r["error"].(map[string]any)["code"]; got != want {
		t.Fatalf("code = %v, want %s", got, want)
	}
}
func TestMovementFIFOAndCancellation(t *testing.T) {
	f := newMovementFixture(t, false)
	a := f.start(1, 100)
	f.walking(t, a)
	b := f.start(2, 101)
	c := f.start(3, 102)
	o, q := f.control.snapshot()
	if o.RequestID != a || len(q) != 2 || q[0].RequestID != b || q[1].RequestID != c {
		t.Fatalf("owner=%+v queue=%+v", o, q)
	}
	if r := f.control.stop(2); r.Code != "movement_busy" {
		t.Fatalf("foreign stop: %+v", r)
	}
	f.control.cancelTask(1, "task_paused")
	f.resultCode(t, a, "task_paused")
	f.walking(t, b)
	if r := f.control.cancel(c); !r.OK {
		t.Fatal(r)
	}
	f.resultCode(t, c, "cancelled")
	f.mu.Lock()
	f.position = types.Position{X: 101}
	f.mu.Unlock()
	f.control.poll()
	if r := f.control.reqs.Result(b); r["ok"] != true {
		t.Fatalf("arrival: %v", r)
	}
	o, q = f.control.snapshot()
	if o != nil || len(q) != 0 {
		t.Fatalf("not idle: %v %v", o, q)
	}
}
func TestMovementQueueDeadlineAndOwnerReplacement(t *testing.T) {
	f := newMovementFixture(t, false)
	a := f.start(1, 100)
	f.walking(t, a)
	b := f.start(2, 101)
	// Same-owner replacement also replaces a nearby destination; never reuse another task's handle.
	replacement := f.start(1, 102)
	f.resultCode(t, a, "replaced_by_owner")
	f.walking(t, replacement)
	f.clock.Add(int64(5 * time.Second))
	f.control.poll()
	f.resultCode(t, b, "movement_busy")
	r := f.control.reqs.Result(b)
	if r["data"].(*types.MovementOwner).TaskID != 1 {
		t.Fatalf("busy result lacks owner: %v", r)
	}
}
func TestMovementCancelledPlannerCannotPublish(t *testing.T) {
	f := newMovementFixture(t, true)
	a := f.start(1, 100)
	<-f.entered
	b := f.start(2, 200)
	f.control.cancelTask(1, "script_error")
	f.resultCode(t, a, "script_error")
	close(f.block)
	f.walking(t, b)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.published) != 1 || f.published[0] != 200 || !f.moving {
		t.Fatalf("stale plan published or stopped newer movement: %v moving=%v", f.published, f.moving)
	}
}
func TestMovementPlanningDeadlineAndSessionClose(t *testing.T) {
	f := newMovementFixture(t, true)
	a := f.start(1, 100)
	<-f.entered
	f.clock.Add(int64(15 * time.Second))
	f.control.poll()
	f.resultCode(t, a, "route_timeout")
	b := f.start(2, 200)
	c := f.start(3, 300)
	f.control.close()
	f.resultCode(t, b, "session_closed")
	f.resultCode(t, c, "session_closed")
	close(f.block)
	if o, q := f.control.snapshot(); o != nil || len(q) != 0 {
		t.Fatalf("ownership survived reset: %v %v", o, q)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.published) != 0 {
		t.Fatalf("cancelled path published: %v", f.published)
	}
}
func TestMovementImmediateEvaluationsAreDistinctOwners(t *testing.T) {
	f := newMovementFixture(t, false)
	a := f.start(0, 100)
	f.walking(t, a)
	b := f.start(0, 101)
	o, q := f.control.snapshot()
	if o.RequestID != a || len(q) != 1 || q[0].RequestID != b {
		t.Fatalf("RPCs shared ownership: %v %v", o, q)
	}
	if r := f.control.stop(0); r.Code != "movement_busy" {
		t.Fatalf("unowned stop: %v", r)
	}
	f.control.cancel(a)
	f.resultCode(t, a, "cancelled")
	f.walking(t, b)
}
func TestMovementTaskCancelClearsAllQueuedWork(t *testing.T) {
	f := newMovementFixture(t, false)
	a := f.start(1, 100)
	f.walking(t, a)
	b := f.start(2, 200)
	c := f.start(2, 201)
	d := f.start(3, 300)
	f.control.cancelTask(2, "task_stopped")
	f.resultCode(t, b, "task_stopped")
	f.resultCode(t, c, "task_stopped")
	_, q := f.control.snapshot()
	if len(q) != 1 || q[0].RequestID != d {
		t.Fatalf("queue=%v", q)
	}
}

// Cancellation follows task ownership, even when the task isn't awaiting its walk.
func TestMovementSchedulerLifecycle(t *testing.T) {
	for _, tc := range []struct{ name, body, control, code string }{
		{"pause", `move.to(100); wait(60)`, `task.pause(1)`, "task_paused"},
		{"stop", `move.to(100); wait(60)`, `task.stop(1)`, "task_stopped"},
		{"failure", `move.to(100); error("failed")`, "", "script_error"},
		{"return", `move.to(100)`, "", "task_finished"},
		{"close", `move.to(100); wait(60)`, "", "session_closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMovementFixture(t, true)
			e := script.NewEngine(func(string, ...any) {}, nil)
			f.control.reqs = e.Reqs
			e.OnTaskInactive = f.control.cancelTask
			e.OnClose = f.control.close
			var request int
			e.Func("move.to", "(x) → Request<MoveDone>", "", func(x float64) map[string]any {
				id, name, call := e.CurrentTaskIdentity()
				if id != 1 || name != "owner" || call == "" {
					t.Errorf("missing identity: %d %s %s", id, name, call)
				}
				r := f.control.start(id, name, call, types.Position{X: x})
				request = r["id"].(int)
				return r
			})
			if _, err := e.Eval(`task.spawn("owner",function() ` + tc.body + ` end)`); err != nil {
				t.Fatal(err)
			}
			e.CallTick()
			if tc.control != "" {
				if _, err := e.Eval(tc.control); err != nil {
					t.Fatal(err)
				}
			}
			e.Close()
			f.resultCode(t, request, tc.code)
		})
	}
}

type blockedMovementNavigator struct {
	navigation.Navigator
	entered, release chan struct{}
	once             sync.Once
}

func (n *blockedMovementNavigator) FindPathContext(_ context.Context, _ uint32, a, b navigation.Point3D) (*navigation.PathResult, error) {
	n.once.Do(func() { close(n.entered) })
	<-n.release
	return &navigation.PathResult{Found: true, Points: []navigation.Point3D{a, b}}, nil
}
func (*blockedMovementNavigator) GetHeight(_ uint32, x, y, z float32) (float32, bool) {
	return z - 5, true
}
func (*blockedMovementNavigator) GetTerrainHeight(_ uint32, x, y float32) (float32, bool) {
	return 0, false
}

func TestMovementRealBackendRejectsCancelledPath(t *testing.T) {
	b := testBot()
	defer b.lua.Close()
	n := &blockedMovementNavigator{entered: make(chan struct{}), release: make(chan struct{})}
	b.nav = n
	var cancelled atomic.Bool
	done := make(chan struct{})
	go func() { b.moveToPointGuarded(100, 0, 0, func() bool { return !cancelled.Load() }); close(done) }()
	<-n.entered
	cancelled.Store(true)
	b.StopMoving()
	close(n.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("planner did not finish")
	}
	if b.IsMoving() {
		t.Fatal("cancelled plan reached the real movement controller")
	}
	b.moveToPointGuarded(200, 0, 0, func() bool { return true })
	if !b.IsMoving() {
		t.Fatal("valid replacement did not start")
	}
	b.StopMoving()
}

func TestMovementWalkingDeadlineAndVerticalGap(t *testing.T) {
	f := newMovementFixture(t, false)
	id := f.start(1, 100)
	f.walking(t, id)
	f.mu.Lock()
	f.position = types.Position{X: 100, Z: 20}
	f.mu.Unlock()
	f.control.poll()
	if r := f.control.reqs.Result(id); r["pending"] != true {
		t.Fatalf("arrived on wrong floor: %v", r)
	}
	f.clock.Add(int64(11 * time.Minute))
	f.control.poll()
	f.resultCode(t, id, "timeout")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.moving {
		t.Fatal("walking continued after deadline")
	}
}

// The walking deadline follows the route, not the straight line: 16 yd to the abbey's back stairs is 120 yd round.
func TestMovementWalkingDeadlineFollowsTheRoute(t *testing.T) {
	f := newMovementFixture(t, false)
	f.control.mu.Lock()
	f.control.backend.remaining = func() float64 { return 120 }
	f.control.mu.Unlock()
	id := f.start(1, 16)
	f.walking(t, id)
	f.clock.Add(int64(30 * time.Second)) // the straight line's deadline (16/5*1.5+10 = 14.8 s) is long gone
	f.control.poll()
	if r := f.control.reqs.Result(id); r["pending"] != true {
		t.Fatalf("timed out on the straight line: %v", r)
	}
	f.clock.Add(int64(18 * time.Second)) // the route's: 120/5*1.5+10 = 46 s
	f.control.poll()
	f.resultCode(t, id, "timeout")
}

func TestMovementCompletionObserverCanInspectOwnership(t *testing.T) {
	f := newMovementFixture(t, false)
	id := f.start(1, 100)
	f.walking(t, id)
	observed := make(chan struct{})
	f.control.reqs.Observe(id, func(map[string]any) { f.control.snapshot(); close(observed) })
	done := make(chan struct{})
	go func() { f.control.cancel(id); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("completion observer deadlocked")
	}
	select {
	case <-observed:
	default:
		t.Fatal("completion not observed")
	}
}

func TestMovementWorldInvalidationCancelsQueueAndLatePlan(t *testing.T) {
	f := newMovementFixture(t, true)
	a := f.start(1, 100)
	<-f.entered
	b := f.start(2, 200)
	f.control.invalidateWorld("teleported")
	f.resultCode(t, a, "teleported")
	f.resultCode(t, b, "teleported")
	close(f.block)
	owner, queue := f.control.snapshot()
	if owner != nil || len(queue) != 0 {
		t.Fatalf("stale ownership: %+v %+v", owner, queue)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.published) != 0 {
		t.Fatal("old-world plan published")
	}
}
func TestMovementWorldInvalidationPoisonsSleepingLease(t *testing.T) {
	f := newMovementFixture(t, false)
	h := f.control.acquire(1, "owner", "call", "normal", 0, 60)
	if f.control.reqs.Result(h["id"].(int))["ok"] != true {
		t.Fatal("acquire failed")
	}
	f.control.invalidateWorld("world_changed")
	h = f.control.acquire(1, "owner", "late", "normal", 0, 60)
	f.resultCode(t, h["id"].(int), "world_changed")
	f.walking(t, f.start(2, 200))
}

func TestMovementDiagnosticsDoNotHoldControllerLock(t *testing.T) {
	f := newMovementFixture(t, false)
	var mu sync.Mutex
	var events []types.Diagnostic
	f.control.backend.observe = func(e types.Diagnostic) { f.control.snapshot(); mu.Lock(); events = append(events, e); mu.Unlock() }
	f.control.acquire(1, "normal", "call", "normal", 0, 60)
	f.control.acquire(2, "recovery", "call", "recovery", 0, 60)
	mu.Lock()
	defer mu.Unlock()
	preempt := 0
	for _, e := range events {
		if e.Code == "preempted" {
			preempt++
		}
	}
	if preempt != 1 {
		t.Fatalf("preemptions=%d events=%+v", preempt, events)
	}
}

func TestRecoveryReleaseTransferPreservesOnlyItsCurrentLease(t *testing.T) {
	for _, mode := range []string{"authorized", "other_task", "expired", "disconnect", "second_transfer"} {
		t.Run(mode, func(t *testing.T) {
			f := newMovementFixture(t, false)
			f.control.acquire(1, "recovery", "call", "recovery", 0, 60)
			task := 1
			if mode == "other_task" {
				task = 2
			}
			serial := f.control.expectRecoveryTransfer(task, 10*time.Second)
			if mode == "expired" {
				f.control.expectedTransferUntil = time.Now().Add(-time.Second)
			}
			reason := "teleported"
			if mode == "disconnect" {
				reason = "world_changed"
			}
			f.control.invalidateWorld(reason)
			if mode == "second_transfer" {
				f.control.invalidateWorld("teleported")
			}
			owner, _ := f.control.snapshot()
			if (owner != nil) != (mode == "authorized") {
				t.Fatalf("owner=%+v", owner)
			}
			if !f.control.transferredSince(serial) {
				t.Fatal("transfer completion not observed")
			}
		})
	}
}

func TestMovementStoppedNearbyOrOnWrongFloorIsNotArrival(t *testing.T) {
	for _, p := range []types.Position{{X: 4}, {Z: 2}} {
		t.Run(fmt.Sprint(p), func(t *testing.T) {
			f := newMovementFixture(t, false)
			id := f.start(1, 0)
			f.walking(t, id)
			f.mu.Lock()
			f.position = p
			f.moving = false
			f.mu.Unlock()
			f.clock.Add(int64(2 * time.Second))
			f.control.poll()
			f.resultCode(t, id, "stuck")
		})
	}
}
func TestMovementPreparationFailureCannotBecomeNearbySuccess(t *testing.T) {
	for _, state := range []string{"blocked", "unknown"} {
		t.Run(state, func(t *testing.T) {
			f := newMovementFixture(t, false)
			f.control.backend.prepare = func(context.Context, types.Position, func() bool) error {
				return &pathfinding.TraversalError{Check: pathfinding.CollisionCheck{State: state, Reason: "fixture", Model: "wall", Segment: 2}}
			}
			id := f.start(1, 1)
			waitMovement(t, func() bool { return f.control.reqs.Result(id)["pending"] != true })
			code := "route_blocked"
			if state == "unknown" {
				code = "collision_unavailable"
			}
			f.resultCode(t, id, code)
			if len(f.published) != 0 {
				t.Fatal("published failed path")
			}
		})
	}
}

func TestMovementRealBackendRejectsChangedPose(t *testing.T) {
	b := testBot()
	defer b.lua.Close()
	n := &blockedMovementNavigator{entered: make(chan struct{}), release: make(chan struct{})}
	b.nav = n
	done := make(chan error, 1)
	go func() { done <- b.prepareMoveContext(context.Background(), 100, 0, 0, nil) }()
	<-n.entered
	b.world.UpdatePosition(0, 0, 3, 0)
	close(n.release)
	select {
	case err := <-done:
		if !errors.Is(err, errRouteStale) {
			t.Fatalf("changed pose: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("planner did not finish")
	}
	if b.IsMoving() {
		t.Fatal("stale path reached controller")
	}
	_, _, z, _, _ := b.world.Position()
	if z != 3 {
		t.Fatal("stale plan rewrote current floor")
	}
}
