package pathfinding

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func flatTerrainFixture() []byte {
	b := make([]byte, 80)
	le := binary.LittleEndian
	for offset, v := range map[int]uint32{0: mapMagicUint, 4: mapVersion, 12: 56, 16: 8, 20: 40, 24: 16, 28: 64, 32: 16,
		40: mapHeightMagic, 44: mapHeightNoHeight, 48: math.Float32bits(42), 52: math.Float32bits(42),
		56: mapAreaMagic, 64: mapLiquidMagic, 76: math.Float32bits(50)} {
		le.PutUint32(b[offset:], v)
	}
	le.PutUint16(b[60:], 1)
	le.PutUint16(b[62:], 777)
	b[68], b[69] = 3, 1
	return b
}
func TestTerrainCancelledLockDoesNotPoisonCache(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "0003232.map"), flatTerrainFixture(), 0600); err != nil {
		t.Fatal(err)
	}
	tm := NewTerrainManager(dir)
	tm.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, _, _, err := tm.GetGridSampleContext(ctx, 0, 0, 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
	if len(tm.grids) != 0 {
		t.Fatal("cancelled request cached a grid")
	}
	tm.mu.Unlock()
	h, w, a, err := tm.GetGridSampleContext(context.Background(), 0, 0, 0)
	if err != nil || h != 42 || w != 50 || a != 777 {
		t.Fatalf("sample: %v %v %v %v", h, w, a, err)
	}
	oldH, ok := tm.GetGridHeight(0, 0, 0)
	oldW, wet := tm.GetWaterLevel(0, 0, 0)
	if !ok || !wet || h != oldH || w != oldW || a != tm.GetArea(0, 0, 0) {
		t.Fatal("combined sample changed terrain semantics")
	}
}
func TestTerrainReadBoundsAndCancelledDecode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.map")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxTerrainFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := readTerrainFile(context.Background(), path); err == nil {
		t.Fatal("oversized file accepted")
	}
	if _, err := readTerrainFile(context.Background(), t.TempDir()); err == nil {
		t.Fatal("directory accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := decodeGridHeights(ctx, flatTerrainFixture(), "fixture"); !errors.Is(err, context.Canceled) {
		t.Fatalf("decode ignored cancellation: %v", err)
	}
}
func TestTerrainContextSampleMatchesRealData(t *testing.T) {
	dir := requireACData(t)
	tm := NewTerrainManager(filepath.Join(dir, "maps"))
	for _, p := range []struct {
		m    uint32
		x, y float32
	}{{0, -8873.48, -110.27}, {1, 1568, -4405.87}, {530, -250, 944}, {571, 5804, 624}} {
		h, w, a, err := tm.GetGridSampleContext(context.Background(), p.m, p.x, p.y)
		if err != nil {
			t.Fatal(err)
		}
		oldH, ok := tm.GetGridHeight(p.m, p.x, p.y)
		oldW, wet := tm.GetWaterLevel(p.m, p.x, p.y)
		if ok && h != oldH || !ok && !math.IsNaN(float64(h)) || wet && w != oldW || !wet && !math.IsNaN(float64(w)) || a != tm.GetArea(p.m, p.x, p.y) {
			t.Fatalf("sample changed on map %d", p.m)
		}
	}
}
