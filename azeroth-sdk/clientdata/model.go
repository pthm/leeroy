package clientdata

import (
	"encoding/binary"
	"fmt"
	"math"
	"path"
	"strings"
)

// Model is a model's drawable geometry, from a WMO (a building and its groups) or an M2 (a doodad): triangles over
// vertices with texture coordinates, each triangle with a material (a texture, and whether its transparent pixels
// are cut out). Vertices are in the model's own axes (z up).
type Model struct {
	Verts     [][3]float32
	UV        [][2]float32
	Tris      [][3]uint32
	TriMat    []int32 // per triangle, into Materials
	Materials []Material
	Doodads   []Doodad // a WMO's doodads (lamps, furniture, a city's trees), by set
	Sets      []DoodadSet
	Liquids   []Liquid // a WMO's water, lava or slime (Stormwind's canals), in its groups
}

// Liquid is a group's liquid: a grid of square tiles LiquidTile yd across from Corner (in the model's axes), the
// surface height at each corner of the grid, and each tile's type (0 water, 1 ocean, 2 magma, 3 slime; -1 none).
type Liquid struct {
	Corner         [3]float32
	XVerts, YVerts int
	Heights        []float32 // XVerts * YVerts: index y*XVerts + x
	Tiles          []int8    // (XVerts-1) * (YVerts-1): index y*(XVerts-1) + x
}

// LiquidTile is the side of a WMO liquid tile, yd: a 128th of a map grid square.
const LiquidTile = 1600.0 / 3 / 128

// Material is a texture and how it's drawn: AlphaTest cuts out pixels under half alpha (leaves, fences).
type Material struct {
	Texture   string
	AlphaTest bool
}

// Doodad is an M2 placed inside a WMO, in the WMO's axes: Rot is a quaternion (x, y, z, w).
type Doodad struct {
	Name  string
	Pos   [3]float32
	Rot   [4]float32
	Scale float32
}

// DoodadSet is a range of a WMO's doodads; set 0 is always shown, plus the one the placement names.
type DoodadSet struct{ Start, Count int }

var le = binary.LittleEndian

func f32(b []byte, i int) float32 { return math.Float32frombits(le.Uint32(b[i:])) }

// chunks calls fn for each chunk in b (tags are stored reversed).
func chunks(b []byte, fn func(tag string, body []byte)) {
	for i := 0; i+8 <= len(b); {
		tag, n := string([]byte{b[i+3], b[i+2], b[i+1], b[i]}), int(le.Uint32(b[i+4:]))
		if i+8+n > len(b) {
			return
		}
		fn(tag, b[i+8:i+8+n])
		i += 8 + n
	}
}

func cstrings(b []byte, off uint32) string {
	if int(off) >= len(b) {
		return ""
	}
	s := b[off:]
	for i, c := range s {
		if c == 0 {
			return string(s[:i])
		}
	}
	return string(s)
}

