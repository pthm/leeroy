package world

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"github.com/pthm/leeroy/azsdk/clientdata"
	"github.com/pthm/leeroy/azsdk/pathfinding"
)

// The client's models, placed by their ADT entries, land where the server's collision geometry for them is: the
// Goldshire inn's bounding box matches the vmap triangles over it, and so does a tree's.
func TestPlacementMatchesVmaps(t *testing.T) {
	if !HasDetail(0) {
		t.Skip("no game client")
	}
	a := adtOf(0, 49, 31)
	box := func(m *clientdata.Model, p clientdata.Placement) (lo, hi pathfinding.Vector3) {
		w := pathfinding.PlacementWorld(p.Pos, p.Rot, p.Scale)
		lo, hi = pathfinding.Vector3{X: 1e9, Y: 1e9, Z: 1e9}, pathfinding.Vector3{X: -1e9, Y: -1e9, Z: -1e9}
		for _, tr := range m.Tris {
			for _, i := range tr {
				v := w(pathfinding.Vector3{X: m.Verts[i][0], Y: m.Verts[i][1], Z: m.Verts[i][2]})
				lo = pathfinding.Vector3{X: min(lo.X, v.X), Y: min(lo.Y, v.Y), Z: min(lo.Z, v.Z)}
				hi = pathfinding.Vector3{X: max(hi.X, v.X), Y: max(hi.Y, v.Y), Z: max(hi.Z, v.Z)}
			}
		}
		return
	}
	vbox := func(lo, hi pathfinding.Vector3) (vlo, vhi pathfinding.Vector3) {
		vlo, vhi = pathfinding.Vector3{X: 1e9, Y: 1e9, Z: 1e9}, pathfinding.Vector3{X: -1e9, Y: -1e9, Z: -1e9}
		vmaps().Triangles(0, lo.X, hi.X, lo.Y, hi.Y, func(p, q, r pathfinding.Vector3) {
			for _, v := range []pathfinding.Vector3{p, q, r} {
				if v.X >= lo.X-0.5 && v.X <= hi.X+0.5 && v.Y >= lo.Y-0.5 && v.Y <= hi.Y+0.5 {
					vlo = pathfinding.Vector3{X: min(vlo.X, v.X), Y: min(vlo.Y, v.Y), Z: min(vlo.Z, v.Z)}
					vhi = pathfinding.Vector3{X: max(vhi.X, v.X), Y: max(vhi.Y, v.Y), Z: max(vhi.Z, v.Z)}
				}
			}
		})
		return
	}
	for _, kind := range []string{"goldshireinn", "elwynntreecanopy"} {
		var p clientdata.Placement
		var m *clientdata.Model
		var err error
		for _, q := range append(a.WMOs, a.Models...) {
			if strings.Contains(strings.ToLower(q.Name), kind) {
				p = q
				break
			}
		}
		if strings.HasSuffix(strings.ToLower(p.Name), ".wmo") {
			m, err = clientdata.LoadWMO(client(), p.Name)
		} else {
			m, err = clientdata.LoadM2(client(), p.Name)
		}
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(strings.ToLower(p.Name), ".wmo") { // an M2: its collision vertices, what the vmaps hold
			raw, _ := client().Read(p.Name)
			n, off := int(binary.LittleEndian.Uint32(raw[0xE0:])), int(binary.LittleEndian.Uint32(raw[0xE4:]))
			m = &clientdata.Model{}
			for i := 0; i < n; i++ {
				o := off + i*12
				m.Verts = append(m.Verts, [3]float32{
					math.Float32frombits(binary.LittleEndian.Uint32(raw[o:])),
					math.Float32frombits(binary.LittleEndian.Uint32(raw[o+4:])), math.Float32frombits(binary.LittleEndian.Uint32(raw[o+8:])),
				})
			}
			m.Tris = [][3]uint32{}
			for i := 0; i+2 < n; i += 3 {
				m.Tris = append(m.Tris, [3]uint32{uint32(i), uint32(i + 1), uint32(i + 2)})
			}
		}
		lo, hi := box(m, p)
		vlo, vhi := vbox(lo, hi)
		t.Logf("%s: model %v..%v; vmaps %v..%v", kind, lo, hi, vlo, vhi)
		if vlo.X-lo.X > 2 || hi.X-vhi.X > 2 || vlo.Y-lo.Y > 2 || hi.Y-vhi.Y > 2 {
			t.Errorf("%s: the model's footprint and the vmap's disagree", kind)
		}
	}
}
