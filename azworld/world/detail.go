package world

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"math"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"

	"github.com/pthm/leeroy/azeroth-sdk/clientdata"
	"github.com/pthm/leeroy/azeroth-sdk/pathfinding"
)

// Detail tiles: a continent's map finer than its minimap (256 px per 533-yard square, ~2 yd a pixel). The minimap has
// the right colours, buildings, water and baked shading but no detail; the ground textures the game paints the terrain
// with (each chunk's texture layers and alpha maps, from the client's ADT files) have the detail. A detail tile is the
// minimap, upscaled smoothly, plus the texture render's fine detail (the render minus itself blurred to the minimap's
// resolution, matched to the minimap's brightness), lit by the terrain's own slopes at full resolution in place of the
// minimap's baked lighting; the buildings, trees and the rest are drawn over it from their models (drawModels).

// DetailVersion changes when detail tiles do (2: the models drawn, 3: textured, 4: detail everywhere, sharp terrain
// lighting, 5: water, 6: no placeholder geometry, 7: none hidden at rest), so browsers don't keep old ones.
const DetailVersion = 7

// MaxDetail is the finest detail level: a square cut into 2^4 x 2^4 sub-squares, 0.13 yd a pixel.
const MaxDetail = 4

// texRepeat: each ground texture repeats 8 times across a chunk (33.3 yd), every 4.2 yd.
const texRepeat = 8

// client is the game files the detail tiles read, extracted into the data dir (data/client) by
// `clientdata -client <game dir> extract data`.
func client() clientdata.Dir { return clientdata.Dir(filepath.Join(DataDir(), "client")) }

// HasDetail reports whether detail tiles can be drawn for map m: a continent, with its game files and minimap
// extracted.
func HasDetail(m uint32) bool {
	_, ok := clientdata.Maps[int(m)]
	return ok && HasClientData() && client().Has()
}

type groundTex struct {
	mips []*image.RGBA // full size, then halved down to 4 px
	avg  [3]float64
}

var (
	detailMu sync.Mutex
	adts     = map[[3]int]*clientdata.ADT{} // ponytail: never evicted; ~3 MB each, a session's few hundred squares fit
	adtTried = map[[3]int]bool{}
	bigAlpha = map[uint32]bool{}
	texCache = map[string]*groundTex{}
	miniRGBA = map[[3]int]*image.RGBA{}
)

func adtOf(m uint32, gx, gy int) *clientdata.ADT {
	k := [3]int{int(m), gx, gy}
	detailMu.Lock()
	defer detailMu.Unlock()
	if adtTried[k] {
		return adts[k]
	}
	adtTried[k] = true
	c, dir := client(), clientdata.Maps[int(m)]
	if _, ok := bigAlpha[m]; !ok {
		if wdt, err := c.Read(fmt.Sprintf(`World\Maps\%s\%s.wdt`, dir, dir)); err == nil && len(wdt) >= 24 {
			bigAlpha[m] = (uint32(wdt[20])|uint32(wdt[21])<<8)&0x84 != 0
		}
	}
	b, err := c.Read(fmt.Sprintf(`World\Maps\%s\%s_%d_%d.adt`, dir, dir, gy, gx)) // <dir>_<col>_<row>: col from y, row from x
	if err != nil {
		return nil
	}
	a, err := clientdata.ParseADT(b, bigAlpha[m])
	if err != nil {
		return nil
	}
	adts[k] = a
	return a
}

func texOf(name string) *groundTex {
	detailMu.Lock()
	t, ok := texCache[name]
	detailMu.Unlock()
	if ok {
		return t
	}
	if b, err := client().Read(name); err == nil {
		if img, err := clientdata.DecodeBLP(b); err == nil {
			t = &groundTex{mips: mipsOf(img)}
			p := t.mips[len(t.mips)-1].Pix
			for i := 0; i < len(p); i += 4 {
				t.avg[0], t.avg[1], t.avg[2] = t.avg[0]+float64(p[i]), t.avg[1]+float64(p[i+1]), t.avg[2]+float64(p[i+2])
			}
			n := float64(len(p) / 4)
			t.avg = [3]float64{t.avg[0] / n, t.avg[1] / n, t.avg[2] / n}
		}
	}
	detailMu.Lock()
	texCache[name] = t
	detailMu.Unlock()
	return t
}

