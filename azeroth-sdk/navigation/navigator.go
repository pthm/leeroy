// Package navigation is the Navigator interface sessions move with: paths, ground heights, floors and line of
// sight, backed by the embedded pathfinding package (the server's mmaps, vmaps and maps from the data dir).
// NewRemoteNavigator is a stub that always fails; Config.PathfindingAddress has no working backend.
package navigation

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/pathfinding"
)

// Point3D is a 3D position in game world coordinates.
type Point3D struct {
	X, Y, Z float32
}

// DistanceTo computes 3D distance.
func (p Point3D) DistanceTo(o Point3D) float32 {
	dx := p.X - o.X
	dy := p.Y - o.Y
	dz := p.Z - o.Z
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}

// DistanceTo2D computes 2D distance (ignoring Z).
func (p Point3D) DistanceTo2D(o Point3D) float32 {
	dx := p.X - o.X
	dy := p.Y - o.Y
	return float32(math.Sqrt(float64(dx*dx + dy*dy)))
}

// PathResult holds the result of a pathfinding query.
type PathResult struct {
	Found   bool
	Partial bool // a known prefix, never completion of the requested destination
	Points  []Point3D
	surface *pathfinding.PathResult // Detour references stay inside navigation

}

// Navigator is the interface for pathfinding.
type Navigator interface {
	// FindPath computes a path from start to dest on the given map.
	FindPath(mapID uint32, start, dest Point3D) (*PathResult, error)

	// FindPathContext cancels loading, lock waits, search and smoothing cooperatively.
	FindPathContext(ctx context.Context, mapID uint32, start, dest Point3D) (*PathResult, error)

	// FindRandomPath finds a random walkable point near center.
	FindRandomPath(mapID uint32, center Point3D, radius float32) (*PathResult, error)

	// GetHeight returns the ground height (Z) at the given point using the pathfinding data (maps + baked vmaps).
	GetHeight(mapID uint32, x, y, z float32) (float32, bool)

	// GetTerrainHeight returns terrain-only height (no vmaps) if available.
	GetTerrainHeight(mapID uint32, x, y float32) (float32, bool)

	// Close releases resources.
	Close()
}

// ============================================================
// Embedded navigator (uses mmap files directly)
// ============================================================

var (
	embeddedMu   sync.Mutex
	embeddedSvcs = make(map[string]*pathfinding.PathFindingService)
)

func embeddedCacheKey(dataDir string) string {
	return dataDir
}

func getOrCreateEmbeddedService(dataDir string) *pathfinding.PathFindingService {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	key := embeddedCacheKey(dataDir)
	if svc, ok := embeddedSvcs[key]; ok {
		return svc
	}

	var svc *pathfinding.PathFindingService
	if dataDir == "" {
		svc = pathfinding.NewPathFindingServiceWithMMapMgr(pathfinding.NewMMapManager(""), nil, nil)
	} else {
		svc = pathfinding.NewPathFindingServiceFromDataDir(dataDir)
	}

	embeddedSvcs[key] = svc
	return svc
}

type embeddedNavigator struct {
	svc *pathfinding.PathFindingService
}

// NewEmbeddedNavigator creates a navigator using a single data directory root
// (containing mmaps/, maps/, vmaps/ subdirectories).
// All navigators for the same dataDir share a single underlying service.
func NewEmbeddedNavigator(dataDir string) Navigator {
	return &embeddedNavigator{
		svc: getOrCreateEmbeddedService(dataDir),
	}
}

func (n *embeddedNavigator) FindPath(mapID uint32, start, dest Point3D) (*PathResult, error) {
	return n.FindPathContext(context.Background(), mapID, start, dest)
}
func (n *embeddedNavigator) FindPathContext(ctx context.Context, mapID uint32, start, dest Point3D) (*PathResult, error) {
	result, err := n.svc.FindPathContext(ctx, mapID, pathfinding.Point3D{X: start.X, Y: start.Y, Z: start.Z},
		pathfinding.Point3D{X: dest.X, Y: dest.Y, Z: dest.Z})
	if err != nil {
		return nil, err
	}
	return convertResult(result), nil
}

