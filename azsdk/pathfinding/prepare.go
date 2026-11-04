package pathfinding

import (
	"context"
	"fmt"
	"math"

	"github.com/pthm/leeroy/azsdk/internal/detour"
)

// PreparedPath is a walking route checked against the collision data, with its jumps and swimming stretches.
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

// TraversalError is returned for a route the collision check refused.
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
	if completePath(r) && pathLength(r.Points) > prepareStretch*1.5 {
		return s.prepareStretches(ctx, m, r, body)
	}
	return s.prepareWhole(ctx, m, r, body)
}

// prepareStretch: the yards of a long route prepared at a time (prepareStretches)
const prepareStretch = 1000

func pathLength(points []Point3D) float32 {
	d := float32(0)
	for i := 1; i < len(points); i++ {
		d += dist2D(points[i], points[i-1])
	}
	return d
}

// prepareStretches prepares a long route a stretch at a time, each routed afresh between its ends on the route and
// checked (and replanned round what blocks it) on its own, then joins them: checked whole, a long route meets some
// model somewhere that every alternative to all of it meets too (a fence by Northshire refused a walk to Menethil
// whose stretches each pass), and each replan around one obstacle was a new route to the end.
func (s *PathFindingService) prepareStretches(ctx context.Context, m uint32, r *PathResult, body BodyProfile) (*PreparedPath, error) {
	cuts := []int{0} // the route's points where its stretches meet
	d := float32(0)
	for i := 1; i < len(r.Points); i++ {
		d += dist2D(r.Points[i], r.Points[i-1])
		if d >= prepareStretch && len(r.Points)-i > 1 {
			cuts, d = append(cuts, i), 0
		}
	}
	if last := len(r.Points) - 1; cuts[len(cuts)-1] != last {
		cuts = append(cuts, last)
	}
	surface := &PathResult{Type: r.Type, RequestedStart: r.RequestedStart, RequestedEnd: r.RequestedEnd, ProjectedStart: r.ProjectedStart, ProjectedEnd: r.ProjectedEnd}
	out := &PreparedPath{Surface: surface, Collision: CollisionCheck{State: "clear"}}
	for k := 0; k+1 < len(cuts); k++ {
		from, to := r.Points[cuts[k]], r.Points[cuts[k+1]]
		if k == 0 {
			from = r.RequestedStart
		}
		if k+2 == len(cuts) {
			to = r.RequestedEnd
		}
		leg, err := s.FindPathContext(ctx, m, from, to)
		if err != nil || !completePath(leg) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return s.prepareWhole(ctx, m, r, body) // a stretch Detour won't route on its own: the route as it is
		}
		p, err := s.prepareWhole(ctx, m, leg, body)
		skip := 0 // a stretch starts where the last one ended: that point once
		if len(out.Points) > 0 {
			skip = 1
		}
		if err != nil {
			if p != nil {
				p.Collision.Segment += max(0, len(out.Points)-skip) // counted along the whole route
			}
			return p, err
		}
		base := len(out.Points) - skip
		for _, j := range p.Jumps {
			out.Jumps = append(out.Jumps, Jump{Start: j.Start + base, End: j.End + base, Speed: j.Speed})
		}
		out.Points = append(out.Points, p.Points[skip:]...)
		out.Swimming = append(out.Swimming, p.Swimming[skip:]...)
		out.NavmeshWins += p.NavmeshWins
		out.Replanned = out.Replanned || p.Replanned
		ls := p.Surface
		surface.Points = append(surface.Points, ls.Points[min(skip, len(ls.Points)):]...)
		surface.PointPolys = append(surface.PointPolys, ls.PointPolys[min(skip, len(ls.PointPolys)):]...)
		surface.Transitions = append(surface.Transitions, ls.Transitions[min(skip, len(ls.Transitions)):]...)
		surface.Corridor = append(surface.Corridor, ls.Corridor...)
	}
	return out, nil
}

// prepareWhole prepares a route as one: its surface checked, then walking, centred corridors, detours round the
// first obstacle, jumps, and the navmesh trusted over short disagreements, in that order.
func (s *PathFindingService) prepareWhole(ctx context.Context, m uint32, r *PathResult, body BodyProfile) (*PreparedPath, error) {
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