// mipsOf is an image and its halvings, down to 4 px on its short side: a long thin texture (32x2) stops at a pixel
// high, never halving to nothing (a 4x0 level made a negative index and crashed the viewer).
func mipsOf(img *image.RGBA) []*image.RGBA {
	mips := []*image.RGBA{img}
	for img.Rect.Dx() > 4 && img.Rect.Dy() > 4 {
		img = halve(img)
		mips = append(mips, img)
	}
	return mips
}

// halve is an image at half size, each pixel the mean of four.
func halve(src *image.RGBA) *image.RGBA {
	w, h := src.Rect.Dx()/2, src.Rect.Dy()/2
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			for c := 0; c < 4; c++ {
				s := int(src.Pix[(2*y)*src.Stride+2*x*4+c]) + int(src.Pix[(2*y)*src.Stride+(2*x+1)*4+c]) +
					int(src.Pix[(2*y+1)*src.Stride+2*x*4+c]) + int(src.Pix[(2*y+1)*src.Stride+(2*x+1)*4+c])
				dst.Pix[y*dst.Stride+x*4+c] = uint8(s / 4)
			}
		}
	}
	return dst
}

func minimapRGBA(m uint32, gx, gy int) *image.RGBA {
	k := [3]int{int(m), gx, gy}
	detailMu.Lock()
	img, ok := miniRGBA[k]
	detailMu.Unlock()
	if ok {
		return img
	}
	if b := MinimapTile(m, gx, gy); b != nil {
		if src, _, err := image.Decode(bytes.NewReader(b)); err == nil {
			img = image.NewRGBA(src.Bounds())
			for y := src.Bounds().Min.Y; y < src.Bounds().Max.Y; y++ {
				for x := src.Bounds().Min.X; x < src.Bounds().Max.X; x++ {
					img.Set(x, y, src.At(x, y))
				}
			}
		}
	}
	detailMu.Lock()
	miniRGBA[k] = img
	detailMu.Unlock()
	return img
}

// paint is one tile's render: the squares, ground textures and minimap tiles it uses, looked up once each (the
// shared caches lock; per pixel that was most of a tile's time).
type paint struct {
	m      uint32
	adts   map[[2]int]*paintADT
	minis  map[[2]int]*image.RGBA
	lastA  [2]int
	last   *paintADT
	lastM  [2]int
	lastMI *image.RGBA
}

type paintADT struct {
	a    *clientdata.ADT
	texs []*groundTex // by the ADT's texture index
}

func newPaint(m uint32) *paint {
	return &paint{m: m, adts: map[[2]int]*paintADT{}, minis: map[[2]int]*image.RGBA{}, lastA: [2]int{-1, -1}, lastM: [2]int{-1, -1}}
}

func (p *paint) adt(gx, gy int) *paintADT {
	k := [2]int{gx, gy}
	if k == p.lastA {
		return p.last
	}
	pa, ok := p.adts[k]
	if !ok {
		if a := adtOf(p.m, gx, gy); a != nil {
			pa = &paintADT{a: a, texs: make([]*groundTex, len(a.Textures))}
			for i, n := range a.Textures {
				pa.texs[i] = texOf(n)
			}
		}
		p.adts[k] = pa
	}
	p.lastA, p.last = k, pa
	return pa
}

func (p *paint) mini(gx, gy int) *image.RGBA {
	k := [2]int{gx, gy}
	if k == p.lastM {
		return p.lastMI
	}
	img, ok := p.minis[k]
	if !ok {
		img = minimapRGBA(p.m, gx, gy)
		p.minis[k] = img
	}
	p.lastM, p.lastMI = k, img
	return img
}

// minimapAt is the minimap's colour at a world point, bilinear between its pixels; ok false off the minimap.
func (p *paint) minimapAt(x, y float64) (rgb [3]float64, ok bool) {
	fr, fc := (GridSize*32-x)/GridSize*256-0.5, (GridSize*32-y)/GridSize*256-0.5 // pixel coordinates across the whole map
	r0, c0 := int(math.Floor(fr)), int(math.Floor(fc))
	for k := 0; k < 4; k++ {
		r, c := r0+k/2, c0+k%2
		if r < 0 || c < 0 {
			return rgb, false
		}
		img := p.mini(r/256, c/256)
		if img == nil {
			return rgb, false
		}
		w := (1 - math.Abs(fr-float64(r))) * (1 - math.Abs(fc-float64(c)))
		px := img.Pix[(r%256)*img.Stride+(c%256)*4:]
		rgb[0], rgb[1], rgb[2] = rgb[0]+w*float64(px[0]), rgb[1]+w*float64(px[1]), rgb[2]+w*float64(px[2])
	}
	return rgb, true
}

