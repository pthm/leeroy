package session

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/navigation"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func TestMultiRouteStopsAtBlockedLeg(t *testing.T) {
	r := newRouteFixture(t)
	calls := 0
	r.find = func(_ context.Context, m uint32, a, b navigation.Point3D) (*navigation.PathResult, error) {
		calls++
		if m != 530 {
			t.Errorf("map=%d", m)
		}
		if calls == 2 {
			b.X = 15
		}
		return &navigation.PathResult{Found: true, Points: []navigation.Point3D{a, b}}, nil
	}
	id := r.startRoute(1, types.Position{Map: 530}, []types.Position{{X: 10, Map: 530}, {X: 20, Map: 530}, {X: 30, Map: 530}})["id"].(int)
	routeResult(t, r, id, "route_unreachable")
	data := r.reqs.Result(id)["data"].(types.RouteCheck)
	if calls != 2 || data.Status != "partial" || data.CompletedLegs != 1 || data.BlockedLeg != 2 || data.Distance != 15 || len(data.Legs) != 2 || *data.Legs[1].Gap != 5 || data.Waypoints[len(data.Waypoints)-1].X != 15 {
		t.Fatalf("%+v calls=%d", data, calls)
	}
}
func TestMultiRouteChainsActualEndpoint(t *testing.T) {
	r := newRouteFixture(t)
	var starts []float32
	r.find = func(_ context.Context, _ uint32, a, b navigation.Point3D) (*navigation.PathResult, error) {
		starts = append(starts, a.X)
		b.X--
		return &navigation.PathResult{Found: true, Points: []navigation.Point3D{a, b}}, nil
	}
	id := r.startRoute(1, types.Position{}, []types.Position{{X: 10}, {X: 20}})["id"].(int)
	routeResult(t, r, id, "")
	data := r.reqs.Result(id)["data"].(types.RouteCheck)
	if data.Status != "verified" || data.BlockedLeg != 0 || data.CompletedLegs != 2 || !reflect.DeepEqual(starts, []float32{0, 9}) {
		t.Fatalf("%+v starts=%v", data, starts)
	}
}
func TestMultiRouteTimeoutKeepsPrefix(t *testing.T) {
	r := newRouteFixture(t)
	r.timeout = 30 * time.Millisecond
	var calls atomic.Int32
	exited := make(chan struct{})
	r.find = func(ctx context.Context, _ uint32, a, b navigation.Point3D) (*navigation.PathResult, error) {
		if calls.Add(1) == 2 {
			<-ctx.Done()
			close(exited)
			return nil, ctx.Err()
		}
		return &navigation.PathResult{Found: true, Points: []navigation.Point3D{a, b}}, nil
	}
	id := r.startRoute(1, types.Position{}, []types.Position{{X: 10}, {X: 20}, {X: 30}})["id"].(int)
	var observed atomic.Int32
	r.reqs.Observe(id, func(map[string]any) { observed.Add(1) })
	routeResult(t, r, id, "route_timeout")
	<-exited
	waitMovement(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return len(r.jobs) == 0 })
	data := r.reqs.Result(id)["data"].(types.RouteCheck)
	if data.Status != "partial" || data.CompletedLegs != 1 || data.BlockedLeg != 2 || len(data.Legs) != 1 || data.Waypoints[1].X != 10 || calls.Load() != 2 || observed.Load() != 1 {
		t.Fatalf("%+v calls=%d observed=%d", data, calls.Load(), observed.Load())
	}
}
func TestMultiRouteRejectsOtherMapBeforeSearch(t *testing.T) {
	r := newRouteFixture(t)
	r.find = func(context.Context, uint32, navigation.Point3D, navigation.Point3D) (*navigation.PathResult, error) {
		t.Error("search before map validation")
		return nil, nil
	}
	id := r.startRoute(1, types.Position{}, []types.Position{{X: 10}, {X: 20, Map: 1}})["id"].(int)
	routeResult(t, r, id, "wrong_map")
	data := r.reqs.Result(id)["data"].(types.RouteCheck)
	if data.BlockedLeg != 2 || data.CompletedLegs != 0 || len(data.Waypoints) != 0 {
		t.Fatalf("%+v", data)
	}
}
func TestMultiRouteOutputBound(t *testing.T) {
	r := newRouteFixture(t)
	calls := 0
	r.find = func(_ context.Context, _ uint32, a, b navigation.Point3D) (*navigation.PathResult, error) {
		calls++
		pts := make([]navigation.Point3D, maxRouteWaypoints)
		for i := range pts {
			pts[i] = a
		}
		pts[len(pts)-1] = b
		return &navigation.PathResult{Found: true, Points: pts}, nil
	}
	id := r.startRoute(1, types.Position{}, []types.Position{{X: 10}, {X: 20}, {X: 30}})["id"].(int)
	routeResult(t, r, id, "route_limit")
	data := r.reqs.Result(id)["data"].(types.RouteCheck)
	if len(data.Waypoints) != maxRouteWaypoints || data.CompletedLegs != 1 || data.BlockedLeg != 2 || calls != 2 {
		t.Fatalf("bound: points=%d completed=%d blocked=%d calls=%d", len(data.Waypoints), data.CompletedLegs, data.BlockedLeg, calls)
	}
}
func TestMultiRouteArguments(t *testing.T) {
	b := testBot()
	defer b.lua.Close()
	b.setupRoutes()
	for _, expr := range []string{
		"world.route({})", "world.route({{1,2}})", "world.route({{x=1,y=2}})",
		"world.route({{1,2,3,4}})", "world.route({{1,2,3,x=1}})",
		"world.route({{x=1,y=2,z=3,typo=true}})", "world.route({{1,2,0/0}})",
		"world.route({[1]={1,2,3},[3]={4,5,6}})", "world.route({{1,2,3}},{foo=1})",
		"world.route({{1,2,3}},{map=-1})", "world.route({{1,2,3}},{map=0.5})",
		"world.route({{1,2,3}},true)", "world.route({{1,2,3}},{1})",
	} {
		if _, err := b.lua.Eval(expr); err == nil || !strings.Contains(err.Error(), "world.route") {
			t.Fatalf("%s: %v", expr, err)
		}
	}
	got := routeArguments([]any{[]any{float64(1), float64(2), float64(3)}, map[string]any{"x": float64(4), "y": float64(5), "z": float64(6)}}, map[string]any{"map": float64(530)}, 0)
	if len(got) != 2 || got[0].Map != 530 || got[1].Z != 6 {
		t.Fatalf("%+v", got)
	}
}