// placeholder: an artist's stand-in texture (DUNGEONS\TEXTURES\TEMP\64.BLP, a grey "64"), on geometry the game never
// shows: a particle emitter's box (Orgrimmar's smoke and embers, a hundred yards across)
func placeholder(tex string) bool { return strings.Contains(strings.ToUpper(tex), `\TEXTURES\TEMP\`) }

// LoadWMO reads a WMO: its root (materials, doodads) and every group file (geometry). Triangles drawn by no material
// (collision only) or a placeholder texture are left out.
func LoadWMO(c Source, name string) (*Model, error) {
	root, err := c.Read(name)
	if err != nil {
		return nil, err
	}
	m := &Model{}
	var motx, modn []byte
	nGroups := 0
	chunks(root, func(tag string, b []byte) {
		switch tag {
		case "MOHD":
			if len(b) >= 8 {
				nGroups = int(le.Uint32(b[4:]))
			}
		case "MOTX":
			motx = b
		case "MOMT":
			for o := 0; o+64 <= len(b); o += 64 {
				blend := le.Uint32(b[o+8:])
				m.Materials = append(m.Materials, Material{Texture: strings.ReplaceAll(cstrings(motx, le.Uint32(b[o+12:])), "/", `\`), AlphaTest: blend != 0})
			}
		case "MODN":
			modn = b
		case "MODS":
			for o := 0; o+32 <= len(b); o += 32 {
				m.Sets = append(m.Sets, DoodadSet{int(le.Uint32(b[o+20:])), int(le.Uint32(b[o+24:]))})
			}
		case "MODD":
			for o := 0; o+40 <= len(b); o += 40 {
				m.Doodads = append(m.Doodads, Doodad{Name: strings.ReplaceAll(cstrings(modn, le.Uint32(b[o:])&0xFFFFFF), "/", `\`),
					Pos: [3]float32{f32(b, o+4), f32(b, o+8), f32(b, o+12)}, Rot: [4]float32{f32(b, o+16), f32(b, o+20), f32(b, o+24), f32(b, o+28)},
					Scale: f32(b, o+32)})
			}
		}
	})
	base := strings.TrimSuffix(name, path.Ext(name))
	for g := 0; g < nGroups; g++ {
		b, err := c.Read(fmt.Sprintf("%s_%03d.wmo", base, g))
		if err != nil {
			continue
		}
		chunks(b, func(tag string, body []byte) {
			if tag == "MOGP" && len(body) > 68 {
				m.addGroup(body[68:])
			}
		})
	}
	return m, nil
}

func (m *Model) addGroup(b []byte) {
	var mopy []byte
	var idx []uint16
	var verts [][3]float32
	var uv [][2]float32
	chunks(b, func(tag string, body []byte) {
		switch tag {
		case "MOPY":
			mopy = body
		case "MOVI":
			for o := 0; o+2 <= len(body); o += 2 {
				idx = append(idx, le.Uint16(body[o:]))
			}
		case "MOVT":
			for o := 0; o+12 <= len(body); o += 12 {
				verts = append(verts, [3]float32{f32(body, o), f32(body, o+4), f32(body, o+8)})
			}
		case "MLIQ":
			if l, ok := parseMLIQ(body); ok {
				m.Liquids = append(m.Liquids, l)
			}
		case "MOTV":
			if uv == nil { // the first set; a second is for blended materials
				for o := 0; o+8 <= len(body); o += 8 {
					uv = append(uv, [2]float32{f32(body, o), f32(body, o+4)})
				}
			}
		}
	})
	if len(uv) < len(verts) {
		uv = append(uv, make([][2]float32, len(verts)-len(uv))...)
	}
	base := uint32(len(m.Verts))
	m.Verts, m.UV = append(m.Verts, verts...), append(m.UV, uv[:len(verts)]...)
	for t := 0; t*3+2 < len(idx) && t*2+1 < len(mopy); t++ {
		mat := mopy[t*2+1]
		a, b, c := uint32(idx[t*3]), uint32(idx[t*3+1]), uint32(idx[t*3+2])
		if mat == 0xFF || int(mat) >= len(m.Materials) || placeholder(m.Materials[mat].Texture) || int(max(a, b, c)) >= len(verts) {
			continue
		}
		m.Tris = append(m.Tris, [3]uint32{base + a, base + b, base + c})
		m.TriMat = append(m.TriMat, int32(mat))
	}
}

// parseMLIQ reads a group's liquid: its header (vertex and tile counts, corner, material), a height per vertex (8
// bytes each, the height last), then a flags byte per tile (the low 4 bits the type, 15 for none).
func parseMLIQ(b []byte) (Liquid, bool) {
	if len(b) < 30 {
		return Liquid{}, false
	}
	l := Liquid{XVerts: int(le.Uint32(b)), YVerts: int(le.Uint32(b[4:])), Corner: [3]float32{f32(b, 16), f32(b, 20), f32(b, 24)}}
	xt, yt := int(le.Uint32(b[8:])), int(le.Uint32(b[12:]))
	nv := l.XVerts * l.YVerts
	if l.XVerts < 2 || l.YVerts < 2 || xt != l.XVerts-1 || yt != l.YVerts-1 || 30+nv*8+xt*yt > len(b) {
		return Liquid{}, false
	}
	for i := 0; i < nv; i++ {
		l.Heights = append(l.Heights, f32(b, 30+i*8+4))
	}
	any := false
	for _, f := range b[30+nv*8 : 30+nv*8+xt*yt] {
		t := int8(f & 0xF)
		if t == 0xF {
			t = -1
		} else {
			t &= 3
			any = true
		}
		l.Tiles = append(l.Tiles, t)
	}
	return l, any
}

// LoadM2 reads an M2 doodad and its first skin (the full-detail triangles). Batches with a texture the game fills in
// (a creature's skin), drawn additively (glows) or hidden at rest (an effect's box: smoke, lightning) are left out.
func LoadM2(c Source, name string) (*Model, error) {
	name = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(name, ".mdx"), ".MDX"), ".mdl")
	if ext := strings.ToLower(path.Ext(name)); ext != ".m2" {
		name = strings.TrimSuffix(name, path.Ext(name)) + ".m2"
	}
	b, err := c.Read(name)
	if err != nil {
		return nil, err
	}
	if len(b) < 0xA8 || string(b[:4]) != "MD20" {
		return nil, fmt.Errorf("%s: not an M2", name)
	}
	arr := func(o int) (int, int) { return int(le.Uint32(b[o:])), int(le.Uint32(b[o+4:])) }
	in := func(off, n, size int) bool { return off >= 0 && n >= 0 && off+n*size <= len(b) }
	m := &Model{}
	nv, ov := arr(0x3C)
	if !in(ov, nv, 48) {
		return nil, fmt.Errorf("%s: vertices out of range", name)
	}
	for i := 0; i < nv; i++ {
		o := ov + i*48
		m.Verts = append(m.Verts, [3]float32{f32(b, o), f32(b, o+4), f32(b, o+8)})
		m.UV = append(m.UV, [2]float32{f32(b, o+32), f32(b, o+36)})
	}
	nt, ot := arr(0x50)
	var texNames []string
	for i := 0; i < nt && in(ot, nt, 16); i++ {
		o := ot + i*16
		n, off := int(le.Uint32(b[o+8:])), int(le.Uint32(b[o+12:]))
		if le.Uint32(b[o:]) != 0 || !in(off, n, 1) { // a replaceable texture: chosen by the game per creature
			texNames = append(texNames, "")
			continue
		}
		texNames = append(texNames, strings.ReplaceAll(cstrings(b[off:off+n], 0), "/", `\`))
	}
	nm, om := arr(0x70)
	nl, ol := arr(0x80)
	// opacity at rest: a fixed16 track's first value in its first animation (1 with no keys), for the colours' alpha
	// (0x48, 40 bytes each: a colour track, then the alpha's) and the texture weights (0x58, through the lookup at 0x90)
	rest := func(track int) float64 {
		if !in(track, 1, 20) {
			return 1
		}
		ns, os := arr(track + 12)
		if ns == 0 || !in(os, 1, 8) {
			return 1
		}
		n, o := arr(os)
		if n == 0 || !in(o, 1, 2) {
			return 1
		}
		return float64(int16(le.Uint16(b[o:]))) / 32767
	}
	nc, oc := arr(0x48)
	nw, ow := arr(0x58)
	ntl, otl := arr(0x90)
	skin, err := c.Read(strings.TrimSuffix(name, path.Ext(name)) + "00.skin")
	if err != nil || len(skin) < 48 || string(skin[:4]) != "SKIN" {
		return nil, fmt.Errorf("%s: no skin", name)
	}
	sarr := func(o int) (int, int) { return int(le.Uint32(skin[o:])), int(le.Uint32(skin[o+4:])) }
	sin := func(off, n, size int) bool { return off >= 0 && n >= 0 && off+n*size <= len(skin) }
	ni, oi := sarr(4)
	ntr, otr := sarr(12)
	nsub, osub := sarr(28)
	nb, ob := sarr(36)
	if !sin(oi, ni, 2) || !sin(otr, ntr, 2) || !sin(osub, nsub, 48) || !sin(ob, nb, 24) {
		return nil, fmt.Errorf("%s: skin out of range", name)
	}
	lookup := func(i int) int { return int(le.Uint16(skin[oi+2*i:])) }
	mats := map[[2]int]int32{} // (texture, alpha test) -> material
	for k := 0; k < nb; k++ {
		o := ob + k*24
		sub, matIdx, combo := int(le.Uint16(skin[o+4:])), int(le.Uint16(skin[o+10:])), int(le.Uint16(skin[o+16:]))
		if sub >= nsub || combo >= nl || !in(ol, nl, 2) {
			continue
		}
		tex := int(le.Uint16(b[ol+2*combo:]))
		if tex >= len(texNames) || texNames[tex] == "" || placeholder(texNames[tex]) {
			continue
		}
		blend := 0
		if matIdx < nm && in(om, nm, 4) {
			blend = int(le.Uint16(b[om+4*matIdx+2:]))
		}
		if blend >= 3 { // additive and modulated: glows, light shafts
			continue
		}
		alpha := 1.0 // hidden at rest: a colour or texture weight at 0 (a smoke emitter's box, Netherstorm's cracks)
		if ci := int(int16(le.Uint16(skin[o+8:]))); ci >= 0 && ci < nc && in(oc, nc, 40) {
			alpha *= rest(oc + ci*40 + 20)
		}
		if wi := int(le.Uint16(skin[o+20:])); wi < ntl && in(otl, ntl, 2) {
			if ti := int(le.Uint16(b[otl+2*wi:])); ti < nw && in(ow, nw, 20) {
				alpha *= rest(ow + ti*20)
			}
		}
		if alpha < 0.05 {
			continue
		}
		key := [2]int{tex, min(blend, 1)}
		mi, ok := mats[key]
		if !ok {
			mi = int32(len(m.Materials))
			m.Materials = append(m.Materials, Material{Texture: texNames[tex], AlphaTest: blend > 0})
			mats[key] = mi
		}
		so := osub + sub*48
		start := int(le.Uint16(skin[so+8:])) + int(le.Uint16(skin[so+2:]))<<16 // index start, and its level (each 65536)
		count := int(le.Uint16(skin[so+10:]))
		for t := start; t+2 < start+count && t+2 < ntr; t += 3 {
			a, bb, cc := le.Uint16(skin[otr+2*t:]), le.Uint16(skin[otr+2*t+2:]), le.Uint16(skin[otr+2*t+4:])
			if int(max(a, bb, cc)) >= ni {
				continue
			}
			va, vb, vc := lookup(int(a)), lookup(int(bb)), lookup(int(cc))
			if max(va, vb, vc) >= nv {
				continue
			}
			m.Tris = append(m.Tris, [3]uint32{uint32(va), uint32(vb), uint32(vc)})
			m.TriMat = append(m.TriMat, mi)
		}
	}
	return m, nil
}