// alphaAt is a layer's alpha map at a point of its chunk (fx, fy in 0..1: down and right), bilinear.
func alphaAt(a []uint8, fx, fy float64) float64 {
	px, py := fy*64-0.5, fx*64-0.5
	x0, y0 := int(math.Floor(px)), int(math.Floor(py))
	v := 0.0
	for k := 0; k < 4; k++ {
		x, y := min(63, max(0, x0+k%2)), min(63, max(0, y0+k/2))
		w := (1 - math.Abs(px-float64(x0+k%2))) * (1 - math.Abs(py-float64(y0+k/2)))
		v += w * float64(a[y*64+x])
	}
	return v / 255
}

// groundAt is the ground textures' colour at a world point (texel per yd: the mip level's), ok false off the ADTs.
func (p *paint) groundAt(x, y float64, mip int) (rgb [3]float64, ok bool) {
	gx, gy := int(math.Floor(32-x/GridSize)), int(math.Floor(32-y/GridSize))
	pa := p.adt(gx, gy)
	if pa == nil {
		return rgb, false
	}
	a := pa.a
	cs := GridSize / 16
	lx, ly := GridSize*float64(32-gx)-x, GridSize*float64(32-gy)-y // yards into the square from its north-west corner
	cr, cc := min(15, int(lx/cs)), min(15, int(ly/cs))
	ch := &a.Chunks[cr*16+cc]
	fx, fy := (lx-float64(cr)*cs)/cs, (ly-float64(cc)*cs)/cs
	rest := 1.0
	for l := len(ch.Layers) - 1; l >= 0; l-- { // each layer covers what's below it
		w := rest
		if l > 0 {
			w = alphaAt(ch.Alpha[l-1], fx, fy) * rest
		}
		rest -= w
		if w <= 0 || int(ch.Layers[l]) >= len(a.Textures) {
			continue
		}
		t := pa.texs[ch.Layers[l]]
		if t == nil {
			continue
		}
		img := t.mips[min(mip, len(t.mips)-1)]
		tw, th := img.Rect.Dx(), img.Rect.Dy()
		u, v := fy*texRepeat, fx*texRepeat
		u, v = u-math.Floor(u), v-math.Floor(v)
		px := img.Pix[min(th-1, int(v*float64(th)))*img.Stride+min(tw-1, int(u*float64(tw)))*4:]
		rgb[0], rgb[1], rgb[2] = rgb[0]+w*float64(px[0]), rgb[1]+w*float64(px[1]), rgb[2]+w*float64(px[2])
	}
	return rgb, true
}

