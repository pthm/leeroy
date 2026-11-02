package pathfinding

import (
	"os"
	"testing"
)

// The Bravery's display (3015) is the ship model; its outline is about 100 yards long, bow at -x.
func TestShipOutline(t *testing.T) {
	m, err := GameObjectModels("../../data/vmaps")
	if os.IsNotExist(err) {
		t.Skip("no vmaps in ../../data")
	}
	if err != nil || m[3015] != "Transportship.wmo" {
		t.Fatalf("display 3015: %q %v", m[3015], err)
	}
	hull, err := ModelOutline("../../data/vmaps/" + m[3015] + ".vmo")
	if err != nil {
		t.Fatal(err)
	}
	lo, hi := float32(0), float32(0)
	for _, p := range hull {
		lo, hi = min(lo, p[0]), max(hi, p[0])
	}
	if len(hull) < 3 || hi-lo < 90 || hi-lo > 110 || -lo < hi {
		t.Fatalf("outline %d points, x %.1f..%.1f", len(hull), lo, hi)
	}
}
