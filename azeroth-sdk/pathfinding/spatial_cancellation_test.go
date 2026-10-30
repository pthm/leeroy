package pathfinding

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSpatialCancelledLocksAndMissingModelStayUnknown(t *testing.T) {
	vm := NewVMapManager(t.TempDir())
	vm.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if _, ok := vm.InLineOfSightContext(ctx, 0, Vector3{}, Vector3{X: 1}); ok {
		t.Fatal("cancelled sight is known")
	}
	cancel()
	vm.mu.Unlock()
	tree := &StaticMapTree{basePath: t.TempDir() + "/"}
	mi := &ModelInstance{spawn: ModelSpawn{name: "absent"}}
	for i := 0; i < 2; i++ {
		if _, ok := tree.modelForInstanceContext(context.Background(), mi); ok {
			t.Fatal("absent model accepted")
		}
		if mi.spawn.name != "absent" {
			t.Fatal("missing model erased from future checks")
		}
	}
	tree.loadMu.Lock()
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	if err := tree.loadMapTileContext(ctx, 0, 0); err == nil {
		t.Fatal("cancelled tile lock")
	}
	cancel()
	tree.loadMu.Unlock()
}
func TestSpatialReadBoundsAndTriangleCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.vmo")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate((64 << 20) + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if file, _, err := openVMapContext(context.Background(), path); err == nil {
		file.Close()
		t.Fatal("oversized file accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	group := GroupModel{Vertices: []Vector3{{}}, Mesh: make([]MeshTriangle, 1024), vbounds: AABox{Low: Vector3{-1, -1, -1}, High: Vector3{1, 1, 1}}}
	if h := group.getHeightContext(ctx, NewRay(Vector3{}, Vector3{Z: 1}), 10); h != math.MaxFloat32 {
		t.Fatal("cancelled intersection", h)
	}
}
func TestSpatialContextRealDataMatchesHeightAndSight(t *testing.T) {
	data := requireACData(t)
	s := NewPathFindingServiceFromDataDir(data)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	x, y, z := float32(-8980.235), float32(-93.89123), float32(85.5113)
	h, ok := s.GetHeightContext(ctx, 0, x, y, z)
	old, known := s.GetHeight(0, x, y, z)
	if ok != known || (ok && math.Abs(float64(h-old)) > 0.01) {
		t.Fatalf("height %v/%v vs %v/%v", h, ok, old, known)
	}
	sight, known := s.InLineOfSightContext(ctx, 0, x, y, z+2, x+1, y, z+2)
	oldSight, oldKnown := s.InLineOfSight(0, x, y, z+2, x+1, y, z+2)
	if sight != oldSight || known != oldKnown {
		t.Fatal("LOS semantics changed")
	}
}
