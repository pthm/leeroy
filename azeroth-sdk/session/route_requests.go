package session

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/navigation"
	"github.com/pthm/leeroy/azeroth-sdk/pathfinding"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

const maxRouteRequests = 16

type routeRequest struct {
	id, task int
	op       string
	started  time.Time
	ctx      context.Context
	cancel   context.CancelFunc
	data     any
	done     bool
	reason   string
}
type routeRequests struct {
	observe func(types.Diagnostic)
	mu      sync.Mutex
	reqs    *script.Requests
	jobs    map[int]*routeRequest
	closed  bool
	timeout time.Duration
	find    func(context.Context, uint32, navigation.Point3D, navigation.Point3D) (*navigation.PathResult, error)
}

func routePosition(p navigation.Point3D, m uint32) types.Position {
	return types.Position{X: float64(p.X), Y: float64(p.Y), Z: float64(p.Z), Map: m}
}
func routeFailure(data types.PathCheck, code, message string) *script.Outcome {
	data.Status, data.Reason, data.Reachable = "failed", code, false
	return script.Fail(code, message, data)
}
func (r *routeRequests) start(task int, m uint32, from, to navigation.Point3D) map[string]any {
	data := types.PathCheck{Status: "failed", Map: m, From: routePosition(from, m), Destination: routePosition(to, m), Source: "session_navmesh", Waypoints: []types.Position{}}
	return r.submit(task, "world.path", map[string]any{"x": to.X, "y": to.Y, "z": to.Z}, data, func(ctx context.Context, j *routeRequest) *script.Outcome {
		result, err := r.find(ctx, m, from, to)
		return checkRoute(data, from, to, result, err)
	})
}
func (r *routeRequests) submit(task int, op string, args map[string]any, data any, run func(context.Context, *routeRequest) *script.Outcome) map[string]any {
	h := r.reqs.Start(script.Spec{Op: op, Args: args, Timeout: time.Hour})
	id := h["id"].(int)
	r.mu.Lock()
	code, message := "", ""
	if r.closed {
		code, message = "session_closed", "session closed"
	} else if len(r.jobs) >= maxRouteRequests {
		code, message = "route_busy", "16 route requests are already pending; await an existing request before submitting more"
	}
	if code != "" {
		r.mu.Unlock()
		r.reqs.Resolve(id, queryFailure(data, code, message))
		return h
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	j := &routeRequest{id: id, task: task, op: op, started: time.Now(), ctx: ctx, cancel: cancel, data: data}
	r.jobs[id] = j
	r.mu.Unlock()
	// A completed/cancelled job retains its slot until the computation exits.
	// Repeated cancellation cannot create an unbounded set of detached workers.
	stop := context.AfterFunc(ctx, func() { r.finish(j, nil) })
	go func() {
		defer func() { stop(); cancel(); r.mu.Lock(); delete(r.jobs, id); r.mu.Unlock() }()
		r.finish(j, run(ctx, j))
	}()
	return h
}
func (r *routeRequests) finish(j *routeRequest, out *script.Outcome) {
	r.mu.Lock()
	if j.done {
		r.mu.Unlock()
		return
	}
	if j.reason != "" {
		out = queryFailure(j.data, j.reason, "route cancelled because its owner became inactive")
	} else if err := j.ctx.Err(); err != nil {
		code := "cancelled"
		if errors.Is(err, context.DeadlineExceeded) {
			code = "route_timeout"
		}
		out = queryFailure(j.data, code, "route deadline or cancellation reached; use shorter legs (maximum 4096 yards)")
	}
	if out == nil {
		r.mu.Unlock()
		return
	}
	j.done = true
	r.mu.Unlock()
	if r.observe != nil {
		outcome := "succeeded"
		if !out.OK {
			outcome = "failed"
		}
		details := types.DiagnosticDetails{}
		switch d := out.Data.(type) {
		case types.PathCheck:
			details.Route = &types.DiagnosticRoute{Source: d.Source, Status: d.Status, From: d.From, Destination: d.Destination, Distance: d.Distance, Gap: d.Gap, FloorValid: d.FloorValid, Collision: d.Collision}
		case types.RouteCheck:
			details.Route = &types.DiagnosticRoute{Source: d.Source, Status: d.Status, From: d.From, Destination: d.Destination, Distance: d.Distance, CompletedLegs: d.CompletedLegs, BlockedLeg: d.BlockedLeg}
			if len(d.Legs) > 0 {
				last := d.Legs[len(d.Legs)-1]
				details.Route.Gap, details.Route.FloorValid, details.Route.Collision = last.Gap, last.FloorValid, last.Collision
			}
		case types.DangerCheck:
			details.State = d.State
		}
		r.observe(types.Diagnostic{Kind: "route_finished", Op: j.op, Request: j.id, TaskID: j.task, Outcome: outcome, Code: out.Code, DurationMS: time.Since(j.started).Milliseconds(), Details: details})
	}
	r.reqs.Resolve(j.id, out)
}
func (r *routeRequests) cancelTask(task int, reason string) {
	r.mu.Lock()
	var jobs []*routeRequest
	for _, j := range r.jobs {
		if task == 0 || j.task == task {
			j.reason = reason
			j.cancel()
			jobs = append(jobs, j)
		}
	}
	r.mu.Unlock()
	for _, j := range jobs {
		r.finish(j, nil)
	}
}

func (r *routeRequests) cancelOwned(task, id int) bool {
	r.mu.Lock()
	j := r.jobs[id]
	if j == nil || j.task != task {
		r.mu.Unlock()
		return false
	}
	if !j.done {
		j.reason = "cancelled"
		j.cancel()
	}
	r.mu.Unlock()
	r.finish(j, nil)
	return true
}
func (r *routeRequests) close() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.cancelTask(0, "session_closed")
}

