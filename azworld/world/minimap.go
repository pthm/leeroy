package world

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// The game client's own map data (extracted by cmd/clientdata into data/): minimap/<map>/<gx>_<gy>.png, one
// 256x256 image per grid square (north up, west left), and roads/<map>/<gx>_<gy>.bin, 128x128 bytes per square:
// the share of each cell covered by road textures (0-255).

// float64(...) around products stops arm64 fusing them into FMA, which shifts exact tile edges by a pixel.
func gridOf(v float64) int { return int(math.Floor(32 - v/GridSize)) }

func clientFile(kind string, m uint32, gx, gy int, ext string) string {
	return filepath.Join(DataDir(), kind, fmt.Sprint(m), fmt.Sprintf("%d_%d.%s", gx, gy, ext))
}

// HasClientData reports whether the client's minimap was extracted.
func HasClientData() bool {
	_, err := os.Stat(filepath.Join(DataDir(), "minimap"))
	return err == nil
}

// MinimapTile is a grid square's minimap PNG, or nil.
func MinimapTile(m uint32, gx, gy int) []byte {
	b, err := os.ReadFile(clientFile("minimap", m, gx, gy, "png"))
	if err != nil {
		return nil
	}
	return b
}

var (
	tileMu sync.Mutex
	tiles  = map[[3]int]image.Image{}
	roads  = map[[3]int][]byte{}
)

// MinimapAt is the minimap colour at a world point; ok false where there's no tile.
func MinimapAt(m uint32, x, y float64) (rgb [3]uint8, ok bool) {
	gx, gy := gridOf(x), gridOf(y)
	k := [3]int{int(m), gx, gy}
	tileMu.Lock()
	t, seen := tiles[k]
	if !seen {
		if b := MinimapTile(m, gx, gy); b != nil {
			t, _ = png.Decode(bytes.NewReader(b))
		}
		tiles[k] = t
	}
	tileMu.Unlock()
	if t == nil {
		return rgb, false
	}
	w, h := t.Bounds().Dx(), t.Bounds().Dy()
	r := min(h-1, int((float64(GridSize*float64(32-gx))-x)/GridSize*float64(h)))
	c := min(w-1, int((float64(GridSize*float64(32-gy))-y)/GridSize*float64(w)))
	cr, cg, cb, _ := t.At(t.Bounds().Min.X+c, t.Bounds().Min.Y+r).RGBA()
	return [3]uint8{uint8(cr >> 8), uint8(cg >> 8), uint8(cb >> 8)}, true
}

// RoadAt is how much of the ground at a world point is road (0-255; 0 without client data).
func RoadAt(m uint32, x, y float64) uint8 {
	gx, gy := gridOf(x), gridOf(y)
	k := [3]int{int(m), gx, gy}
	tileMu.Lock()
	t, seen := roads[k]
	if !seen {
		t, _ = os.ReadFile(clientFile("roads", m, gx, gy, "bin"))
		roads[k] = t
	}
	tileMu.Unlock()
	const n = 128
	if len(t) < n*n {
		return 0
	}
	r := min(n-1, int((float64(GridSize*float64(32-gx))-x)/GridSize*n))
	c := min(n-1, int((float64(GridSize*float64(32-gy))-y)/GridSize*n))
	return t[r*n+c]
}
