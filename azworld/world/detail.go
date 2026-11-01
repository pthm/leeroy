package world

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/pthm/leeroy/azeroth-sdk/clientdata"
	"github.com/pthm/leeroy/azeroth-sdk/pathfinding"
)

// Detail tiles: a continent's map finer than its minimap (256 px per 533-yard square, ~2 yd a pixel). The minimap has
// the right colours, buildings, water and baked shading but no detail; the ground textures the game paints the terrain
// with (each chunk's texture layers and alpha maps, from the client's ADT files) have the detail. A detail tile is the
// minimap, upscaled smoothly, plus the texture render's fine detail: the render minus itself blurred to the
// minimap's resolution. Where the two disagree a lot (a building or water the textures don't have) less detail is
// added, so roofs and water stay clean.

// DetailVersion changes when detail tiles do (2: with the models drawn), so browsers don't keep old ones.
const DetailVersion = 2

// MaxDetail is the finest detail level: a square cut into 2^4 x 2^4 sub-squares, 0.13 yd a pixel.
const MaxDetail = 4

// texRepeat: each ground texture repeats 8 times across a chunk (33.3 yd), every 4.2 yd.
const texRepeat = 8

var client = sync.OnceValue(func() *clientdata.Client {
	dir := os.Getenv("WOW_CLIENT")
	if dir == "" { // ponytail: the install the repo's docs use; set WOW_CLIENT for another
		dir = filepath.Join(os.Getenv("HOME"), "Downloads", "ChromieCraft_3.3.5a")
	}
	c, err := clientdata.OpenClient(dir)
	if err != nil {
		return nil
	}
	return c
})

