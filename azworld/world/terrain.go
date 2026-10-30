package world

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"sync"

	"github.com/pthm/leeroy/azeroth-sdk/pathfinding"
)

// GridSize is the yards per map grid square (one .map file, one minimap tile).
const GridSize = 1600.0 / 3

var terrain = sync.OnceValue(func() *pathfinding.TerrainManager {
	return pathfinding.NewTerrainManager(filepath.Join(DataDir(), "maps"))
})

// Sample is the ground height (NaN off the map), water surface (NaN if none) and area id at a world point, from the
// server's map files. Terrain only: building floors live in vmaps.
func Sample(mapID uint32, x, y float64) (height, water float64, area uint16) {
	t := terrain()
	height, water = math.NaN(), math.NaN()
	if h, ok := t.GetGridHeight(mapID, float32(x), float32(y)); ok {
		height = float64(h)
	}
	if w, ok := t.GetWaterLevel(mapID, float32(x), float32(y)); ok {
		water = float64(w)
	}
	return height, water, t.GetArea(mapID, float32(x), float32(y))
}

// sampleRouteTerrain propagates cancellation while retaining Sample's unknown
// terrain convention for unavailable files.
func sampleRouteTerrain(ctx context.Context, mapID uint32, x, y float64) (float64, float64, uint16) {
	h, w, a, err := terrain().GetGridSampleContext(ctx, mapID, float32(x), float32(y))
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		panic(err)
	}
	if err != nil {
		return math.NaN(), math.NaN(), 0
	}
	return float64(h), float64(w), a
}
