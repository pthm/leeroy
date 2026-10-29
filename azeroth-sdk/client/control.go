package client

// Server-controlled movement, as the real client handles it: while the server moves you (a flight, a teleport in
// progress, fear, confusion, mind control) the client sends no movement of its own, and your position follows the
// server's spline. Scripts see why through MovementLock.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"sort"
	"sync"
	"time"
)

// ErrServerDriven: a movement packet wasn't sent because the server is moving the character.
var ErrServerDriven = errors.New("the server is moving you (flight, teleport, fear...): movement is locked")

// 3.3.5 movement opcodes the client originates (MSG_MOVE_START_* .. HEARTBEAT); acks and teleport/worldport acks are
// not in this set and always go through.
var playerMoveOpcodes = map[uint16]bool{0xB5: true, 0xB6: true, 0xB7: true, 0xB8: true, 0xB9: true, 0xBA: true, 0xBB: true,
	0xBC: true, 0xBD: true, 0xBE: true, 0xBF: true, 0xC0: true, 0xC1: true, 0xC2: true, 0xC3: true, 0xC9: true, 0xCA: true,
	0xCB: true, 0xDA: true, 0xDB: true, 0xEE: true}

const (
	unitFlagStunned  = 0x00040000
	unitFlagConfused = 0x00400000
	unitFlagFleeing  = 0x00800000
	unitFlagPossess  = 0x01000000
)

// MovementLock says who's moving your character: "" when you are, else in_flight, teleporting, feared, confused,
// charmed, stunned or rooted. The first five are server-driven (no movement packets go out); stunned and rooted just
// can't move.
func (w *WorldClient) MovementLock() string {
	w.phaseMu.Lock()
	ph := w.phase
	w.phaseMu.Unlock()
	if ph == PhaseFarTransfer || ph == PhaseNearTeleport {
		return "teleporting"
	}
	var flags uint32
	if me := w.GetObject(w.charGUID); me != nil {
		flags = me.Value(UnitFieldFlags)
	}
	switch {
	case flags&UnitFlagTaxiFlight != 0:
		return "in_flight"
	case flags&unitFlagPossess != 0:
		return "charmed"
	case flags&unitFlagFleeing != 0:
		return "feared"
	case flags&unitFlagConfused != 0:
		return "confused"
	case flags&unitFlagStunned != 0:
		return "stunned"
	case w.rooted:
		return "rooted"
	}
	return ""
}

// serverDriven: the server moves us; our own movement packets would fight it (or trip its anticheat).
func (w *WorldClient) serverDriven() bool {
	switch w.MovementLock() {
	case "in_flight", "teleporting", "feared", "confused", "charmed":
		return true
	}
	return false
}

// selfSpline is the server's current path for our character (a flight, a charge, a knockback, fear).
type selfSpline struct {
	mu    sync.Mutex
	pts   [][3]float32
	start time.Time
	dur   time.Duration
}

func (s *selfSpline) set(pts [][3]float32, dur time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pts, s.start, s.dur = pts, time.Now(), dur
}

func (s *selfSpline) clear() {
	s.mu.Lock()
	s.pts = nil
	s.mu.Unlock()
}

// at: where along the path we are now (by distance travelled, at constant speed); ok=false when no spline is active.
// done=true once it has run its course.
func (s *selfSpline) at() (p [3]float32, done, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pts) < 2 || s.dur <= 0 {
		return p, false, false
	}
	f := float64(time.Since(s.start)) / float64(s.dur)
	if f >= 1 {
		return s.pts[len(s.pts)-1], true, true
	}
	total, segs := 0.0, make([]float64, len(s.pts)-1)
	for i := range segs {
		segs[i] = dist3f(s.pts[i], s.pts[i+1])
		total += segs[i]
	}
	want := f * total
	for i, d := range segs {
		if want <= d || i == len(segs)-1 {
			t := float32(0)
			if d > 0 {
				t = float32(math.Min(want/d, 1))
			}
			a, b := s.pts[i], s.pts[i+1]
			return [3]float32{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t, a[2] + (b[2]-a[2])*t}, false, true
		}
		want -= d
	}
	return s.pts[len(s.pts)-1], true, true
}

// SelfSplineDestination: where the server is taking us and how long until we're there (ok=false if it isn't).
func (w *WorldClient) SelfSplineDestination() (dest [3]float32, left time.Duration, ok bool) {
	s := &w.spline
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pts) == 0 {
		return dest, 0, false
	}
	left = s.dur - time.Since(s.start)
	if left < 0 {
		return dest, 0, false
	}
	return s.pts[len(s.pts)-1], left, true
}

