package world

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math"
)

// DetailBox draws the box x in [xMin, xMax], y in [yMin, yMax] of continent m, outW pixels wide, north up and west
// left, in greyscale: the viewer's detail tiles (ground textures, the terrain's lighting, buildings and trees from their
// models) at the coarsest level as fine as a pixel, averaged down; where there are none (open sea, no client data), the
// relief from the server's terrain.
func DetailBox(m uint32, xMin, xMax, yMin, yMax float64, outW int) *image.Gray {
	res := (yMax - yMin) / float64(outW)
	outH := int(math.Round((xMax - xMin) / res))
	sum := make([]float64, outW*outH)
	cnt := make([]float64, outW*outH)
	if HasDetail(m) {
		level := 1
		for level < MaxDetail && GridSize/float64(int(1)<<level)/256 > res {
			level++
		}
		sub := GridSize / float64(int(1)<<level)
		tres := sub / 256
		for gx := gridOf(xMax); gx <= gridOf(xMin); gx++ {
			for gy := gridOf(yMax); gy <= gridOf(yMin); gy++ {
				for sx := 0; sx < 1<<level; sx++ {
					for sy := 0; sy < 1<<level; sy++ {
						xN, yW := GridSize*float64(32-gx)-float64(sx)*sub, GridSize*float64(32-gy)-float64(sy)*sub
						if xN-sub > xMax || xN < xMin || yW-sub > yMax || yW < yMin {
							continue
						}
						b := DetailTile(m, gx, gy, level, sx, sy)
						if b == nil {
							continue
						}
						img, err := jpeg.Decode(bytes.NewReader(b))
						if err != nil {
							continue
						}
						for r := 0; r < 256; r++ {
							row := int((xMax - (xN - (float64(r)+0.5)*tres)) / res)
							if row < 0 || row >= outH {
								continue
							}
							for c := 0; c < 256; c++ {
								col := int((yMax - (yW - (float64(c)+0.5)*tres)) / res)
								if col < 0 || col >= outW {
									continue
								}
								cr, cg, cb, _ := img.At(c, r).RGBA()
								sum[row*outW+col] += (0.299*float64(cr) + 0.587*float64(cg) + 0.114*float64(cb)) / 257
								cnt[row*outW+col]++
							}
						}
					}
				}
			}
		}
	}
	var relief image.Image
	out := image.NewGray(image.Rect(0, 0, outW, outH))
	for i := range sum {
		if cnt[i] > 0 {
			out.Pix[i] = uint8(math.Min(255, sum[i]/cnt[i]))
			continue
		}
		if relief == nil { // only drawn if needed
			relief = RenderRelief(m, xMin, xMax, yMin, yMax, res).Image()
		}
		r, g, b, _ := relief.At(i%outW, i/outW).RGBA()
		out.SetGray(i%outW, i/outW, color.Gray{uint8((0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 257)})
	}
	return out
}