// DetailTile is a 256x256 JPEG of sub-square (sx, sy) of grid square gx, gy of continent m, the square cut into
// 2^level x 2^level (sx from x, north first; sy from y, west first), or nil (no client, not a continent, no minimap).
func DetailTile(m uint32, gx, gy, level, sx, sy int) []byte {
	if level < 1 || level > MaxDetail || !HasDetail(m) || minimapRGBA(m, gx, gy) == nil {
		return nil
	}
	const n = 256
	sub := GridSize / float64(int(1)<<level)
	res := sub / n
	rad := 1 << level // the blur: a minimap pixel either side
	W := n + 2*rad
	xN, yW := GridSize*float64(32-gx)-float64(sx)*sub, GridSize*float64(32-gy)-float64(sy)*sub
	// the mip whose texels are about a pixel: full size is 256 texels per 4.2 yd
	mip := max(0, int(math.Round(math.Log2(256/(GridSize/16/texRepeat)*res))))
	p := newPaint(m)
	ground := make([][3]float64, W*W)
	have := make([]bool, W*W)
	bands(W, func(r0, r1 int) {
		bp := newPaint(m) // each band its own lookups: they aren't shared between goroutines
		for r := r0; r < r1; r++ {
			for c := 0; c < W; c++ {
				x, y := xN-(float64(r-rad)+0.5)*res, yW-(float64(c-rad)+0.5)*res
				ground[r*W+c], have[r*W+c] = bp.groundAt(x, y, mip)
			}
		}
	})
	blur := boxBlur(ground, W, rad)
	sharp, soft := terrainShade(m, xN+float64(rad)*res, yW+float64(rad)*res, res, W, rad)
	out := image.NewRGBA(image.Rect(0, 0, n, n))
	cl := func(v float64) uint8 { return uint8(math.Max(0, math.Min(255, math.Round(v)))) }
	bands(n, func(r0, r1 int) {
		bp := newPaint(m)
		for r := r0; r < r1; r++ {
			for c := 0; c < n; c++ {
				x, y := xN-(float64(r)+0.5)*res, yW-(float64(c)+0.5)*res
				i := (r+rad)*W + c + rad
				mm, ok := bp.minimapAt(x, y)
				if !ok { // past the minimap's edge (its neighbour has none): the textures alone
					mm = blur[i]
				}
				if have[i] {
					g, b := ground[i], blur[i]
					// The minimap has the game's lighting baked in (a slope facing away from the light, south-east, is
					// darker than the textures): the detail added is matched to its brightness. Buildings and the like,
					// which the minimap has and the textures don't, are drawn over all this from their models.
					s := math.Max(0.25, math.Min(2, lum(mm)/math.Max(1, lum(b))))
					mm = [3]float64{mm[0] + s*(g[0]-b[0]), mm[1] + s*(g[1]-b[1]), mm[2] + s*(g[2]-b[2])}
				}
				// the terrain's own lighting, sharp, in place of the minimap's (a minimap pixel wide: on a slope, where
				// lighting is most of what you see, ridges and gullies came out as smears)
				f := math.Max(0.5, math.Min(1.6, sharp[i]/soft[i]))
				mm = [3]float64{mm[0] * f, mm[1] * f, mm[2] * f}
				o := out.Pix[r*out.Stride+c*4:]
				o[0], o[1], o[2], o[3] = cl(mm[0]), cl(mm[1]), cl(mm[2]), 255
			}
		}
	})
	drawModels(out, p, xN, yW, res)
	var buf bytes.Buffer
	jpeg.Encode(&buf, out, &jpeg.Options{Quality: 85})
	return buf.Bytes()
}

var vmaps = sync.OnceValue(func() *pathfinding.VMapManager { return pathfinding.NewVMapManager(filepath.Join(DataDir(), "vmaps")) })

// leafOpacity is how opaque foliage is at most: Elwynn's canopy trees are 20-40 yd across and would hide the roads.
const leafOpacity = 0.6

// shadowLen is how far a model's shadow reaches, yd: the sun is 45 degrees up, so a wall casts its height, up to this.
const shadowLen = 10.0

