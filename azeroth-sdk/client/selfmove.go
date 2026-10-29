package client

// Our own movement state, as the real client keeps it, and the parts of every movement packet that come from it:
// falling (jumps, knockbacks, drops), swimming, root, water walk / feather fall / hover, speeds, and the transport we
// stand on. The layout is WorldSession::ReadMovementInfo's.

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"time"
)

const (
	moveFlagOnTransport     uint32 = 0x00000200
	moveFlagRoot            uint32 = 0x00000800
	moveFlagSplineElevation uint32 = 0x04000000
	moveFlagWaterWalking    uint32 = 0x10000000
	moveFlagFallingSlow     uint32 = 0x20000000 // feather fall, slow fall
	moveFlagHover           uint32 = 0x40000000
	moveFlagsHeld                  = MoveFlagForward | MoveFlagBackward | MoveFlagStrafeLeft | MoveFlagStrafeRight | MoveFlagTurnLeft | MoveFlagTurnRight

	moveFlag2AlwaysPitch  uint16 = 0x0020
	moveFlag2Interpolated uint16 = 0x0400

	CmsgMoveKnockBackAck uint16 = 0x00F0
	MsgMoveStartSwim     uint16 = 0x00CA
)

// UnitMoveType: the index of each speed (Unit.h).
const (
	moveWalk = iota
	moveRun
	moveRunBack
	moveSwim
	moveSwimBack
	moveTurnRate
	moveFlight
	moveFlightBack
	movePitchRate
)

// speedOpcodes is SetSpeed2Opc_table: the forced change the client acks, its ack, and the spline change (the server
// moving the unit, no ack).
var speedOpcodes = [9]struct{ force, ack, spline uint16 }{
	{0x2DA, 0x2DB, 0x301}, // walk
	{SmsgForceRunSpeedChange, CmsgForceRunSpeedChangeAck, 0x2FE},
	{0x0E4, 0x0E5, 0x2FF}, // run back
	{SmsgForceSwimSpeedChange, CmsgForceSwimSpeedChangeAck, 0x300},
	{0x2DC, 0x2DD, 0x302}, // swim back
	{0x2DE, 0x2DF, 0x303}, // turn rate
	{0x381, 0x382, 0x385}, // flight
	{0x383, 0x384, 0x386}, // flight back
	{0x45C, 0x45D, 0x45E}, // pitch rate
}

// moveToggles: SMSG_MOVE_WATER_WALK .. SMSG_MOVE_UNSET_HOVER, the flag each sets or clears and its ack
// (WorldSession::HandleMoveFlagChangeOpcode).
var moveToggles = map[uint16]struct {
	flag uint32
	on   bool
	ack  uint16
}{
	0x0DE: {moveFlagWaterWalking, true, 0x2D0},
	0x0DF: {moveFlagWaterWalking, false, 0x2D0},
	0x0F2: {moveFlagFallingSlow, true, 0x2CF},
	0x0F3: {moveFlagFallingSlow, false, 0x2CF},
	0x0F4: {moveFlagHover, true, 0x0F6},
	0x0F5: {moveFlagHover, false, 0x0F6},
}

// Fall physics of the 3.3.5 client.
const (
	gravity       = 19.29110527
	terminalSpeed = 60.148003
	safeFallSpeed = 7.0      // terminal speed under feather fall
	jumpVelocity  = 7.955547 // MSG_MOVE_JUMP's z speed is minus this (down is positive)
	fallTick      = 100 * time.Millisecond
	fallBeat      = 500 * time.Millisecond // heartbeats while in the air
	fallMinDrop   = 2.0                    // ground this far below us means we fall (navmesh heights are approximate)
	fallMaxDrop   = 13.48                  // MIN_FALL_DMG_DIST: deeper "drops" after a teleport are more likely a floor we can't see
)

// selfFall is a fall in progress: a jump, a knockback, or a drop.
type selfFall struct {
	start            time.Time
	elapsed          time.Duration
	x, y, z          float32 // where it began
	vz, sin, cos, xy float32 // jump info: vz down-positive
	held             uint32  // movement keys held through it
	lastSent         time.Time
}