// HasDetail reports whether detail tiles can be drawn for map m: a continent, with the game client and its minimap.
func HasDetail(m uint32) bool {
	_, ok := clientdata.Maps[int(m)]
	return ok && HasClientData() && client() != nil
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
			t = &groundTex{mips: []*image.RGBA{img}}
			for img.Rect.Dx() > 4 {
				img = halve(img)
				t.mips = append(t.mips, img)
			}
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
				k := 0.0
				if have[i] {
					g, b := ground[i], blur[i]
					d := math.Abs(mm[0]-b[0]) + math.Abs(mm[1]-b[1]) + math.Abs(mm[2]-b[2])
					k = math.Max(0, math.Min(1, 1-(d-60)/80)) // what the textures don't have (buildings, water): less detail
					mm = [3]float64{mm[0] + k*(g[0]-b[0]), mm[1] + k*(g[1]-b[1]), mm[2] + k*(g[2]-b[2])}
				}
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

// shadowLen is how far a model's shadow reaches, yd: the sun is 45 degrees up, so a wall casts its height, up to this.
const shadowLen = 10.0

// drawModels draws the buildings, trees, rocks and fences over a detail tile from their models (the server's
// collision geometry, data/vmaps), seen from above: each pixel the highest surface, lit by its face's slope from the
// north-west (the minimap's light), outlined where it drops to a wall, casting a short shadow on what's below. The
// colour is still the minimap's (a roof's tiles, a canopy's green): the models carry no textures.
func drawModels(out *image.RGBA, p *paint, xN, yW, res float64) {
	m := p.m
	n := out.Rect.Dx()
	zm := int(math.Ceil(shadowLen / res)) // margin: shadows cast from beyond the tile's edge
	Z := n + 2*zm
	top := make([]float32, Z*Z)
	light := make([]float32, Z*Z)
	face := make([]int32, Z*Z) // which triangle is on top, to colour it as one
	nFaces := int32(0)
	for i := range top {
		top[i] = float32(math.Inf(-1))
	}
	x0, y0 := xN+float64(zm)*res, yW+float64(zm)*res                                   // the buffer's north-west corner
	sun := [3]float64{1 / math.Sqrt(3.96), 1 / math.Sqrt(3.96), 1.4 / math.Sqrt(3.96)} // towards the sun: north-west, up
	any := false
	vmaps().Triangles(m, float32(x0-float64(Z)*res), float32(x0), float32(y0-float64(Z)*res), float32(y0), func(a, b, c pathfinding.Vector3) {
		// pixel space: row from x (north at 0), column from y (west at 0)
		var R, C, H [3]float64
		for k, v := range [3]pathfinding.Vector3{a, b, c} {
			R[k], C[k], H[k] = (x0-float64(v.X))/res, (y0-float64(v.Y))/res, float64(v.Z)
		}
		r0, r1 := max(0, int(math.Floor(min(R[0], R[1], R[2])))), min(Z-1, int(math.Ceil(max(R[0], R[1], R[2]))))
		c0, c1 := max(0, int(math.Floor(min(C[0], C[1], C[2])))), min(Z-1, int(math.Ceil(max(C[0], C[1], C[2]))))
		d := (C[1]-C[2])*(R[0]-R[2]) + (R[2]-R[1])*(C[0]-C[2])
		if r0 > r1 || c0 > c1 || math.Abs(d) < 1e-9 {
			return // off the buffer, or seen edge-on (a wall)
		}
		// the face's normal, up, in world axes (x north, y west, z up)
		ux, uy, uz := float64(b.X-a.X), float64(b.Y-a.Y), float64(b.Z-a.Z)
		vx, vy, vz := float64(c.X-a.X), float64(c.Y-a.Y), float64(c.Z-a.Z)
		nx, ny, nz := uy*vz-uz*vy, uz*vx-ux*vz, ux*vy-uy*vx
		if nz < 0 {
			nx, ny, nz = -nx, -ny, -nz
		}
		l := float32(math.Max(0, (nx*sun[0]+ny*sun[1]+nz*sun[2])/math.Sqrt(nx*nx+ny*ny+nz*nz)))
		nFaces++
		for r := r0; r <= r1; r++ {
			for c := c0; c <= c1; c++ {
				pr, pc := float64(r)+0.5, float64(c)+0.5
				l1 := ((C[1]-C[2])*(pr-R[2]) + (R[2]-R[1])*(pc-C[2])) / d
				l2 := ((C[2]-C[0])*(pr-R[2]) + (R[0]-R[2])*(pc-C[2])) / d
				l3 := 1 - l1 - l2
				if l1 < 0 || l2 < 0 || l3 < 0 {
					continue
				}
				if z := float32(l1*H[0] + l2*H[1] + l3*H[2]); z > top[r*Z+c] {
					top[r*Z+c], light[r*Z+c], face[r*Z+c] = z, l, nFaces
					any = true
				}
			}
		}
	})
	if !any {
		return
	}
	// what's below the models: the terrain, or a model's surface
	// sampled every yard or so and interpolated: the terrain's own grid is ~4 yd
	step := max(1, int(math.Round(1/res)))
	G := Z/step + 2
	coarse := make([]float64, G*G)
	for r := 0; r < G; r++ {
		for c := 0; c < G; c++ {
			coarse[r*G+c], _, _ = Sample(m, x0-float64(r*step)*res, y0-float64(c*step)*res)
		}
	}
	ground := make([]float32, Z*Z)
	for r := 0; r < Z; r++ {
		fr := (float64(r) + 0.5) / float64(step)
		r0, wr := int(fr), fr-math.Floor(fr)
		for c := 0; c < Z; c++ {
			fc := (float64(c) + 0.5) / float64(step)
			c0, wc := int(fc), fc-math.Floor(fc)
			ground[r*Z+c] = float32((1-wr)*((1-wc)*coarse[r0*G+c0]+wc*coarse[r0*G+c0+1]) + wr*((1-wc)*coarse[(r0+1)*G+c0]+wc*coarse[(r0+1)*G+c0+1]))
		}
	}
	surface := func(i int) float32 { return max(top[i], ground[i]) }
	// A model's colour is the minimap's, which is ~2 yd a pixel: blurred across a roof's edge. Each small face (a roof
	// pane, a tower's cap) takes the mean of the minimap over it instead, so colours change where the faces do; big
	// faces (a city's streets, a courtyard) keep the minimap's own variation.
	type acc struct {
		sum [3]float64
		n   int
	}
	faces := make([]acc, nFaces+1)
	mini := make([][3]float64, n*n)
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			i := (r+zm)*Z + c + zm
			if top[i] > ground[i]+0.3 {
				mm, _ := p.minimapAt(xN-(float64(r)+0.5)*res, yW-(float64(c)+0.5)*res)
				mini[r*n+c] = mm
				a := &faces[face[i]]
				a.sum[0], a.sum[1], a.sum[2], a.n = a.sum[0]+mm[0], a.sum[1]+mm[1], a.sum[2]+mm[2], a.n+1
			}
		}
	}
	maxFace := int(150 / (res * res)) // pixels in 150 square yards
	steps := int(shadowLen / res)
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			i := (r+zm)*Z + c + zm
			model := top[i] > ground[i]+0.3
			f := 1.0
			if model {
				f = 0.55 + 0.7*float64(light[i])                      // flat roofs about as the minimap has them
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
				if top[j] > h+float32(float64(k)*res)+0.3 {
					f *= 0.68
					break
				}
			}
			if f != 1 {
				p := out.Pix[r*out.Stride+c*4:]
				if model { // a model's own colour, without the ground's texture
					mm := mini[r*n+c]
					if a := faces[face[i]]; a.n > 0 && a.n <= maxFace {
						for k := range mm {
							mm[k] = 0.8*a.sum[k]/float64(a.n) + 0.2*mm[k]
						}
					}
					p[0], p[1], p[2] = uint8(mm[0]), uint8(mm[1]), uint8(mm[2])
				}
				for k := 0; k < 3; k++ {
					p[k] = uint8(math.Min(255, float64(p[k])*f))
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
		go func(r0, r1 int) { defer wg.Done(); fn(r0, r1) }(i*n/k, (i+1)*n/k)
	}
	wg.Wait()
}

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
