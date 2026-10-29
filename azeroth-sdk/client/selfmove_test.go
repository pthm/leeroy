package client

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

type sentPkt struct {
	op   uint16
	data []byte
}

// moveTestClient: our character is guid 7 at (10, 20, 100); sends are captured, falls don't tick on their own.
func moveTestClient() (*WorldClient, *[]sentPkt) {
	w := NewWorldClient("u", nil, func(string, ...interface{}) {})
	w.charGUID = 7
	w.posX, w.posY, w.posZ = 10, 20, 100
	close(w.stopChan)
	sent := &[]sentPkt{}
	w.OnPacketSend = func(op uint16, d []byte) { *sent = append(*sent, sentPkt{op, append([]byte(nil), d...)}) }
	return w, sent
}

// readSent parses a movement packet we sent: packed guid, counter if withCounter, movement info, and what follows.
func readSent(t *testing.T, p sentPkt, withCounter bool) (counter uint32, mi movementInfo, rest []byte) {
	t.Helper()
	r := bytes.NewReader(p.data)
	if g, err := readPackedGUID(r); err != nil || g != 7 {
		t.Fatalf("op %#x: guid %d %v", p.op, g, err)
	}
	if withCounter {
		binary.Read(r, binary.LittleEndian, &counter)
	}
	mi, err := readMovementInfo(r)
	if err != nil {
		t.Fatalf("op %#x: %v", p.op, err)
	}
	rest = make([]byte, r.Len())
	r.Read(rest)
	return counter, mi, rest
}

func TestFallDrop(t *testing.T) {
	for _, c := range []struct{ vz, t, terminal, want float32 }{
		{0, 1, terminalSpeed, gravity / 2},
		{-jumpVelocity, 2 * jumpVelocity / gravity, terminalSpeed, 0}, // a jump comes back down to where it started
		{0, 10, terminalSpeed, terminalSpeed*terminalSpeed/(2*gravity) + terminalSpeed*(10-terminalSpeed/gravity)},
		{0, 2, safeFallSpeed, safeFallSpeed*safeFallSpeed/(2*gravity) + safeFallSpeed*(2-safeFallSpeed/gravity)},
	} {
		if got := fallDrop(c.vz, c.t, c.terminal); math.Abs(float64(got-c.want)) > 1e-3 {
			t.Errorf("fallDrop(%v, %v, %v) = %v, want %v", c.vz, c.t, c.terminal, got, c.want)
		}
	}
}

func TestKnockBackAckFallAndLand(t *testing.T) {
	w, sent := moveTestClient()
	w.SetGroundFunc(func(_ uint32, x, y, z float32) (float32, bool) { return 90, true })
	relocated := false
	w.OnServerRelocate = func(x, y, z, o float32, reason string) { relocated = reason == "knockback" }
	// Unit::KnockbackFrom: packed guid, counter, vcos, vsin, speedXY, -speedZ
	w.handleMoveKnockBack(append(packGUIDForTest(7), le(uint32(3), float32(1), float32(0), float32(5), float32(-10))...))
	if !relocated || !w.Falling() || len(*sent) != 1 || (*sent)[0].op != CmsgMoveKnockBackAck {
		t.Fatalf("relocated=%v falling=%v sent=%v", relocated, w.Falling(), *sent)
	}
	counter, mi, _ := readSent(t, (*sent)[0], true)
	if counter != 3 || mi.flags&MoveFlagFalling == 0 || mi.fallTime != 0 || mi.jump != [4]float32{-10, 0, 1, 5} {
		t.Fatalf("ack counter=%d flags=%#x fallTime=%d jump=%v", counter, mi.flags, mi.fallTime, mi.jump)
	}

	start := w.fall.start
	if !w.stepFall(start.Add(600 * time.Millisecond)) {
		t.Fatal("landed mid-air")
	}
	if w.posZ <= 100 || math.Abs(float64(w.posX-13)) > 1e-4 {
		t.Fatalf("0.6 s in: pos=(%v, %v)", w.posX, w.posZ)
	}
	hb := (*sent)[len(*sent)-1]
	if _, mi, _ := readSent(t, hb, false); hb.op != MsgMoveHeartbeat || mi.flags&MoveFlagFalling == 0 || mi.fallTime != 600 {
		t.Fatalf("heartbeat op=%#x flags=%#x fallTime=%d", hb.op, mi.flags, mi.fallTime)
	}
	if w.stepFall(start.Add(3 * time.Second)) {
		t.Fatal("still falling after 3 s")
	}
	land := (*sent)[len(*sent)-1]
	if _, mi, _ := readSent(t, land, false); land.op != MsgMoveFallLand || mi.flags&MoveFlagFalling != 0 || mi.pos[2] != 90 || math.Abs(float64(mi.pos[0]-25)) > 1e-4 {
		t.Fatalf("land op=%#x flags=%#x pos=%v", land.op, mi.flags, mi.pos)
	}
}

