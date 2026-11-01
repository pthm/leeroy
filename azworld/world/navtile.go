package world

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"sync"

	"github.com/pthm/leeroy/azeroth-sdk/pathfinding"
)

// Navmesh tiles: a continent's walkable surfaces (the server's navmesh: streets, floors, stairs, a building's storeys,
// caves) drawn like the instances' floor plans, for the viewer's navmesh overlay. A pixel is the lowest walkable
// surface over it (a building's ground floor, not its roof), coloured by its height above the terrain: the open ground
// neutral and faint, raised floors warm, cellars and caves cool; covered floors (another storey a few yards over them:
// indoors) opaque, so a building's rooms show; walls and drops darkened. The storeys above are outlined over it.

var (
	navMu   sync.Mutex
	navTris = map[[3]int][][3]pathfinding.Point3D{} // by map and square; ponytail: kept for the session
)

func squareTris(m uint32, gx, gy int) [][3]pathfinding.Point3D {
	k := [3]int{int(m), gx, gy}
	navMu.Lock()
	t, ok := navTris[k]
	navMu.Unlock()
	if ok {
		return t
	}
	t, _ = mmaps().SquareTriangles(m, gx, gy)
	navMu.Lock()
	navTris[k] = t
	navMu.Unlock()
	return t
}

// NavTile is a 256x256 PNG of grid square gx, gy of continent m, or of its sub-square (sx, sy) when cut into
// 2^level x 2^level (level 0: the whole square), transparent where nothing is walkable; nil past level 4.
func NavTile(m uint32, gx, gy, level, sx, sy int) []byte {
	if level < 0 || level > MaxDetail {
		return nil
	}
	const n = 256
	sub := GridSize / float64(int(1)<<level)
	res := sub / n
	xN, yW := GridSize*float64(32-gx)-float64(sx)*sub, GridSize*float64(32-gy)-float64(sy)*sub
	z := make([]float32, n*n)  // the lowest walkable surface
	zh := make([]float32, n*n) // the highest
	for i := range z {
		z[i], zh[i] = float32(math.Inf(1)), float32(math.Inf(-1))
	}
	any := false
	for _, t := range squareTris(m, gx, gy) {
		// pixel space: column from y (west left), row from x (north up)
		var c, r [3]float64
		for k, v := range t {
			c[k], r[k] = (yW-float64(v.Y))/res, (xN-float64(v.X))/res
		}
		c0, c1 := max(0, int(math.Floor(min(c[0], c[1], c[2])))), min(n-1, int(math.Ceil(max(c[0], c[1], c[2]))))
		r0, r1 := max(0, int(math.Floor(min(r[0], r[1], r[2])))), min(n-1, int(math.Ceil(max(r[0], r[1], r[2]))))
		den := (r[1]-r[2])*(c[0]-c[2]) + (c[2]-c[1])*(r[0]-r[2])
		if c0 > c1 || r0 > r1 || math.Abs(den) < 1e-9 {
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
				z[y*n+x], zh[y*n+x] = min(z[y*n+x], h), max(zh[y*n+x], h)
				any = true
			}
		}
	}
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	if any {
		ground := terrainHeights(m, xN, yW, res, n)
		edge := max(1.0, res) // a yard's step, or a pixel's at coarse levels
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				h := z[y*n+x]
				if math.IsInf(float64(h), 1) {
					continue
				}
				covered := zh[y*n+x] > h+2.5
				rel := float64(h - ground[y*n+x])
				if math.IsNaN(rel) {
					rel = 0
				}
				c := floorTint(0.5 + rel/24) // 12 yd down: slate; the ground: grey; 12 yd up: sandstone
				a := uint8(230)
				if math.Abs(rel) < 1.5 && !covered {
					a = 110 // the open ground: faint, so the floors that aren't stand out
				}
				for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					nx, ny := x+d[0], y+d[1]
					if nx < 0 || ny < 0 || nx >= n || ny >= n {
						continue
					}
					if o := z[ny*n+nx]; math.IsInf(float64(o), 1) || math.Abs(float64(o-h)) > edge {
						c, a = [3]float64{c[0] * 0.4, c[1] * 0.4, c[2] * 0.4}, 240
						break
					}
				}
				// a storey above: its edges outlined in pale sandstone
				if top := zh[y*n+x]; covered {
					for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
						nx, ny := x+d[0], y+d[1]
						if nx >= 0 && ny >= 0 && nx < n && ny < n && math.Abs(float64(zh[ny*n+nx]-top)) > edge {
							c, a = [3]float64{240, 220, 170}, 255
							break
						}
					}
				}
				img.SetNRGBA(x, y, color.NRGBA{uint8(c[0]), uint8(c[1]), uint8(c[2]), a})
			}
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}