func checkRoute(data types.PathCheck, from, to navigation.Point3D, res *navigation.PathResult, err error) *script.Outcome {
	if err != nil {
		code, message := "not_available", "navmesh query failed: "+err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			code, message = "route_timeout", "route query exceeded its deadline; use shorter legs (maximum 4096 yards)"
		} else if errors.Is(err, context.Canceled) {
			code, message = "cancelled", "route query cancelled"
		} else if errors.Is(err, pathfinding.ErrQueryLimit) {
			code, message = "route_limit", err.Error()
		}
		return routeFailure(data, code, message)
	}
	if res != nil {
		data.Collision = collisionEvidence(res.Collision, res.Replanned)
		if res.Collision != nil && res.Collision.State != "clear" {
			code := "route_blocked"
			if res.Collision.State == "unknown" {
				code = "collision_unavailable"
			}
			return routeFailure(data, code, res.Collision.Reason)
		}
	}
	if res == nil || !res.Found || len(res.Points) == 0 {
		return routeFailure(data, "route_unreachable", "no navmesh path connects the requested points")
	}
	start, end := res.Points[0], res.Points[len(res.Points)-1]
	gap, vertical, startGap := dist3(end, to), math.Abs(float64(end.Z-to.Z)), dist3(start, from)
	floor := vertical <= 3 && math.Abs(float64(start.Z-from.Z)) <= 3
	data.Gap, data.StartGap, data.VerticalGap, data.FloorValid = &gap, &startGap, &vertical, &floor
	if startGap > 3 {
		return routeFailure(data, "route_unreachable", startsOff(start, from))
	}
	prev := from
	for _, p := range res.Points {
		data.Distance += dist3(prev, p)
		prev = p
		data.Waypoints = append(data.Waypoints, routePosition(p, data.Map))
	}
	if res.Partial || math.Hypot(float64(end.X-to.X), float64(end.Y-to.Y)) > 3 || !floor {
		data.Status, data.Reason = "partial", "route_unreachable"
		return script.Fail("route_unreachable", endsShort(end, to), data)
	}
	data.Status, data.Reason, data.Reachable = "verified", "verified", true
	return script.Ok(data)
}

// endsShort explains a navmesh path that stops short of to: where it ends, so a caller can go there instead or
// see that the requested height is on a floor the navmesh doesn't have (an upper storey, a roof, a wrong z).
func endsShort(end, to navigation.Point3D) string {
	return fmt.Sprintf("navmesh path ends %.1f yd short of the requested 3D destination: the nearest reachable point is (%.1f, %.1f, %.1f), %s",
		dist3(end, to), end.X, end.Y, end.Z, relHeight(end.Z-to.Z, "it"))
}

