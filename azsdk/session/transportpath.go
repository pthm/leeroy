package session

// Where a boat or zeppelin is. The server moves a transport along its taxi path (TaxiPathNode.dbc, the template's
// data0) by its path progress, a clock in ms it sends only when the transport comes into view (UPDATEFLAG_TRANSPORT);
// the real client computes the position from it, and so do we. A port of AzerothCore's TransportMgr::GeneratePath
// (key frames, their distances on catmull-rom splines, accelerating to and braking from moveSpeed at accelRate) and
// MotionTransport::Update / CalculateSegmentPos.

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

type pathNode struct {
	index        uint32
	mapID        uint32
	x, y, z      float64
	flags, delay uint32 // flags: 1 teleport, 2 stop (delay seconds)
}

var pathNodes = struct {
	once   sync.Once
	byPath map[uint32][]pathNode
}{}

// loadPathNodes reads TaxiPathNode.dbc: ID, PathID, NodeIndex, ContinentID, x, y, z, Flags, Delay, ArrivalEventID,
// DepartureEventID.
func loadPathNodes(dataDir string) map[uint32][]pathNode {
	pathNodes.once.Do(func() {
		pathNodes.byPath = map[uint32][]pathNode{}
		b, err := os.ReadFile(filepath.Join(dataDir, "dbc", "TaxiPathNode.dbc"))
		if err != nil || len(b) < 20 || string(b[:4]) != "WDBC" {
			return
		}
		le := binary.LittleEndian
		n, rs := le.Uint32(b[4:]), le.Uint32(b[12:])
		for i := uint32(0); i < n; i++ {
			r := b[20+i*rs:]
			f := func(c int) float64 { return float64(math.Float32frombits(le.Uint32(r[c*4:]))) }
			path := le.Uint32(r[4:])
			pathNodes.byPath[path] = append(pathNodes.byPath[path], pathNode{index: le.Uint32(r[8:]), mapID: le.Uint32(r[12:]),
				x: f(4), y: f(5), z: f(6), flags: le.Uint32(r[28:]), delay: le.Uint32(r[32:])})
		}
		for _, nodes := range pathNodes.byPath {
			sort.Slice(nodes, func(i, j int) bool { return nodes[i].index < nodes[j].index })
		}
	})
	return pathNodes.byPath
}

// spline is Movement::Spline in catmull-rom mode, not cyclic: points[0] and points[n+1] are the virtual ends.
type spline struct {
	points  [][3]float64
	lengths []float64 // lengths[i]: from the first segment's start to point i (lengths[1] = 0)
}

func newSpline(controls [][3]float64) *spline {
	n := len(controls)
	s := &spline{points: make([][3]float64, n+2)}
	copy(s.points[1:], controls)
	s.points[0] = [3]float64{controls[0][0] - 1, controls[0][1], controls[0][2]} // controls[0] - (cos 0, sin 0, 0)
	s.points[n+1] = controls[n-1]
	s.lengths = make([]float64, n+1)
	for i := 1; i < n; i++ { // initLengths: STEPS_PER_SEGMENT 3
		cur, length := s.points[i], 0.0
		for k := 1; k <= 3; k++ {
			next := s.eval(i, float64(k)/3)
			length += math.Sqrt(sq(next[0]-cur[0]) + sq(next[1]-cur[1]) + sq(next[2]-cur[2]))
			cur = next
		}
		s.lengths[i+1] = s.lengths[i] + length
	}
	return s
}

func sq(v float64) float64 { return v * v }

// length between points i and j (Spline::length(i, j))
func (s *spline) length(i, j int) float64 { return s.lengths[j] - s.lengths[i] }

var catmullRom = [4][4]float64{{-0.5, 1.5, -1.5, 0.5}, {1, -2.5, 2, -0.5}, {-0.5, 0, 0.5, 0}, {0, 1, 0, 0}}

func (s *spline) weigh(index int, tv [4]float64) [3]float64 {
	var out [3]float64
	for k := 0; k < 4; k++ {
		w := tv[0]*catmullRom[0][k] + tv[1]*catmullRom[1][k] + tv[2]*catmullRom[2][k] + tv[3]*catmullRom[3][k]
		p := s.points[index-1+k]
		out[0], out[1], out[2] = out[0]+p[0]*w, out[1]+p[1]*w, out[2]+p[2]*w
	}
	return out
}

