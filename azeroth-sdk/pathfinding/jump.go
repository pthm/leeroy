package pathfinding

import (
	"context"
	"math"
)

// Jump is a jump along a prepared route: take off at Points[Start] running at Speed, fly the arc (the points
// between, sampled from the client's jump physics) and land at Points[End].
type Jump struct {
	Start, End int
	Speed      float32
}

// The client's jump (client/selfmove.go; movement checks they agree): JumpVelocity yd/s up at take-off under
// Gravity, so the feet peak 1.64 yd up after 0.41 s. JumpRunSpeed is the base run speed the arc is planned at.
const (
	JumpVelocity = float32(7.955547)
	Gravity      = float32(19.29110527)
	JumpRunSpeed = float32(7.0)
)

const (
	jumpRunUp = float32(4.0)  // take off at most this far (2D) back from where walking stopped
	jumpSpan  = float32(8.0)  // a landing this far out is past anything a 1.64 yd jump clears
	jumpStep  = float32(0.25) // arc samples, as dense as surface support
	jumpTries = 120           // arcs tried per obstacle, under the caller's deadline
)

type plannedJump struct {
	takeoff   int       // index into the walked points
	arc       []Point3D // the flight after take-off, ending at the landing
	resume    int       // the route point after the landing
	triangles int
}

// planJump looks for a jump over what stopped walking at route point i (src[i]): from a walked point at most
// jumpRunUp back (not before floor, the last landing), in the direction of the route a few yards on, an arc that
// clears everything with the whole body and comes down on support beside the route past the obstacle. Nearest
// take-offs first, so the run-up stays short.
func (s *PathFindingService) planJump(ctx context.Context, m uint32, walked []Point3D, swim []bool, floor int, src []Point3D, srcSwim, supported []bool, i int, body BodyProfile) (plannedJump, bool) {
	air := body
	air.StepHeight = 0 // nothing passes under the feet in the air
	last := len(walked) - 1
	tries := 0
	for k := last; k >= floor; k-- {
		if swim[k] || dist2D(walked[k], walked[last]) > jumpRunUp {
			break
		}
		for _, ahead := range []float32{3, 5, 7} {
			aim, ok := pointAhead(walked[k], src, i, ahead)
			if !ok {
				continue
			}
			if tries++; tries > jumpTries || ctx.Err() != nil {
				return plannedJump{}, false
			}
			arc, tri, ok := s.jumpArc(ctx, m, walked[k], aim, air)
			if !ok {
				continue
			}
			land := arc[len(arc)-1]
			r := rejoin(land, src, srcSwim, supported, i)
			if r < 0 {
				continue
			}
			return plannedJump{takeoff: k, arc: arc, resume: r + 1, triangles: tri}, true
		}
	}
	return plannedJump{}, false
}

// pointAhead: the first route point from i on at least d (2D) from p
func pointAhead(p Point3D, src []Point3D, i int, d float32) (Point3D, bool) {
	for j := i; j < len(src); j++ {
		if dist2D(p, src[j]) >= d {
			return src[j], true
		}
	}
	return Point3D{}, false
}

// rejoin: the supported route point from i on nearest the landing, within a yard across and half a yard up or down,
// or -1. Looks at most jumpSpan beyond the landing along the route.
func rejoin(land Point3D, src []Point3D, srcSwim, supported []bool, i int) int {
	best, bestD := -1, float32(1)
	for j := i; j < len(src); j++ {
		d := dist2D(land, src[j])
		if d > jumpSpan*2 {
			break
		}
		if supported[j] && !srcSwim[j] && d <= bestD && abs32(src[j].Z-land.Z) <= 0.5 {
			best, bestD = j, d
		}
	}
	return best
}

// jumpArc flies a jump from t towards aim at JumpRunSpeed: the feet follow the client's parabola, every step of the
// arc is a full-body sweep, and the jump lands where the feet come down onto support after the peak. ok false if
// the body hits anything, or there's nothing to land on within jumpSpan.
func (s *PathFindingService) jumpArc(ctx context.Context, m uint32, t, aim Point3D, air BodyProfile) ([]Point3D, int, bool) {
	dir := unit(Vector3{aim.X - t.X, aim.Y - t.Y, 0})
	if length(dir) == 0 {
		return nil, 0, false
	}
	peak := JumpVelocity / Gravity
	var arc []Point3D
	prev, triangles := t, 0
	for d := jumpStep; d <= jumpSpan; d += jumpStep {
		if ctx.Err() != nil {
			return nil, 0, false
		}
		sec := d / JumpRunSpeed
		p := Point3D{t.X + dir.X*d, t.Y + dir.Y*d, t.Z + JumpVelocity*sec - Gravity*sec*sec/2}
		// look for the ground from where the feet were a step ago: coming down they drop ~0.3 yd a step, and would
		// pass through a slope between samples
		ground, found := s.supportBelow(ctx, m, Point3D{p.X, p.Y, max(prev.Z, p.Z)})
		landing := found && sec > peak && p.Z <= ground+0.05 && s.footing(ctx, m, Point3D{p.X, p.Y, ground}, air.Radius)
		if found && sec > peak && p.Z <= ground+0.05 && !landing {
			return nil, 0, false // coming down onto something a body can't stand on (a rail): it can't fly through it either
		}
		if landing {
			p.Z = ground + 0.02
		}
		hit := s.vmapMgr.SweepBodyContext(ctx, m, prev, p, air)
		if hit.State != "clear" {
			return nil, 0, false
		}
		triangles += hit.Triangles
		arc = append(arc, p)
		if landing {
			return arc, triangles, true
		}
		prev = p
	}
	return nil, 0, false
}

// footing: a body can stand at p, not just touch a rail: support within 0.3 yd of p's height at least three of four
// points a body radius around it
func (s *PathFindingService) footing(ctx context.Context, m uint32, p Point3D, r float32) bool {
	n := 0
	for _, d := range [][2]float32{{r, 0}, {-r, 0}, {0, r}, {0, -r}} {
		if g, ok := s.supportBelow(ctx, m, Point3D{p.X + d[0], p.Y + d[1], p.Z + 0.3}); ok && g >= p.Z-0.3 {
			n++
		}
	}
	return n >= 3
}

// supportBelow: the highest terrain or collision surface at or just under p, at most 4 yd down
func (s *PathFindingService) supportBelow(ctx context.Context, m uint32, p Point3D) (float32, bool) {
	ground, found := float32(math.Inf(-1)), false
	if s.terrainMgr != nil {
		if h, _, _, e := s.terrainMgr.GetGridSampleContext(ctx, m, p.X, p.Y); e == nil && h <= p.Z+0.05 && h >= p.Z-4 {
			ground, found = h, true
		}
	}
	if ray := s.vmapMgr.TraceMovementContext(ctx, m, Point3D{p.X, p.Y, p.Z + 0.05}, Point3D{p.X, p.Y, p.Z - 4}); ray.State == "blocked" && ray.Position.Z > ground {
		ground, found = ray.Position.Z, true
	}
	return ground, found
}

func dist2D(a, b Point3D) float32 {
	return float32(math.Hypot(float64(a.X-b.X), float64(a.Y-b.Y)))
}

func abs32(v float32) float32 { return float32(math.Abs(float64(v))) }
