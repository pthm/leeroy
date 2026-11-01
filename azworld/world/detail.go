package world

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/pthm/leeroy/azeroth-sdk/clientdata"
)

// Detail tiles: a continent's map finer than its minimap (256 px per 533-yard square, ~2 yd a pixel). The minimap has
// the right colours, buildings, water and baked shading but no detail; the ground textures the game paints the terrain
// with (each chunk's texture layers and alpha maps, from the client's ADT files) have the detail. A detail tile is the
// minimap, upscaled smoothly, plus the texture render's fine detail: the render minus itself blurred to the
// minimap's resolution. Where the two disagree a lot (a building or water the textures don't have) less detail is
// added, so roofs and water stay clean.

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

// minimapAt is the minimap's colour at a world point, bilinear between its pixels; ok false off the minimap.
func minimapAt(m uint32, x, y float64) (rgb [3]float64, ok bool) {
	fr, fc := (GridSize*32-x)/GridSize*256-0.5, (GridSize*32-y)/GridSize*256-0.5 // pixel coordinates across the whole map
	r0, c0 := int(math.Floor(fr)), int(math.Floor(fc))
	for k := 0; k < 4; k++ {
		r, c := r0+k/2, c0+k%2
		img := minimapRGBA(m, r/256, c/256)
		if img == nil || r < 0 || c < 0 {
			return rgb, false
		}
		w := (1 - math.Abs(fr-float64(r))) * (1 - math.Abs(fc-float64(c)))
		p := img.Pix[(r%256)*img.Stride+(c%256)*4:]
		rgb[0], rgb[1], rgb[2] = rgb[0]+w*float64(p[0]), rgb[1]+w*float64(p[1]), rgb[2]+w*float64(p[2])
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
func groundAt(m uint32, x, y float64, mip int) (rgb [3]float64, ok bool) {
	gx, gy := int(math.Floor(32-x/GridSize)), int(math.Floor(32-y/GridSize))
	a := adtOf(m, gx, gy)
	if a == nil {
		return rgb, false
	}
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
		t := texOf(a.Textures[ch.Layers[l]])
		if t == nil {
			continue
		}
		img := t.mips[min(mip, len(t.mips)-1)]
		tw, th := img.Rect.Dx(), img.Rect.Dy()
		u, v := math.Mod(fy*texRepeat, 1), math.Mod(fx*texRepeat, 1)
		p := img.Pix[min(th-1, int(v*float64(th)))*img.Stride+min(tw-1, int(u*float64(tw)))*4:]
		rgb[0], rgb[1], rgb[2] = rgb[0]+w*float64(p[0]), rgb[1]+w*float64(p[1]), rgb[2]+w*float64(p[2])
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
	ground := make([][3]float64, W*W)
	have := make([]bool, W*W)
	for r := 0; r < W; r++ {
		for c := 0; c < W; c++ {
			x, y := xN-(float64(r-rad)+0.5)*res, yW-(float64(c-rad)+0.5)*res
			ground[r*W+c], have[r*W+c] = groundAt(m, x, y, mip)
		}
	}
	blur := boxBlur(ground, W, rad)
	out := image.NewRGBA(image.Rect(0, 0, n, n))
	cl := func(v float64) uint8 { return uint8(math.Max(0, math.Min(255, math.Round(v)))) }
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			x, y := xN-(float64(r)+0.5)*res, yW-(float64(c)+0.5)*res
			i := (r+rad)*W + c + rad
			mm, ok := minimapAt(m, x, y)
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
	var buf bytes.Buffer
	jpeg.Encode(&buf, out, &jpeg.Options{Quality: 85})
	return buf.Bytes()
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
