package client

import (
	"bytes"
	"encoding/binary"
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
	binary.Write(&b, binary.LittleEndian, pack(5-5, 0-4, 0-(-2)))
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
