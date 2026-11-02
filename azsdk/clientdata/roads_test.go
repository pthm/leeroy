package clientdata

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Hellfire's Path of Glory is painted with bones, not a road texture: it's a road all the same.
func TestRoadsPathOfGlory(t *testing.T) {
	src := Dir("../../data/client")
	if !src.Has() {
		t.Skip("no extracted client files in ../../data/client")
	}
	out := t.TempDir()
	if _, err := Roads(src, 530, out); err != nil {
		t.Fatal(err)
	}
	const size = 533.33333
	x, y := -215.0, 1800.0 // on the road, between the Dark Portal and Hellfire Citadel
	gx, gy := int(math.Floor(32-x/size)), int(math.Floor(32-y/size))
	b, err := os.ReadFile(filepath.Join(out, "530", itoa(gx)+"_"+itoa(gy)+".bin"))
	if err != nil {
		t.Fatal(err)
	}
	row, col := int((size*float64(32-gx)-x)/(size/RoadCells)), int((size*float64(32-gy)-y)/(size/RoadCells))
	best := 0
	for r := row - 2; r <= row+2; r++ {
		for c := col - 2; c <= col+2; c++ {
			best = max(best, int(b[r*RoadCells+c]))
		}
	}
	if best < 100 {
		t.Fatalf("Path of Glory at (%v, %v): road cover %d", x, y, best)
	}
}

func itoa(n int) string { return string(rune('0'+n/10)) + string(rune('0'+n%10)) }
