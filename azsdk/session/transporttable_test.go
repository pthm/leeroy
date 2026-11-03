package session

import (
	"math"
	"testing"
)

// The stored routines: every transport has two stops or more with a wait point, and an elevator's or tram's position
// follows its animation from where it's spawned, rotated by its path's rotation.
func TestTransportTable(t *testing.T) {
	if len(transportTable) < 40 {
		t.Fatalf("%d transports", len(transportTable))
	}
	for _, tt := range transportTable {
		if len(tt.Stops) < 2 {
			t.Errorf("%d %s: %d stops", tt.Entry, tt.Name, len(tt.Stops))
		}
		for _, s := range tt.Stops {
			if s.Wait == [3]float32{} {
				t.Errorf("%d %s: a stop without a wait point", tt.Entry, tt.Name)
			}
		}
	}
	// a tram car turned round (path rotation π) runs its offsets the other way
	car := tableTransport{Spawn: [4]float32{0, 2500, 7, 0}, PathRot: math.Pi, Anim: [][4]float32{{0, 0, 0, 0}, {1000, 0, 100, 0}, {2000, 0, 0, 0}}}
	if p := car.staticAt(500); math.Abs(float64(p[1])-2450) > 0.01 || math.Abs(float64(p[0])) > 0.01 {
		t.Errorf("turned car halfway out: %v, want y 2450", p)
	}
	if p := car.staticAt(2500); math.Abs(float64(p[1])-2450) > 0.01 { // loops
		t.Errorf("turned car a loop on: %v, want y 2450", p)
	}
	// a pause across the loop's end is one stop; the bot sees it docked on both sides of the wrap
	lift := tableTransport{Anim: [][4]float32{{0, 0, 0, 0}, {3500, 0, 0, 0}, {7000, 0, 0, 96}, {11833, 0, 0, 96}, {15167, 0, 0, 0}, {16667, 0, 0, 0}}}
	if ps := pauses(lift.Anim); len(ps) != 2 || ps[0] != [2]uint32{15167, 3500} || ps[1] != [2]uint32{7000, 11833} {
		t.Errorf("pauses %v, want [[15167 3500] [7000 11833]]", ps)
	}
}
