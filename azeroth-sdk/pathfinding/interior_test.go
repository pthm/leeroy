package pathfinding

import (
	"context"
	"math"
	"testing"

	"github.com/o0olele/detour-go/detour"
)

// Each cell is a ten-yard square, in Detour X/Z coordinates, at height h.
// Shared edges are connected; overlapping floors have distinct vertices.
type floorCell struct{ x, z, h uint16 }

func interiorFinder(t *testing.T, cells ...floorCell) *PathFinder {
	t.Helper()
	p := detour.DtNavMeshCreateParams{Nvp: 4, PolyCount: int32(len(cells)), Cs: 1, Ch: 1,
		Bmax: [3]float32{64, 64, 64}, WalkableHeight: 2, WalkableRadius: 0.3, WalkableClimb: 0.5, BuildBvTree: true}
	indices := map[[3]uint16]uint16{}
	for _, c := range cells {
		for _, v := range [][3]uint16{{c.x, c.h, c.z}, {c.x, c.h, c.z + 10}, {c.x + 10, c.h, c.z + 10}, {c.x + 10, c.h, c.z}} {
			idx, ok := indices[v]
			if !ok {
				idx = uint16(len(p.Verts) / 3)
				indices[v] = idx
				p.Verts = append(p.Verts, v[:]...)
			}
			p.Polys = append(p.Polys, idx)
		}
		p.Polys = append(p.Polys, 0xffff, 0xffff, 0xffff, 0xffff)
		p.PolyFlags = append(p.PolyFlags, 1)
		p.PolyAreas = append(p.PolyAreas, 0)
	}
	p.VertCount = int32(len(p.Verts) / 3)
	for i := range cells {
		for j := range cells {
			if i == j {
				continue
			}
			for a := 0; a < 4; a++ {
				for b := 0; b < 4; b++ {
					if p.Polys[i*8+a] == p.Polys[j*8+(b+1)%4] && p.Polys[i*8+(a+1)%4] == p.Polys[j*8+b] {
						p.Polys[i*8+4+a] = uint16(j)
					}
				}
			}
		}
	}
	var data []byte
	var size int
	if !detour.DtCreateNavMeshData(&p, &data, &size) {
		t.Fatal("build interior mesh")
	}
	mesh := detour.DtAllocNavMesh()
	if st := mesh.Init2(data, size, detour.DT_TILE_FREE_DATA); detour.DtStatusFailed(st) {
		t.Fatal(st)
	}
	mgr := NewMMapManager(t.TempDir())
	mgr.meshes[0] = &mapData{navMesh: mesh, loadedTiles: map[uint32]bool{}}
	pf, err := NewPathFinder(mgr, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return pf
}

func TestInteriorStackedFloorsStayDistinct(t *testing.T) {
	pf := interiorFinder(t, floorCell{10, 10, 0}, floorCell{10, 10, 3})
	for _, z := range []float32{0, 3} {
		r, err := pf.FindPath(Point3D{12, 12, z}, Point3D{18, 18, z})
		if err != nil || r.Type != PathfindNormal {
			t.Fatalf("z=%v: %+v %v", z, r, err)
		}
		for _, p := range r.Points {
			if math.Abs(float64(p.Z-z)) > 0.05 {
				t.Fatalf("changed floor: %+v", r)
			}
		}
	}
	r, err := pf.FindPath(Point3D{12, 12, 0}, Point3D{18, 18, 3})
	if err != nil || r.Type&PathfindIncomplete == 0 {
		t.Fatalf("disconnected floors: %+v %v", r, err)
	}
}

func TestInteriorCornerRequiresConnectedCorridor(t *testing.T) {
	pf := interiorFinder(t, floorCell{10, 10, 0}, floorCell{20, 10, 0}, floorCell{20, 20, 0})
	r, err := pf.FindPathContext(context.Background(), Point3D{12, 12, 0}, Point3D{28, 28, 0})
	if err != nil || r.Type != PathfindNormal || len(r.Points) < 3 {
		t.Fatalf("corner: %+v %v", r, err)
	}
	for _, p := range r.Points {
		if p.X > 20.01 && p.Y < 19.99 {
			t.Fatalf("outside L-shaped corridor: %+v", r)
		}
	}
}

func TestInteriorRealRoutes(t *testing.T) {
	svc := NewPathFindingServiceFromDataDir(requireACData(t))
	cases := []struct {
		name string
		a, b Point3D
	}{
		{"bridge", Point3D{-8930, 545, 94.715}, Point3D{-8880, 590, 93.402}},
		{"inn_stairs", Point3D{-9460, 8, 57}, Point3D{-9472, 34, 64}},
		{"cave_upper", Point3D{-9797, 132, 25}, Point3D{-9807, 117, 24}},
		{"cave_lower", Point3D{-9824, 122, 4}, Point3D{-9843.54, 127.525, 5.3698}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := svc.FindPath(0, tc.a, tc.b)
			if err != nil || r == nil || r.Type&(PathfindNopath|PathfindNotUsingPath) != 0 || len(r.Points) < 2 {
				t.Fatalf("route: %+v %v", r, err)
			}
			t.Logf("type=%x requested=%+v -> %+v points=%+v", r.Type, tc.a, tc.b, r.Points)
			for _, p := range r.Points {
				if math.IsNaN(float64(p.Z)) || math.IsInf(float64(p.Z), 0) {
					t.Fatal(p)
				}
			}
		})
	}
}
