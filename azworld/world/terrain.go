package world

import (
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