func TestRouteValidationCancelledWithMovementLease(t *testing.T) {
	for _, code := range []string{"preempted", "lease_timeout"} {
		t.Run(code, func(t *testing.T) {
			b := testBot()
			defer b.lua.Close()
			n := &cancellableNavigator{blockedMovementNavigator: blockedMovementNavigator{entered: make(chan struct{})}}
			b.nav = n
			b.setupActions()
			seconds := 60.0
			if code == "lease_timeout" {
				seconds = 0.02
			}
			b.moves.acquire(1, "travel", "", "normal", 5, seconds)
			id := b.routes.startRoute(1, types.Position{}, []types.Position{{X: 10}, {X: 20}})["id"].(int)
			<-n.entered
			// Completion observers can inspect movement ownership without taking a
			// lock that the invalidation callback still holds.
			b.lua.Reqs.Observe(id, func(map[string]any) { b.moves.snapshot() })
			if code == "preempted" {
				b.moves.acquire(2, "recovery", "", "recovery", 5, 60)
			} else {
				time.Sleep(30 * time.Millisecond)
				b.moves.poll()
			}
			routeResult(t, b.routes, id, code)
			next := b.routes.start(2, 0, navigation.Point3D{}, navigation.Point3D{X: 10})["id"].(int)
			routeResult(t, b.routes, next, "")
			if n.calls.Load() != 2 {
				t.Fatalf("cancelled validator continued: calls=%d", n.calls.Load())
			}
		})
	}
}
