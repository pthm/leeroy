package session

// Transports: boats and zeppelins (gameobject type 15, on a taxi path: transportpath.go) and elevators and trams
// (type 11, on an animation: transporttable.go). Where each is, from the path progress the server sends when it comes
// into view (every type-15 transport on our map when we arrive on it, Map::SendInitTransports; an elevator when it's
// near), and boarding and leaving one by its stored routine (transports.json): at a stop, wait for it to be there, then
// walk between the stop's wait point and the stop's spot aboard. A boat keeps its clock on another map, so one seen
// once stays known.

import (
	"cmp"
	"math"
	"sync"
	"time"

	"github.com/pthm/leeroy/azsdk/script"
	"github.com/pthm/leeroy/azsdk/types"
)

const (
	goTypeStaticTransport = 11
	goTypeMOTransport     = 15
)

type transportRec struct {
	entry uint32
	pp    uint32          // path progress when seen
	at    time.Time       // when
	table *tableTransport // its stored routine, if it has one
}

type transportsState struct {
	mu    sync.Mutex
	recs  map[uint64]*transportRec
	paths map[uint32]*transportPath // by entry; nil: its template or path couldn't be built
}

// scanTransports records the transports in view and their path progress (a newer sighting replaces an older one):
// every boat, and the elevators and trams with a stored routine.
func (b *Bot) scanTransports() {
	t := b.trs
	_, _, _, _, m := b.world.Position()
	for _, o := range b.world.GameObjects() {
		typ := (o.Value(fieldGOBytes1) >> 8) & 0xFF
		if (typ != goTypeMOTransport && typ != goTypeStaticTransport) || o.PathProgressAt.IsZero() {
			continue
		}
		entry := o.Entry
		if typ == goTypeMOTransport {
			entry = b.object(o).Entry // asks for the template, if it hasn't come yet
		}
		x, y, _ := o.InterpolatedPosition()
		tt := tableFor(entry, m, x, y)
		if typ == goTypeStaticTransport && tt == nil {
			continue
		}
		t.mu.Lock()
		if r := t.recs[o.GUID]; r == nil || o.PathProgressAt.After(r.at) {
			t.recs[o.GUID] = &transportRec{entry: entry, pp: o.PathProgress, at: o.PathProgressAt, table: tt}
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

// transportState is where transport guid is at now, its stored routine, the table stop it waits at (-1 while it
// moves) and, for each table stop, the ms until it next arrives there and leaves it (0 arriving: it's there).
func (b *Bot) transportState(guid uint64, now time.Time) (pose transportPose, tt *tableTransport, at int, arrive, depart []uint32, ok bool) {
	b.trs.mu.Lock()
	r := b.trs.recs[guid]
	b.trs.mu.Unlock()
	if r == nil {
		return transportPose{}, nil, -1, nil, nil, false
	}
	tt, at = r.table, -1
	pp := r.pp + uint32(now.Sub(r.at).Milliseconds())
	until := func(ms, period uint32) uint32 { return (ms + period - pp%period) % period }
	if tt != nil && len(tt.Anim) > 0 { // an elevator or a tram
		period := tt.period()
		p := tt.staticAt(pp % period)
		pose = transportPose{mapID: tt.Map, x: float64(p[0]), y: float64(p[1]), z: float64(p[2]), o: float64(tt.Spawn[3]), moving: true, stop: -1}
		for i, s := range tt.Stops {
			a, d := until(s.Arrive, period), until(s.Depart, period)
			if d < a || d == 0 && s.Arrive == s.Depart { // between arriving and leaving
				a, at, pose.moving = 0, i, false
			}
			arrive, depart = append(arrive, a), append(depart, d)
		}
		return pose, tt, at, arrive, depart, true
	}
	tp := b.transportPathOf(r.entry)
	if tp == nil {
		return transportPose{}, tt, -1, nil, nil, false
	}
	pose = tp.at(pp)
	frames := []int{} // its stops: the table's, else the path's
	if tt != nil {
		for _, s := range tt.Stops {
			frames = append(frames, s.Frame)
		}
	} else {
		for i := range tp.frames {
			if tp.frames[i].stop() {
				frames = append(frames, i)
			}
		}
	}
	for i, f := range frames {
		a, d := until(tp.frames[f].arrive, tp.period), until(tp.frames[f].depart, tp.period)
		if pose.stop == f {
			a, at = 0, i
		}
		arrive, depart = append(arrive, a), append(depart, d)
	}
	return pose, tt, at, arrive, depart, true
}

// transportAt is where transport guid is at now (for our position while aboard).
func (b *Bot) transportAt(guid uint64, now time.Time) (transportPose, bool) {
	pose, _, _, _, _, ok := b.transportState(guid, now)
	return pose, ok
}

// transportView is a transport for scripts.
func (b *Bot) transportView(guid uint64, now time.Time, aboard uint64) (types.Transport, bool) {
	pose, tt, at, arrive, depart, ok := b.transportState(guid, now)
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
		Z: round1f(pose.z), O: math.Round(pose.o*100) / 100, Moving: pose.moving, Aboard: guid == aboard,
		Stops: []types.TransportStop{}, Seen: true,
	}
	if tt != nil {
		v.Kind, v.Name = tt.Kind, cmp.Or(name, tt.Name)
		if len(tt.Anim) > 0 {
			v.PeriodS = float64(tt.period()) / 1000
		}
	}
	if tp := b.transportPathOf(entry); tp != nil && (tt == nil || len(tt.Anim) == 0) {
		v.PeriodS = float64(tp.period) / 1000
	}
	for i := range arrive {
		s := types.TransportStop{ArrivesInS: float64(arrive[i]) / 1000, DepartsInS: float64(depart[i]) / 1000}
		if tt != nil {
			st := tt.Stops[i]
			s.Map, s.X, s.Y, s.Z = st.Map, round1f(float64(st.At[0])), round1f(float64(st.At[1])), round1f(float64(st.At[2]))
			s.Wait = &types.Position{X: round1f(float64(st.Wait[0])), Y: round1f(float64(st.Wait[1])), Z: round1f(float64(st.Wait[2])), Map: st.Map}
		}
		if i == at {
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

// stopRange: how near a stop's wait point you must stand to board there; walkSpeed: the walk on or off (run speed);
// walkMargin: how long, over the walk, it must still be at the stop (the smallest lifts wait 1.3 s; the clock is good to
// a fifth of a second)
const (
	stopRange  = 12
	walkSpeed  = 7.0
	walkMargin = 0.35
)

func (b *Bot) setupTransports() {
	e, w := b.lua, b.world
	b.trs = &transportsState{recs: map[uint64]*transportRec{}, paths: map[uint32]*transportPath{}}
	w.TransportPose = func(guid uint64) (x, y, z, o float32, ok bool) {
		p, ok := b.transportAt(guid, time.Now())
		return float32(p.x), float32(p.y), float32(p.z), float32(p.o), ok
	}
	e.Func("transports.list", "() → Transport[]", "the boats, zeppelins, elevators and trams on your map (the server shows them when you arrive; a boat on another continent's leg, when it comes back), with where each stops, where to wait for it there (stops[].wait), and when it next arrives at and leaves each stop; elevators and trams on your map not yet in view are listed with seen false (their stops, no times)",
		func() []types.Transport {
			b.scanTransports()
			aboard, _ := w.Transport()
			_, _, _, _, m := w.Position()
			b.trs.mu.Lock()
			guids := make([]uint64, 0, len(b.trs.recs))
			seen := map[*tableTransport]bool{}
			for g, r := range b.trs.recs {
				guids = append(guids, g)
				seen[r.table] = true
			}
			b.trs.mu.Unlock()
			out := []types.Transport{}
			now := time.Now()
			for _, g := range guids {
				if v, ok := b.transportView(g, now, aboard); ok {
					out = append(out, v)
				}
			}
			for i := range transportTable { // the elevators and trams here we haven't seen: where, not when
				tt := &transportTable[i]
				if len(tt.Anim) == 0 || tt.Map != m || seen[tt] {
					continue
				}
				v := types.Transport{Entry: tt.Entry, Name: tt.Name, Kind: tt.Kind, Map: tt.Map, X: round1f(float64(tt.Spawn[0])),
					Y: round1f(float64(tt.Spawn[1])), Z: round1f(float64(tt.Spawn[2])), PeriodS: float64(tt.period()) / 1000, Stops: []types.TransportStop{}}
				for _, st := range tt.Stops {
					v.Stops = append(v.Stops, types.TransportStop{Map: st.Map, X: round1f(float64(st.At[0])), Y: round1f(float64(st.At[1])), Z: round1f(float64(st.At[2])),
						Wait: &types.Position{X: round1f(float64(st.Wait[0])), Y: round1f(float64(st.Wait[1])), Z: round1f(float64(st.Wait[2])), Map: st.Map}})
				}
				out = append(out, v)
			}
			sortBy(out, func(t types.Transport) float64 { return float64(t.Entry) })
			return out
		})
	e.Func("transports.board", "(guid) → Request<Boarding>", "board a transport at the stop you're waiting at (within 12 yd of its wait point, transports.list: stops[].wait): waits for it to be there with time to step on, then walks onto it; aboard, it carries you (me.position() follows) until transports.leave. too_far; no_route: no stored routine for it. Times out if it doesn't come within a round",
		func(guid uint64) map[string]any {
			args := map[string]any{"transport": gs(guid)}
			b.scanTransports()
			_, tt, _, _, _, ok := b.transportState(guid, time.Now())
			if !ok {
				return b.refuse("transports.board", args, "not_found", "no such transport seen (transports.list)")
			}
			if tt == nil {
				return b.refuse("transports.board", args, "no_route", "no stored routine for boarding it")
			}
			x, y, z, _, m := w.Position()
			stop, best := -1, math.Inf(1)
			for i, s := range tt.Stops {
				if d := math.Hypot(float64(s.Wait[0]-x), float64(s.Wait[1]-y)); s.Map == m && d < best && math.Abs(float64(s.Wait[2]-z)) < 8 {
					stop, best = i, d
				}
			}
			if best > stopRange {
				return b.refuse("transports.board", args, "too_far", "wait at one of its stops: within 12 yd of a stop's wait point (transports.list: stops[].wait)")
			}
			st := tt.Stops[stop]
			args["stop"] = stop + 1
			done := make(chan error, 1)
			started := false
			return b.request("transports.board", args, b.roundTimeout(guid), func() *script.Outcome {
				if !started {
					pose, _, at, _, depart, ok := b.transportState(guid, time.Now())
					if !ok || at != stop {
						return nil // not here yet
					}
					to := pose.toWorld([4]float32{st.Deck[0], st.Deck[1], st.Deck[2], 0})
					px, py, _, _, _ := w.Position()
					walk := math.Hypot(float64(to[0]-px), float64(to[1]-py))/walkSpeed + walkMargin
					if float64(depart[stop])/1000 < walk {
						return nil // leaving: the next time round
					}
					started = true
					go func() { done <- w.WalkStraight(to, guid, [4]float32{st.Deck[0], st.Deck[1], st.Deck[2], 0}) }()
					return nil
				}
				select {
				case err := <-done:
					if err != nil {
						return script.Fail("failed", err.Error(), nil)
					}
					return script.Ok(types.Boarding{Transport: types.GUID(guid), X: round1f(float64(st.Deck[0])), Y: round1f(float64(st.Deck[1])), Z: round1f(float64(st.Deck[2]))})
				default:
					return nil
				}
			}, nil)
		})
	e.Func("transports.leave", "() → Request<Position>", "leave the transport you're on at the stop it's at, or the next one: waits for it to be there, then walks off onto the stop's wait point (the pier, the landing). not_aboard; no_route: no stored routine for it. Times out if it doesn't stop within a round",
		func() map[string]any {
			guid, _ := w.Transport()
			if guid == 0 {
				return b.refuse("transports.leave", nil, "not_aboard", "you're not on a transport")
			}
			if _, tt, _, _, _, ok := b.transportState(guid, time.Now()); !ok || tt == nil {
				return b.refuse("transports.leave", nil, "no_route", "no stored routine for leaving it")
			}
			done := make(chan error, 1)
			started := false
			return b.request("transports.leave", map[string]any{"transport": gs(guid)}, b.roundTimeout(guid), func() *script.Outcome {
				if !started {
					_, tt, at, _, depart, ok := b.transportState(guid, time.Now())
					if !ok || at < 0 {
						return nil // between stops
					}
					wp := tt.Stops[at].Wait
					px, py, _, _, _ := w.Position()
					if float64(depart[at])/1000 < math.Hypot(float64(wp[0]-px), float64(wp[1]-py))/walkSpeed+walkMargin {
						return nil // leaving: the next stop
					}
					started = true
					go func() { done <- w.WalkStraight(wp, 0, [4]float32{}) }()
					return nil
				}
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

// roundTimeout: how long boarding or leaving waits for transport guid: a whole round of it, and the walk
func (b *Bot) roundTimeout(guid uint64) time.Duration {
	b.trs.mu.Lock()
	r := b.trs.recs[guid]
	b.trs.mu.Unlock()
	period := uint32(0)
	if r != nil && r.table != nil && len(r.table.Anim) > 0 {
		period = r.table.period()
	} else if r != nil {
		if tp := b.transportPathOf(r.entry); tp != nil {
			period = tp.period
		}
	}
	return time.Duration(period)*time.Millisecond + 30*time.Second
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
