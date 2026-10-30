package pathfinding

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/o0olele/detour-go/detour"
)

func TestSearchCancellationStopsBatches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	_, err := runSlicedSearch(ctx, detour.DT_IN_PROGRESS, func(n int, _ *int) detour.DtStatus {
		calls++
		if n > 32 {
			t.Fatalf("unbounded search slice: %d", n)
		}
		cancel()
		return detour.DT_IN_PROGRESS
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("work continued after cancel: %v calls=%d", err, calls)
	}
}
func TestMeshLockWaitCancellation(t *testing.T) {
	var mu sync.RWMutex
	mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- lockContext(ctx, mu.TryRLock) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled lock waiter stuck")
	}
	mu.Unlock()
	if err := lockContext(context.Background(), mu.TryRLock); err != nil {
		t.Fatal(err)
	}
	mu.RUnlock()
}
func TestTileLoadCancellationDoesNotPoisonCache(t *testing.T) {
	m := NewMMapManager(t.TempDir())
	md := &mapData{loadedTiles: map[uint32]bool{}}
	md.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := m.loadTileContext(ctx, md, 0, 1, 1)
	md.mu.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) || len(md.loadedTiles) != 0 {
		t.Fatalf("cancelled load cached absence: %v %v", err, md.loadedTiles)
	}
	if err := m.loadTileContext(context.Background(), md, 0, 1, 1); err != nil {
		t.Fatal(err)
	}
	if !md.loadedTiles[packTileID(1, 1)] {
		t.Fatal("subsequent load could not proceed")
	}
}

type cancellingReader struct {
	cancel context.CancelFunc
	reads  int
}

func (r *cancellingReader) Read(p []byte) (int, error) { r.reads++; r.cancel(); return len(p), nil }
func TestTileReadChecksCancellationBetweenChunks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &cancellingReader{cancel: cancel}
	n, err := io.ReadFull(contextReader{ctx, source}, make([]byte, 128<<10))
	if n != 64<<10 || !errors.Is(err, context.Canceled) || source.reads != 1 {
		t.Fatalf("uncancelled read: n=%d reads=%d err=%v", n, source.reads, err)
	}
}
func TestOversizedTileRejectedBeforeAllocation(t *testing.T) {
	dir := t.TempDir()
	header := make([]byte, mmapTileHeaderSize)
	binary.LittleEndian.PutUint32(header, mmapMagic)
	binary.LittleEndian.PutUint32(header[8:], mmapVersion)
	binary.LittleEndian.PutUint32(header[12:], maxTileBytes+1)
	if err := os.WriteFile(filepath.Join(dir, "0000101.mmtile"), header, 0600); err != nil {
		t.Fatal(err)
	}
	err := NewMMapManager(dir).loadTileContext(context.Background(), &mapData{loadedTiles: map[uint32]bool{}}, 0, 1, 1)
	if err == nil || !strings.Contains(err.Error(), "tile exceeds") {
		t.Fatalf("size limit: %v", err)
	}
}
func TestPathCancellationAndBoundsBeforeIO(t *testing.T) {
	svc := NewPathFindingService("does-not-exist", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.FindPathContext(ctx, 0, Point3D{}, Point3D{X: 10}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := svc.FindPathContext(context.Background(), 0, Point3D{}, Point3D{X: MaxLegYards + 1}); !errors.Is(err, ErrQueryLimit) {
		t.Fatal(err)
	}
}
