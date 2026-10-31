package pathfinding

import (
	"context"
	"fmt"
	"github.com/o0olele/detour-go/detour"
	"math"
)

const MaxProjectionHorizontal = float32(0.75)
const MaxProjectionVertical = float32(1.5)

// Bound support sampling as well as path interpolation near steps and ledges.
const surfaceSampleSpacing = 0.25

// MaxSupportBelow: real ground may lie this far below the navmesh. Its detail mesh floats up to about its climb
// height over the terrain near fences and on slopes with nothing in between (Northshire's vineyards, the road to
// Goldshire), where characters stand on the ground.
const MaxSupportBelow = float32(2.0)

// projectionTooFar: requested point a is too far from its navmesh projection b to be the same floor. a may be up to
// MaxSupportBelow under it (standing on the ground beneath a floating navmesh), MaxProjectionVertical over it.
func projectionTooFar(a, b Point3D) bool {
	return math.Hypot(float64(a.X-b.X), float64(a.Y-b.Y)) > float64(MaxProjectionHorizontal) || a.Z > b.Z+MaxProjectionVertical || a.Z < b.Z-MaxSupportBelow
}

// surfacePath uses the funnel with ALL_CROSSINGS, retaining corners and each
// change of support polygon. The following sample inherits that polygon, never
// a global nearest-poly query. Off-mesh links remain explicit unsupported actions.
func (pf *PathFinder) surfacePath(ctx context.Context, a, b []float32, corridor []detour.DtPolyRef) (points []Point3D, refs []detour.DtPolyRef, links []bool, truncated bool, err error) {
	corners := make([]float32, maxPointPathLength*3)
	flags := make([]detour.DtStraightPathFlags, maxPointPathLength)
	polys := make([]detour.DtPolyRef, maxPointPathLength)
	var n int
	status := pf.query.FindStraightPath(a, b, corridor, len(corridor), corners, flags, polys, &n, maxPointPathLength, detour.DT_STRAIGHTPATH_ALL_CROSSINGS)
	if detour.DtStatusFailed(status) || n == 0 {
		return nil, nil, nil, false, fmt.Errorf("surface corridor unavailable")
	}
	return pf.sampleSurfaceCorners(ctx, corridor, corners, flags, polys, n, status)
}

func (pf *PathFinder) sampleSurfaceCorners(ctx context.Context, corridor []detour.DtPolyRef, corners []float32, flags []detour.DtStraightPathFlags, polys []detour.DtPolyRef, n int, status detour.DtStatus) (points []Point3D, refs []detour.DtPolyRef, links []bool, truncated bool, err error) {
	appendPoint := func(v []float32, ref detour.DtPolyRef, link bool) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		h, known := pf.surfaceHeight(ref, v)
		if !link && !known {
			return fmt.Errorf("surface height unavailable for polygon %d", ref)
		}
		if link {
			h = v[1]
		}
		points = append(points, Point3D{v[2], v[0], h})
		refs = append(refs, ref)
		links = append(links, link)
		return nil
	}
	if err = appendPoint(corners[:3], corridor[0], false); err != nil {
		return
	}
	for i := 1; i < n; i++ {
		from, to := corners[(i-1)*3:i*3], corners[i*3:(i+1)*3]
		ref := polys[i-1]
		if ref == 0 {
			ref = corridor[len(corridor)-1]
		}
		link := flags[i-1]&detour.DT_STRAIGHTPATH_OFFMESH_CONNECTION != 0
		steps := int(math.Ceil(math.Hypot(float64(to[0]-from[0]), float64(to[2]-from[2])) / surfaceSampleSpacing))
		if steps < 1 || link {
			steps = 1
		}
		for k := 1; k <= steps; k++ {
			if len(points) >= maxPointPathLength {
				truncated = true
				return
			}
			t := float32(k) / float32(steps)
			v := []float32{from[0] + t*(to[0]-from[0]), from[1] + t*(to[1]-from[1]), from[2] + t*(to[2]-from[2])}
			if err = appendPoint(v, ref, link); err != nil {
				return
			}
			// The crossing belongs to the polygon entered there. Its height is still
			// sampled from the incoming surface; the next segment checks continuity.
			if k == steps && polys[i] != 0 && flags[i]&detour.DT_STRAIGHTPATH_OFFMESH_CONNECTION == 0 {
				next := polys[i]
				if next != ref {
					// The funnel coalesces identical corner coordinates. Restore skipped
					// corridor polygons as explicit zero-length portal crossings.
					fromIndex, toIndex := -1, -1
					for j, p := range corridor {
						if p == ref {
							fromIndex = j
						}
						if p == next {
							toIndex = j
						}
					}
					for j := fromIndex + 1; fromIndex >= 0 && j < toIndex; j++ {
						refs[len(refs)-1] = corridor[j]
						points = append(points, points[len(points)-1])
						refs = append(refs, corridor[j])
						links = append(links, false)
					}
				}
				refs[len(refs)-1] = next
			}
		}
	}
	if len(points) == 1 {
		points = append(points, points[0])
		refs = append(refs, refs[0])
		links = append(links, false)
	}
	truncated = status&detour.DT_BUFFER_TOO_SMALL != 0
	if !truncated {
		points, refs, links, err = pf.refineSurface(ctx, points, refs, links)
	}
	return
}

