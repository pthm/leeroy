package pathfinding

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

func leafIndex(bounds AABox, count int) BIH {
	b := BIH{bounds: bounds, tree: []uint32{3 << 30, uint32(count), 0}}
	for i := 0; i < count; i++ {
		b.objs = append(b.objs, uint32(i))
	}
	return b
}
func collisionFixture(t *testing.T, vertices []Vector3, mesh []MeshTriangle) (*VMapManager, *StaticMapTree) {
	t.Helper()
	vm := NewVMapManager(t.TempDir())
	box := boundsOf(vertices...)
	g := GroupModel{Vertices: vertices, Mesh: mesh, vbounds: box, groupTree: leafIndex(box, len(mesh))}
	model := &WorldModel{groupModels: []GroupModel{g}}
	mi := &ModelInstance{spawn: ModelSpawn{name: "fixture", Flags: 1, iScale: 1}}
	mi.initTransform()
	tree := &StaticMapTree{basePath: vm.basePath, treeValues: map[uint32]*ModelInstance{0: mi}, loadedTiles: map[uint32]bool{}}
	tree.addModelToCacheLocked(vm.basePath+"fixture.vmo", model)
	vm.trees[0] = tree
	return vm, tree
}
func wallFixture(t *testing.T) (*VMapManager, *StaticMapTree) {
	return collisionFixture(t, []Vector3{{-5, -3, 0}, {-5, 3, 0}, {-5, 3, 4}, {-5, -3, 4}}, []MeshTriangle{{0, 1, 2}, {0, 2, 3}})
}
func TestMovementCapsuleDetectsThinWallAndBodyWidth(t *testing.T) {
	vm, _ := wallFixture(t)
	ctx := context.Background()
	body := WalkingBody()
	r := vm.SweepBodyContext(ctx, 0, Point3D{}, Point3D{X: 10}, body)
	if r.State != "blocked" || math.Abs(float64(r.Fraction-0.47)) > 0.002 || r.Model != "fixture" || r.Normal.X > -0.9 {
		t.Fatalf("thin wall: %+v", r)
	}
	// A centre ray misses the wall edge, but the body clips it.
	a, b := Point3D{Y: 3.15, Z: 1}, Point3D{X: 10, Y: 3.15, Z: 1}
	if ray := vm.TraceMovementContext(ctx, 0, a, b); ray.State != "clear" {
		t.Fatal(ray)
	}
	a.Z = 0
	b.Z = 0
	if r := vm.SweepBodyContext(ctx, 0, a, b, body); r.State != "blocked" {
		t.Fatal(r)
	}
	a.Y = 4
	b.Y = 4
	if r := vm.SweepBodyContext(ctx, 0, a, b, body); r.State != "clear" {
		t.Fatal(r)
	}
}
func TestMovementCapsuleCeilingOverlapAndScale(t *testing.T) {
	vm, _ := collisionFixture(t, []Vector3{{-20, -20, 1.8}, {20, -20, 1.8}, {20, 20, 1.8}, {-20, 20, 1.8}}, []MeshTriangle{{0, 1, 2}, {0, 2, 3}})
	if r := vm.SweepBodyContext(context.Background(), 0, Point3D{}, Point3D{X: 1}, WalkingBody()); r.State != "blocked" || r.Fraction != 0 {
		t.Fatal(r)
	}
	vm, tree := wallFixture(t)
	mi := tree.treeValues[0]
	mi.spawn.iScale = 2
	mi.spawn.iPos = Vector3{X: 3}
	mi.initTransform()
	if r := vm.SweepBodyContext(context.Background(), 0, Point3D{}, Point3D{X: 20}, WalkingBody()); r.State != "blocked" || math.Abs(float64(r.Position.X-12.7)) > 0.01 {
		t.Fatal(r)
	}
}
func TestMovementCollisionUnavailableIsUnknown(t *testing.T) {
	vm, tree := wallFixture(t)
	delete(tree.modelCache, vm.basePath+"fixture.vmo")
	if r := vm.SweepBodyContext(context.Background(), 0, Point3D{}, Point3D{X: 10}, WalkingBody()); r.State != "unknown" {
		t.Fatal(r)
	}
	vm, tree = wallFixture(t)
	tree.isTiled = true
	box := AABox{Vector3{VMAP_mid - 10, VMAP_mid - 10, -10}, Vector3{VMAP_mid + 10, VMAP_mid + 10, 10}}
	tree.tree = leafIndex(box, 1)
	delete(tree.treeValues, 0)
	if r := vm.SweepBodyContext(context.Background(), 0, Point3D{}, Point3D{X: 10}, WalkingBody()); r.State != "unknown" {
		t.Fatal(r)
	}
	tree.tree = leafIndex(box, 0)
	if r := vm.SweepBodyContext(context.Background(), 0, Point3D{}, Point3D{X: 10}, WalkingBody()); r.State != "clear" {
		t.Fatal(r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := vm.SweepBodyContext(ctx, 0, Point3D{}, Point3D{X: 10}, WalkingBody()); r.State != "unknown" {
		t.Fatal(r)
	}
}
func TestCollisionIndexClippingAndMalformedNodes(t *testing.T) {
	box := AABox{Vector3{-10, -10, -10}, Vector3{10, 10, 10}}
	b := BIH{bounds: box, tree: []uint32{3, math.Float32bits(-1), math.Float32bits(1), 3 << 30, 1, 0, (3 << 30) | 1, 1, 0}, objs: []uint32{7, 9}}
	for _, tc := range []struct {
		lo, hi float32
		count  int
	}{{-5, -2, 1}, {-.5, .5, 0}, {2, 5, 1}, {-5, 5, 2}} {
		q := box
		q.Low.X = tc.lo
		q.High.X = tc.hi
		count := 0
		if err := b.overlap(context.Background(), q, func(uint32) error { count++; return nil }); err != nil || count != tc.count {
			t.Fatalf("%+v count=%d err=%v", tc, count, err)
		}
	}
	b.tree[0] = 999
	if err := b.overlap(context.Background(), box, func(uint32) error { return nil }); err == nil {
		t.Fatal("malformed index accepted")
	}
}
func TestMovementCollisionRealInteriorRoutes(t *testing.T) {
	data := requireACData(t)
	svc := NewPathFindingServiceFromDataDir(data)
	cases := []struct {
		name string
		a, b Point3D
	}{
		{"bridge", Point3D{-8930, 545, 94.715}, Point3D{-8880, 590, 93.402}},
		{"inn", Point3D{-9460, 8, 57}, Point3D{-9472, 34, 64}},
		{"upper", Point3D{-9797, 132, 25}, Point3D{-9807, 117, 24}},
		{"lower", Point3D{-9824, 122, 4}, Point3D{-9843.54, 127.525, 5.3698}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r, err := svc.FindPathContext(ctx, 0, tc.a, tc.b)
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			triangles := 0
			for i := 1; i < len(r.Points); i++ {
				hit := svc.vmapMgr.SweepBodyContext(ctx, 0, r.Points[i-1], r.Points[i], WalkingBody())
				triangles += hit.Triangles
				if hit.State == "unknown" {
					t.Fatalf("segment %d: %+v", i, hit)
				}
				if hit.State == "blocked" {
					t.Logf("navmesh route needs clearance repair at segment %d: %+v", i, hit)
					break
				}
			}
			t.Logf("%d segments, %d narrow-phase triangles, %s", len(r.Points)-1, triangles, time.Since(started))
		})
	}
}

func TestModelSpawnTrimsExtractedTerminator(t *testing.T) {
	var b bytes.Buffer
	for _, v := range []any{uint32(1), uint16(0), uint32(0), [3]float32{}, [3]float32{}, float32(1), uint32(9)} {
		if err := binary.Write(&b, binary.LittleEndian, v); err != nil {
			t.Fatal(err)
		}
	}
	b.WriteString("thing.m2\x00")
	var s ModelSpawn
	if err := s.readFrom(&b); err != nil || s.name != "thing.m2" {
		t.Fatalf("%q %v", s.name, err)
	}
}

func TestPreparedRealInteriorRoutes(t *testing.T) {
	s := NewPathFindingServiceFromDataDir(requireACData(t))
	for _, tc := range []struct {
		name string
		a, b Point3D
	}{
		{"bridge", Point3D{-8930, 545, 94.715}, Point3D{-8880, 590, 93.402}},
		{"road", Point3D{-9466, 74, 57}, Point3D{-9496, 73, 57}},
		{"inn", Point3D{-9460, 8, 57}, Point3D{-9472, 34, 64}},
		{"upper", Point3D{-9797, 132, 25}, Point3D{-9807, 117, 24}},
		{"lower", Point3D{-9824, 122, 4}, Point3D{-9843.54, 127.525, 5.3698}},
		// Northshire routes the deadmines run's agents were refused (2026-10-30)
		{"mine mouth", Point3D{-8700, -125, 87}, Point3D{-8655, -127, 89.7}},           // hillside above Md_Goldmine.wmo's floor
		{"mine road", Point3D{-8785, -162, 82}, Point3D{-8671, -118, 92.2}},            // a tree canopy at ground level
		{"abbey stairs", Point3D{-8912.28, -214.13, 81.96}, Point3D{-8910, -230, 82}},  // 1.05 yd steps in Nsabbey.wmo
		{"abbey fountain", Point3D{-8911.7, -111.1, 82}, Point3D{-8785, -162, 82}},     // past Waterbasin.m2's rim
		{"abbey to vineyard", Point3D{-8946, -131.5, 83.67}, Point3D{-8850, -224, 82}}, // navmesh 1.55 yd over the terrain
		{"road to goldshire", Point3D{-9054.7, -459.4, 72.57}, Point3D{-9460, 32, 57}}, // the same, several times
		// overnight refusals (2026-10-31)
		{"tile seam", Point3D{-10122.321, 1106.5042, 36.423527}, Point3D{-10238, 1010, 34.706173}},           // tiles 177/178 disagree by 0.47 yd at y 1066.67
		{"big tile seam", Point3D{-10510.307, 1046.3115, 60.53798}, Point3D{-9053, 453, 69.477394}},          // 1.16 yd at x -9066.67
		{"tower corner", Point3D{-9568.202, -712.1493, 90.351875}, Point3D{-9564.4, -706.8, 90.628}},         // Tower of Azora: the start on a polygon corner
		{"westfall to stormwind", Point3D{-10289.533, 913.2661, 37.025093}, Point3D{-8835, 608, 59.75}},      // 8192 search nodes ran out
		{"sentinel hill stairs", Point3D{-11213.776, 1174.5902, 96.182884}, Point3D{-11210.4, 1179.4, 93.9}}, // destination 2.8 yd under the navmesh
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			raw, err := s.FindPathContext(ctx, 0, tc.a, tc.b)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			r, err := s.PrepareWalkingContext(ctx, 0, raw, WalkingBody())
			if tc.name == "inn" && err == nil {
				// the shipped navmesh crosses collision at the inn's entrance that a body can't walk through; jumps take it
				t.Logf("inn: %d jumps", len(r.Jumps))
			}
			if err != nil {
				t.Fatalf("prepare: %+v %v", r, err)
			}
			if tc.name == "lower" && r.Replanned {
				// the navmesh runs over the mine cart (about 1 yd up): follow it rather than steer round
				t.Fatal("left the navmesh to avoid the mine cart")
			}

			t.Logf("%d points, repaired=%v, triangles=%d, preparation=%s", len(r.Points), r.Replanned, r.Collision.Triangles, time.Since(start))
			if r.Collision.State != "clear" || r.Points[0] != tc.a {
				t.Fatal(r)
			}
		})
	}
}

