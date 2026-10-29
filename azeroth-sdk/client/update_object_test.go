package client

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func le(vs ...any) []byte {
	var b bytes.Buffer
	for _, v := range vs {
		binary.Write(&b, binary.LittleEndian, v)
	}
	return b.Bytes()
}

// createSpline is MoveSpline as PacketBuilder::WriteCreate writes it, then a 0xCAFE sentinel.
func createSpline(flags uint32, facing []byte, nodes [][3]float32, final [3]float32) []byte {
	b := le(flags)
	b = append(b, facing...)
	b = append(b, le(uint32(500), uint32(2000), uint32(7), float32(1), float32(1), float32(0), uint32(0), uint32(len(nodes)))...)
	for _, n := range nodes {
		b = append(b, le(n)...)
	}
	b = append(b, le(uint8(0), final, uint32(0xCAFE))...)
	return b
}

func TestCreateSplineFlags(t *testing.T) {
	nodes := [][3]float32{{1, 2, 3}, {4, 5, 6}}
	for _, c := range []struct {
		name   string
		flags  uint32
		facing []byte
		final  [3]float32
		dest   [3]float32
	}{
		{"final angle + catmullrom", splineFinalAngle | splineCatmullrom, le(float32(1.5)), [3]float32{4, 5, 6}, [3]float32{4, 5, 6}},
		{"final target", splineFinalTarget, le(uint64(0xF130000000000001)), [3]float32{4, 5, 6}, [3]float32{4, 5, 6}},
		{"final point", splineFinalPoint, le(float32(9), float32(9), float32(9)), [3]float32{4, 5, 6}, [3]float32{4, 5, 6}},
		{"cyclic: zero final, last node", 0x00080000 | 0x00100000 | splineCatmullrom, nil, [3]float32{}, [3]float32{4, 5, 6}},
	} {
		data := createSpline(c.flags, c.facing, nodes, c.final)
		w := NewWorldClient("u", nil, func(string, ...interface{}) {})
		o := &WorldObject{}
		r := bytes.NewReader(data)
		w.parseCreateSplineData(r, o)
		var sentinel uint32
		binary.Read(r, binary.LittleEndian, &sentinel)
		if sentinel != 0xCAFE {
			t.Errorf("%s: parse desynced (sentinel %#x)", c.name, sentinel)
		}
		if !o.IsMoving || [3]float32{o.DestX, o.DestY, o.DestZ} != c.dest {
			t.Errorf("%s: moving=%v dest=%v want %v", c.name, o.IsMoving, [3]float32{o.DestX, o.DestY, o.DestZ}, c.dest)
		}
		r = bytes.NewReader(data)
		w.skipSplineData(r)
		binary.Read(r, binary.LittleEndian, &sentinel)
		if sentinel != 0xCAFE {
			t.Errorf("%s: skip desynced", c.name)
		}
	}
}

func TestSkipValuesUpdateManyBlocks(t *testing.T) {
	// PLAYER has 42 mask blocks; set a bit in the last one.
	b := []byte{42}
	for i := 0; i < 42; i++ {
		m := uint32(0)
		if i == 0 || i == 41 {
			m = 0x80000001
		}
		b = append(b, le(m)...)
	}
	b = append(b, le(uint32(1), uint32(2), uint32(3), uint32(4), uint32(0xCAFE))...)
	r := bytes.NewReader(b)
	NewWorldClient("u", nil, func(string, ...interface{}) {}).skipValuesUpdate(r)
	var sentinel uint32
	binary.Read(r, binary.LittleEndian, &sentinel)
	if sentinel != 0xCAFE {
		t.Fatalf("skip desynced: %#x", sentinel)
	}
}

func TestFeignDeathKeepsHealth(t *testing.T) {
	w := NewWorldClient("u", nil, func(string, ...interface{}) {})
	const g = 0x10
	w.objects[g] = &WorldObject{GUID: g, Values: map[uint16]uint32{UnitFieldHealth: 900, UnitFieldMaxHealth: 1000}}
	// Feign Death: UNIT_FIELD_FLAGS |= 0x20000000, UNIT_DYNAMIC_FLAGS = DEAD; health unchanged (not sent).
	mask := make([]uint32, 3)
	mask[UnitFieldFlags/32] |= 1 << (UnitFieldFlags % 32)
	mask[UnitDynamicFlags/32] |= 1 << (UnitDynamicFlags % 32)
	b := append([]byte{3}, le(mask)...)
	b = append(b, le(uint32(0x20000000), uint32(UnitDynflagDead))...)
	w.readValuesUpdate(bytes.NewReader(b), g)
	if o := w.GetObject(g); o.Health() != 900 || !o.IsAlive() {
		t.Fatalf("feign death: health=%d alive=%v", o.Health(), o.IsAlive())
	}
}