// startsOff explains a start the navmesh doesn't cover (a storey or ledge it lacks): where its nearest floor is
func startsOff(start, from navigation.Point3D) string {
	return fmt.Sprintf("you're %.1f yd from the navmesh: its nearest floor is at (%.1f, %.1f, %.1f), %s; step onto it (or back the way you came) and route from there",
		dist3(start, from), start.X, start.Y, start.Z, relHeight(start.Z-from.Z, "you"))
}

// relHeight: "4.2 yd above you", "beside you"
func relHeight(dz float32, what string) string {
	if dz > 3 {
		return fmt.Sprintf("%.1f yd above %s", dz, what)
	} else if dz < -3 {
		return fmt.Sprintf("%.1f yd below %s", -dz, what)
	}
	return "beside " + what
}

func (b *Bot) setupRoutes() {
	e := b.lua
	r := &routeRequests{observe: e.RecordObservationEvent, reqs: e.Reqs, jobs: map[int]*routeRequest{}, timeout: pathfinding.QueryTimeout, find: func(ctx context.Context, m uint32, from, to navigation.Point3D) (*navigation.PathResult, error) {
		if b.nav == nil {
			return nil, errors.New("navigation data is unavailable")
		}
		return b.findPath(ctx, m, from, to)
	}}
	b.routes = r
	e.InternalFunc("world._via_portal", "(x, y, z) → table?", func(x, y, z float64) map[string]any {
		if b.nav == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		px, py, pz, _, m := b.world.Position()
		v, ok := b.viaPortal(ctx, m, navigation.Point3D{X: px, Y: py, Z: pz}, navigation.Point3D{X: float32(x), Y: float32(y), Z: float32(z)})
		if !ok {
			return nil
		}
		pt := func(p navigation.Point3D) map[string]any {
			return map[string]any{"x": round1(p.X), "y": round1(p.Y), "z": round1(p.Z)}
		}
		return map[string]any{"name": v.Name, "enter": pt(v.Enter), "exit": pt(v.Exit)}
	})
	e.InternalFunc("world._cancel", "(request_id) → bool", func(id int) bool {
		task, _, _ := e.CurrentTaskIdentity()
		return r.cancelOwned(task, id)
	})
	b.registerRouteValidator(r)
	b.registerDanger(r)
	inactive, closeSession := e.OnTaskInactive, e.OnClose
	e.OnTaskInactive = func(id int, reason string) {
		if inactive != nil {
			inactive(id, reason)
		}
		r.cancelTask(id, reason)
	}
	e.OnClose = func() {
		if closeSession != nil {
			closeSession()
		}
		r.close()
	}
	e.Func("world.path", "(x, y, z) → Request<PathCheck>", "await a supported navmesh route with static body clearance from your current 3D position; collision evidence includes state, segment, model and bounded replan outcome. Dynamic doors and lifts are outside this check. Deadline 15s including queueing, max leg 4096 yards / 1024 tiles, at most 16 pending queries and one active search per session. Identical successful searches are cached (128 entries). Task pause/stop/failure/return and session close cancel pending work. Failures include route_blocked, collision_unavailable, route_unreachable, route_timeout, route_limit, route_busy and not_available; partial data never authorizes movement",
		func(x, y, z float32) any {
			px, py, pz, _, m := b.world.Position()
			task, _, _ := e.CurrentTaskIdentity()
			return r.start(task, m, navigation.Point3D{X: px, Y: py, Z: pz}, navigation.Point3D{X: x, Y: y, Z: z})
		})
}

func collisionEvidence(c *pathfinding.CollisionCheck, replanned bool) *types.CollisionEvidence {
	if c == nil {
		return nil
	}
	return &types.CollisionEvidence{State: c.State, Reason: c.Reason, Segment: c.Segment, Position: types.Position{X: float64(c.Position.X), Y: float64(c.Position.Y), Z: float64(c.Position.Z)}, Normal: types.Position{X: float64(c.Normal.X), Y: float64(c.Normal.Y), Z: float64(c.Normal.Z)}, Model: c.Model, Replanned: replanned, Scope: "static"}
}
