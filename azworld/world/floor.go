package world

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"path/filepath"
	"sync"

	"github.com/pthm/leeroy/azeroth-sdk/pathfinding"
)

// Instances have no terrain or grid minimap to draw (their maps are buildings), so the viewer draws their navmesh:
// the walkable floor, shaded by height, its edges (walls, drops) darkened.

type floorPlan struct {
	tris   [][3]pathfinding.Point3D
	lo, hi float32
}

var (
	mmaps  = sync.OnceValue(func() *pathfinding.MMapManager { return pathfinding.NewMMapManager(filepath.Join(DataDir(), "mmaps")) })
	floors sync.Map // map id -> func() *floorPlan
)

func floorOf(m uint32) *floorPlan {
	f, _ := floors.LoadOrStore(m, sync.OnceValue(func() *floorPlan {
		tris, err := mmaps().FloorTriangles(m, entrance(m))
		if err != nil || len(tris) == 0 {
			return nil
		}
		p := &floorPlan{tris: tris, lo: float32(math.Inf(1)), hi: float32(math.Inf(-1))}
		for _, t := range tris {
			for _, v := range t {
				p.lo, p.hi = min(p.lo, v.Z), max(p.hi, v.Z)
			}
		}
		return p
	}))
	return f.(func() *floorPlan)()
}

// entrance: where the instance's portal puts you in, or nil (then the whole navmesh is drawn)
func entrance(m uint32) *pathfinding.Point3D {
	rows, err := Rows(World(), "SELECT target_position_x, target_position_y, target_position_z FROM areatrigger_teleport WHERE target_map = ? LIMIT 1", m)
	if err != nil || len(rows) == 0 {
		return nil
	}
	return &pathfinding.Point3D{X: float32(atof(rows[0][0])), Y: float32(atof(rows[0][1])), Z: float32(atof(rows[0][2]))}
}

// Continent reports whether a map is one of the continents, drawn from terrain and minimap tiles; anything else
// (instances, battlegrounds) is drawn from its navmesh by FloorTile.
func Continent(m uint32) bool { return m == 0 || m == 1 || m == 530 || m == 571 }

// FloorTile draws grid square gx, gy of map m from its navmesh at res yards per pixel, north up and west left like
// the minimap tiles: walkable floor coloured by height (one scale per map, so tiles agree), the rest transparent.
// Where floors overlap the highest is drawn. nil if the map has no navmesh.
func FloorTile(m uint32, gx, gy int, res float64) []byte {
	p := floorOf(m)
	if p == nil {
		return nil
	}
	xMax, yMax := GridSize*float64(32-gx), GridSize*float64(32-gy)
	n := int(math.Ceil(GridSize / res))
	z := make([]float32, n*n)
	for i := range z {
		z[i] = float32(math.Inf(-1))
	}
	for _, t := range p.tris {
		// pixel space: column from y (west left), row from x (north up)
		var c, r [3]float64
		for k, v := range t {
			c[k], r[k] = (yMax-float64(v.Y))/res, (xMax-float64(v.X))/res
		}
		c0, c1 := max(0, int(math.Floor(min(c[0], c[1], c[2])))), min(n-1, int(math.Ceil(max(c[0], c[1], c[2]))))
		r0, r1 := max(0, int(math.Floor(min(r[0], r[1], r[2])))), min(n-1, int(math.Ceil(max(r[0], r[1], r[2]))))
		if c0 > c1 || r0 > r1 {
			continue
		}
		den := (r[1]-r[2])*(c[0]-c[2]) + (c[2]-c[1])*(r[0]-r[2])
		if math.Abs(den) < 1e-9 {
			continue
		}
		for y := r0; y <= r1; y++ {
			for x := c0; x <= c1; x++ {
				px, py := float64(x)+0.5, float64(y)+0.5
				a := ((r[1]-r[2])*(px-c[2]) + (c[2]-c[1])*(py-r[2])) / den
				b := ((r[2]-r[0])*(px-c[2]) + (c[0]-c[2])*(py-r[2])) / den
				if a < -0.01 || b < -0.01 || a+b > 1.01 {
					continue
				}
				h := float32(a)*t[0].Z + float32(b)*t[1].Z + float32(1-a-b)*t[2].Z
				if i := y*n + x; h > z[i] {
					z[i] = h
				}
			}
		}
	}
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	span := max(1, float64(p.hi-p.lo))
	drawn := false
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			h := z[y*n+x]
			if math.IsInf(float64(h), -1) {
				continue
			}
			drawn = true
			c := floorTint((float64(h) - float64(p.lo)) / span)
			// an edge: a neighbour that's empty or a floor over a yard away (a wall, a drop, a level above)
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := x+d[0], y+d[1]
				if nx < 0 || ny < 0 || nx >= n || ny >= n {
					continue
				}
				if o := z[ny*n+nx]; math.IsInf(float64(o), -1) || math.Abs(float64(o-h)) > 1 {
					c = [3]float64{c[0] * 0.45, c[1] * 0.45, c[2] * 0.45}
					break
				}
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(c[0]), uint8(c[1]), uint8(c[2]), 255})
		}
	}
	if !drawn {
		img = image.NewNRGBA(image.Rect(0, 0, 1, 1)) // nothing here: a transparent pixel, scaled up by the viewer
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// floorTint: deep floors slate blue, high ones sandstone
func floorTint(u float64) [3]float64 {
	u = math.Max(0, math.Min(1, u))
	lo, mid, hi := [3]float64{70, 88, 120}, [3]float64{130, 128, 118}, [3]float64{222, 196, 150}
	a, b, f := lo, mid, u*2
	if u > 0.5 {
		a, b, f = mid, hi, (u-0.5)*2
	}
	return [3]float64{a[0] + (b[0]-a[0])*f, a[1] + (b[1]-a[1])*f, a[2] + (b[2]-a[2])*f}
}
