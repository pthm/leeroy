package world

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

var ramp = []struct {
	u float64
	c [3]float64
}{{0, [3]float64{86, 125, 70}}, {0.35, [3]float64{120, 150, 84}}, {0.6, [3]float64{158, 150, 104}},
	{0.8, [3]float64{140, 120, 96}}, {1, [3]float64{235, 232, 225}}}

func tint(u float64) [3]float64 {
	u = math.Max(0, math.Min(1, u))
	for k := 1; k < len(ramp); k++ {
		if u <= ramp[k].u {
			a, b, f := ramp[k-1].c, ramp[k].c, (u-ramp[k-1].u)/(ramp[k].u-ramp[k-1].u)
			return [3]float64{a[0] + (b[0]-a[0])*f, a[1] + (b[1]-a[1])*f, a[2] + (b[2]-a[2])*f}
		}
	}
	return ramp[len(ramp)-1].c
}

// Map is a rendered map image: PNG bytes and its size in pixels.
type Map struct {
	PNG  []byte
	W, H int
}

// RenderMap draws the box x in [xMin, xMax], y in [yMin, yMax] at res yards per pixel, north (+x) up and west (+y)
// left: shaded relief from the server's terrain, water, the game's own minimap where it was extracted, and zone
// borders. heightRange fixes the colour scale (tiles must agree; nil fits the box). overlay, if set, can draw on it
// with set(worldX, worldY, colour).
func RenderMap(m uint32, xMin, xMax, yMin, yMax, res float64, heightRange *[2]float64,
	overlay func(set func(wx, wy float64, c [3]uint8))) Map {
	W, H := int(math.Ceil((yMax-yMin)/res)), int(math.Ceil((xMax-xMin)/res))
	zoneOf := Areas(m).ZoneOf
	heights, water, zone := make([]float64, W*H), make([]float64, W*H), make([]int, W*H)
	for r := 0; r < H; r++ {
		for c := 0; c < W; c++ {
			h, wl, a := Sample(m, xMax-float64(r)*res, yMax-float64(c)*res)
			heights[r*W+c], water[r*W+c], zone[r*W+c] = h, wl, zoneOf[int(a)]
		}
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	if heightRange != nil {
		lo, hi = heightRange[0], heightRange[1]
	} else {
		for _, h := range heights {
			if !math.IsNaN(h) {
				lo, hi = math.Min(lo, h), math.Max(hi, h)
			}
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	for r := 0; r < H; r++ {
		for c := 0; c < W; c++ {
			k, h := r*W+c, heights[r*W+c]
			if math.IsNaN(h) {
				img.Set(c, r, color.RGBA{40, 40, 40, 255})
				continue
			}
			// hillshade: light from the north-west, slopes exaggerated x2
			hx := (heights[min(H-1, r+1)*W+c] - heights[max(0, r-1)*W+c]) / res
			hy := (heights[r*W+min(W-1, c+1)] - heights[r*W+max(0, c-1)]) / res
			shade := math.Max(0.35, math.Min(1.25, 0.85-(hx+hy)*0.42))
			if math.IsNaN(shade) {
				shade = 1
			}
			col := tint((h - lo) / math.Max(1, hi-lo))
			for i := range col {
				col[i] *= shade
			}
			if water[k] > h {
				d := math.Min(1, (water[k]-h)/20)
				col = [3]float64{70 - d*30, 120 - d*30, 170 - d*20}
			}
			if mm, ok := MinimapAt(m, xMax-float64(r)*res, yMax-float64(c)*res); ok {
				col = [3]float64{float64(mm[0]), float64(mm[1]), float64(mm[2])}
			}
			if z := zone[k]; (c+1 < W && zone[k+1] != z) || (r+1 < H && zone[k+W] != z) {
				for i := range col {
					col[i] *= 0.45
				}
			}
			img.Set(c, r, color.RGBA{uint8(math.Min(255, col[0])), uint8(math.Min(255, col[1])), uint8(math.Min(255, col[2])), 255})
		}
	}
	if overlay != nil {
		overlay(func(wx, wy float64, c [3]uint8) {
			if math.IsNaN(wx) || math.IsNaN(wy) {
				return
			}
			r, col := int(jsRound((xMax-wx)/res)), int(jsRound((yMax-wy)/res)) // JS Math.round: -0.5 -> 0
			if r >= 0 && r < H && col >= 0 && col < W {
				img.Set(col, r, color.RGBA{c[0], c[1], c[2], 255})
			}
		})
	}
	var b bytes.Buffer
	(&png.Encoder{CompressionLevel: png.DefaultCompression}).Encode(&b, img)
	return Map{PNG: b.Bytes(), W: W, H: H}
}