// selfTransport: the transport (boat, zeppelin) the server has us on and where we stand on it.
type selfTransport struct {
	guid uint64
	pos  [4]float32
	seat int8
}

var errCantJump = errors.New("can't jump: already in the air, swimming, rooted or hovering")

// movementInfo is MovementInfo as WorldSession::WriteMovementInfo and Unit::BuildMovementPacket write it, after the guid.
type movementInfo struct {
	flags     uint32
	flags2    uint16
	time      uint32
	pos       [4]float32
	transGUID uint64
	transPos  [4]float32
	transTime uint32
	transSeat int8
	pitch     float32
	fallTime  uint32
	jump      [4]float32 // z speed, sin, cos, xy speed
}

func readMovementInfo(r *bytes.Reader) (mi movementInfo, err error) {
	rd := func(v any) {
		if err == nil {
			err = binary.Read(r, binary.LittleEndian, v)
		}
	}
	rd(&mi.flags)
	rd(&mi.flags2)
	rd(&mi.time)
	rd(&mi.pos)
	if mi.flags&moveFlagOnTransport != 0 {
		if err == nil {
			mi.transGUID, err = readPackedGUID(r)
		}
		rd(&mi.transPos)
		rd(&mi.transTime)
		rd(&mi.transSeat)
		if mi.flags2&moveFlag2Interpolated != 0 {
			var t2 uint32
			rd(&t2)
		}
	}
	if mi.flags&(MoveFlagSwimming|MoveFlagFlying) != 0 || mi.flags2&moveFlag2AlwaysPitch != 0 {
		rd(&mi.pitch)
	}
	rd(&mi.fallTime)
	if mi.flags&MoveFlagFalling != 0 {
		rd(&mi.jump)
	}
	if mi.flags&moveFlagSplineElevation != 0 {
		var e float32
		rd(&e)
	}
	return mi, err
}

// setSelfTransportLocked records the transport our movement info from the server puts us on, or clears it.
func (w *WorldClient) setSelfTransportLocked(mi movementInfo) {
	if mi.flags&moveFlagOnTransport == 0 {
		w.transport = selfTransport{}
		return
	}
	w.transport = selfTransport{guid: mi.transGUID, pos: mi.transPos, seat: mi.transSeat}
}

// writeSelfMovement writes our MovementInfo with the given keys held, adding the flags our state implies. Caller holds
// moveMu.
func (w *WorldClient) writeSelfMovement(buf *bytes.Buffer, flags, ts uint32) {
	flags |= w.moveState
	var fallTime uint32
	if f := w.fall; f != nil {
		flags |= MoveFlagFalling
		fallTime = uint32(f.elapsed.Milliseconds())
	}
	if w.swimming {
		flags = flags&^MoveFlagFalling | MoveFlagSwimming
	}
	if w.rooted {
		flags = flags&^moveFlagMaskMoving | moveFlagRoot
	}
	// ponytail: we can't walk or fall on a deck (no transport paths or deck collision), so moving or falling steps
	// off: without ONTRANSPORT the server drops us as its passenger (HandleMoverRelocation). Our world position is
	// still where we boarded or last got one from the server; following the transport's path would fix it.
	if flags&(moveFlagsHeld&^(MoveFlagTurnLeft|MoveFlagTurnRight)) != 0 || flags&MoveFlagFalling != 0 {
		w.transport = selfTransport{}
	}
	if w.transport.guid != 0 {
		flags |= moveFlagOnTransport
	}
	le := binary.LittleEndian
	binary.Write(buf, le, flags)
	binary.Write(buf, le, uint16(0)) // flags2
	binary.Write(buf, le, ts)
	binary.Write(buf, le, [4]float32{w.posX, w.posY, w.posZ, w.orientation})
	if flags&moveFlagOnTransport != 0 {
		writePackedGUID(buf, w.transport.guid)
		binary.Write(buf, le, w.transport.pos)
		binary.Write(buf, le, ts)
		binary.Write(buf, le, w.transport.seat)
	}
	if flags&(MoveFlagSwimming|MoveFlagFlying) != 0 {
		binary.Write(buf, le, float32(0)) // pitch
	}
	binary.Write(buf, le, fallTime)
	if flags&MoveFlagFalling != 0 {
		f := w.fall
		binary.Write(buf, le, [4]float32{f.vz, f.sin, f.cos, f.xy})
	}
}

