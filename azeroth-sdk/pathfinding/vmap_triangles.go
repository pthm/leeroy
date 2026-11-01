package pathfinding

import (
	"context"
	"sync"
)

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
		for _, t := range tree.worldTriangles(ctx, mi) {
			if max(t[0].X, t[1].X, t[2].X) < xMin || min(t[0].X, t[1].X, t[2].X) > xMax ||
				max(t[0].Y, t[1].Y, t[2].Y) < yMin || min(t[0].Y, t[1].Y, t[2].Y) > yMax {
				continue
			}
			fn(t[0], t[1], t[2])
		}
	}
	return true
}

// worldTris: each model instance's triangles in world coordinates, made once. ponytail: never freed; a session's
// map browsing holds tens of MB, a sweep of a whole continent would hold its every model.
var worldTris sync.Map // *ModelInstance -> [][3]Vector3

func (tree *StaticMapTree) worldTriangles(ctx context.Context, mi *ModelInstance) [][3]Vector3 {
	if v, ok := worldTris.Load(mi); ok {
		return v.([][3]Vector3)
	}
	var out [][3]Vector3
	if model, ok := tree.modelForInstanceContext(ctx, mi); ok && model != nil {
		tree.dataMu.RLock()
		pos, inv, scale := mi.iPos, mi.iInvRot, mi.spawn.iScale
		tree.dataMu.RUnlock()
		if scale == 0 {
			scale = 1
		}
		world := toWorld(pos, inv, scale)
		for gi := range model.groupModels {
			g := &model.groupModels[gi]
			wv := make([]Vector3, len(g.Vertices))
			for i, v := range g.Vertices {
				wv[i] = world(v)
			}
			for _, t := range g.Mesh {
				if int(t.idx0) < len(wv) && int(t.idx1) < len(wv) && int(t.idx2) < len(wv) {
					out = append(out, [3]Vector3{wv[t.idx0], wv[t.idx1], wv[t.idx2]})
				}
			}
		}
	}
	worldTris.Store(mi, out) // a model that won't load is stored empty: not read again
	return out
}

// toWorld maps model coordinates to the world for an instance at pos (world), rotated by inv's transpose, scaled:
// the inverse of intersectModelHeight's transform (x and y flipped by the server's internal coordinates).
func toWorld(pos Vector3, inv [3][3]float32, scale float32) func(Vector3) Vector3 {
	return func(v Vector3) Vector3 {
		p := Vector3{
			(inv[0][0]*v.X + inv[1][0]*v.Y + inv[2][0]*v.Z) * scale,
			(inv[0][1]*v.X + inv[1][1]*v.Y + inv[2][1]*v.Z) * scale,
			(inv[0][2]*v.X + inv[1][2]*v.Y + inv[2][2]*v.Z) * scale,
		}
		return Vector3{pos.X - p.X, pos.Y - p.Y, pos.Z + p.Z}
	}
}

// PlacementWorld maps a model's own coordinates to the world for a model placed by an ADT (MDDF or MODF): pos and rot
// as the ADT stores them (its x and z are the world's y and x from the map's far corner, y is height; rot in
// degrees), the same transform the server's vmaps use for that placement.
func PlacementWorld(pos, rot [3]float32, scale float32) func(Vector3) Vector3 {
	if scale == 0 {
		scale = 1
	}
	return toWorld(Vector3{VMAP_mid - pos[2], VMAP_mid - pos[0], pos[1]}, inverseRotationMatrix(Vector3{rot[0], rot[1], rot[2]}), scale)
}
