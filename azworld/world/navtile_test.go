package world

import (
	"bytes"
	"image/png"
	"os"
	"testing"
)

// The navmesh over Goldshire's inn: the tile has walkable pixels, the inn's ground floor drawn opaque (covered by its
// upper storey), and nothing past the finest level.
func TestNavTile(t *testing.T) {
	if _, err := os.Stat(DataDir() + "/mmaps"); err != nil {
		t.Skip("no mmaps")
	}
	b := NavTile(0, 49, 31, 3, 5, 7)
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	walk, up := 0, 0
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			r, _, _, a := img.At(x, y).RGBA()
			if a > 0 {
				walk++
			}
			if a>>8 >= 230 && r>>8 > 60 { // opaque: a covered floor, or an edge or outline
				up++
			}
		}
	}
	t.Logf("%d walkable pixels, %d opaque", walk, up)
	if walk < 10000 || up == 0 {
		t.Fatal("want the ground and the inn's floors")
	}
	if NavTile(0, 49, 31, 5, 0, 0) != nil {
		t.Fatal("no level past 4")
	}
	if len(os.Getenv("NAV_SHOT")) > 0 {
		os.WriteFile(os.Getenv("NAV_SHOT"), b, 0o644)
	}
}
