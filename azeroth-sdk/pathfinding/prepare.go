package pathfinding

import (
	"context"
	"fmt"
	"github.com/o0olele/detour-go/detour"
	"math"
)

type PreparedPath struct {
	Points   []Point3D
	Swimming []bool
	Jumps    []Jump // in order along Points
	// NavmeshWins counts the stretches (each at most navmeshWins yards) where the route follows the navmesh
	// through what the collision data refused
	NavmeshWins int
	Surface     *PathResult
	Collision   CollisionCheck
	Replanned   bool
}
type TraversalError struct{ Check CollisionCheck }

func (e *TraversalError) Error() string {
	return fmt.Sprintf("%s at segment %d: %s", e.Check.State, e.Check.Segment, e.Check.Reason)
}

// navmeshWins is the longest stretch (2D) a route follows the navmesh against the collision data.
const navmeshWins = float32(2.0)

// PrepareWalkingContext validates static support and body clearance. At most
// three alternative portal placements and six local waypoints are tried under
// the caller's deadline, then the route with jumps over what stopped it, then
// the navmesh over stretches of up to navmeshWins yards the collision data
// refuses. A longer obstruction remains a failed route.
func (s *PathFindingService) PrepareWalkingContext(ctx context.Context, m uint32, r *PathResult, body BodyProfile) (*PreparedPath, error) {
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !body.valid() {
		return nil, fmt.Errorf("invalid body profile")
	}
	if !completePath(r) || len(r.Points) > maxPointPathLength {
		return nil, fmt.Errorf("complete surface path required")
	}
	if err := s.ValidateSurfaceContext(ctx, m, r); err != nil {
		return nil, err
	}
	out, err := s.prepareCandidate(ctx, m, r, body, false, false)
	if err == nil {
		return out, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if out == nil || out.Collision.State != "blocked" {
		return out, err
	}
	first, firstErr := out, err
	corridors := []*PathResult{r}
	for _, fraction := range []float32{0.5, 0.25, 0.75} {
		candidate, e := s.CenteredPathContext(ctx, m, r, fraction)
		if e != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		corridors = append(corridors, candidate)
		prepared, e := s.prepareCandidate(ctx, m, candidate, body, false, false)
		if e == nil {
			prepared.Replanned = true
			return prepared, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	// Try a small, fixed set of local waypoints around the first obstacle.
	// Every leg still comes from Detour and every candidate is checked in full.
	n := vec(first.Collision.Normal)
	side := unit(Vector3{-n.Y, n.X, 0})
	if length(side) > 0 {
		for _, radius := range []float32{2, 4, 7} {
			for _, sign := range []float32{-1, 1} {
				v := vec(first.Collision.Position).Add(side.Mul(radius * sign)).Add(n.Mul(0.5))
				z, ok := s.FloorHeightContext(ctx, m, v.X, v.Y, v.Z)
				if !ok {
					continue
				}
				via := Point3D{v.X, v.Y, z}
				left, e := s.FindPathContext(ctx, m, r.RequestedStart, via)
				if e != nil || !completePath(left) {
					continue
				}
				right, e := s.FindPathContext(ctx, m, via, r.RequestedEnd)
				if e != nil || !completePath(right) {
					continue
				}
				candidate := left.Clone()
				candidate.Points = append(candidate.Points, right.Points...)
				candidate.PointPolys = append(candidate.PointPolys, right.PointPolys...)
				candidate.Transitions = append(candidate.Transitions, right.Transitions...)
				candidate.Corridor = append(candidate.Corridor, right.Corridor...)
				candidate.RequestedEnd = r.RequestedEnd
				candidate.ProjectedEnd = right.ProjectedEnd
				if len(candidate.Points) > maxPointPathLength {
					continue
				}
				if e = s.ValidateSurfaceContext(ctx, m, candidate); e != nil {
					continue
				}
				prepared, e := s.prepareCandidate(ctx, m, candidate, body, false, false)
				if e == nil {
					prepared.Replanned = true
					return prepared, nil
				}
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
			}
		}
	}
	// Walking round it failed: jump what the navmesh climbs but a step can't, on the corridor or its alternatives.
	for i, c := range corridors {
		jumped, e := s.prepareCandidate(ctx, m, c, body, true, false)
		if e == nil {
			jumped.Replanned = i > 0
			return jumped, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	// Still blocked: the navmesh is the best evidence of where a character can go, and the collision data disagrees
	// with it in places (a 1.2 yd strip across Goldshire inn's front that the navmesh climbs from the main room, the
	// navmesh over a tree's roots). Short stretches of disagreement follow the navmesh.
	if trusted, e := s.prepareCandidate(ctx, m, r, body, true, true); e == nil {
		return trusted, nil
	} else if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return first, firstErr
}

// prepareCandidate checks one corridor: walking, then with jumps, then with the navmesh winning stretches of up to
// navmeshWins yards where the collision data refuses it (trust).
func (s *PathFindingService) prepareCandidate(ctx context.Context, m uint32, r *PathResult, body BodyProfile, jumps, trust bool) (*PreparedPath, error) {
	out := &PreparedPath{Surface: r, Points: append([]Point3D(nil), r.Points...), Swimming: make([]bool, len(r.Points)), Collision: CollisionCheck{State: "clear"}}
	fail := func(c CollisionCheck) (*PreparedPath, error) { out.Collision = c; return out, &TraversalError{c} }
	if s.vmapMgr == nil {
		return fail(unknownCollision("collision data unavailable"))
	}
	// Resolve movement modes while holding the mesh lock only for polygon access.
	pf, err := s.acquirePathFinderContext(ctx, m)
	if err != nil {
		return nil, err
	}
	if err = lockContext(ctx, pf.meshMu.TryRLock); err != nil {
		s.releasePathFinder(m, pf)
		return nil, err
	}
	modes := make([]uint16, len(r.PointPolys))
	for i, ref := range r.PointPolys {
		if detour.DtStatusFailed(pf.navMesh.GetPolyFlags(ref, &modes[i])) {
			err = fmt.Errorf("invalid surface reference")
			break
		}
	}
	pf.meshMu.RUnlock()
	s.releasePathFinder(m, pf)
	if err != nil {
		return nil, err
	}
	supported := make([]bool, len(out.Points))
	for i, p := range out.Points {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if modes[i]&2 != 0 {
			return fail(CollisionCheck{State: "blocked", Reason: "hazardous_surface", Segment: i, Position: p})
		}
		// Resolve physical support near the selected surface. Terrain above a cave
		// cannot participate. A missing model remains unknown, never a terrain fallback.
		ground, water := float32(math.Inf(-1)), float32(math.Inf(-1))
		if s.terrainMgr != nil {
			h, w, _, e := s.terrainMgr.GetGridSampleContext(ctx, m, p.X, p.Y)
			if e == nil {
				water = w
				if h <= p.Z+MaxProjectionVertical {
					ground = h
				}
			}
		}
		if modes[i]&8 != 0 && (math.IsInf(float64(water), 0) || math.IsNaN(float64(water))) {
			// the navmesh's water with none in the terrain: liquid inside a building (Stormwind's canals), which the
			// collision data doesn't carry. Its water polygons lie on the surface.
			// ponytail: navmesh surface as the water level; read the vmaps' WMO liquid if this proves too coarse
			if ray := s.vmapMgr.TraceMovementContext(ctx, m, Point3D{p.X, p.Y, p.Z + MaxProjectionVertical}, Point3D{p.X, p.Y, p.Z - 1.6}); ray.State == "clear" {
				out.Points[i].Z = p.Z - 1.4
				out.Swimming[i] = true
				supported[i] = true
				continue
			}
		}
		if modes[i]&8 != 0 && !math.IsInf(float64(water), 0) && water-ground > 1.6 {
			out.Points[i].Z = water - 1.4
			out.Swimming[i] = true
			supported[i] = true
			continue
		}
		ray := s.vmapMgr.TraceMovementContext(ctx, m, Point3D{p.X, p.Y, p.Z + MaxProjectionVertical}, Point3D{p.X, p.Y, p.Z - MaxSupportBelow})
		if ray.State == "unknown" {
			ray.Segment = i
			return fail(ray)
		}
		// the collision floor nearest the surface point wins: terrain within reach above it is the hillside a cave
		// or building is cut into, not the floor (Echo Ridge Mine's mouth)
		if ray.State == "blocked" && (math.IsInf(float64(ground), 0) || math.Abs(float64(ray.Position.Z-p.Z)) < math.Abs(float64(ground-p.Z))) {
			ground = ray.Position.Z
		}
		// no support: the navmesh is over something too thin to sample (a fence rail); walking to it fails below,
		// unless a jump clears it
		if math.IsInf(float64(ground), 0) || math.IsNaN(float64(ground)) || ground > p.Z+MaxProjectionVertical || ground < p.Z-MaxSupportBelow {
			continue
		}
		supported[i] = true
		out.Points[i].Z = ground + 0.02
	}
	if last := len(out.Points) - 1; !supported[last] {
		return fail(CollisionCheck{State: "blocked", Reason: "unsupported_floor", Segment: last, Position: out.Points[last]})
	}
	if projectionTooFar(r.RequestedEnd, out.Points[len(out.Points)-1]) {
		return fail(CollisionCheck{State: "blocked", Reason: "endpoint_floor_mismatch", Segment: len(out.Points) - 1, Position: out.Points[len(out.Points)-1]})
	}
	// The actual pose and destination connections are checked too. Neither is
	// silently replaced by a projected navmesh point in the controller.
	// Keep the surface endpoint: requested Z often describes an NPC/spawn rather
	// than exact collision height. Projection bounds were checked by the planner.
	src := append([]Point3D{r.RequestedStart}, out.Points...)
	srcSwim := append([]bool{out.Swimming[0]}, out.Swimming...)
	srcOK := append([]bool{true}, supported...)
	walked, swim := []Point3D{src[0]}, []bool{srcSwim[0]}
	overridden := float32(0) // yards of the current stretch the navmesh won
	for i := 1; i < len(src); i++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		c := s.walkable(ctx, m, walked[len(walked)-1], src[i], swim[len(swim)-1], srcSwim[i], srcOK[i], body)
		if c.State == "clear" {
			walked, swim = append(walked, src[i]), append(swim, srcSwim[i])
			out.Collision.Triangles += c.Triangles
			overridden = 0
			continue
		}
		// What the navmesh climbs but a step can't (a fence rail, 1.53 yd), the client jumps.
		if c.State == "blocked" && jumps && overridden == 0 {
			floor := 0
			if n := len(out.Jumps); n > 0 {
				floor = out.Jumps[n-1].End
			}
			if j, ok := s.planJump(ctx, m, walked, swim, floor, src, srcSwim, srcOK, i, body); ok {
				walked, swim = append(walked[:j.takeoff+1], j.arc...), swim[:j.takeoff+1]
				for range j.arc {
					swim = append(swim, false)
				}
				out.Jumps = append(out.Jumps, Jump{Start: j.takeoff, End: len(walked) - 1, Speed: JumpRunSpeed})
				out.Collision.Triangles += j.triangles
				i = j.resume - 1
				continue
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		if c.State == "blocked" && trust {
			if d := dist2D(walked[len(walked)-1], src[i]); overridden+d <= navmeshWins {
				if overridden == 0 {
					out.NavmeshWins++
				}
				overridden += d
				walked, swim = append(walked, src[i]), append(swim, srcSwim[i])
				continue
			}
		}
		c.Segment = i
		return fail(c)
	}
	out.Points, out.Swimming = walked, swim
	return out, nil
}

// walkable checks one walking segment from a to b: b has support, the climb is a step or a slope the body takes,
// and the body sweeps through clear.
func (s *PathFindingService) walkable(ctx context.Context, m uint32, a, b Point3D, swimA, swimB, supportedB bool, body BodyProfile) CollisionCheck {
	if !supportedB {
		return CollisionCheck{State: "blocked", Reason: "unsupported_floor", Position: b}
	}
	dx := math.Hypot(float64(b.X-a.X), float64(b.Y-a.Y))
	dz := math.Abs(float64(b.Z - a.Z))
	if !swimA && !swimB && dz > math.Max(float64(body.StepHeight), dx*math.Tan(float64(body.MaxSlope)*math.Pi/180)) {
		return CollisionCheck{State: "blocked", Reason: "step_or_slope_limit", Position: b}
	}
	if swimA || swimB {
		body.StepHeight = 0
	}
	return s.vmapMgr.SweepBodyContext(ctx, m, a, b, body)
}

func completePath(r *PathResult) bool {
	return r != nil && len(r.Points) > 1 && r.Type&(PathfindNopath|PathfindNotUsingPath|PathfindIncomplete|PathfindShort|PathfindFarFromPolyStart|PathfindFarFromPolyEnd) == 0
}