func (n *embeddedNavigator) FindRandomPath(mapID uint32, center Point3D, radius float32) (*PathResult, error) {
	result, err := n.svc.FindRandomPath(mapID, pathfinding.Point3D{X: center.X, Y: center.Y, Z: center.Z}, radius)
	if err != nil {
		return nil, err
	}
	return convertResult(result), nil
}

func (n *embeddedNavigator) GetHeight(mapID uint32, x, y, z float32) (float32, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	return n.svc.GetHeightContext(ctx, mapID, x, y, z)
}

// InLineOfSight: line of sight between two points against world models (buildings, caves), as the server checks spells.
func (n *embeddedNavigator) InLineOfSight(mapID uint32, a, b Point3D) (bool, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	return n.InLineOfSightContext(ctx, mapID, a, b)
}

func (n *embeddedNavigator) GetTerrainHeight(mapID uint32, x, y float32) (float32, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	return n.svc.GetTerrainHeightContext(ctx, mapID, x, y)
}

func (n *embeddedNavigator) FloorHeight(mapID uint32, x, y, hint float32) (float32, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	return n.svc.FloorHeightContext(ctx, mapID, x, y, hint)
}

func (n *embeddedNavigator) GetWaterLevel(mapID uint32, x, y float32) (float32, bool) {
	return n.svc.GetWaterLevel(mapID, x, y)
}

func (n *embeddedNavigator) Close() {}

func convertResult(r *pathfinding.PathResult) *PathResult {
	if r == nil {
		return &PathResult{Found: false}
	}
	unusable := pathfinding.PathfindNopath | pathfinding.PathfindNotUsingPath
	found := r.Type&unusable == 0 && len(r.Points) > 0
	points := make([]Point3D, len(r.Points))
	for i, p := range r.Points {
		points[i] = Point3D{X: p.X, Y: p.Y, Z: p.Z}
	}
	return &PathResult{Found: found, Partial: r.Type&(pathfinding.PathfindIncomplete|pathfinding.PathfindShort|pathfinding.PathfindFarFromPolyStart|pathfinding.PathfindFarFromPolyEnd) != 0, Points: points, surface: r.Clone()}
}

// ============================================================
// Remote navigator (gRPC client) - stubbed / optional
// ============================================================

// NewRemoteNavigator creates a navigator that connects to a remote pathfinding service.
// In this extraction, remote is deprioritized in favor of embedded.
// Full gRPC support (requiring gen/pb + grpc deps) can be restored behind a build tag
// or separate optional package if needed for bridge scenarios.
func NewRemoteNavigator(address string) (Navigator, error) {
	return nil, fmt.Errorf("remote pathfinding (gRPC) is optional and not built-in for core use; use NewEmbeddedNavigator(dataDir) instead")
}

func (n *embeddedNavigator) InLineOfSightContext(ctx context.Context, m uint32, a, b Point3D) (bool, bool) {
	return n.svc.InLineOfSightContext(ctx, m, a.X, a.Y, a.Z, b.X, b.Y, b.Z)
}

// Context variants share the caller's whole-operation budget during preparation.
func (n *embeddedNavigator) GetHeightContext(ctx context.Context, m uint32, x, y, z float32) (float32, bool) {
	return n.svc.GetHeightContext(ctx, m, x, y, z)
}
func (n *embeddedNavigator) FloorHeightContext(ctx context.Context, m uint32, x, y, z float32) (float32, bool) {
	return n.svc.FloorHeightContext(ctx, m, x, y, z)
}
func (n *embeddedNavigator) GetTerrainHeightContext(ctx context.Context, m uint32, x, y float32) (float32, bool) {
	return n.svc.GetTerrainHeightContext(ctx, m, x, y)
}

func (r *PathResult) Clone() *PathResult {
	if r == nil {
		return nil
	}
	q := *r
	q.Points = slices.Clone(r.Points)
	q.surface = r.surface.Clone()
	return &q
}
