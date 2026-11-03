package run

import (
	"math"
	"testing"

	"github.com/pthm/leeroy/internal/bench/config"
)

func TestSpotSpread(t *testing.T) {
	s := &config.Scenario{Agents: []config.Agent{{Char: "A"}, {Char: "B"}}}
	s.Start = &config.Place{X: 100, Y: 200, Z: 10, Map: 9999} // no map files: the start's height
	s.Spread = 8
	ax, ay, _, ao := spot(s, "A")
	bx, by, _, bo := spot(s, "B")
	if math.Hypot(ax-bx, ay-by) != 8 || math.Hypot(ax-100, ay-200) != 4 {
		t.Fatalf("not 8 yd apart around the start: (%v,%v) (%v,%v)", ax, ay, bx, by)
	}
	// each faces the other: orientation is the angle from +x, counterclockwise
	face := func(x, y, o, tx, ty float64) bool {
		return math.Abs(math.Remainder(math.Atan2(ty-y, tx-x)-o, 2*math.Pi)) < 1e-9
	}
	if !face(ax, ay, ao, bx, by) || !face(bx, by, bo, ax, ay) {
		t.Fatalf("not face to face: A %v, B %v", ao, bo)
	}
	s.Spread = 0
	if x, y, _, _ := spot(s, "B"); x != 100 || y != 200 {
		t.Fatal("no spread: the start")
	}
}

func TestSpotPairs(t *testing.T) {
	a, b := &config.Place{X: 0, Y: 0, Z: 1, Map: 9999}, &config.Place{X: 500, Y: 0, Z: 1, Map: 9999}
	s := &config.Scenario{Agents: []config.Agent{{Char: "A", Start: a}, {Char: "B", Start: a}, {Char: "C", Start: b}, {Char: "D", Start: b}}}
	s.Spread = 8
	cx, cy, _, _ := spot(s, "C")
	dx, dy, _, _ := spot(s, "D")
	if math.Hypot(cx-dx, cy-dy) != 8 || math.Hypot(cx-500, cy) != 4 {
		t.Fatalf("C and D aren't 8 yd apart around their own spot: (%v,%v) (%v,%v)", cx, cy, dx, dy)
	}
}