// fallDrop: how far below its start a fall that began with vertical speed vz (down positive) is after t seconds.
func fallDrop(vz, t, terminal float32) float32 {
	if vz >= terminal {
		return vz * t
	}
	tt := (terminal - vz) / gravity // until terminal speed
	if t <= tt {
		return vz*t + gravity*t*t/2
	}
	return vz*tt + gravity*tt*tt/2 + terminal*(t-tt)
}

// SetGroundFunc gives the client the floor under a point near z (from the navmesh), so it falls like the real client:
// after a teleport into the air, a knockback or a jump, landing with MSG_MOVE_FALL_LAND. Without it jumps and
// knockbacks land back at the height they started from and nothing else falls.
func (w *WorldClient) SetGroundFunc(f func(mapID uint32, x, y, z float32) (float32, bool)) {
	w.moveMu.Lock()
	w.ground = f
	w.moveMu.Unlock()
}

// Falling: we're in the air (a jump, a knockback, a drop) and the client moves us until we land.
func (w *WorldClient) Falling() bool {
	w.moveMu.Lock()
	defer w.moveMu.Unlock()
	return w.fall != nil
}

// startFallLocked begins a fall from where we are, and runs it unless one is already running.
func (w *WorldClient) startFallLocked(now time.Time, vz, sin, cos, xy float32, held uint32) {
	running := w.fall != nil
	w.fall = &selfFall{start: now, x: w.posX, y: w.posY, z: w.posZ, vz: vz, sin: sin, cos: cos, xy: xy, held: held, lastSent: now}
	if !running {
		go w.runFall()
	}
}

func (w *WorldClient) runFall() {
	t := time.NewTicker(fallTick)
	defer t.Stop()
	for {
		select {
		case <-w.stopChan:
			return
		case now := <-t.C:
			if !w.stepFall(now) {
				return
			}
		}
	}
}

// stepFall moves the fall on to now: a heartbeat every fallBeat, MSG_MOVE_FALL_LAND on reaching the ground. False once
// we're not falling.
func (w *WorldClient) stepFall(now time.Time) bool {
	w.moveMu.Lock()
	f, ground, mapID, prevZ, state := w.fall, w.ground, w.mapID, w.posZ, w.moveState
	w.moveMu.Unlock()
	if f == nil {
		return false
	}
	t := float32(now.Sub(f.start).Seconds())
	terminal := float32(terminalSpeed)
	if state&moveFlagFallingSlow != 0 {
		terminal = safeFallSpeed
	}
	x, y := f.x+f.cos*f.xy*t, f.y+f.sin*f.xy*t
	z := f.z - fallDrop(f.vz, t, terminal)
	floor, ok := f.z, false
	if ground != nil {
		floor, ok = ground(mapID, x, y, max(prevZ, z))
	}
	if !ok {
		floor = f.z // no ground data: land at the height we started from
	}

	w.moveMu.Lock()
	defer w.moveMu.Unlock()
	if w.fall != f { // landed, replaced or cancelled meanwhile
		return w.fall != nil
	}
	f.elapsed = now.Sub(f.start)
	if f.vz+gravity*t > 0 && z <= floor {
		w.fall = nil
		w.setPositionLocked(x, y, floor, w.orientation)
		w.sendMovementLockedAt(MsgMoveFallLand, f.held, getMSTime())
		return false
	}
	w.setPositionLocked(x, y, z, w.orientation)
	if now.Sub(f.lastSent) >= fallBeat {
		f.lastSent = now
		w.sendMovementLockedAt(MsgMoveHeartbeat, f.held, getMSTime())
	}
	return true
}

