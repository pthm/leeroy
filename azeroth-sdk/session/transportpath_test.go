package session

import (
	"math"
	"os"
	"testing"
)

// The Bravery (path 967, speed 30, acceleration 1) where the server put it at these path progresses (mod-recorder's
// transport events): leaving Stormwind, docked there, crossing to Kalimdor.
func TestTransportPathMatchesServer(t *testing.T) {
	if _, err := os.Stat("../../data/dbc/TaxiPathNode.dbc"); err != nil {
		t.Skip("no game data in ../../data")
	}
	tp := newTransportPath(loadPathNodes("../../data")[967], 30, 1)
	if tp == nil || tp.period != 234433 {
		t.Fatalf("period: %+v", tp)
	}
	for _, s := range []struct {
		pp   uint32
		m    uint32
		x, y float64
	}{{1000, 0, -9008.63, 1412.4}, {21050, 0, -8677.84, 1333.61}, {41078, 0, -8650.72, 1346.05}, {81123, 0, -8650.72, 1346.05},
		{101157, 0, -8587.18, 1381.05}, {121185, 1, 6209.21, 697.563}} {
		p := tp.at(s.pp)
		if p.mapID != s.m || math.Hypot(p.x-s.x, p.y-s.y) > 0.5 {
			t.Errorf("at %d: map %d (%.2f, %.2f), server map %d (%.2f, %.2f)", s.pp, p.mapID, p.x, p.y, s.m, s.x, s.y)
		}
	}
	if p := tp.at(61107); p.moving || p.stop < 0 {
		t.Errorf("docked at Stormwind: %+v", p)
	}
}
