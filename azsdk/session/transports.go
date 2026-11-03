package session

// Boats and zeppelins: where each is (from its path progress, transportpath.go), boarding one at its dock and leaving it
// at another. The server sends every transport on our map when we arrive on it (Map::SendInitTransports), with its
// path progress; a transport keeps its clock on another map, so one seen once stays known.

import (
	"math"
	"sync"
	"time"

	"github.com/pthm/leeroy/azsdk/script"
	"github.com/pthm/leeroy/azsdk/types"
)

const goTypeMOTransport = 15

type transportRec struct {
	entry uint32
	pp    uint32    // path progress when seen
	at    time.Time // when
}

type transportsState struct {
	mu    sync.Mutex
	recs  map[uint64]*transportRec
	paths map[uint32]*transportPath // by entry; nil: its template or path couldn't be built
}

// scanTransports records the transports in view and their path progress (a newer sighting replaces an older one).
func (b *Bot) scanTransports() {
	t := b.trs
	for _, o := range b.world.GameObjects() {
		if (o.Value(fieldGOBytes1)>>8)&0xFF != goTypeMOTransport || o.PathProgressAt.IsZero() {
			continue
		}
		entry := b.object(o).Entry // asks for the template, if it hasn't come yet
		t.mu.Lock()
		if r := t.recs[o.GUID]; r == nil || o.PathProgressAt.After(r.at) {
			t.recs[o.GUID] = &transportRec{entry: entry, pp: o.PathProgress, at: o.PathProgressAt}
		}
		t.mu.Unlock()
	}
}

// transportPathOf is entry's path, built from its template (data0 the taxi path, data1 speed, data2 acceleration).
func (b *Bot) transportPathOf(entry uint32) *transportPath {
	t := b.trs
	t.mu.Lock()
	tp, done := t.paths[entry]
	t.mu.Unlock()
	if done {
		return tp
	}
	creatures.Lock()
	tmpl := goNames.t[entry]
	creatures.Unlock()
	if tmpl == nil {
		return nil // not yet: asked for
	}
	tp = newTransportPath(loadPathNodes(b.config.DataDir)[tmpl.data[0]], float64(tmpl.data[1]), float64(tmpl.data[2]))
	t.mu.Lock()
	t.paths[entry] = tp
	t.mu.Unlock()
	return tp
}

// transportAt is where transport guid is at now, its path and its path progress then.
func (b *Bot) transportAt(guid uint64, now time.Time) (transportPose, *transportPath, uint32, bool) {
	b.trs.mu.Lock()
	r := b.trs.recs[guid]
	b.trs.mu.Unlock()
	if r == nil {
		return transportPose{}, nil, 0, false
	}
	tp := b.transportPathOf(r.entry)
	if tp == nil {
		return transportPose{}, nil, 0, false
	}
	pp := r.pp + uint32(now.Sub(r.at).Milliseconds())
	pose := tp.at(pp)
	return pose, tp, pp, true
}

// transportView is a transport for scripts.
func (b *Bot) transportView(guid uint64, now time.Time, aboard uint64) (types.Transport, bool) {
	pose, tp, pp, ok := b.transportAt(guid, now)
	if !ok {
		return types.Transport{}, false
	}
	b.trs.mu.Lock()
	entry := b.trs.recs[guid].entry
	b.trs.mu.Unlock()
	creatures.Lock()
	name := goNames.m[entry]
	creatures.Unlock()
	v := types.Transport{
		GUID: types.GUID(guid), Entry: entry, Name: name, Map: pose.mapID, X: round1f(pose.x), Y: round1f(pose.y),
		Z: round1f(pose.z), O: math.Round(pose.o*100) / 100, Moving: pose.moving, PeriodS: float64(tp.period) / 1000, Aboard: guid == aboard,
		Stops: []types.TransportStop{},
	}
	timer := pp % tp.period
	until := func(ms uint32) float64 { return float64((ms+tp.period-timer)%tp.period) / 1000 }
	for i := range tp.frames {
		f := &tp.frames[i]
		if !f.stop() {
			continue
		}
		s := types.TransportStop{
			Map: f.node.mapID, X: round1f(f.node.x), Y: round1f(f.node.y), Z: round1f(f.node.z),
			ArrivesInS: until(f.arrive), DepartsInS: until(f.depart),
		}
		if pose.stop == i {
			s.ArrivesInS = 0
			d := s
			v.Docked = &d
		}
		v.Stops = append(v.Stops, s)
	}
	return v, true
}

func round1f(v float64) float64 { return math.Round(v*10) / 10 }

// toWorld: a point in a transport's axes, in the world, at its pose
func (p transportPose) toWorld(l [4]float32) [3]float32 {
	s, c := math.Sincos(p.o)
	return [3]float32{float32(p.x + float64(l[0])*c - float64(l[1])*s), float32(p.y + float64(l[1])*c + float64(l[0])*s), float32(p.z) + l[2]}
}

// boardRange: how far from a deck spot you can walk onto it (the pier beside a docked ship)
const boardRange = 35

