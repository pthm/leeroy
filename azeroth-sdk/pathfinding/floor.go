package pathfinding

import "github.com/o0olele/detour-go/detour"

// FloorTriangles is the walkable detail triangles of a map's navmesh in world coordinates: the floor plan of a map
// with no terrain to draw (an instance), for the viewer. With from, only the floor connected to it (an instance's
// entrance): the navmesh also covers the rock shell over an instance, which nobody can reach.
func (m *MMapManager) FloorTriangles(mapID uint32, from *Point3D) ([][3]Point3D, error) {
	if err := m.EnsureAllTilesLoaded(mapID); err != nil {
		return nil, err
	}
	md, err := m.getOrLoadMapData(mapID)
	if err != nil {
		return nil, err
	}
	var keep map[detour.DtPolyRef]bool
	if from != nil {
		q, err := m.CreateQuery(mapID)
		if err != nil {
			return nil, err
		}
		filter := detour.DtAllocDtQueryFilter()
		filter.SetIncludeFlags(0x01 | 0x08 | 0x02)
		var start detour.DtPolyRef
		var pt [3]float32
		md.mu.RLock()
		q.FindNearestPoly([]float32{from.Y, from.Z, from.X}, []float32{3, 6, 3}, filter, &start, pt[:])
		md.mu.RUnlock()
		if start != 0 {
			keep = map[detour.DtPolyRef]bool{start: true}
		}
	}
	md.mu.RLock()
	defer md.mu.RUnlock()
	nm := md.navMesh
	if keep != nil { // flood the polygon links out from the start
		queue := make([]detour.DtPolyRef, 0, len(keep))
		for ref := range keep {
			queue = append(queue, ref)
		}
		for len(queue) > 0 {
			ref := queue[0]
			queue = queue[1:]
			var t *detour.DtMeshTile
			var p *detour.DtPoly
			if detour.DtStatusFailed(nm.GetTileAndPolyByRef(ref, &t, &p)) {
				continue
			}
			for l := p.FirstLink; l != detour.DT_NULL_LINK; l = t.Links[l].Next {
				if n := t.Links[l].Ref; n != 0 && !keep[n] {
					keep[n] = true
					queue = append(queue, n)
				}
			}
		}
	}
	var out [][3]Point3D
	for i := 0; i < int(nm.GetMaxTiles()); i++ {
		t := nm.GetTile(i)
		if t == nil || t.Header == nil {
			continue
		}
		base := nm.GetPolyRefBase(t)
		out = tileTriangles(t, out, func(p int) bool { return keep == nil || keep[base|detour.DtPolyRef(p)] })
	}
	return out, nil
}

// tileTriangles appends a navmesh tile's walkable detail triangles (world coordinates) for the polygons keep allows.
func tileTriangles(t *detour.DtMeshTile, out [][3]Point3D, keep func(p int) bool) [][3]Point3D {
	for p := 0; p < int(t.Header.PolyCount); p++ {
		poly := &t.Polys[p]
		if poly.GetType() == detour.DT_POLYTYPE_OFFMESH_CONNECTION || !keep(p) {
			continue
		}
		d := t.DetailMeshes[p]
		for j := 0; j < int(d.TriCount); j++ {
			tri := t.DetailTris[(int(d.TriBase)+j)*4:]
			var w [3]Point3D
			for k := 0; k < 3; k++ {
				var v []float32
				if tri[k] < poly.VertCount {
					v = t.Verts[int(poly.Verts[tri[k]])*3:]
				} else {
					v = t.DetailVerts[(d.VertBase+uint32(tri[k]-poly.VertCount))*3:]
				}
				w[k] = Point3D{X: v[2], Y: v[0], Z: v[1]} // recast is {y, z, x}
			}
			out = append(out, w)
		}
	}
	return out
}

// SquareTriangles is the walkable detail triangles (world coordinates) of the navmesh over grid square gx, gy of a
// continent, loading its tile: its streets, floors and stairs, a building's storeys, a cave. For the viewer.
func (m *MMapManager) SquareTriangles(mapID uint32, gx, gy int) ([][3]Point3D, error) {
	if err := m.EnsureTileLoaded(mapID, int32(gx), int32(gy)); err != nil {
		return nil, err
	}
	md, err := m.getOrLoadMapData(mapID)
	if err != nil {
		return nil, err
	}
	md.mu.RLock()
	defer md.mu.RUnlock()
	nm := md.navMesh
	// the square's box in recast's axes ({y, z, x}): a tile is drawn if it overlaps it
	xMax, yMax := float32(32-gx)*sizeOfGrids, float32(32-gy)*sizeOfGrids
	var out [][3]Point3D
	for i := 0; i < int(nm.GetMaxTiles()); i++ {
		t := nm.GetTile(i)
		if t == nil || t.Header == nil {
			continue
		}
		lo, hi := t.Header.Bmin, t.Header.Bmax
		if hi[0] < yMax-sizeOfGrids+1 || lo[0] > yMax-1 || hi[2] < xMax-sizeOfGrids+1 || lo[2] > xMax-1 {
			continue
		}
		out = tileTriangles(t, out, func(int) bool { return true })
	}
	return out, nil
}
