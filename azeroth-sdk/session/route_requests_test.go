package session

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/navigation"
	"github.com/pthm/leeroy/azeroth-sdk/pathfinding"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func TestRouteVerification(t *testing.T) {
	from, to := navigation.Point3D{}, navigation.Point3D{X: 20}
	for _, tc := range []struct {
		name         string
		result       *navigation.PathResult
		err          error
		status, code string
		floor        *bool
	}{
		{name: "verified", result: &navigation.PathResult{Found: true, Points: []navigation.Point3D{from, to}}, status: "verified"},
		{name: "direct line fallback", result: &navigation.PathResult{Points: []navigation.Point3D{from, to}}, status: "failed", code: "route_unreachable"},
		{name: "wrong destination floor", result: &navigation.PathResult{Found: true, Points: []navigation.Point3D{from, {X: 20, Z: 10}}}, status: "partial", code: "route_unreachable"},
		{name: "wrong start floor", result: &navigation.PathResult{Found: true, Points: []navigation.Point3D{{Z: 10}, to}}, status: "failed", code: "route_unreachable"},
		{name: "blocked endpoint", result: &navigation.PathResult{Found: true, Points: []navigation.Point3D{from, {X: 10}}}, status: "partial", code: "route_unreachable"},
		{name: "unavailable", err: errors.New("no data"), status: "failed", code: "not_available"},
		{name: "limit", err: pathfinding.ErrQueryLimit, status: "failed", code: "route_limit"},
		{name: "timeout", err: context.DeadlineExceeded, status: "failed", code: "route_timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := checkRoute(types.PathCheck{}, from, to, tc.result, tc.err)
			data := out.Data.(types.PathCheck)
			if out.Code != tc.code || data.Status != tc.status || data.Reachable != (tc.code == "") {
				t.Fatalf("%+v %+v", out, data)
			}
			if tc.status == "partial" && (len(data.Waypoints) != 2 || data.Gap == nil || *data.Gap <= 3) {
				t.Fatalf("missing partial evidence: %+v", data)
			}
			if tc.name == "wrong destination floor" && (data.FloorValid == nil || *data.FloorValid) {
				t.Fatalf("wrong floor verified: %+v", data)
			}
			if tc.result == nil && data.FloorValid != nil {
				t.Fatal("unknown floor certified")
			}
		})
	}
}
func newRouteFixture(t *testing.T) *routeRequests {
	t.Helper()
	r := &routeRequests{reqs: script.NewRequests(script.NewBus("")), jobs: map[int]*routeRequest{}, timeout: time.Second}
	t.Cleanup(r.close)
	return r
}

func TestCancelOwnedQueryCancelsBackendAndRefusesForeignTask(t *testing.T) {
	r := newRouteFixture(t)
	started, exited := make(chan struct{}), make(chan struct{})
	r.find = func(ctx context.Context, _ uint32, _, _ navigation.Point3D) (*navigation.PathResult, error) {
		close(started)
		<-ctx.Done()
		close(exited)
		return nil, ctx.Err()
	}
	h := r.start(7, 0, navigation.Point3D{}, navigation.Point3D{X: 1})
	<-started
	id := h["id"].(int)
	if r.cancelOwned(8, id) {
		t.Fatal("foreign query cancelled")
	}
	if !r.cancelOwned(7, id) {
		t.Fatal("owned query not cancelled")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("backend survived cancellation")
	}
	routeResult(t, r, id, "cancelled")
}
func routeResult(t *testing.T, r *routeRequests, id int, code string) {
	t.Helper()
	waitMovement(t, func() bool { return r.reqs.Result(id)["pending"] != true })
	got := r.reqs.Result(id)
	if code == "" {
		if got["ok"] != true {
			t.Fatalf("result %v", got)
		}
		return
	}
	if e, ok := got["error"].(map[string]any); !ok || e["code"] != code {
		t.Fatalf("wanted %s: %v", code, got)
	}
}
func TestRouteDeadlineAndLateCompletion(t *testing.T) {
	r := newRouteFixture(t)
	r.timeout = 10 * time.Millisecond
	entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	r.find = func(ctx context.Context, _ uint32, a, b navigation.Point3D) (*navigation.PathResult, error) {
		close(entered)
		<-release
		defer close(exited)
		if ctx.Err() == nil {
			t.Error("backend was not cancelled")
		}
		return &navigation.PathResult{Found: true, Points: []navigation.Point3D{a, b}}, nil
	}
	id := r.start(1, 0, navigation.Point3D{}, navigation.Point3D{X: 1})["id"].(int)
	<-entered
	routeResult(t, r, id, "route_timeout")
	close(release)
	<-exited
	waitMovement(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return len(r.jobs) == 0 })
	routeResult(t, r, id, "route_timeout")
}
func TestRouteQueueBoundSurvivesCancellation(t *testing.T) {
	r := newRouteFixture(t)
	release := make(chan struct{})
	defer close(release)
	r.find = func(context.Context, uint32, navigation.Point3D, navigation.Point3D) (*navigation.PathResult, error) {
		<-release
		return nil, nil
	}
	for i := 0; i < maxRouteRequests; i++ {
		r.start(1, 0, navigation.Point3D{}, navigation.Point3D{})
	}
	r.cancelTask(1, "task_stopped")
	// Cancellation must not release admission slots while the backend still runs.
	id := r.start(2, 0, navigation.Point3D{}, navigation.Point3D{})["id"].(int)
	routeResult(t, r, id, "route_busy")
}
func TestRouteSchedulerLifecycle(t *testing.T) {
	for _, tc := range []struct{ name, body, control, code string }{
		{"pause", "p=world.path(10,0,0);wait(60)", "task.pause(1)", "task_paused"},
		{"stop", "p=world.path(10,0,0);wait(60)", "task.stop(1)", "task_stopped"},
		{"failure", "p=world.path(10,0,0);error('failed')", "", "script_error"},
		{"return", "p=world.path(10,0,0)", "", "task_finished"},
		{"close", "p=world.path(10,0,0);wait(60)", "", "session_closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := testBot()
			b.setupMovementRequests()
			b.setupRoutes()
			b.routes.find = func(ctx context.Context, _ uint32, _, _ navigation.Point3D) (*navigation.PathResult, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			closed := false
			defer func() {
				if !closed {
					b.lua.Close()
				}
			}()
			if _, err := b.lua.Eval("task.spawn('owner',function() " + tc.body + " end)"); err != nil {
				t.Fatal(err)
			}
			b.lua.CallTick()
			if tc.control != "" {
				if _, err := b.lua.Eval(tc.control); err != nil {
					t.Fatal(err)
				}
			}
			b.lua.Close()
			closed = true
			routeResult(t, b.routes, 1, tc.code)
		})
	}
}

