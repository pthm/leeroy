package clientdata

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

// ADT is one terrain tile's texture layers: 16x16 chunks, each with up to 4 texture layers and an alpha map
// (64x64) for every layer above the first.
type ADT struct {
	Textures []string
	Chunks   [256]Chunk // row-major: index = y*16 + x
}

type Chunk struct {
	Layers []uint32  // texture index per layer (into Textures)
	Alpha  [][]uint8 // per layer above the first: 64x64 coverage, 0-255
}

// Coverage: how much of texture tex covers point (cx, cy) of a chunk, 0-255 (layer 0 is what the layers above leave).
func (c *Chunk) Coverage(tex uint32, cx, cy int) uint8 {
	// the game blends layers in order: each layer's alpha covers what's below
	cov := make([]float64, len(c.Layers))
	rest := 1.0
	for l := len(c.Layers) - 1; l >= 1; l-- {
		a := float64(c.Alpha[l-1][cy*64+cx]) / 255 * rest
		cov[l], rest = a, rest-a
	}
	if len(cov) > 0 {
		cov[0] = rest
	}
	total := 0.0
	for l, t := range c.Layers {
		if t == tex {
			total += cov[l]
		}
	}
	return uint8(total * 255)
}

// ParseADT reads an ADT's textures and alpha maps. bigAlpha: the map's WDT says 8-bit alpha (MPHD flags 0x4|0x80).
func ParseADT(b []byte, bigAlpha bool) (*ADT, error) {
	le := binary.LittleEndian
	a := &ADT{}
	chunk := 0
	for i := 0; i+8 <= len(b); {
		tag, n := string([]byte{b[i+3], b[i+2], b[i+1], b[i]}), int(le.Uint32(b[i+4:]))
		body := i + 8
		if body+n > len(b) {
			break
		}
		switch tag {
		case "MTEX":
			for _, s := range bytes.Split(b[body:body+n], []byte{0}) {
				if len(s) > 0 {
					a.Textures = append(a.Textures, strings.ReplaceAll(string(s), "/", `\`))
				}
			}
		case "MCNK":
			if chunk < 256 {
				c, err := parseMCNK(b[i:body+n], bigAlpha)
				if err != nil {
					return nil, fmt.Errorf("chunk %d: %w", chunk, err)
				}
				a.Chunks[chunk] = c
			}
			chunk++
		}
		i = body + n
	}
	return a, nil
}

func parseMCNK(m []byte, bigAlpha bool) (Chunk, error) {
	le := binary.LittleEndian
	var c Chunk
	if len(m) < 8+128 {
		return c, fmt.Errorf("short MCNK")
	}
	h := m[8:]
	flags, nLayers, ofsLayer, ofsAlpha := le.Uint32(h[0:]), le.Uint32(h[0x0C:]), le.Uint32(h[0x1C:]), le.Uint32(h[0x24:])
	// offsets are from the start of the MCNK chunk (its header included); the sub-chunks have their own 8-byte headers
	if nLayers == 0 || int(ofsLayer)+8+int(nLayers)*16 > len(m) {
		return c, nil
	}
	ly := m[ofsLayer+8:]
	alpha := []byte{}
	if int(ofsAlpha)+8 <= len(m) {
		n := int(le.Uint32(m[ofsAlpha+4:]))
		if int(ofsAlpha)+8+n <= len(m) {
			alpha = m[ofsAlpha+8 : int(ofsAlpha)+8+n]
		}
	}
	for l := 0; l < int(nLayers) && l < 4; l++ {
		e := ly[l*16:]
		c.Layers = append(c.Layers, le.Uint32(e[0:]))
		if l == 0 {
			continue
		}
		lflags, off := le.Uint32(e[4:]), le.Uint32(e[8:])
		am := make([]uint8, 4096)
		if int(off) <= len(alpha) {
			src := alpha[off:]
			switch {
			case lflags&0x200 != 0: // compressed: runs of fill or copy, 4096 bytes out
				for o, p := 0, 0; o < 4096 && p < len(src); {
					ctl := src[p]
					p++
					cnt := int(ctl & 0x7F)
					if ctl&0x80 != 0 {
						if p < len(src) {
							for k := 0; k < cnt && o < 4096; k++ {
								am[o] = src[p]
								o++
							}
							p++
						}
					} else {
						for k := 0; k < cnt && o < 4096 && p < len(src); k++ {
							am[o] = src[p]
							o, p = o+1, p+1
						}
					}
				}
			case bigAlpha:
				copy(am, src)
			default: // 4 bits per point, low nibble first
				for k := 0; k < 2048 && k < len(src); k++ {
					am[k*2], am[k*2+1] = (src[k]&0xF)*17, (src[k]>>4)*17
				}
				if flags&0x8000 == 0 { // "do not fix alpha": else the last row and column repeat the previous ones
					for y := 0; y < 64; y++ {
						am[y*64+63] = am[y*64+62]
					}
					copy(am[63*64:], am[62*64:63*64])
				}
			}
		}
		c.Alpha = append(c.Alpha, am)
	}
	return c, nil
}