// ValidateSurface checks polygon containment, portal adjacency and vertical support
// for the exact segments that will be followed. A 2D ray alone cannot verify a floor.
func (pf *PathFinder) validateSurface(ctx context.Context, r *PathResult) error {
	if len(r.Points) != len(r.PointPolys) {
		return fmt.Errorf("surface references missing")
	}

	allowed := map[detour.DtPolyRef]bool{}
	for _, p := range r.Corridor {
		allowed[p] = true
	}
	for i, p := range r.Points {
		if err := ctx.Err(); err != nil {
			return err
		}
		ref := r.PointPolys[i]
		if !pf.query.IsValidPolyRef(ref, pf.filter) {
			return fmt.Errorf("invalid surface at point %d", i)
		}
		if i < len(r.Transitions) && r.Transitions[i] {
			return fmt.Errorf("unsupported off-mesh transition at segment %d", i)
		}
		if i == 0 {
			continue
		}
		a := r.Points[i-1]

		// Each generated segment lies in one convex polygon. Verify containment
		// on that polygon and adjacency at crossings, avoiding the port's raycast
		// t=0 precision failure at shared edges. This also rejects removed corners.
		if ref != r.PointPolys[i-1] && !pf.adjacent(r.PointPolys[i-1], ref) {
			return fmt.Errorf("disconnected surface at segment %d", i)
		}
		if !allowed[ref] {
			return fmt.Errorf("segment %d leaves corridor", i)
		}
		// Check actual support at both ends and the middle on the segment's polygon.
		for _, t := range []float32{0, 0.5, 1} {
			v := []float32{a.Y + t*(p.Y-a.Y), a.Z + t*(p.Z-a.Z), a.X + t*(p.X-a.X)}
			h, known := pf.surfaceHeight(r.PointPolys[i-1], v)
			if !known || math.Abs(float64(h-v[1])) > 0.4 {
				return fmt.Errorf("floor discontinuity at segment %d (t=%g known=%v height=%g sample=%v from=%+v to=%+v)", i, t, known, h, v, a, p)
			}
		}
	}
	return nil
}
func (s *PathFindingService) ValidateSurfaceContext(ctx context.Context, m uint32, r *PathResult) error {
	pf, err := s.acquirePathFinderContext(ctx, m)
	if err != nil {
		return err
	}
	defer s.releasePathFinder(m, pf)
	if err := lockContext(ctx, pf.meshMu.TryRLock); err != nil {
		return err
	}
	defer pf.meshMu.RUnlock()
	return pf.validateSurface(ctx, r)
}