// drawModels draws the client's models over a detail tile, textured, seen from above: every building, wall and
// bridge (WMO), tree, fence, lamp and crate (M2), and the doodads the buildings place. Each pixel is the highest
// surface's texture (pixels a leaf or fence texture leaves transparent let what's below show), lit by its face's
// slope from the north-west (the minimap's light), outlined where it drops to a wall, casting a short shadow on what's
// below.
func drawModels(out *image.RGBA, p *paint, xN, yW, res float64) {
	m := p.m
	n := out.Rect.Dx()
	zm := int(math.Ceil(shadowLen / res)) // margin: shadows cast from beyond the tile's edge
	Z := n + 2*zm
	top := make([]float32, Z*Z)
	light := make([]float32, Z*Z)
	col := make([][3]uint8, Z*Z)
	// foliage (alpha-tested textures: leaves, and fences' and banners' gaps) is a layer of its own over the rest, drawn
	// partly see-through so the roads and roofs under a forest's canopy still show
	leafTop := make([]float32, Z*Z)
	leafCol := make([][4]uint8, Z*Z)
	for i := range top {
		top[i], leafTop[i] = float32(math.Inf(-1)), float32(math.Inf(-1))
	}
	x0, y0 := xN+float64(zm)*res, yW+float64(zm)*res                                   // the buffer's north-west corner
	sun := [3]float64{1 / math.Sqrt(3.96), 1 / math.Sqrt(3.96), 1.4 / math.Sqrt(3.96)} // towards the sun: north-west, up
	// the triangles over the buffer, set up once: where they fall in pixels, their light and texture level
	type prep struct {
		R, C, H, U, V  [3]float64
		r0, r1, c0, c1 int
		d              float64
		l              float32
		img            *image.RGBA
		alpha          bool
	}
	var tris []prep
	bx0, bx1, by0, by1 := x0-float64(Z)*res, x0, y0-float64(Z)*res, y0
	var water []waterTri
	modelsIn(m, bx0, bx1, by0, by1, func(pl *placed) {
		for k, w := range pl.water {
			if max(w[0][0], w[1][0], w[2][0]) >= float32(bx0) && min(w[0][0], w[1][0], w[2][0]) <= float32(bx1) &&
				max(w[0][1], w[1][1], w[2][1]) >= float32(by0) && min(w[0][1], w[1][1], w[2][1]) <= float32(by1) {
				water = append(water, waterTri{w, pl.waterType[k]})
			}
		}
		pl.trisIn(bx0, bx1, by0, by1, func(t int32) {
			tri := pl.tris[t]
			mat := pl.mats[pl.triMat[t]]
			if mat.tex == nil {
				return
			}
			// pixel space: row from x (north at 0), column from y (west at 0)
			var q prep
			for k, vi := range tri {
				v := pl.verts[vi]
				q.R[k], q.C[k], q.H[k] = (x0-float64(v[0]))/res, (y0-float64(v[1]))/res, float64(v[2])
				q.U[k], q.V[k] = float64(pl.uv[vi][0]), float64(pl.uv[vi][1])
			}
			R, C := q.R, q.C
			if math.IsNaN(R[0] + R[1] + R[2] + C[0] + C[1] + C[2] + q.H[0] + q.H[1] + q.H[2]) {
				return // a broken vertex
			}
			q.r0, q.r1 = max(0, int(math.Floor(min(R[0], R[1], R[2])))), min(Z-1, int(math.Ceil(max(R[0], R[1], R[2]))))
			q.c0, q.c1 = max(0, int(math.Floor(min(C[0], C[1], C[2])))), min(Z-1, int(math.Ceil(max(C[0], C[1], C[2]))))
			q.d = (C[1]-C[2])*(R[0]-R[2]) + (R[2]-R[1])*(C[0]-C[2])
			if q.r0 > q.r1 || q.c0 > q.c1 || math.Abs(q.d) < 1e-9 {
				return // off the buffer, or seen edge-on (a wall)
			}
			// the face's normal, up, in world axes (x north, y west, z up)
			a, b, c := pl.verts[tri[0]], pl.verts[tri[1]], pl.verts[tri[2]]
			ux, uy, uz := float64(b[0]-a[0]), float64(b[1]-a[1]), float64(b[2]-a[2])
			vx, vy, vz := float64(c[0]-a[0]), float64(c[1]-a[1]), float64(c[2]-a[2])
			nx, ny, nz := uy*vz-uz*vy, uz*vx-ux*vz, ux*vy-uy*vx
			if nz < 0 {
				nx, ny, nz = -nx, -ny, -nz
			}
			q.l = float32(math.Max(0, (nx*sun[0]+ny*sun[1]+nz*sun[2])/math.Sqrt(nx*nx+ny*ny+nz*nz)))
			// the mip whose texels are about a pixel here: texture area over screen area
			full := mat.tex.mips[0]
			uvArea := math.Abs((q.U[1]-q.U[0])*(q.V[2]-q.V[0])-(q.U[2]-q.U[0])*(q.V[1]-q.V[0])) * float64(full.Rect.Dx()*full.Rect.Dy())
			q.img = mat.tex.mips[min(len(mat.tex.mips)-1, max(0, int(math.Round(0.5*math.Log2(math.Max(1, uvArea/math.Abs(q.d)))))))]
			q.alpha = mat.alphaTest
			tris = append(tris, q)
		})
	})
	waterZ, waterT := waterLayer(m, water, xN, yW, res, n)
	if len(tris) == 0 && waterZ == nil {
		return
	}
	// in bands of rows, in parallel: each band draws every triangle's part in its rows
	bands(Z, func(br0, br1 int) {
		for qi := range tris {
			q := &tris[qi]
			r0, r1 := max(br0, q.r0), min(br1-1, q.r1)
			if r0 > r1 {
				continue
			}
			R, C, H, d := q.R, q.C, q.H, q.d
			tw, th := q.img.Rect.Dx(), q.img.Rect.Dy()
			for r := r0; r <= r1; r++ {
				for c := q.c0; c <= q.c1; c++ {
					pr, pc := float64(r)+0.5, float64(c)+0.5
					l1 := ((C[1]-C[2])*(pr-R[2]) + (R[2]-R[1])*(pc-C[2])) / d
					l2 := ((C[2]-C[0])*(pr-R[2]) + (R[0]-R[2])*(pc-C[2])) / d
					l3 := 1 - l1 - l2
					if l1 < 0 || l2 < 0 || l3 < 0 {
						continue
					}
					i := r*Z + c
					z := float32(l1*H[0] + l2*H[1] + l3*H[2])
					if z <= top[i] || q.alpha && z <= leafTop[i] {
						continue
					}
					u, v := l1*q.U[0]+l2*q.U[1]+l3*q.U[2], l1*q.V[0]+l2*q.V[1]+l3*q.V[2]
					if math.IsNaN(u+v+float64(z)) || math.IsInf(u+v, 0) {
						continue // a model with broken texture coordinates or vertices
					}
					u, v = u-math.Floor(u), v-math.Floor(v)
					px := q.img.Pix[max(0, min(th-1, int(v*float64(th))))*q.img.Stride+max(0, min(tw-1, int(u*float64(tw))))*4:]
					if q.alpha {
						if px[3] < 64 {
							continue // a gap between leaves
						}
						lf := 0.55 + 0.7*float64(q.l)
						leafTop[i] = z
						leafCol[i] = [4]uint8{uint8(math.Min(255, float64(px[0])*lf)), uint8(math.Min(255, float64(px[1])*lf)), uint8(math.Min(255, float64(px[2])*lf)), px[3]}
						continue
					}
					top[i], light[i], col[i] = z, q.l, [3]uint8{px[0], px[1], px[2]}
				}
			}
		}
	})
	ground := terrainHeights(m, x0, y0, res, Z) // what's below the models
	surface := func(i int) float32 { return max(top[i], ground[i]) }
	caster := func(i int) float32 { return max(top[i], leafTop[i]) } // what casts a shadow: foliage too
	steps := int(shadowLen / res)
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			i := (r+zm)*Z + c + zm
			model := top[i] > ground[i]-0.3 // at or over the ground: a city's street is a model too
			f := 1.0
			if model {
				f = 0.55 + 0.7*float64(light[i])
				for _, j := range []int{i - 1, i + 1, i - Z, i + Z} { // a wall drops away beside it: outline
					if surface(i)-surface(j) > 1.5 {
						f *= 0.6
						break
					}
				}
			}
			// shadow: something between this point and the sun (north-west, 45 degrees up)
			h := surface(i)
			for k := 1; k <= steps; k++ {
				o := int(float64(k) / math.Sqrt2)
				j := (r+zm-o)*Z + c + zm - o
				if caster(j) > h+float32(float64(k)*res)+0.3 {
					f *= 0.68
					break
				}
			}
			px := out.Pix[r*out.Stride+c*4:]
			if model {
				px[0], px[1], px[2] = col[i][0], col[i][1], col[i][2]
			}
			if f != 1 {
				for k := 0; k < 3; k++ {
					px[k] = uint8(math.Min(255, float64(px[k])*f))
				}
			}
			if wi := r*n + c; waterZ != nil && waterZ[wi] > surface(i)+0.02 { // under water: the water over it
				blendWater(px, waterT[wi], float64(waterZ[wi]-surface(i)))
			}
			if leafTop[i] > surface(i) && (waterZ == nil || leafTop[i] > waterZ[r*n+c]) { // foliage over it, at most 60% opaque
				a := float64(leafCol[i][3]) / 255 * leafOpacity
				for k := 0; k < 3; k++ {
					px[k] = uint8(float64(px[k])*(1-a) + float64(leafCol[i][k])*a)
				}
			}
		}
	}
}