// maybeFall starts a fall when the ground is well below us (a teleport into the air).
func (w *WorldClient) maybeFall() {
	w.moveMu.Lock()
	ground, mapID, x, y, z := w.ground, w.mapID, w.posX, w.posY, w.posZ
	w.moveMu.Unlock()
	if ground == nil {
		return
	}
	// ponytail: no collision (decks, platforms, upper floors), so only a drop that can't hurt us; a real teleport high
	// into the air leaves us hovering until a vmap/gameobject-aware ground lets us trust deeper ones.
	g, ok := ground(mapID, x, y, z)
	if !ok || z-g < fallMinDrop || z-g > fallMaxDrop {
		return
	}
	if _, _, onSpline := w.spline.at(); onSpline {
		return
	}
	w.moveMu.Lock()
	defer w.moveMu.Unlock()
	if w.fall != nil || w.swimming || w.moveState&moveFlagHover != 0 || w.transport.guid != 0 || w.posX != x || w.posY != y || w.posZ != z {
		return
	}
	w.startFallLocked(time.Now(), 0, float32(math.Sin(float64(w.orientation))), float32(math.Cos(float64(w.orientation))), 0, 0)
	w.sendMovementLockedAt(MsgMoveHeartbeat, 0, getMSTime())
}

// SendJump sends MSG_MOVE_JUMP with the movement keys still held; the fall then runs until we land
// (MSG_MOVE_FALL_LAND).
func (w *WorldClient) SendJump() error {
	w.moveMu.Lock()
	defer w.moveMu.Unlock()
	if w.fall != nil || w.swimming || w.rooted || w.moveState&moveFlagHover != 0 {
		return errCantJump
	}
	held := w.currentMoveFlags & moveFlagsHeld
	var fwd, side float64
	switch {
	case held&MoveFlagForward != 0:
		fwd = 1
	case held&MoveFlagBackward != 0:
		fwd = -1
	}
	switch {
	case held&MoveFlagStrafeLeft != 0:
		side = 1
	case held&MoveFlagStrafeRight != 0:
		side = -1
	}
	a := float64(w.orientation)
	var xy float32
	if fwd != 0 || side != 0 {
		a += math.Atan2(side, fwd)
		xy = w.speeds[moveRun]
		if fwd < 0 {
			xy = w.speeds[moveRunBack]
		}
	}
	w.startFallLocked(time.Now(), -jumpVelocity, float32(math.Sin(a)), float32(math.Cos(a)), xy, held)
	return w.sendMovementLockedAt(MsgMoveJump, held, getMSTime())
}

// handleMoveKnockBack: SMSG_MOVE_KNOCK_BACK (Unit::KnockbackFrom): packed guid, counter, direction cos and sin,
// horizontal speed, vertical speed (down positive). We ack it with the fall it starts, then fly it out and land.
func (w *WorldClient) handleMoveKnockBack(data []byte) {
	r := bytes.NewReader(data)
	guid, err := readPackedGUID(r)
	var kb struct {
		Counter          uint32
		Cos, Sin, XY, VZ float32
	}
	if err != nil || !w.isSelfGUID(guid) || binary.Read(r, binary.LittleEndian, &kb) != nil {
		return
	}
	w.moveMu.Lock()
	x, y, z := w.posX, w.posY, w.posZ
	w.moveMu.Unlock()
	w.applySelfServerRelocate(x, y, z, "knockback") // drop the path we were on

	w.moveMu.Lock()
	defer w.moveMu.Unlock()
	w.swimming = false
	w.startFallLocked(time.Now(), kb.VZ, kb.Sin, kb.Cos, kb.XY, 0)
	w.currentMoveFlags = 0
	buf := new(bytes.Buffer)
	writePackedGUID(buf, w.charGUID)
	binary.Write(buf, binary.LittleEndian, kb.Counter)
	w.writeSelfMovement(buf, 0, getMSTime())
	w.sendPacket(CmsgMoveKnockBackAck, buf.Bytes())
}