// Detail-triangle projection uses double precision and a two-centimetre edge
// tolerance. The Go port's float32 barycentric test can reject a shared edge at
// large world coordinates. This fallback stays on the SAME polygon.
func (pf *PathFinder) surfaceHeight(ref detour.DtPolyRef, p []float32) (float32, bool) {
	var h float32

	var tile *detour.DtMeshTile
	var poly *detour.DtPoly
	if detour.DtStatusFailed(pf.navMesh.GetTileAndPolyByRef(ref, &tile, &poly)) {
		return 0, false
	}
	if poly.GetType() == detour.DT_POLYTYPE_OFFMESH_CONNECTION {
		return 0, false
	}
	detail := tile.DetailMeshes[pf.navMesh.DecodePolyIdPoly(ref)]
	best := 0.02 * 0.02
	found := false
	for j := 0; j < int(detail.TriCount); j++ {
		tri := tile.DetailTris[(int(detail.TriBase)+j)*4:]
		var v [3][]float32
		for k := 0; k < 3; k++ {
			if tri[k] < poly.VertCount {
				v[k] = tile.Verts[int(poly.Verts[tri[k]])*3:]
			} else {
				v[k] = tile.DetailVerts[(detail.VertBase+uint32(tri[k]-poly.VertCount))*3:]
			}
		}
		a, b, c := v[0], v[1], v[2]
		ux, uz := float64(b[0]-a[0]), float64(b[2]-a[2])
		vx, vz := float64(c[0]-a[0]), float64(c[2]-a[2])
		px, pz := float64(p[0]-a[0]), float64(p[2]-a[2])
		den := ux*vz - uz*vx
		if math.Abs(den) > 1e-12 {
			u, v := (px*vz-pz*vx)/den, (ux*pz-uz*px)/den
			if u >= 0 && v >= 0 && u+v <= 1 {
				return float32(float64(a[1]) + u*float64(b[1]-a[1]) + v*float64(c[1]-a[1])), true
			}
		}
		for k := 0; k < 3; k++ {
			a, b := v[k], v[(k+1)%3]
			dx, dz := float64(b[0]-a[0]), float64(b[2]-a[2])
			l := dx*dx + dz*dz
			if l == 0 {
				continue
			}
			t := max(0.0, min(1.0, (float64(p[0]-a[0])*dx+float64(p[2]-a[2])*dz)/l))
			ex, ez := float64(p[0]-a[0])-t*dx, float64(p[2]-a[2])-t*dz
			d := ex*ex + ez*ez
			if d <= best {
				best = d
				h = float32(float64(a[1]) + t*float64(b[1]-a[1]))
				found = true
			}
		}
	}
	return h, found
}

func (pf *PathFinder) adjacent(a, b detour.DtPolyRef) bool {
	var tile *detour.DtMeshTile
	var poly *detour.DtPoly
	if detour.DtStatusFailed(pf.navMesh.GetTileAndPolyByRef(a, &tile, &poly)) {
		return false
	}
	for i := poly.FirstLink; i != detour.DT_NULL_LINK; i = tile.Links[i].Next {
		if tile.Links[i].Ref == b {
			return true
		}
	}
	return false
}