// bands runs fn over [0, n) in a band of rows per core, in parallel.
func bands(n int, fn func(r0, r1 int)) {
	k := min(n, runtime.NumCPU())
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(r0, r1 int) {
			defer wg.Done()
			defer func() { // a panic here would end the viewer (the HTTP server only recovers its own goroutine): log it
				if e := recover(); e != nil {
					log.Printf("detail tile band %d-%d: %v\n%s", r0, r1, e, debug.Stack())
				}
			}()
			fn(r0, r1)
		}(i*n/k, (i+1)*n/k)
	}
	wg.Wait()
}

// terrainHeights is the terrain's height at each pixel of a Z x Z buffer from (x0, y0) going south and east, sampled
// every yard or so and interpolated (the terrain's own grid is ~2 yd: corners and middles of 4-yd squares).
func terrainHeights(m uint32, x0, y0, res float64, Z int) []float32 {
	step := max(1, int(math.Round(1/res)))
	G := Z/step + 2
	coarse := make([]float64, G*G)
	for r := 0; r < G; r++ {
		for c := 0; c < G; c++ {
			coarse[r*G+c], _, _ = Sample(m, x0-float64(r*step)*res, y0-float64(c*step)*res)
		}
	}
	h := make([]float32, Z*Z)
	for r := 0; r < Z; r++ {
		fr := (float64(r) + 0.5) / float64(step)
		r0, wr := int(fr), fr-math.Floor(fr)
		for c := 0; c < Z; c++ {
			fc := (float64(c) + 0.5) / float64(step)
			c0, wc := int(fc), fc-math.Floor(fc)
			h[r*Z+c] = float32((1-wr)*((1-wc)*coarse[r0*G+c0]+wc*coarse[r0*G+c0+1]) + wr*((1-wc)*coarse[(r0+1)*G+c0]+wc*coarse[(r0+1)*G+c0+1]))
		}
	}
	return h
}