func (b *Bot) setupTransports() {
	e, w := b.lua, b.world
	b.trs = &transportsState{recs: map[uint64]*transportRec{}, paths: map[uint32]*transportPath{}}
	w.TransportPose = func(guid uint64) (x, y, z, o float32, ok bool) {
		p, _, _, ok := b.transportAt(guid, time.Now())
		return float32(p.x), float32(p.y), float32(p.z), float32(p.o), ok
	}
	e.Func("transports.list", "() → Transport[]", "every boat and zeppelin seen (the server shows all of a map's when you arrive on it): where it is now, where it docks on its round and when it next arrives at and leaves each stop",
		func() []types.Transport {
			b.scanTransports()
			aboard, _ := w.Transport()
			b.trs.mu.Lock()
			guids := make([]uint64, 0, len(b.trs.recs))
			for g := range b.trs.recs {
				guids = append(guids, g)
			}
			b.trs.mu.Unlock()
			out := []types.Transport{}
			now := time.Now()
			for _, g := range guids {
				if v, ok := b.transportView(g, now, aboard); ok {
					out = append(out, v)
				}
			}
			sortBy(out, func(t types.Transport) float64 { return float64(t.Entry) })
			return out
		})
	e.Func("transports.board", "(guid) → Request<Boarding>", "step onto a docked transport from beside it (within 35 yd of where its crew stand): you walk straight onto its deck and ride it from there (it carries you; me.position() follows). not_docked while it's moving; too_far; leaving it: transports.leave",
		func(guid uint64) map[string]any {
			args := map[string]any{"transport": gs(guid)}
			b.scanTransports()
			now := time.Now()
			pose, tp, pp, ok := b.transportAt(guid, now)
			if !ok {
				return b.refuse("transports.board", args, "not_found", "no such transport seen (transports.list)")
			}
			x, y, _, _, m := w.Position()
			if pose.mapID != m {
				return b.refuse("transports.board", args, "wrong_map", "it's on another map now")
			}
			if pose.stop < 0 {
				return b.refuse("transports.board", args, "not_docked", "it's moving: board it while it waits at a stop (transports.list: stops, docked)")
			}
			if left := (tp.frames[pose.stop].depart + tp.period - pp%tp.period) % tp.period; left < 3000 {
				return b.refuse("transports.board", args, "not_docked", "it's leaving")
			}
			// where to stand: the crew's spots, the nearest to us (the pier's side)
			var spot [4]float32
			best := math.Inf(1)
			for _, o := range w.GetNearbyUnits(2000) { // their world positions are where they were when they came into view
				if o.OnTransport != guid {
					continue
				}
				p := pose.toWorld(o.TransportPos)
				if d := math.Hypot(float64(p[0]-x), float64(p[1]-y)); d < best {
					best, spot = d, [4]float32{o.TransportPos[0], o.TransportPos[1], o.TransportPos[2], 0}
				}
			}
			if math.IsInf(best, 1) {
				return b.refuse("transports.board", args, "not_available", "none of its crew in view to tell where its deck is: come closer")
			}
			if best > boardRange {
				return b.refuse("transports.board", args, "too_far", "stand within 35 yd of its deck (the pier beside it)")
			}
			to := pose.toWorld(spot)
			done := make(chan error, 1)
			go func() { done <- w.WalkStraight(to, guid, spot) }()
			return b.request("transports.board", args, 15*time.Second, func() *script.Outcome {
				select {
				case err := <-done:
					if err != nil {
						return script.Fail("failed", err.Error(), nil)
					}
					return script.Ok(types.Boarding{Transport: types.GUID(guid), X: round1f(float64(spot[0])), Y: round1f(float64(spot[1])), Z: round1f(float64(spot[2]))})
				default:
					return nil
				}
			}, nil)
		})
	e.Func("transports.leave", "([x, y, z]) → Request<Position>", "walk straight off the transport you're on, onto (x, y, z), at most 35 yd away; without one, the nearest walkable floor within 35 yd (the pier, at a dock). not_aboard",
		func(x, y, z any) map[string]any {
			guid, _ := w.Transport()
			if guid == 0 {
				return b.refuse("transports.leave", nil, "not_aboard", "you're not on a transport")
			}
			px, py, pz, _, _ := w.Position()
			var to [3]float32
			if fx, ok := x.(float64); ok {
				fy, _ := y.(float64)
				fz, _ := z.(float64)
				to = [3]float32{float32(fx), float32(fy), float32(fz)}
			} else if p, ok := b.nearestFloor(px, py, pz, boardRange); ok {
				to = p
			} else {
				return b.refuse("transports.leave", nil, "no_floor", "no walkable floor within 35 yd: wait for a dock")
			}
			if math.Hypot(float64(to[0]-px), float64(to[1]-py)) > boardRange {
				return b.refuse("transports.leave", nil, "too_far", "at most 35 yd from where you stand")
			}
			done := make(chan error, 1)
			go func() { done <- w.WalkStraight(to, 0, [4]float32{}) }()
			return b.request("transports.leave", nil, 15*time.Second, func() *script.Outcome {
				select {
				case err := <-done:
					if err != nil {
						return script.Fail("failed", err.Error(), nil)
					}
					qx, qy, qz, _, m := w.Position()
					return script.Ok(types.Position{X: float64(qx), Y: float64(qy), Z: float64(qz), Map: m})
				default:
					return nil
				}
			}, nil)
		})
}

// nearestFloor: the nearest point with navmesh floor within r yd of (x, y) at about height z (8 yd up or down): rings
// of 16 points 2 yd apart.
func (b *Bot) nearestFloor(x, y, z, r float32) ([3]float32, bool) {
	fn, ok := b.nav.(interface {
		FloorHeight(mapID uint32, x, y, hint float32) (float32, bool)
	})
	if !ok || b.nav == nil {
		return [3]float32{}, false
	}
	_, _, _, _, m := b.world.Position()
	for d := float32(2); d <= r; d += 2 {
		for k := 0; k < 16; k++ {
			s, c := math.Sincos(float64(k) * math.Pi / 8)
			px, py := x+d*float32(c), y+d*float32(s)
			if h, ok := fn.FloorHeight(m, px, py, z); ok {
				return [3]float32{px, py, h}, true
			}
		}
	}
	return [3]float32{}, false
}