// Add samples at changes in the detail surface, not only at a uniform spacing.
func (pf *PathFinder) refineSurface(ctx context.Context, pts []Point3D, polys []detour.DtPolyRef, trans []bool) (out []Point3D, refs []detour.DtPolyRef, links []bool, err error) {
	out = append(out, pts[0])
	refs = append(refs, polys[0])
	links = append(links, false)
	var segment func(Point3D, Point3D, detour.DtPolyRef, detour.DtPolyRef, bool, int) error
	segment = func(a, b Point3D, ref, endRef detour.DtPolyRef, link bool, depth int) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(out) >= maxPointPathLength {
			return ErrQueryLimit
		}
		mid := Point3D{(a.X + b.X) / 2, (a.Y + b.Y) / 2, (a.Z + b.Z) / 2}
		h, ok := pf.surfaceHeight(ref, []float32{mid.Y, mid.Z, mid.X})
		if !link && ok && depth < 8 && math.Hypot(float64(a.X-b.X), float64(a.Y-b.Y)) > 0.02 && math.Abs(float64(mid.Z-h)) > 0.1 {
			mid.Z = h
			if err := segment(a, mid, ref, ref, false, depth+1); err != nil {
				return err
			}
			return segment(mid, b, ref, endRef, false, depth+1)
		}
		out = append(out, b)
		refs = append(refs, endRef)
		links = append(links, link)
		return nil
	}
	for i := 1; i < len(pts); i++ {
		if err = segment(pts[i-1], pts[i], polys[i-1], polys[i], trans[i], 0); err != nil {
			return
		}
	}
	return
}

// centeredCorridor is a bounded alternative to the shortest funnel path. Portal
// interior points avoid hugging boundaries when a body's width clips a corner.
func (pf *PathFinder) centeredCorridor(ctx context.Context, r *PathResult, fraction float32) (*PathResult, error) {
	if len(r.Corridor) < 2 {
		return nil, fmt.Errorf("no alternate corridor")
	}
	n := len(r.Corridor) + 1
	corners := make([]float32, n*3)
	refs := make([]detour.DtPolyRef, n)
	flags := make([]detour.DtStraightPathFlags, n)
	a, b := r.Points[0], r.Points[len(r.Points)-1]
	copy(corners, []float32{a.Y, a.Z, a.X})
	copy(corners[(n-1)*3:], []float32{b.Y, b.Z, b.X})
	refs[0] = r.Corridor[0]
	for i := 0; i < len(r.Corridor)-1; i++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var tile *detour.DtMeshTile
		var poly *detour.DtPoly
		if detour.DtStatusFailed(pf.navMesh.GetTileAndPolyByRef(r.Corridor[i], &tile, &poly)) || poly.GetType() != detour.DT_POLYTYPE_GROUND {
			return nil, fmt.Errorf("unsupported corridor transition")
		}
		found := false
		for j := poly.FirstLink; j != detour.DT_NULL_LINK; j = tile.Links[j].Next {
			link := tile.Links[j]
			if link.Ref != r.Corridor[i+1] {
				continue
			}
			if int(link.Edge) >= int(poly.VertCount) {
				return nil, fmt.Errorf("invalid portal")
			}
			va := tile.Verts[int(poly.Verts[link.Edge])*3:]
			vb := tile.Verts[int(poly.Verts[(int(link.Edge)+1)%int(poly.VertCount)])*3:]
			f := fraction
			if link.Side != 0xff {
				f = (float32(link.Bmin) + fraction*float32(int(link.Bmax)-int(link.Bmin))) / 255
			}
			for k := 0; k < 3; k++ {
				corners[(i+1)*3+k] = va[k] + f*(vb[k]-va[k])
			}
			refs[i+1] = r.Corridor[i+1]
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("disconnected portal")
		}
	}
	pts, polys, links, short, err := pf.sampleSurfaceCorners(ctx, r.Corridor, corners, flags, refs, n, detour.DT_SUCCESS)
	if err != nil {
		return nil, err
	}
	if short {
		return nil, ErrQueryLimit
	}
	out := r.Clone()
	out.Points, out.PointPolys, out.Transitions = pts, polys, links
	return out, pf.validateSurface(ctx, out)
}
func (s *PathFindingService) CenteredPathContext(ctx context.Context, m uint32, r *PathResult, fraction float32) (*PathResult, error) {
	pf, err := s.acquirePathFinderContext(ctx, m)
	if err != nil {
		return nil, err
	}
	defer s.releasePathFinder(m, pf)
	if err := lockContext(ctx, pf.meshMu.TryRLock); err != nil {
		return nil, err
	}
	defer pf.meshMu.RUnlock()
	return pf.centeredCorridor(ctx, r, fraction)
}