func TestMovementRayKeepsSharedTriangleEdges(t *testing.T) {
	vm, _ := collisionFixture(t, []Vector3{{-30, -30, 0}, {0, -30, 0}, {0, 0, 0}, {-30, 0, 0}}, []MeshTriangle{{0, 1, 2}, {0, 2, 3}})
	for i := 0; i < 100; i++ {
		v := float32(12) + float32(i)/17
		hit := vm.TraceMovementContext(context.Background(), 0, Point3D{v, v, 1.5}, Point3D{v, v, -1.5})
		if hit.State != "blocked" || math.Abs(float64(hit.Position.Z)) > .001 {
			t.Fatalf("seam %g: %+v", v, hit)
		}
	}
}

// Northshire's vineyard fences (Elwynnwoodfence01.m2, 1.53 yd) are climbed by the navmesh but not by a step: routes
// the deadmines run's agents were refused cross them with a jump on the client's parabola, landing on ground (not on
// the rail). Routes a step or a detour walks have no jump.
func TestPreparedRealJumps(t *testing.T) {
	s := NewPathFindingServiceFromDataDir(requireACData(t))
	for _, tc := range []struct {
		name  string
		a, b  Point3D
		jumps bool
	}{
		{"vineyard fence", Point3D{-9056.294, -400.1895, 73.01769}, Point3D{-9075, -378, 73.77846}, true},
		{"vineyard fence east", Point3D{-9007.553, -332.25662, 74.74897}, Point3D{-9026.81, -331.989, 73.7}, true},
		{"vineyard to abbey", Point3D{-9113.123, -338.90445, 73.33916}, Point3D{-8948, -132, 84}, true},
		{"fence downhill", Point3D{-9398.846, 100.93745, 58.88109}, Point3D{-9758, 192, 51.2}, true}, // lands below take-off
		{"abbey stairs", Point3D{-8912.28, -214.13, 81.96}, Point3D{-8910, -230, 82}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			raw, err := s.FindPathContext(ctx, 0, tc.a, tc.b)
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.PrepareWalkingContext(ctx, 0, raw, WalkingBody())
			if err != nil {
				t.Fatalf("prepare: %+v %v", r.Collision, err)
			}
			if (len(r.Jumps) > 0) != tc.jumps {
				t.Fatalf("%d jumps", len(r.Jumps))
			}
			for _, j := range r.Jumps {
				a, b := r.Points[j.Start], r.Points[j.End]
				for k := j.Start + 1; k < j.End; k++ { // the feet on the parabola
					d := dist2D(a, r.Points[k]) / j.Speed
					if want := a.Z + JumpVelocity*d - Gravity*d*d/2; abs32(r.Points[k].Z-want) > 0.01 {
						t.Fatalf("arc[%d] z %.2f, the client's jump is at %.2f", k, r.Points[k].Z, want)
					}
				}
				if !s.footing(ctx, 0, Point3D{b.X, b.Y, b.Z - 0.02}, WalkingBody().Radius) {
					t.Errorf("landed where a body can't stand: %+v", b)
				}
				t.Logf("jump %.1f yd from %.1f %.1f %.2f to %.1f %.1f %.2f", dist2D(a, b), a.X, a.Y, a.Z, b.X, b.Y, b.Z)
			}
		})
	}
}

