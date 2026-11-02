package world

import (
	"math"
	"strings"
	"sync"

	"github.com/pthm/leeroy/azsdk/clientdata"
	"github.com/pthm/leeroy/azsdk/pathfinding"
)

// The client's models as placed on the map, for drawing them textured on the detail tiles: every WMO (buildings,
// walls, bridges) and M2 (trees, fences, lamps, crates) an ADT places, and the doodads each WMO places in its own
// doodad sets (a city's trees and statues, an inn's lanterns and barrels), with their vertices in world coordinates.

// placed is one model in the world: its triangles' corners in world coordinates, texture coordinates and materials.
type placed struct {
	xMin, xMax, yMin, yMax float32
	cells                  map[[2]int32][]int32 // triangles by the cellSize square their middle is in
	reach                  float32              // the most a triangle reaches from its middle, yd
	verts                  [][3]float32
	uv                     [][2]float32
	tris                   [][3]uint32
	triMat                 []int32
	mats                   []placedMat
	water                  [][3][3]float32 // a WMO's liquid surface, two triangles a tile, world coordinates
	waterType              []int8          // per water triangle: 0 water, 1 ocean, 2 magma, 3 slime
}

type placedMat struct {
	tex       *groundTex
	alphaTest bool
}

var (
	modelMu    sync.Mutex
	modelFiles = map[string]*clientdata.Model{} // by file name; nil if it wouldn't load
	placements sync.Map                         // unique id -> []*placed (a WMO: itself, then its doodads)
)

func modelFile(name string, wmo bool) *clientdata.Model {
	key := strings.ToLower(name)
	modelMu.Lock()
	m, ok := modelFiles[key]
	modelMu.Unlock()
	if ok {
		return m
	}
	var err error
	if wmo {
		m, err = clientdata.LoadWMO(client(), name)
	} else {
		m, err = clientdata.LoadM2(client(), name)
	}
	if err != nil {
		m = nil
	}
	modelMu.Lock()
	modelFiles[key] = m
	modelMu.Unlock()
	return m
}

// place is a model put into the world by transform tw, or nil.
func place(m *clientdata.Model, tw func(pathfinding.Vector3) pathfinding.Vector3) *placed {
	if m == nil || len(m.Tris) == 0 && len(m.Liquids) == 0 {
		return nil
	}
	p := &placed{uv: m.UV, tris: m.Tris, triMat: m.TriMat, xMin: math.MaxFloat32, yMin: math.MaxFloat32, xMax: -math.MaxFloat32, yMax: -math.MaxFloat32}
	p.verts = make([][3]float32, len(m.Verts))
	for i, v := range m.Verts {
		w := tw(pathfinding.Vector3{X: v[0], Y: v[1], Z: v[2]})
		p.verts[i] = [3]float32{w.X, w.Y, w.Z}
		p.xMin, p.xMax, p.yMin, p.yMax = min(p.xMin, w.X), max(p.xMax, w.X), min(p.yMin, w.Y), max(p.yMax, w.Y)
	}
	for _, mt := range m.Materials {
		p.mats = append(p.mats, placedMat{tex: texOf(mt.Texture), alphaTest: mt.AlphaTest})
	}
	for _, l := range m.Liquids { // a WMO's water: each tile's corners, moved into the world like its vertices
		at := func(x, y int) [3]float32 {
			w := tw(pathfinding.Vector3{X: l.Corner[0] + float32(x)*clientdata.LiquidTile, Y: l.Corner[1] + float32(y)*clientdata.LiquidTile, Z: l.Heights[y*l.XVerts+x]})
			return [3]float32{w.X, w.Y, w.Z}
		}
		for y := 0; y < l.YVerts-1; y++ {
			for x := 0; x < l.XVerts-1; x++ {
				ty := l.Tiles[y*(l.XVerts-1)+x]
				if ty < 0 {
					continue
				}
				a, b, c, d := at(x, y), at(x+1, y), at(x+1, y+1), at(x, y+1)
				p.water = append(p.water, [3][3]float32{a, b, c}, [3][3]float32{a, c, d})
				p.waterType = append(p.waterType, ty, ty)
				for _, v := range [4][3]float32{a, b, c, d} {
					p.xMin, p.xMax, p.yMin, p.yMax = min(p.xMin, v[0]), max(p.xMax, v[0]), min(p.yMin, v[1]), max(p.yMax, v[1])
				}
			}
		}
	}
	p.cells = map[[2]int32][]int32{}
	for t, tri := range m.Tris {
		a, b, c := p.verts[tri[0]], p.verts[tri[1]], p.verts[tri[2]]
		mx, my := (a[0]+b[0]+c[0])/3, (a[1]+b[1]+c[1])/3
		for _, v := range [3][3]float32{a, b, c} {
			p.reach = max(p.reach, float32(math.Abs(float64(v[0]-mx))), float32(math.Abs(float64(v[1]-my))))
		}
		k := cellOf(mx, my)
		p.cells[k] = append(p.cells[k], int32(t))
	}
	return p
}

