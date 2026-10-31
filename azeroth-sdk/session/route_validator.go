package session

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/pthm/leeroy/azeroth-sdk/navigation"
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

const maxRouteLegs = 64
const maxRouteWaypoints = 8192

func queryFailure(data any, code, message string) *script.Outcome {
	switch d := data.(type) {
	case types.DangerCheck:
		d.State, d.Reason = "unknown", code
		return script.Fail(code, message, d)
	case types.PathCheck:
		return routeFailure(d, code, message)
	case types.RouteCheck:
		d.Status, d.Reason = "failed", code
		if len(d.Waypoints) > 1 {
			d.Status = "partial"
		}
		return script.Fail(code, message, d)
	default:
		panic("unknown route query data")
	}
}

// Snapshot before each potentially blocking query. Later append operations must
// not mutate the prefix already published to a timeout/cancellation observer.
func (r *routeRequests) progress(j *routeRequest, data types.RouteCheck) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j.done || j.ctx.Err() != nil {
		return false
	}
	data.Waypoints = slices.Clone(data.Waypoints)
	data.Legs = slices.Clone(data.Legs)
	j.data = data
	return true
}
func (r *routeRequests) startRoute(task int, from types.Position, points []types.Position) map[string]any {
	points = slices.Clone(points)
	data := types.RouteCheck{Map: from.Map, From: from, Destination: points[len(points)-1], Source: "session_navmesh", Status: "failed", Waypoints: []types.Position{}, Legs: []types.RouteLeg{}, BlockedLeg: 1}
	return r.submit(task, "world.route", map[string]any{"map": from.Map, "waypoint_count": len(points)}, data, func(ctx context.Context, j *routeRequest) *script.Outcome {
		// Reject any cross-map leg before running even the first search.
		for i, p := range points {
			if p.Map != from.Map {
				data.BlockedLeg = i + 1
				return queryFailure(data, "wrong_map", "all route points must be on the character's current map")
			}
		}
		current := navigation.Point3D{X: float32(from.X), Y: float32(from.Y), Z: float32(from.Z)}
		for i, p := range points {
			data.BlockedLeg = i + 1
			if !r.progress(j, data) {
				return nil
			}
			dest := navigation.Point3D{X: float32(p.X), Y: float32(p.Y), Z: float32(p.Z)}
			res, err := r.find(ctx, from.Map, current, dest)
			checked := checkRoute(types.PathCheck{Map: from.Map, From: routePosition(current, from.Map), Destination: p, Source: "session_navmesh"}, current, dest, res, err)
			check := checked.Data.(types.PathCheck)
			data.Legs = append(data.Legs, types.RouteLeg{Collision: check.Collision, Index: i + 1, From: check.From, Destination: p, Status: check.Status, Reason: check.Reason, Distance: check.Distance, Gap: check.Gap, StartGap: check.StartGap, VerticalGap: check.VerticalGap, FloorValid: check.FloorValid})
			if len(data.Waypoints)+len(check.Waypoints) > maxRouteWaypoints {
				return queryFailure(data, "route_limit", "route exceeds 8192 navmesh points; split it into shorter queries")
			}
			data.Waypoints = append(data.Waypoints, check.Waypoints...)
			data.Distance += check.Distance
			if !checked.OK {
				return queryFailure(data, checked.Code, fmt.Sprintf("waypoint %d: %s", i+1, checked.Message))
			}
			data.CompletedLegs++
			// Start the next check at the actual reachable endpoint, not an assumed
			// position on a potentially disconnected floor near the requested point.
			end := check.Waypoints[len(check.Waypoints)-1]
			current = navigation.Point3D{X: float32(end.X), Y: float32(end.Y), Z: float32(end.Z)}
		}
		data.Status, data.Reason, data.BlockedLeg = "verified", "verified", 0
		return script.Ok(data)
	})
}

func routeArguments(pointsArg, optsArg any, currentMap uint32) []types.Position {
	bad := func(why string) {
		panic("world.route(points [, opts]): " + why + "; accepted opts: map; points: {x,y,z} tuples or {x=...,y=...,z=...,map=...}")
	}
	finite := func(v any) (float64, bool) {
		n, ok := v.(float64)
		return n, ok && !math.IsNaN(n) && !math.IsInf(n, 0) && math.Abs(n) <= math.MaxFloat32
	}
	mapID := func(v any) uint32 {
		n, ok := finite(v)
		if !ok || n < 0 || n > math.MaxUint32 || n != math.Trunc(n) {
			bad("map must be an unsigned integer")
		}
		return uint32(n)
	}
	m := currentMap
	if optsArg != nil {
		opts, ok := optsArg.(map[string]any)
		if !ok {
			bad("opts must be a table")
		}
		for key, val := range opts {
			if key != "map" {
				bad("unknown option " + key)
			}
			m = mapID(val)
		}
	}
	arr, ok := pointsArg.([]any)
	if !ok || len(arr) < 1 || len(arr) > maxRouteLegs {
		bad("points must be a dense array of 1..64 waypoints")
	}
	out := make([]types.Position, len(arr))
	for i, value := range arr {
		var xyz [3]any
		pm := m
		switch p := value.(type) {
		case []any:
			if len(p) != 3 {
				bad("each positional waypoint requires exactly x, y, z")
			}
			copy(xyz[:], p)
		case map[string]any:
			for key := range p {
				if key != "x" && key != "y" && key != "z" && key != "map" {
					bad("unknown waypoint field " + key)
				}
			}
			xyz = [3]any{p["x"], p["y"], p["z"]}
			if v, has := p["map"]; has {
				pm = mapID(v)
			}
		default:
			bad("each waypoint must be a table")
		}
		numbers := [3]float64{}
		for axis, val := range xyz {
			n, ok := finite(val)
			if !ok {
				bad(fmt.Sprintf("waypoint %d requires finite x, y, z", i+1))
			}
			numbers[axis] = n
		}
		out[i] = types.Position{X: numbers[0], Y: numbers[1], Z: numbers[2], Map: pm}
	}
	return out
}
func (b *Bot) registerRouteValidator(r *routeRequests) {
	e := b.lua
	e.Func("world.route", "(points [, opts = nil]) → Request<RouteCheck>", "validate 1..64 ordered 3D waypoints from your captured position before walking. points are {x,y,z} tuples or {x=...,y=...,z=...,map=...}; opts accepts only map (default current map), and every point must be on the current map. One 15s deadline covers queueing and all legs; max 4096 yards/1024 tiles per leg and 8192 output points. Shares world.path admission, cancellation and cache. Returns connected navmesh prefix, completed_legs, first blocked_leg and per-leg gaps; partial and timeout results remain failures. Does not assess hostile exposure.",
		func(points, opts any) any {
			x, y, z, _, m := b.world.Position()
			parsed := routeArguments(points, opts, m)
			task, _, _ := e.CurrentTaskIdentity()
			return r.startRoute(task, types.Position{X: float64(x), Y: float64(y), Z: float64(z), Map: m}, parsed)
		})
}
