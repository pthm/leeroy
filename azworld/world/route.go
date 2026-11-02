package world

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/navigation"
)

const routeTimeout = 15 * time.Second
const maxRouteLeg = 4096
const maxRouteCells = 400000
const maxRouteRows = 20000
const maxRouteWaypoints = 64

type routeError struct{ code, message string }

func (e routeError) Error() string { return e.message }
func checkRouteContext(ctx context.Context) {
	if err := ctx.Err(); err != nil {
		panic(err)
	}
}

type routeRequest struct {
	Map       float64
	From, To  [3]float64
	ExplicitZ bool
	Level     float64
	Horde     bool
}
type routeSource struct {
	sample  func(context.Context, uint32, float64, float64) (float64, float64, uint16)
	roadAt  func(context.Context, uint32, float64, float64) uint8
	roads   func(context.Context, float64, float64, float64, float64, float64) [][][2]float64
	threats func(context.Context, float64, float64, float64, float64, float64, bool) []threat
	// navmesh: the navmesh's path between two points (nil if none or no navmesh), for when the terrain grid can't
	// connect them: a pass too steep for its 8-yard cells, a bridge, a town on terrain holes
	navmesh func(ctx context.Context, m uint32, from, to [3]float64) [][3]float64
}

func defaultRouteSource() routeSource {
	return routeSource{sampleRouteTerrain, roadAtContext, roadPathsContext, threatsContext, navmeshPath}
}

var navigator = sync.OnceValue(func() navigation.Navigator { return navigation.NewEmbeddedNavigator(DataDir()) })

func navmeshPath(ctx context.Context, m uint32, from, to [3]float64) [][3]float64 {
	n := navigator()
	p := func(v [3]float64, z float32) navigation.Point3D {
		return navigation.Point3D{X: float32(v[0]), Y: float32(v[1]), Z: z}
	}
	// The endpoints' z is the terrain's, which can be under the floor the navmesh has there (a town's raised
	// streets, a bridge), and detour then snaps to a poly nobody can leave and returns a partial path as found: try
	// a few heights over the terrain, and take a path only if it ends at the destination.
	for _, dz := range []float32{0, 8, 16, -8} {
		z := [2]float32{float32(from[2]) + dz, float32(to[2]) + dz}
		res, err := n.FindPathContext(ctx, m, p(from, z[0]), p(to, z[1]))
		if err != nil || res == nil || !res.Found || len(res.Points) == 0 {
			continue
		}
		if e := res.Points[len(res.Points)-1]; math.Hypot(float64(e.X)-to[0], float64(e.Y)-to[1]) > 5 {
			continue
		}
		out := make([][3]float64, len(res.Points))
		for i, q := range res.Points {
			out[i] = [3]float64{float64(q.X), float64(q.Y), float64(q.Z)}
		}
		return out
	}
	return nil
}

// thin keeps at most n of a polyline's points, its ends among them: Ramer-Douglas-Peucker on x and y, with a
// tolerance that doubles from 2 yards until it fits.
func thin(pts [][3]float64, n int) [][3]float64 {
	if len(pts) <= n {
		return pts
	}
	off := func(p, a, b [3]float64) float64 { // p's distance from the segment a-b
		dx, dy := b[0]-a[0], b[1]-a[1]
		t := 0.0
		if l := dx*dx + dy*dy; l > 0 {
			t = math.Max(0, math.Min(1, ((p[0]-a[0])*dx+(p[1]-a[1])*dy)/l))
		}
		return math.Hypot(p[0]-a[0]-t*dx, p[1]-a[1]-t*dy)
	}
	for tol := 2.0; ; tol *= 2 {
		keep := make([]bool, len(pts))
		keep[0], keep[len(pts)-1] = true, true
		var split func(a, b int)
		split = func(a, b int) {
			far, at := tol, -1
			for k := a + 1; k < b; k++ {
				if d := off(pts[k], pts[a], pts[b]); d > far {
					far, at = d, k
				}
			}
			if at >= 0 {
				keep[at] = true
				split(a, at)
				split(at, b)
			}
		}
		split(0, len(pts)-1)
		var out [][3]float64
		for k, p := range pts {
			if keep[k] {
				out = append(out, p)
			}
		}
		if len(out) <= n {
			return out
		}
	}
}

var routeSlot = make(chan struct{}, 1)

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func validateRouteRequest(r routeRequest) {
	if !finite(r.Map) || r.Map < 0 || r.Map > math.MaxUint32 || r.Map != math.Trunc(r.Map) {
		panic(routeError{"invalid_arguments", "map must be an unsigned integer"})
	}
	if !finite(r.Level) || r.Level < 1 || r.Level > 255 || r.Level != math.Trunc(r.Level) {
		panic(routeError{"invalid_arguments", "level must be an integer from 1 to 255"})
	}
	for _, p := range [][3]float64{r.From, r.To} {
		for _, v := range p {
			if !finite(v) || math.Abs(v) > math.MaxFloat32 {
				panic(routeError{"invalid_arguments", "coordinates must be finite float32 values"})
			}
		}
		if math.Abs(p[0]) > 17066 || math.Abs(p[1]) > 17066 {
			panic(routeError{"route_limit", "x and y must be inside the world coordinate bounds (-17066..17066)"})
		}
	}
	dx, dy, dz := r.From[0]-r.To[0], r.From[1]-r.To[1], 0.0
	if r.ExplicitZ {
		dz = r.From[2] - r.To[2]
	}
	if math.Hypot(math.Hypot(dx, dy), dz) > maxRouteLeg {
		panic(routeError{"route_limit", "maximum endpoint distance is 4096 yards; split the route into shorter queries"})
	}
}
func routePosition(p [3]float64, m float64, z bool) *obj {
	o := newObj("x", p[0], "y", p[1], "map", m)
	if z {
		o.set("z", p[2])
	}
	return o
}
func routeFailed(base *obj, code, message string) *obj {
	return base.set("status", "failed").set("reason", code).set("error", newObj("code", code, "message", message))
}