// cellSize: a placed model's triangles are found by the square of this side their middle is in, so a tile of a city
// (one model, hundreds of thousands of triangles) looks at the few thousand near it.
const cellSize = 32

func cellOf(x, y float32) [2]int32 {
	return [2]int32{int32(math.Floor(float64(x / cellSize))), int32(math.Floor(float64(y / cellSize)))}
}

// trisIn calls fn with each triangle that may overlap the box.
func (p *placed) trisIn(xMin, xMax, yMin, yMax float64, fn func(t int32)) {
	r := float64(p.reach)
	lo, hi := cellOf(float32(xMin-r), float32(yMin-r)), cellOf(float32(xMax+r), float32(yMax+r))
	if int(hi[0]-lo[0]+1)*int(hi[1]-lo[1]+1) > 4*len(p.cells) { // a model with huge triangles: just walk its cells
		for _, ts := range p.cells {
			for _, t := range ts {
				fn(t)
			}
		}
		return
	}
	for cx := lo[0]; cx <= hi[0]; cx++ {
		for cy := lo[1]; cy <= hi[1]; cy++ {
			for _, t := range p.cells[[2]int32{cx, cy}] {
				fn(t)
			}
		}
	}
}

// placedOf is an ADT placement's models in the world, made once per unique id.
func placedOf(pl clientdata.Placement, wmo bool) []*placed {
	if v, ok := placements.Load(pl.UniqueID); ok {
		return v.([]*placed)
	}
	var out []*placed
	tw := pathfinding.PlacementWorld(pl.Pos, pl.Rot, pl.Scale)
	m := modelFile(pl.Name, wmo)
	if p := place(m, tw); p != nil {
		out = append(out, p)
	}
	if wmo && m != nil {
		for si, set := range m.Sets {
			if si != 0 && si != pl.DoodadSet {
				continue
			}
			for _, d := range m.Doodads[min(set.Start, len(m.Doodads)):min(set.Start+set.Count, len(m.Doodads))] {
				d := d
				// the doodad's own axes -> the WMO's (scale, rotate by its quaternion, move) -> the world
				dt := func(v pathfinding.Vector3) pathfinding.Vector3 {
					r := rotate(d.Rot, [3]float32{v.X * d.Scale, v.Y * d.Scale, v.Z * d.Scale})
					return tw(pathfinding.Vector3{X: d.Pos[0] + r[0], Y: d.Pos[1] + r[1], Z: d.Pos[2] + r[2]})
				}
				if p := place(modelFile(d.Name, false), dt); p != nil {
					out = append(out, p)
				}
			}
		}
	}
	placements.Store(pl.UniqueID, out) // ponytail: kept for the session, like the ADTs
	return out
}

// rotate is v rotated by the unit quaternion q (x, y, z, w).
func rotate(q [4]float32, v [3]float32) [3]float32 {
	x, y, z, w := q[0], q[1], q[2], q[3]
	// t = 2 q.xyz x v; v' = v + w t + q.xyz x t
	tx, ty, tz := 2*(y*v[2]-z*v[1]), 2*(z*v[0]-x*v[2]), 2*(x*v[1]-y*v[0])
	return [3]float32{v[0] + w*tx + (y*tz - z*ty), v[1] + w*ty + (z*tx - x*tz), v[2] + w*tz + (x*ty - y*tx)}
}

// modelsIn calls fn with every placed model whose footprint overlaps the box, each once.
func modelsIn(m uint32, xMin, xMax, yMin, yMax float64, fn func(*placed)) {
	seen := map[uint32]bool{}
	const reach = 60 // a model placed in a neighbouring square can reach this far into this one (a big tree's canopy)
	for gx := int(math.Floor(32 - (xMax+reach)/GridSize)); gx <= int(math.Floor(32-(xMin-reach)/GridSize)); gx++ {
		for gy := int(math.Floor(32 - (yMax+reach)/GridSize)); gy <= int(math.Floor(32-(yMin-reach)/GridSize)); gy++ {
			a := adtOf(m, gx, gy)
			if a == nil {
				continue
			}
			for k, list := range [2][]clientdata.Placement{a.WMOs, a.Models} {
				for _, pl := range list {
					if seen[pl.UniqueID] {
						continue
					}
					seen[pl.UniqueID] = true
					for _, p := range placedOf(pl, k == 0) {
						if float64(p.xMax) >= xMin && float64(p.xMin) <= xMax && float64(p.yMax) >= yMin && float64(p.yMin) <= yMax {
							fn(p)
						}
					}
				}
			}
		}
	}
}