func TestDropAfterTeleportFalls(t *testing.T) {
	w, sent := moveTestClient()
	w.maybeFall() // no ground: never falls
	w.SetGroundFunc(func(_ uint32, x, y, z float32) (float32, bool) { return 99, true })
	w.maybeFall() // 1 yd: stands
	w.SetGroundFunc(func(_ uint32, x, y, z float32) (float32, bool) { return 20, true })
	w.maybeFall() // 80 yd: likely a floor the navmesh doesn't have, so we stand rather than take the fall damage
	if w.Falling() || len(*sent) != 0 {
		t.Fatal("fell without ground under us")
	}
	w.SetGroundFunc(func(_ uint32, x, y, z float32) (float32, bool) { return 90, true })
	w.maybeFall()
	if !w.Falling() || len(*sent) != 1 {
		t.Fatalf("falling=%v sent=%d", w.Falling(), len(*sent))
	}
	if _, mi, _ := readSent(t, (*sent)[0], false); mi.flags&MoveFlagFalling == 0 || mi.jump[0] != 0 {
		t.Fatalf("fall start flags=%#x jump=%v", mi.flags, mi.jump)
	}
	if w.stepFall(w.fall.start.Add(2 * time.Second)) {
		t.Fatal("still falling")
	}
	if last := (*sent)[len(*sent)-1]; last.op != MsgMoveFallLand || w.posZ != 90 {
		t.Fatalf("last op=%#x z=%v", last.op, w.posZ)
	}
}

func TestJumpKeepsMovementKeys(t *testing.T) {
	w, sent := moveTestClient()
	w.MoveForward()
	w.SendJump() // "not connected": the packet is still captured
	if w.SendJump() != errCantJump {
		t.Fatal("jumped in mid-air")
	}
	jump := (*sent)[len(*sent)-1]
	_, mi, _ := readSent(t, jump, false)
	if jump.op != MsgMoveJump || mi.flags&(MoveFlagForward|MoveFlagFalling) != MoveFlagForward|MoveFlagFalling ||
		mi.jump != [4]float32{-jumpVelocity, 0, 1, BaseSpeedRun} {
		t.Fatalf("op=%#x flags=%#x jump=%v", jump.op, mi.flags, mi.jump)
	}
	w.stepFall(w.fall.start.Add(time.Second)) // no ground func: lands where it started, still running
	land := (*sent)[len(*sent)-1]
	if _, mi, _ := readSent(t, land, false); land.op != MsgMoveFallLand || mi.flags != MoveFlagForward || mi.pos[2] != 100 {
		t.Fatalf("land op=%#x flags=%#x z=%v", land.op, mi.flags, mi.pos[2])
	}
}

