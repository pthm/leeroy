package clientdata

import (
	"encoding/binary"
	"fmt"
	"image"
)

// DecodeBLP decodes a BLP2 image's full-size level: DXT1/3/5, palettised, or raw BGRA.
func DecodeBLP(b []byte) (*image.RGBA, error) {
	if len(b) < 1172 || string(b[:4]) != "BLP2" {
		return nil, fmt.Errorf("not a BLP2 image")
	}
	le := binary.LittleEndian
	comp, alphaDepth, alphaType := b[8], b[9], b[10]
	w, h := int(le.Uint32(b[12:])), int(le.Uint32(b[16:]))
	off, size := le.Uint32(b[20:]), le.Uint32(b[84:])
	if int(off+size) > len(b) || w <= 0 || h <= 0 {
		return nil, fmt.Errorf("bad BLP mip table")
	}
	data := b[off : off+size]
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	switch comp {
	case 1: // palettised: indices, then alpha
		pal := b[148 : 148+1024]
		for i := 0; i < w*h && i < len(data); i++ {
			p := pal[int(data[i])*4:]
			a := uint8(255)
			switch alphaDepth {
			case 8:
				if w*h+i < len(data) {
					a = data[w*h+i]
				}
			case 1:
				if w*h+i/8 < len(data) && data[w*h+i/8]>>(i%8)&1 == 0 {
					a = 0
				}
			}
			img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = p[2], p[1], p[0], a
		}
	case 2: // DXT
		block := 16
		if alphaType == 0 && alphaDepth <= 1 {
			block = 8 // DXT1
		}
		for by := 0; by < (h+3)/4; by++ {
			for bx := 0; bx < (w+3)/4; bx++ {
				i := (by*((w+3)/4) + bx) * block
				if i+block > len(data) {
					continue
				}
				blk := data[i : i+block]
				var alpha [16]uint8
				for k := range alpha {
					alpha[k] = 255
				}
				color := blk
				if block == 16 {
					color = blk[8:]
					if alphaType == 7 { // DXT5: interpolated alpha
						a0, a1 := blk[0], blk[1]
						var pal [8]uint8
						pal[0], pal[1] = a0, a1
						for k := 1; k < 7; k++ {
							if a0 > a1 {
								pal[k+1] = uint8((int(a0)*(7-k) + int(a1)*k) / 7)
							} else if k < 5 {
								pal[k+1] = uint8((int(a0)*(5-k) + int(a1)*k) / 5)
							}
						}
						if a0 <= a1 {
							pal[6], pal[7] = 0, 255
						}
						bits := uint64(0)
						for k := 0; k < 6; k++ {
							bits |= uint64(blk[2+k]) << (8 * k)
						}
						for k := range alpha {
							alpha[k] = pal[bits>>(3*k)&7]
						}
					} else { // DXT3: explicit 4-bit alpha
						for k := range alpha {
							alpha[k] = (blk[k/2] >> (4 * (k % 2)) & 0xF) * 17
						}
					}
				}
				c0, c1 := le.Uint16(color[0:]), le.Uint16(color[2:])
				rgb := func(c uint16) [3]int {
					return [3]int{int(c>>11&31) * 255 / 31, int(c>>5&63) * 255 / 63, int(c&31) * 255 / 31}
				}
				p := [4][3]int{rgb(c0), rgb(c1)}
				transparent := false
				if c0 > c1 || block == 16 {
					for k := 0; k < 3; k++ {
						p[2][k], p[3][k] = (2*p[0][k]+p[1][k])/3, (p[0][k]+2*p[1][k])/3
					}
				} else {
					for k := 0; k < 3; k++ {
						p[2][k] = (p[0][k] + p[1][k]) / 2
					}
					transparent = true
				}
				idx := le.Uint32(color[4:])
				for k := 0; k < 16; k++ {
					x, y := bx*4+k%4, by*4+k/4
					if x >= w || y >= h {
						continue
					}
					sel := idx >> (2 * k) & 3
					a := alpha[k]
					if transparent && sel == 3 {
						a = 0
					}
					o := (y*w + x) * 4
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = uint8(p[sel][0]), uint8(p[sel][1]), uint8(p[sel][2]), a
				}
			}
		}
	case 3: // raw BGRA
		for i := 0; i < w*h && i*4+3 < len(data); i++ {
			img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = data[i*4+2], data[i*4+1], data[i*4], data[i*4+3]
		}
	default:
		return nil, fmt.Errorf("unsupported BLP compression %d", comp)
	}
	return img, nil
}
