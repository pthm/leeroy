package client

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

func TestSplinePath(t *testing.T) {
	// linear: start (0,0,0), dest (10,0,0), one middle point (5,4,-2) packed as middle - point
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, [3]float32{10, 0, 0})
	pack := func(x, y, z float32) uint32 {
		return uint32(int32(x/0.25))&0x7FF | (uint32(int32(y/0.25))&0x7FF)<<11 | (uint32(int32(z/0.25))&0x3FF)<<22
	}
	binary.Write(&b, binary.LittleEndian, pack(0, -4, 2)) // (5-5, 0-4, 0-(-2))
	pts := readSplinePath(bytes.NewReader(b.Bytes()), 0, 2, [3]float32{0, 0, 0})
	if len(pts) != 3 || pts[1] != [3]float32{5, 4, -2} || pts[2] != [3]float32{10, 0, 0} {
		t.Fatalf("linear: %v", pts)
	}
	var s selfSpline
	s.set([][3]float32{{0, 0, 0}, {10, 0, 0}, {10, 10, 0}}, 2*time.Second)
	s.start = time.Now().Add(-time.Second) // halfway: 10 of 20 yards
	if p, done, ok := s.at(); !ok || done || p[0] < 9.5 || p[1] > 0.5 {
		t.Fatalf("halfway: %v", p)
	}
	s.start = time.Now().Add(-3 * time.Second)
	if p, done, _ := s.at(); !done || p != [3]float32{10, 10, 0} {
		t.Fatalf("end: %v %v", p, done)
	}
}

// A corpse is "dead", not "rooted" (the server roots it); a released ghost walks.
func TestMovementLockDead(t *testing.T) {
	w := NewWorldClient("u", nil, func(string, ...interface{}) {})
	w.charGUID = 5
	me := &WorldObject{GUID: 5, Values: map[uint16]uint32{UnitFieldHealth: 0, UnitFieldMaxHealth: 700}}
	w.objects[5] = me
	w.rooted = true
	if got := w.MovementLock(); got != "dead" {
		t.Fatalf("corpse: %q, want dead", got)
	}
	me.Values[UnitFieldHealth], me.Values[playerFlags] = 1, playerFlagGhost
	w.rooted = false
	if got := w.MovementLock(); got != "" {
		t.Fatalf("ghost: %q, want free", got)
	}
	me.Values[UnitFieldHealth], me.Values[playerFlags] = 700, 0
	w.rooted = true
	if got := w.MovementLock(); got != "rooted" {
		t.Fatalf("alive and rooted: %q", got)
	}
}

// Aboard a transport, our position is the transport's pose with our spot on it turned by its orientation.
func TestPositionFollowsTransport(t *testing.T) {
	w := NewWorldClient("u", nil, func(string, ...interface{}) {})
	w.transport = selfTransport{guid: 9, pos: [4]float32{10, 0, 6, 0}}
	w.TransportPose = func(g uint64) (x, y, z, o float32, ok bool) { return 100, 200, 0, math.Pi / 2, g == 9 }
	x, y, z, _, _ := w.Position()
	if math.Abs(float64(x-100)) > 0.01 || math.Abs(float64(y-210)) > 0.01 || z != 6 {
		t.Fatalf("aboard: %v %v %v, want 100 210 6", x, y, z)
	}
}