type countingNavigator struct {
	navigation.Navigator
	calls atomic.Int32
}

func (n *countingNavigator) FindPathContext(_ context.Context, _ uint32, a, b navigation.Point3D) (*navigation.PathResult, error) {
	n.calls.Add(1)
	return &navigation.PathResult{Found: true, Points: []navigation.Point3D{a, b}}, nil
}
func TestRouteCacheIsolationAndBound(t *testing.T) {
	b := testBot()
	defer b.lua.Close()
	n := &countingNavigator{}
	b.nav = n
	a, z := navigation.Point3D{}, navigation.Point3D{X: 10}
	first, err := b.findPath(context.Background(), 0, a, z)
	if err != nil {
		t.Fatal(err)
	}
	first.Points[1].Z = 100
	second, err := b.findPath(context.Background(), 0, a, z)
	if err != nil {
		t.Fatal(err)
	}
	if second.Points[1].Z != 0 || n.calls.Load() != 1 {
		t.Fatal("cache not isolated or not used")
	}
	b.findPath(context.Background(), 1, a, z)
	if n.calls.Load() != 2 {
		t.Fatal("cache ignored map")
	}
	for i := 0; i < 150; i++ {
		b.findPath(context.Background(), 0, a, navigation.Point3D{X: float32(i)})
	}
	if len(b.routeCache) > 128 {
		t.Fatal("unbounded cache")
	}
}

func TestRouteResultRetainsMap(t *testing.T) {
	r := newRouteFixture(t)
	r.find = func(_ context.Context, m uint32, a, b navigation.Point3D) (*navigation.PathResult, error) {
		if m != 530 {
			t.Errorf("query map = %d", m)
		}
		return &navigation.PathResult{Found: true, Points: []navigation.Point3D{a, b}}, nil
	}
	id := r.start(1, 530, navigation.Point3D{}, navigation.Point3D{X: 1})["id"].(int)
	routeResult(t, r, id, "")
	data := r.reqs.Result(id)["data"].(types.PathCheck)
	if data.Map != 530 || data.From.Map != 530 || data.Destination.Map != 530 || data.Waypoints[1].Map != 530 {
		t.Fatalf("lost map: %+v", data)
	}
}

func TestRecoveryDeadlineCancelsRouteBackend(t *testing.T) {
	b := testBot()
	defer b.lua.Close()
	b.setupActions()
	entered, exited := make(chan struct{}), make(chan struct{})
	b.routes.find = func(ctx context.Context, _ uint32, _, _ navigation.Point3D) (*navigation.PathResult, error) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return nil, ctx.Err()
	}
	evalMovement(t, b.lua, "task.spawn('recovery',function() death._recovery_begin(0.01); pending_path=world.path(100,0,0); wait(60) end)")
	b.lua.CallTick()
	<-entered
	time.Sleep(20 * time.Millisecond)
	b.lua.CallTick()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("recovery timeout did not reach route backend")
	}
	// The recovery coordinator owns request 1; its nested path owns request 2.
	routeResult(t, b.routes, 2, "recovery_timeout")
}
