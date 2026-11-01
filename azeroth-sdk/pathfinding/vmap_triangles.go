package pathfinding

import "context"

// Triangles calls fn with every triangle, in world coordinates, of the models (buildings, trees, rocks, fences: the
// server's collision geometry) whose bounds overlap the box on map mapID, loading the vmap tiles it covers. False if
// the map has no vmaps. For drawing them (the viewer's map), not for queries: it walks every loaded model instance.
func (vm *VMapManager) Triangles(mapID uint32, xMin, xMax, yMin, yMax float32, fn func(a, b, c Vector3)) bool {
	ctx := context.Background()
	tree, err := vm.getTreeContext(ctx, mapID)
	if err != nil || tree == nil {
		return false
	}
	gx0, gy0 := gameToGridCoords(xMax, yMax)
	gx1, gy1 := gameToGridCoords(xMin, yMin)
	for gx := max(0, gx0); gx <= min(63, gx1); gx++ {
		for gy := max(0, gy0); gy <= min(63, gy1); gy++ {
			if tree.loadMapTileContext(ctx, uint32(gx), uint32(gy)) != nil {
				return false
			}
		}
	}
	tree.dataMu.RLock()
	var hits []*ModelInstance
	for _, mi := range tree.treeValues {
		if mi == nil || mi.spawn.name == "" {
			continue
		}
		if tree.isTiled && mi.spawn.Flags&0x4 != 0 {
			lo, hi := mi.spawn.iBound[0], mi.spawn.iBound[1]
			if hi.X < xMin || lo.X > xMax || hi.Y < yMin || lo.Y > yMax {
				continue
			}
		}
		hits = append(hits, mi)
	}
	tree.dataMu.RUnlock()
	for _, mi := range hits {
		model, ok := tree.modelForInstanceContext(ctx, mi)
		if !ok || model == nil {
			continue
		}
		tree.dataMu.RLock()
		pos, inv, scale := mi.iPos, mi.iInvRot, mi.spawn.iScale
		tree.dataMu.RUnlock()
		if scale == 0 {
			scale = 1
		}
		// model space -> world: the inverse of intersectModelHeight's transform (rotation transposed, x and y
		// flipped by the server's internal coordinates)
		world := func(v Vector3) Vector3 {
			p := Vector3{
				(inv[0][0]*v.X + inv[1][0]*v.Y + inv[2][0]*v.Z) * scale,
				(inv[0][1]*v.X + inv[1][1]*v.Y + inv[2][1]*v.Z) * scale,
				(inv[0][2]*v.X + inv[1][2]*v.Y + inv[2][2]*v.Z) * scale,
			}
			return Vector3{pos.X - p.X, pos.Y - p.Y, pos.Z + p.Z}
		}
		for gi := range model.groupModels {
			g := &model.groupModels[gi]
			wv := make([]Vector3, len(g.Vertices))
			for i, v := range g.Vertices {
				wv[i] = world(v)
			}
			for _, t := range g.Mesh {
				if int(t.idx0) < len(wv) && int(t.idx1) < len(wv) && int(t.idx2) < len(wv) {
					fn(wv[t.idx0], wv[t.idx1], wv[t.idx2])
				}
			}
		}
	}
	return true
}