// handleMoveStateOpcode handles the speed changes and movement flag toggles the server sends; false if opcode isn't one.
func (w *WorldClient) handleMoveStateOpcode(opcode uint16, data []byte) bool {
	for mt, op := range speedOpcodes {
		switch opcode {
		case op.force:
			w.handleForceSpeedChange(mt, data)
			return true
		case op.spline: // SMSG_SPLINE_SET_*_SPEED: packed guid, speed
			r := bytes.NewReader(data)
			var speed float32
			if guid, err := readPackedGUID(r); err == nil && w.isSelfGUID(guid) && binary.Read(r, binary.LittleEndian, &speed) == nil {
				w.moveMu.Lock()
				w.speeds[mt] = speed
				w.moveMu.Unlock()
			}
			return true
		}
	}
	if t, ok := moveToggles[opcode]; ok {
		r := bytes.NewReader(data)
		guid, err := readPackedGUID(r)
		var counter uint32
		if err != nil || !w.isSelfGUID(guid) || binary.Read(r, binary.LittleEndian, &counter) != nil {
			return true
		}
		w.moveMu.Lock()
		defer w.moveMu.Unlock()
		applied := uint32(0)
		if t.on {
			w.moveState |= t.flag
			applied = 1
		} else {
			w.moveState &^= t.flag
		}
		buf := new(bytes.Buffer)
		writePackedGUID(buf, w.charGUID)
		binary.Write(buf, binary.LittleEndian, counter)
		w.writeSelfMovement(buf, w.currentMoveFlags, getMSTime())
		binary.Write(buf, binary.LittleEndian, applied)
		w.sendPacket(t.ack, buf.Bytes())
		return true
	}
	return false
}

// handleForceSpeedChange: SMSG_FORCE_*_SPEED_CHANGE (Unit::SendSpeedToController): packed guid, counter, a uint8 0 for
// run only, speed. Only ours is acked; a vehicle's or pet's isn't our speed.
func (w *WorldClient) handleForceSpeedChange(mt int, data []byte) {
	r := bytes.NewReader(data)
	guid, err := readPackedGUID(r)
	if err != nil || !w.isSelfGUID(guid) {
		return
	}
	var counter uint32
	var speed float32
	binary.Read(r, binary.LittleEndian, &counter)
	if mt == moveRun {
		r.ReadByte()
	}
	if binary.Read(r, binary.LittleEndian, &speed) != nil {
		return
	}
	w.moveMu.Lock()
	w.speeds[mt] = speed
	w.moveMu.Unlock()
	w.sendForceSpeedAck(speedOpcodes[mt].ack, counter, speed)
}

// handleMultipleMoves: SMSG_MULTIPLE_MOVES (Player::SendInitialPacketsAfterAddToMap: root, feather fall, water walk,
// hover at login): uint32 size, then the packets.
func (w *WorldClient) handleMultipleMoves(data []byte) {
	if len(data) >= 4 {
		w.dispatchMoves(data[4:])
	}
}

// handleCompressedMoves: SMSG_COMPRESSED_MOVES, uint32 size and the packets zlib-compressed.
func (w *WorldClient) handleCompressedMoves(data []byte) {
	if len(data) < 4 || binary.LittleEndian.Uint32(data) > 1<<20 {
		return
	}
	zr, err := zlib.NewReader(bytes.NewReader(data[4:]))
	if err != nil {
		return
	}
	defer zr.Close()
	out, _ := io.ReadAll(io.LimitReader(zr, int64(binary.LittleEndian.Uint32(data))))
	w.dispatchMoves(out)
}

// dispatchMoves handles packets packed as uint8 size (opcode and body), uint16 opcode, body.
func (w *WorldClient) dispatchMoves(data []byte) {
	for len(data) >= 3 {
		n := int(data[0])
		if n < 2 || 1+n > len(data) {
			return
		}
		w.handlePacket(binary.LittleEndian.Uint16(data[1:3]), data[3:1+n])
		data = data[1+n:]
	}
}