func dist3f(a, b [3]float32) float64 {
	dx, dy, dz := float64(a[0]-b[0]), float64(a[1]-b[1]), float64(a[2]-b[2])
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// readSplinePath reads a MONSTER_MOVE path after the point count (AzerothCore PacketBuilder): catmull-rom paths
// (flights, flying mobs) are every point; linear paths are the destination then packed offsets from the midpoint.
func readSplinePath(r *bytes.Reader, flags, count uint32, start [3]float32) [][3]float32 {
	pts := [][3]float32{start}
	v3 := func() (p [3]float32) { binary.Read(r, binary.LittleEndian, &p); return }
	if flags&splineMaskCatmullRom != 0 {
		for i := uint32(0); i < count && r.Len() >= 12; i++ {
			pts = append(pts, v3())
		}
		return pts
	}
	dest := v3()
	mid := [3]float32{(start[0] + dest[0]) / 2, (start[1] + dest[1]) / 2, (start[2] + dest[2]) / 2}
	for i := uint32(1); i < count && r.Len() >= 4; i++ {
		var packed uint32
		binary.Read(r, binary.LittleEndian, &packed)
		x := float32(int32(packed<<21)>>21) * 0.25 // 11 bits
		y := float32(int32(packed<<10)>>21) * 0.25 // 11 bits
		z := float32(int32(packed)>>22) * 0.25     // 10 bits
		pts = append(pts, [3]float32{mid[0] - x, mid[1] - y, mid[2] - z})
	}
	return append(pts, dest)
}

// 3.3.5 MoveSplineFlag (AzerothCore MoveSplineFlag.h)
const (
	splineParabolic      = 0x00000800
	splineFlying         = 0x00002000
	splineFinalPoint     = 0x00008000
	splineFinalTarget    = 0x00010000
	splineFinalAngle     = 0x00020000
	splineCatmullrom     = 0x00040000
	splineAnimation      = 0x00200000
	splineMaskCatmullRom = splineFlying | splineCatmullrom
)

// BindPoint is the last SMSG_BINDPOINTUPDATE body (x, y, z, map, area: where the hearthstone goes), nil before one.
func (w *WorldClient) BindPoint() []byte {
	b, _ := w.bindPoint.Load().([]byte)
	return b
}

// AuraInfo is one aura slot as SMSG_AURA_UPDATE describes it.
type AuraInfo struct {
	Slot          uint8
	Spell         uint32
	Flags         uint8 // 0x10 positive, 0x80 negative, 0x08 self-cast, 0x20 has a duration
	CasterLevel   uint8
	Stacks        uint8
	Caster        uint64
	MaxDurationMS uint32 // 0 = permanent
	DurationMS    uint32 // remaining when the update arrived (At)
	At            time.Time
}

// Harmful: a debuff.
func (a AuraInfo) Harmful() bool { return a.Flags&0x80 != 0 }

// Left: time remaining now (0 for permanent auras).
func (a AuraInfo) Left() time.Duration {
	if a.Flags&auraFlagDuration == 0 || a.MaxDurationMS == 0 {
		return 0
	}
	return max(0, time.Duration(a.DurationMS)*time.Millisecond-time.Since(a.At))
}

type auraTable struct {
	mu sync.Mutex
	m  map[uint64]map[uint8]AuraInfo
}

func (t *auraTable) set(unit uint64, a AuraInfo, hooks []func(uint64, *AuraInfo, *AuraInfo)) {
	t.mu.Lock()
	if t.m == nil {
		t.m = map[uint64]map[uint8]AuraInfo{}
	}
	slots := t.m[unit]
	if slots == nil {
		slots = map[uint8]AuraInfo{}
		t.m[unit] = slots
	}
	var prev *AuraInfo
	if p, ok := slots[a.Slot]; ok {
		prev = &p
	}
	var cur *AuraInfo
	if a.Spell == 0 {
		delete(slots, a.Slot)
		if len(slots) == 0 {
			delete(t.m, unit)
		}
	} else {
		slots[a.Slot] = a
		cur = &a
	}
	t.mu.Unlock()
	for _, h := range hooks {
		h(unit, prev, cur)
	}
}

func (t *auraTable) replace(unit uint64, all []AuraInfo, hooks []func(uint64, *AuraInfo, *AuraInfo)) {
	t.mu.Lock()
	if t.m == nil {
		t.m = map[uint64]map[uint8]AuraInfo{}
	}
	old := t.m[unit]
	slots := map[uint8]AuraInfo{}
	for _, a := range all {
		slots[a.Slot] = a
	}
	t.m[unit] = slots
	if len(slots) == 0 {
		delete(t.m, unit)
	}
	t.mu.Unlock()
	for _, h := range hooks {
		for slot, p := range old {
			if n, ok := slots[slot]; !ok || n.Spell != p.Spell {
				p := p
				h(unit, &p, nil)
			}
		}
		for slot, n := range slots {
			if p, ok := old[slot]; !ok || p.Spell != n.Spell {
				n := n
				h(unit, nil, &n)
			}
		}
	}
}

// Auras: a unit's auras, by slot order.
func (w *WorldClient) Auras(unit uint64) []AuraInfo {
	w.auras.mu.Lock()
	defer w.auras.mu.Unlock()
	out := []AuraInfo{}
	for _, a := range w.auras.m[unit] {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

// AddAuraHook: fn(unit, prev, cur) for every aura gained (prev nil), lost (cur nil) or changed (stacks, refresh).
func (w *WorldClient) AddAuraHook(fn func(unit uint64, prev, cur *AuraInfo)) {
	w.auraHookMu.Lock()
	defer w.auraHookMu.Unlock()
	w.auraHookList = append(w.auraHookList, fn)
}

func (w *WorldClient) auraHooks() []func(uint64, *AuraInfo, *AuraInfo) {
	w.auraHookMu.Lock()
	defer w.auraHookMu.Unlock()
	return append([]func(uint64, *AuraInfo, *AuraInfo){}, w.auraHookList...)
}

// Jump: MSG_MOVE_JUMP, then MSG_MOVE_FALL_LAND when we land, as the real client does.
func (w *WorldClient) Jump() error {
	if w.serverDriven() {
		return ErrServerDriven
	}
	return w.SendJump()
}