func TestForceSpeedChanges(t *testing.T) {
	for mt, op := range speedOpcodes {
		w, sent := moveTestClient()
		// Unit::SendSpeedToController: packed guid, counter, uint8 0 for run, speed
		body := le(uint32(9))
		if mt == moveRun {
			body = append(body, 0)
		}
		body = append(body, le(float32(3.5))...)
		w.handlePacket(op.force, append(packGUIDForTest(8), body...)) // a vehicle's or pet's: not ours
		if w.speeds[mt] == 3.5 || len(*sent) != 0 {
			t.Fatalf("type %d: another unit's change applied", mt)
		}
		w.handlePacket(op.force, append(packGUIDForTest(7), body...))
		if w.speeds[mt] != 3.5 || len(*sent) != 1 || (*sent)[0].op != op.ack {
			t.Fatalf("type %d: speed=%v sent=%v", mt, w.speeds[mt], *sent)
		}
		if counter, _, rest := readSent(t, (*sent)[0], true); counter != 9 || !bytes.Equal(rest, le(float32(3.5))) {
			t.Fatalf("type %d: ack counter=%d rest=%x", mt, counter, rest)
		}
		w.handlePacket(op.spline, append(packGUIDForTest(7), le(float32(1.25))...))
		if w.speeds[mt] != 1.25 || len(*sent) != 1 {
			t.Fatalf("type %d: spline speed=%v sends=%d", mt, w.speeds[mt], len(*sent))
		}
	}
}

func TestMoveTogglesAndLoginMultipleMoves(t *testing.T) {
	sub := func(op uint16, body []byte) []byte { return append(le(uint8(2+len(body)), op), body...) }
	guidCounter := func(c uint32) []byte { return append([]byte{0x01, 7}, le(c)...) }
	// Player::SendInitialPacketsAfterAddToMap: root, feather fall, water walk, hover
	moves := append(sub(SmsgForceMoveRoot, guidCounter(1)), sub(0x0F2, guidCounter(2))...)
	moves = append(moves, sub(0x0DE, guidCounter(3))...)
	moves = append(moves, sub(0x0F4, guidCounter(4))...)
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(moves)
	zw.Close()
	for name, pkt := range map[string]sentPkt{
		"multiple":   {SmsgMultipleMoves, append(le(uint32(len(moves))), moves...)},
		"compressed": {SmsgCompressedMoves, append(le(uint32(len(moves))), z.Bytes()...)},
	} {
		w, sent := moveTestClient()
		w.handlePacket(pkt.op, pkt.data)
		all := moveFlagWaterWalking | moveFlagFallingSlow | moveFlagHover
		if !w.rooted || w.moveState != all || len(*sent) != 4 {
			t.Fatalf("%s: rooted=%v state=%#x sent=%d", name, w.rooted, w.moveState, len(*sent))
		}
		for i, ack := range []uint16{CmsgForceMoveRootAck, 0x2CF, 0x2D0, 0x0F6} {
			p := (*sent)[i]
			counter, mi, rest := readSent(t, p, true)
			if p.op != ack || counter != uint32(i+1) {
				t.Fatalf("%s: ack %d op=%#x counter=%d", name, i, p.op, counter)
			}
			if i > 0 && !bytes.Equal(rest, le(uint32(1))) {
				t.Fatalf("%s: ack %d applied=%x", name, i, rest)
			}
			if i == 3 && mi.flags&(all|moveFlagRoot) != all|moveFlagRoot {
				t.Fatalf("%s: flags %#x", name, mi.flags)
			}
		}
		w.handlePacket(0x0DF, append(packGUIDForTest(7), le(uint32(5))...)) // land walk
		p := (*sent)[len(*sent)-1]
		if _, mi, rest := readSent(t, p, true); p.op != 0x2D0 || mi.flags&moveFlagWaterWalking != 0 || !bytes.Equal(rest, le(uint32(0))) {
			t.Fatalf("%s: land walk ack op=%#x flags=%#x rest=%x", name, p.op, mi.flags, rest)
		}
	}
}