// terrainShade is how lit the terrain is at each pixel of a W x W buffer: its slope against the sun (the north-west,
// 45 degrees up), 1 on flat ground. sharp is per pixel; blurred, over a minimap pixel (rad), as the minimap has it.
func terrainShade(m uint32, x0, y0, res float64, W, rad int) (sharp, blurred []float64) {
	h := terrainHeights(m, x0, y0, res, W)
	sun := [3]float64{1 / math.Sqrt(3.96), 1 / math.Sqrt(3.96), 1.4 / math.Sqrt(3.96)}
	flat := sun[2]
	px := make([][3]float64, W*W)
	sharp = make([]float64, W*W)
	d := max(1, int(math.Round(1/res))) // differences over a yard or two either side
	for r := 0; r < W; r++ {
		for c := 0; c < W; c++ {
			hn, hs := h[max(0, r-d)*W+c], h[min(W-1, r+d)*W+c]
			hw, he := h[r*W+max(0, c-d)], h[r*W+min(W-1, c+d)]
			span := 2 * float64(d) * res
			dx, dy := float64(hn-hs)/span, float64(hw-he)/span // rise towards the north, towards the west
			l := (-dx*sun[0] - dy*sun[1] + sun[2]) / math.Sqrt(dx*dx+dy*dy+1)
			if math.IsNaN(l) {
				l = flat
			}
			v := math.Max(0.2, l) / flat
			sharp[r*W+c] = v
			px[r*W+c] = [3]float64{v, v, v}
		}
	}
	bl := boxBlur(px, W, rad)
	blurred = make([]float64, W*W)
	for i, v := range bl {
		blurred[i] = v[0]
	}
	return sharp, blurred
}

type waterTri struct {
	v  [3][3]float32
	ty int8
}