// eval is the position t (0..1) of the way along segment index; deriv its direction.
func (s *spline) eval(index int, t float64) [3]float64 {
	return s.weigh(index, [4]float64{t * t * t, t * t, t, 1})
}
func (s *spline) deriv(index int, t float64) [3]float64 {
	return s.weigh(index, [4]float64{3 * t * t, 2 * t, 1, 0})
}

type keyFrame struct {
	node                         pathNode
	index                        int
	distSinceStop, distUntilStop float64
	distFromPrev, nextDist       float64
	timeFrom, timeTo             float64
	teleport                     bool
	arrive, depart, nextArrive   uint32
	spline                       *spline
}

func (k *keyFrame) stop() bool { return k.node.flags == 2 }

// transportPath is a transport template's path: its key frames and timing.
type transportPath struct {
	frames               []keyFrame
	period               uint32 // ms: the path's length in time (GAMEOBJECT_LEVEL)
	speed, accel         float64
	accelTime, accelDist float64
}

// newTransportPath is TransportMgr::GeneratePath for a path's nodes and the template's speed and acceleration; nil if
// it can't be built.
func newTransportPath(nodes []pathNode, speed, accel float64) *transportPath {
	if len(nodes) < 3 || speed <= 0 || accel <= 0 {
		return nil
	}
	tp := &transportPath{speed: speed, accel: accel}
	var frames []keyFrame
	var splinePath [][3]float64
	mapChange := false
	for i, nd := range nodes {
		if mapChange {
			mapChange = false
			continue
		}
		if i != len(nodes)-1 && (nd.flags&1 != 0 || nd.mapID != nodes[i+1].mapID) {
			if len(frames) > 0 {
				frames[len(frames)-1].teleport = true
			}
			mapChange = true
			continue
		}
		frames = append(frames, keyFrame{node: nd, distSinceStop: -1, distUntilStop: -1, distFromPrev: -1})
		splinePath = append(splinePath, [3]float64{nd.x, nd.y, nd.z})
	}
	if len(splinePath) >= 2 { // the catmull-rom spline's own end points
		splinePath, frames = splinePath[1:len(splinePath)-1], frames[1:len(frames)-1]
	}
	if len(frames) < 2 {
		return nil
	}
	frames[len(frames)-1].teleport = true // last to first is always a teleport, even for closed paths
	tp.accelTime, tp.accelDist = speed/accel, 0.5*speed*speed/accel

	firstStop, lastStop := -1, -1
	frames[0].distFromPrev, frames[0].index = 0, 1
	if frames[0].stop() {
		firstStop, lastStop = 0, 0
	}
	start := 0
	for i := 1; i < len(frames); i++ {
		if frames[i-1].teleport || i+1 == len(frames) {
			extra := 0
			if !frames[i-1].teleport {
				extra = 1
			}
			sp := newSpline(splinePath[start : i+extra])
			for j := start; j < i+extra; j++ {
				frames[j].index = j - start + 1
				frames[j].distFromPrev = sp.length(j-start, j+1-start)
				if j > 0 {
					frames[j-1].nextDist = frames[j].distFromPrev
				}
				frames[j].spline = sp
			}
			if frames[i-1].teleport {
				frames[i].index = i - start + 1
				frames[i].distFromPrev = 0
				frames[i-1].nextDist = 0
				frames[i].spline = sp
			}
			start = i
		}
		if frames[i].stop() {
			if firstStop == -1 {
				firstStop = i
			}
			lastStop = i
		}
	}
	n := len(frames)
	frames[n-1].nextDist = frames[0].distFromPrev
	if firstStop == -1 || lastStop == -1 {
		firstStop, lastStop = 0, 0
	}
	d := 0.0
	for i := 0; i < n; i++ {
		j := (i + lastStop) % n
		if frames[j].stop() || j == lastStop {
			d = 0
		} else {
			d += frames[j].distFromPrev
		}
		frames[j].distSinceStop = d
	}
	d = 0
	for i := n - 1; i >= 0; i-- {
		j := (i + firstStop) % n
		d += frames[(j+1)%n].distFromPrev
		frames[j].distUntilStop = d
		if frames[j].stop() || j == firstStop {
			d = 0
		}
	}
	ad := tp.accelDist
	for i := range frames {
		f := &frames[i]
		switch total := f.distSinceStop + f.distUntilStop; {
		case total < 2*ad: // won't reach full speed
			if f.distSinceStop < f.distUntilStop {
				f.timeTo = 2*math.Sqrt(total/accel) - math.Sqrt(2*f.distSinceStop/accel)
			} else {
				f.timeTo = math.Sqrt(2 * f.distUntilStop / accel)
			}
		case f.distSinceStop < ad:
			f.timeTo = total/speed + speed/accel - math.Sqrt(2*f.distSinceStop/accel)
		case f.distUntilStop < ad:
			f.timeTo = math.Sqrt(2 * f.distUntilStop / accel)
		default:
			f.timeTo = f.distUntilStop/speed + 0.5*speed/accel
		}
	}
	seg := 0.0
	for i := 0; i < n; i++ {
		j := (i + lastStop) % n
		if frames[j].stop() || j == lastStop {
			seg = frames[j].timeTo
		}
		frames[j].timeFrom = seg - frames[j].timeTo
	}
	cur := 0.0
	if frames[0].stop() {
		cur = float64(frames[0].node.delay)
		frames[0].depart = uint32(cur * 1000)
	}
	for i := 1; i < n; i++ {
		cur += frames[i-1].timeTo
		if frames[i].stop() {
			frames[i].arrive = uint32(cur * 1000)
			frames[i-1].nextArrive = frames[i].arrive
			cur += float64(frames[i].node.delay)
			frames[i].depart = uint32(cur * 1000)
		} else {
			cur -= frames[i].timeTo
			frames[i].arrive = uint32(cur * 1000)
			frames[i-1].nextArrive = frames[i].arrive
			frames[i].depart = frames[i].arrive
		}
	}
	frames[n-1].nextArrive = frames[n-1].depart
	tp.period = frames[n-1].depart
	tp.frames = frames
	return tp
}