func TestSwimmingAndTransportInMovement(t *testing.T) {
	w, sent := moveTestClient()
	w.SetSwimming(true)
	w.SendHeartbeat()
	if _, mi, _ := readSent(t, (*sent)[0], false); mi.flags&MoveFlagSwimming == 0 {
		t.Fatalf("swimming flags %#x", mi.flags)
	}
	w.SetSwimming(false)

	// Player::SendTeleportAckPacket onto a boat: packed guid, counter, BuildMovementPacket with the transport
	const boat = 0x1FC0000000000011
	tele := append(packGUIDForTest(7), le(uint32(2), moveFlagOnTransport, uint16(0), uint32(0), [4]float32{1000, 2000, 5, 1})...)
	tele = append(tele, packGUIDForTest(boat)...)
	tele = append(tele, le([4]float32{3, 4, 6, 0.5}, uint32(77), int8(-1), uint32(0))...)
	w.handleMoveTeleportAck(tele)
	w.SendHeartbeat()
	_, mi, _ := readSent(t, (*sent)[len(*sent)-1], false)
	if mi.flags&moveFlagOnTransport == 0 || mi.transGUID != boat || mi.transPos != [4]float32{3, 4, 6, 0.5} || mi.transSeat != -1 || mi.pos != [4]float32{1000, 2000, 5, 1} {
		t.Fatalf("on the boat: %+v", mi)
	}

	// SMSG_NEW_WORLD with the boat (TELE_TO_NOT_LEAVE_TRANSPORT): our place on it, so not our world position
	w.handleNewWorld(le(uint32(1), [4]float32{7, 8, 9, 0}))
	if w.transport.guid != 0 || w.posX != 1000 {
		t.Fatalf("new world on the boat: transport=%v pos=%v", w.transport, w.posX)
	}
	// our create (BuildMovementUpdate: UPDATEFLAG_LIVING, movement info, 9 speeds) puts us back on it, in the world
	create := append(le(uint16(0x20), moveFlagOnTransport, uint16(0), uint32(0), [4]float32{-500, 600, 7, 2}), packGUIDForTest(boat)...)
	create = append(create, le([4]float32{7, 8, 9, 0}, uint32(0), int8(-1), uint32(0), [9]float32{2.5, 7, 4.5, 4.7, 2.5, 7, 4.5, 3.14, 3.14})...)
	w.readMovementUpdate(bytes.NewReader(create), &WorldObject{GUID: 7})
	if w.transport.guid != boat || w.transport.pos != [4]float32{7, 8, 9, 0} || w.posX != -500 || w.MoveSpeed() != 7 {
		t.Fatalf("create on the boat: transport=%v pos=%v speed=%v", w.transport, w.posX, w.MoveSpeed())
	}

	// walking off at the dock: no ONTRANSPORT, so the server drops us as its passenger
	w.moveMu.Lock()
	w.sendMovementLockedAt(MsgMoveStartForward, MoveFlagForward, 1)
	w.moveMu.Unlock()
	if _, mi, _ := readSent(t, (*sent)[len(*sent)-1], false); mi.flags&moveFlagOnTransport != 0 || w.transport.guid != 0 {
		t.Fatalf("walked off: flags=%#x transport=%v", mi.flags, w.transport)
	}

	// hearth after a ride we never got off (server removed us): NEW_WORLD is the world, and so is our create
	w.transport = selfTransport{guid: boat}
	w.handleNewWorld(le(uint32(0), [4]float32{-8900, -100, 80, 0}))
	create = le(uint16(0x20), uint32(0), uint16(0), uint32(0), [4]float32{-8900, -100, 80, 0}, uint32(0), [9]float32{2.5, 7, 4.5, 4.7, 2.5, 7, 4.5, 3.14, 3.14})
	w.readMovementUpdate(bytes.NewReader(create), &WorldObject{GUID: 7})
	if w.transport.guid != 0 || w.posX != -8900 || w.posZ != 80 {
		t.Fatalf("hearthed off the boat: transport=%v pos=(%v,%v)", w.transport, w.posX, w.posZ)
	}

	tele = append(packGUIDForTest(7), le(uint32(3), moveFlagOnTransport, uint16(0), uint32(0), [4]float32{1, 2, 3, 0})...)
	tele = append(tele, packGUIDForTest(boat)...)
	tele = append(tele, le([4]float32{3, 4, 6, 0.5}, uint32(77), int8(-1), uint32(0))...)
	w.handleMoveTeleportAck(tele)
	tele = append(packGUIDForTest(7), le(uint32(4), uint32(0), uint16(0), uint32(0), [4]float32{1, 2, 3, 0}, uint32(0))...)
	w.handleMoveTeleportAck(tele)
	if w.transport.guid != 0 {
		t.Fatal("still on the boat after teleporting off it")
	}
}