// waterLayer is the liquid surface's height at each pixel of an n x n tile (-inf: none) and its type: the terrain's
// water (lakes, rivers, the sea: the server's map files, every yard or so) and the WMOs' (Stormwind's canals). nil if
// there's none.
func waterLayer(m uint32, wmo []waterTri, xN, yW, res float64, n int) ([]float32, []int8) {
	z := make([]float32, n*n)
	ty := make([]int8, n*n)
	for i := range z {
		z[i] = float32(math.Inf(-1))
	}
	any := false
	step := max(1, int(math.Round(1/res)))
	for r := 0; r < n; r += step {
		for c := 0; c < n; c += step {
			_, w, _ := Sample(m, xN-(float64(r)+float64(step)/2)*res, yW-(float64(c)+float64(step)/2)*res)
			if math.IsNaN(w) {
				continue
			}
			any = true
			for rr := r; rr < min(n, r+step); rr++ {
				for cc := c; cc < min(n, c+step); cc++ {
					z[rr*n+cc] = float32(w)
				}
			}
		}
	}
	for _, t := range wmo {
		var R, C, H [3]float64
		for k, v := range t.v {
			R[k], C[k], H[k] = (xN-float64(v[0]))/res, (yW-float64(v[1]))/res, float64(v[2])
		}
		r0, r1 := max(0, int(math.Floor(min(R[0], R[1], R[2])))), min(n-1, int(math.Ceil(max(R[0], R[1], R[2]))))
		c0, c1 := max(0, int(math.Floor(min(C[0], C[1], C[2])))), min(n-1, int(math.Ceil(max(C[0], C[1], C[2]))))
		d := (C[1]-C[2])*(R[0]-R[2]) + (R[2]-R[1])*(C[0]-C[2])
		if r0 > r1 || c0 > c1 || math.Abs(d) < 1e-9 {
			continue
		}
		for r := r0; r <= r1; r++ {
			for c := c0; c <= c1; c++ {
				pr, pc := float64(r)+0.5, float64(c)+0.5
				l1 := ((C[1]-C[2])*(pr-R[2]) + (R[2]-R[1])*(pc-C[2])) / d
				l2 := ((C[2]-C[0])*(pr-R[2]) + (R[0]-R[2])*(pc-C[2])) / d
				l3 := 1 - l1 - l2
				if l1 < -1e-6 || l2 < -1e-6 || l3 < -1e-6 {
					continue
				}
				if h := float32(l1*H[0] + l2*H[1] + l3*H[2]); h > z[r*n+c] {
					z[r*n+c], ty[r*n+c] = h, t.ty
					any = true
				}
			}
		}
	}
	if !any {
		return nil, nil
	}
	return z, ty
}

// waterColor is each liquid's colour and how opaque it is at most: water, ocean, magma, slime.
var waterColor = [4]struct {
	rgb [3]float64
	max float64
}{{[3]float64{38, 82, 104}, 0.85}, {[3]float64{24, 58, 86}, 0.9}, {[3]float64{235, 100, 25}, 0.97}, {[3]float64{80, 150, 40}, 0.9}}

// blendWater lays a liquid over a pixel, more opaque the deeper it is (the bed shows through the shallows), with a
// pale edge where it laps the shore.
func blendWater(px []uint8, ty int8, depth float64) {
	w := waterColor[max(0, min(3, int(ty)))]
	a := math.Min(w.max, 0.4+0.11*depth)
	rgb := w.rgb
	if depth < 0.35 && ty < 2 { // the shore's edge
		e := 0.3 * (1 - depth/0.35)
		rgb = [3]float64{rgb[0]*(1-e) + 205*e, rgb[1]*(1-e) + 220*e, rgb[2]*(1-e) + 225*e}
	}
	for k := 0; k < 3; k++ {
		px[k] = uint8(float64(px[k])*(1-a) + rgb[k]*a)
	}
}

func lum(c [3]float64) float64 { return 0.3*c[0] + 0.59*c[1] + 0.11*c[2] }

// boxBlur is a W x W image blurred by a (2r+1)-wide box, horizontally then vertically (running sums).
func boxBlur(a [][3]float64, W, r int) [][3]float64 {
	pass := func(src [][3]float64, at func(i, k int) int) [][3]float64 {
		dst := make([][3]float64, len(src))
		for i := 0; i < W; i++ {
			var s [3]float64
			n := 0
			for k := -r; k < W+r; k++ {
				if k+r < W { // entering
					v := src[at(i, k+r)]
					s[0], s[1], s[2], n = s[0]+v[0], s[1]+v[1], s[2]+v[2], n+1
				}
				if k-r-1 >= 0 { // leaving
					v := src[at(i, k-r-1)]
					s[0], s[1], s[2], n = s[0]-v[0], s[1]-v[1], s[2]-v[2], n-1
				}
				if k >= 0 && k < W {
					f := 1 / float64(n)
					dst[at(i, k)] = [3]float64{s[0] * f, s[1] * f, s[2] * f}
				}
			}
		}
		return dst
	}
	h := pass(a, func(row, k int) int { return row*W + k })
	return pass(h, func(col, k int) int { return k*W + col })
}