// Where the collision data refuses what the navmesh walks for a short stretch, the navmesh wins: Goldshire inn's
// 1.2 yd strip across its front (1.82 yd up from the main room), a character left hovering beside its bar. A start on a
// polygon's edge (the inn's doorstep) passes the surface check.
func TestPreparedRealNavmeshWins(t *testing.T) {
	s := NewPathFindingServiceFromDataDir(requireACData(t))
	for _, tc := range []struct {
		name string
		a, b Point3D
		wins bool
	}{
		{"inn main room out", Point3D{-9466, 24.9, 56.4}, Point3D{-9462, 33, 57}, false}, // round the strip
		{"inn bar edge out", Point3D{-9461.119, 18.562378, 57.265217}, Point3D{-9460.3, 31.9, 57}, true},
		{"inn doorstep in", Point3D{-9464.262, 32.798706, 56.98606}, Point3D{-9462.7, 16.2, 57}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			raw, err := s.FindPathContext(ctx, 0, tc.a, tc.b)
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.PrepareWalkingContext(ctx, 0, raw, WalkingBody())
			if err != nil {
				t.Fatalf("prepare: %+v %v", r.Collision, err)
			}
			if (r.NavmeshWins > 0) != tc.wins {
				t.Fatalf("navmesh won %d stretches, %d jumps", r.NavmeshWins, len(r.Jumps))
			}
			t.Logf("navmesh won %d, %d jumps, %d points", r.NavmeshWins, len(r.Jumps), len(r.Points))
		})
	}
}