// Planning stays on the calling goroutine. Cancellation stops work between
// samples/search steps and reaches SQL, rather than abandoning a worker.
// Local terrain/road reads already in the kernel must finish before the next check.
func planRoute(parent context.Context, r routeRequest, src routeSource) (out *obj) {
	base := newObj("source", "terrain_grid", "validation_required", true)
	ctx, cancel := context.WithTimeout(parent, routeTimeout)
	defer cancel()
	defer func() {
		if p := recover(); p != nil {
			var err error
			switch e := p.(type) {
			case routeError:
				out = routeFailed(base, e.code, e.message)
				return
			case qerr:
				err = e.error
			case error:
				err = e
			default:
				panic(p)
			}
			switch {
			case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
				out = routeFailed(base, "route_timeout", "15-second planning deadline reached; use shorter legs (maximum 4096 yards)")
			case errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
				out = routeFailed(base, "cancelled", "route planning cancelled")
			default:
				// Do not convert programming errors into missing terrain.
				if _, ok := p.(qerr); !ok {
					panic(p)
				}
				out = routeFailed(base, "not_available", "world data query failed: "+err.Error())
			}
		}
	}()
	validateRouteRequest(r)
	base.set("map", r.Map).set("from", routePosition(r.From, r.Map, r.ExplicitZ)).set("destination", routePosition(r.To, r.Map, r.ExplicitZ))
	checkRouteContext(ctx)
	select {
	case <-ctx.Done():
		panic(ctx.Err())
	case routeSlot <- struct{}{}:
	}
	defer func() { <-routeSlot }()
	checkRouteContext(ctx)
	result := searchTerrain(ctx, src, r.Map, [2]float64{r.From[0], r.From[1]}, [2]float64{r.To[0], r.To[1]}, r.Level, r.Horde)
	way := thin(result.get("waypoints").([][3]float64), maxRouteWaypoints) // fewer, further apart: world.route walks between them
	result.set("waypoints", way)
	fromGround, _, _ := src.sample(ctx, uint32(r.Map), r.From[0], r.From[1])
	checkRouteContext(ctx)
	toGround, _, _ := src.sample(ctx, uint32(r.Map), r.To[0], r.To[1])
	checkRouteContext(ctx)
	if !finite(fromGround) || !finite(toGround) {
		panic(routeError{"not_available", "terrain height is unavailable at an exact endpoint"})
	}
	from, to := r.From, r.To
	if !r.ExplicitZ {
		from[2], to[2] = fromGround, toGround
	}
	// Terrain interior points are estimates. Preserve explicit endpoint Z exactly,
	// including destinations in caves or above/below the sampled outdoor surface.
	way[0], way[len(way)-1] = from, to
	distance := 0.0
	for i := 1; i < len(way); i++ {
		a, b := way[i-1], way[i]
		distance += math.Hypot(math.Hypot(a[0]-b[0], a[1]-b[1]), a[2]-b[2])
	}
	checkRouteContext(ctx)
	base.set("status", "unverified").set("reason", "navmesh_validation_required").set("from", routePosition(from, r.Map, true)).set("destination", routePosition(to, r.Map, true))
	base.set("distance", distance).merge(result)
	if r.ExplicitZ {
		base.set("terrain_endpoint_gaps", newObj("from", math.Abs(from[2]-fromGround), "destination", math.Abs(to[2]-toGround)))
	}
	return base.set("note", "terrain proposal only; submit waypoints to world.route(points,{map=...}) or act.travel before walking. Preserve the endpoint elevations; buildings, bridges, caves and live hostile exposure are not verified.")
}

func routeCommand(args []string, flags map[string]string) (out *obj) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(qerr); ok {
				out = routeFailed(newObj("source", "terrain_grid"), "invalid_arguments", e.Error())
				return
			}
			panic(p)
		}
	}()
	return planRoute(context.Background(), parseRouteRequest(args, flags), defaultRouteSource())
}

func parseRouteRequest(args []string, flags map[string]string) routeRequest {
	bad := func(message string) { panic(qerr{fmt.Errorf("%s", message)}) }
	for key := range flags {
		if key != "level" && key != "map" && key != "horde" {
			bad("route: unknown flag --" + key)
		}
	}
	if flags["horde"] != "" {
		bad("--horde is a boolean flag, with no value")
	}
	if len(args) != 4 && len(args) != 7 {
		bad("route expects x1 y1 x2 y2 [--map N], or x1 y1 z1 x2 y2 z2 map")
	}
	n := func(i int) float64 { return num(args[i], fmt.Sprintf("coordinate %d", i+1), true) }
	lv, has := flags["level"]
	r := routeRequest{Level: num(lv, "--level", has)}
	_, r.Horde = flags["horde"]
	if len(args) == 7 {
		if _, has := flags["map"]; has {
			bad("3D route already includes map; do not also supply --map")
		}
		r.From, r.To, r.Map, r.ExplicitZ = [3]float64{n(0), n(1), n(2)}, [3]float64{n(3), n(4), n(5)}, n(6), true
	} else {
		r.From, r.To = [3]float64{n(0), n(1), 0}, [3]float64{n(2), n(3), 0}
		if m, has := flags["map"]; has {
			r.Map = num(m, "--map", true)
		}
	}
	return r
}