func TestIsSelfGUIDFullCompare(t *testing.T) {
	w := NewWorldClient("u", nil, func(string, ...interface{}) {})
	w.charGUID = 0x5
	if !w.isSelfGUID(0x5) || w.isSelfGUID(0xF130000100000005) {
		t.Fatal("a creature sharing our low guid must not be us")
	}
}

func TestAurasDroppedOnRemoveAndDestroy(t *testing.T) {
	w := NewWorldClient("u", nil, func(string, ...interface{}) {})
	w.auras.set(1, AuraInfo{Slot: 0, Spell: 100}, nil)
	w.auras.set(1, AuraInfo{Slot: 0}, nil)
	if _, ok := w.auras.m[1]; ok {
		t.Fatal("empty unit entry kept after last aura removed")
	}
	w.objects[2] = &WorldObject{GUID: 2, Values: map[uint16]uint32{}}
	w.auras.set(2, AuraInfo{Slot: 1, Spell: 200}, nil)
	w.handleDestroyObject(le(uint64(2), uint8(0)))
	if _, ok := w.auras.m[2]; ok {
		t.Fatal("auras kept after destroy")
	}
}

func TestMovementPacketIsMoving(t *testing.T) {
	w := NewWorldClient("u", nil, func(string, ...interface{}) {})
	const g = 0x20
	o := &WorldObject{GUID: g, Values: map[uint16]uint32{}, MoveDuration: 5e9}
	w.objects[g] = o
	for _, c := range []struct {
		flags  uint32
		moving bool
	}{{MoveFlagForward, true}, {MoveFlagStrafeLeft, true}, {MoveFlagFalling, true}, {0x4000 /* PENDING_STOP */, false}, {MoveFlagTurnLeft, false}} {
		pkt := append(packGUIDForTest(g), le(c.flags, uint16(0), uint32(0), [4]float32{1, 2, 3, 0}, uint32(0))...)
		if c.flags&MoveFlagFalling != 0 {
			pkt = append(pkt, le([4]float32{})...)
		}
		w.handleMovementPacket(MsgMoveHeartbeat, pkt)
		if o.IsMoving != c.moving || o.MoveDuration != 0 {
			t.Errorf("flags %#x: moving=%v dur=%v", c.flags, o.IsMoving, o.MoveDuration)
		}
	}
}

func TestMonsterMoveTransportOffsets(t *testing.T) {
	w := NewWorldClient("u", nil, func(string, ...interface{}) {})
	const unit, ship, lost = 0x30, 0x1FC0000000000040, 0x50
	w.objects[unit] = &WorldObject{GUID: unit, Values: map[uint16]uint32{}}
	w.objects[lost] = &WorldObject{GUID: lost, Values: map[uint16]uint32{}, PosX: 7, PosY: 7, PosZ: 7}
	w.objects[ship] = &WorldObject{GUID: ship, Values: map[uint16]uint32{}, PosX: 100, PosY: 200, PosZ: 10, Orientation: math.Pi / 2}
	body := le(int8(0), uint8(0), float32(2), float32(1), float32(3), uint32(1), uint8(1))
	w.handleMonsterMoveTransport(append(append(packGUIDForTest(unit), packGUIDForTest(ship)...), body...))
	o := w.objects[unit]
	if math.Abs(float64(o.PosX-99)) > 1e-4 || math.Abs(float64(o.PosY-202)) > 1e-4 || o.PosZ != 13 {
		t.Fatalf("world pos = %v,%v,%v want 99,202,13", o.PosX, o.PosY, o.PosZ)
	}
	w.handleMonsterMoveTransport(append(append(packGUIDForTest(lost), packGUIDForTest(0x99)...), body...))
	if o := w.objects[lost]; o.PosX != 7 || o.PosY != 7 || o.PosZ != 7 {
		t.Fatal("unknown transport must leave the position alone")
	}
}

func TestNearTeleportKeepsObjects(t *testing.T) {
	w := NewWorldClient("u", nil, func(string, ...interface{}) {})
	w.charGUID = 0x1
	const far = 0x40
	w.objects[far] = &WorldObject{GUID: far, Values: map[uint16]uint32{}, PosX: 400}
	w.handleMoveTeleportAck(append(packGUIDForTest(w.charGUID), le(uint32(1), uint32(0), uint16(0), uint32(0), float32(0), float32(0), float32(0), float32(0))...))
	if w.GetObject(far) == nil {
		t.Fatal("near teleport dropped an object the server still has in view")
	}
}