// transportPose is where a transport is at a moment of its path.
type transportPose struct {
	mapID      uint32
	x, y, z, o float64
	moving     bool
	stop       int // the stop frame it's waiting at, else -1
}

// at is the pose at path progress ms (MotionTransport::Update's current frame, then its position).
func (tp *transportPath) at(progress uint32) transportPose {
	if tp == nil || tp.period == 0 {
		return transportPose{stop: -1}
	}
	timer := progress % tp.period
	for i := range tp.frames {
		f := &tp.frames[i]
		if timer >= f.arrive && timer < f.depart { // waiting at a stop
			return tp.restingAt(i)
		}
		if timer >= f.depart && timer < f.nextArrive {
			if f.teleport || f.nextDist == 0 || f.spline == nil { // the jump to the next map: there already
				return tp.restingAt((i + 1) % len(tp.frames))
			}
			t := tp.segmentPos(f, float64(timer)/1000)
			p, dir := f.spline.eval(f.index, t), f.spline.deriv(f.index, t)
			return transportPose{mapID: f.node.mapID, x: p[0], y: p[1], z: p[2], o: orientation(dir), moving: true, stop: -1}
		}
	}
	return tp.restingAt(0)
}

// restingAt: at key frame i, facing the way it came in (the server stops updating the position when it stops).
func (tp *transportPath) restingAt(i int) transportPose {
	f := &tp.frames[i]
	pose := transportPose{mapID: f.node.mapID, x: f.node.x, y: f.node.y, z: f.node.z, stop: i}
	if !f.stop() {
		pose.stop = -1
	}
	if f.spline != nil && f.index > 1 {
		pose.o = orientation(f.spline.deriv(f.index-1, 1))
	} else if f.spline != nil {
		pose.o = orientation(f.spline.deriv(f.index, 0))
	}
	return pose
}

func orientation(dir [3]float64) float64 {
	o := math.Atan2(dir[1], dir[0]) + math.Pi
	return math.Mod(o+2*math.Pi, 2*math.Pi)
}

// segmentPos is CalculateSegmentPos: how far (0..1) along frame f's segment the transport is at now (seconds).
func (tp *transportPath) segmentPos(f *keyFrame, now float64) float64 {
	since := f.timeFrom + (now - float64(f.depart)/1000)
	until := f.timeTo - (now - float64(f.depart)/1000)
	var pos float64
	if since < until {
		d := tp.accelDist + (since-tp.accelTime)*tp.speed
		if since < tp.accelTime {
			d = 0.5 * tp.accel * since * since
		}
		pos = d - f.distSinceStop
	} else {
		d := tp.accelDist + (until-tp.accelTime)*tp.speed
		if until < tp.accelTime {
			d = 0.5 * tp.accel * until * until
		}
		pos = f.distUntilStop - d
	}
	return pos / f.nextDist
}
