package session

// Travel the server drives: flights (taxi), and teleports (portals, hearthstone, summons, graveyards, instances).
// While the server moves you the client behaves like the real one: no movement of its own (client/control.go),
// move.to is refused with the reason, and your position follows the server's path. Events tell scripts when
// control changes hands (movement_changed, flight_start, flight_end, teleported).

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

const (
	smsgShowTaxiNodes       = 0x1A9
	cmsgTaxiQueryAvailable  = 0x1AC
	cmsgActivateTaxi        = 0x1AD
	smsgActivateTaxiReply   = 0x1AE
	smsgNewTaxiPath         = 0x1AF
	smsgBindPointUpdate     = 0x155
	cmsgBinderActivate      = 0x1B5
	cmsgQuestLogRemoveQuest = 0x194
	cmsgActivateTaxiExpress = 0x312
	flightSpeed             = 32.0 // yards per second (PLAYER_FLIGHT_SPEED)
)

type taxiNode struct {
	id, mapID uint32
	x, y, z   float32
	name      string
	alliance  bool // a mount for that side: the node serves it
	horde     bool
}

type taxiPath struct{ from, to, price uint32 }

var taxi = struct {
	once  sync.Once
	nodes map[uint32]*taxiNode
	paths map[uint32][]taxiPath // from -> paths
}{}

// loadTaxi reads TaxiNodes.dbc and TaxiPath.dbc (copied to <data>/dbc by the setup).
func loadTaxi() {
	taxi.once.Do(func() {
		taxi.nodes, taxi.paths = map[uint32]*taxiNode{}, map[uint32][]taxiPath{}
		read := func(name string) (n, rs uint32, recs, strs []byte) {
			b, err := os.ReadFile(filepath.Join(spellsDir, "dbc", name))
			if err != nil || len(b) < 20 || string(b[:4]) != "WDBC" {
				return 0, 0, nil, nil
			}
			n, rs = binary.LittleEndian.Uint32(b[4:]), binary.LittleEndian.Uint32(b[12:])
			return n, rs, b[20 : 20+n*rs], b[20+n*rs:]
		}
		le := binary.LittleEndian
		n, rs, recs, strs := read("TaxiNodes.dbc")
		for i := uint32(0); i < n; i++ {
			r := recs[i*rs:]
			f := func(c int) float32 { return math.Float32frombits(le.Uint32(r[c*4:])) }
			nd := &taxiNode{id: le.Uint32(r), mapID: le.Uint32(r[4:]), x: f(2), y: f(3), z: f(4),
				horde: le.Uint32(r[rs-8:]) != 0, alliance: le.Uint32(r[rs-4:]) != 0} // the last two fields: MountCreatureID[horde, alliance]
			if off := le.Uint32(r[5*4:]); off < uint32(len(strs)) {
				nd.name = string(bytes.SplitN(strs[off:], []byte{0}, 2)[0])
			}
			taxi.nodes[nd.id] = nd
		}
		n, rs, recs, _ = read("TaxiPath.dbc")
		for i := uint32(0); i < n; i++ {
			r := recs[i*rs:]
			p := taxiPath{from: le.Uint32(r[4:]), to: le.Uint32(r[8:]), price: le.Uint32(r[12:])}
			taxi.paths[p.from] = append(taxi.paths[p.from], p)
		}
	})
}

type travelState struct {
	mu      sync.Mutex
	known   map[uint32]bool
	current map[uint64]uint32 // flight master -> its node (from its map)
	flight  *types.Flight     // the flight we asked for, until we land
	started time.Time
	lock    string // last MovementLock, for movement_changed
	lastPos types.Position
	home    *types.Home // where the hearthstone goes (SMSG_BINDPOINTUPDATE)
	bound   time.Time   // the server confirmed a bind (the Bind spell, 3286)
}

func (b *Bot) taxiNode(id uint32, known bool) types.TaxiNode {
	loadTaxi()
	nd := taxi.nodes[id]
	if nd == nil {
		return types.TaxiNode{ID: id}
	}
	return types.TaxiNode{ID: id, Name: nd.name, Map: nd.mapID, X: round1(nd.x), Y: round1(nd.y), Z: round1(nd.z), Known: known}
}

// myFaction: is the character Alliance? (taxi nodes serve one side or both)
func (b *Bot) alliance() bool {
	switch b.GetRace() {
	case 2, 5, 6, 8, 10:
		return false
	}
	return true
}

// route: the cheapest chain of known nodes from -> to (Dijkstra over TaxiPath), with its cost; nil if none.
func route(from, to uint32, known map[uint32]bool) ([]uint32, uint32) {
	loadTaxi()
	cost, prev := map[uint32]uint32{from: 0}, map[uint32]uint32{}
	done := map[uint32]bool{}
	for {
		cur, best := uint32(0), uint32(math.MaxUint32)
		for n, c := range cost {
			if !done[n] && c < best {
				cur, best = n, c
			}
		}
		if cur == 0 {
			return nil, 0
		}
		if cur == to {
			break
		}
		done[cur] = true
		for _, p := range taxi.paths[cur] {
			if !known[p.to] {
				continue
			}
			if c, ok := cost[p.to]; !ok || best+p.price < c {
				cost[p.to], prev[p.to] = best+p.price, cur
			}
		}
	}
	nodes := []uint32{to}
	for n := to; n != from; {
		n = prev[n]
		nodes = append([]uint32{n}, nodes...)
	}
	return nodes, cost[to]
}

// parseBindPoint: SMSG_BINDPOINTUPDATE: x, y, z, map, area
func parseBindPoint(data []byte) types.Home {
	r := bytes.NewReader(data)
	var x, y, z float32
	var m, area uint32
	for _, v := range []any{&x, &y, &z, &m, &area} {
		binary.Read(r, binary.LittleEndian, v)
	}
	return types.Home{X: round1(x), Y: round1(y), Z: round1(z), Map: m, Area: areaNames()[area]}
}

func (b *Bot) setupTravel() {
	t := &travelState{known: map[uint32]bool{}, current: map[uint64]uint32{}}
	if bp := b.world.BindPoint(); bp != nil { // sent at login, before this hook existed
		h := parseBindPoint(bp)
		t.home = &h
	}
	b.travel = t
	w, e := b.world, b.lua
	var menus = make(chan [2]uint64, 4) // (npc, node) as flight maps arrive, for flights.map
	w.AddPacketHook(func(op uint16, data []byte) {
		r := bytes.NewReader(data)
		u32 := func() (v uint32) { binary.Read(r, binary.LittleEndian, &v); return }
		switch op {
		case smsgShowTaxiNodes: // u32 1, npc guid, current node, mask u32[14]
			u32()
			var npc uint64
			binary.Read(r, binary.LittleEndian, &npc)
			cur := u32()
			t.mu.Lock()
			for i := uint32(0); i < 14; i++ {
				m := u32()
				for bit := uint32(0); bit < 32; bit++ {
					t.known[i*32+bit+1] = m&(1<<bit) != 0
				}
			}
			t.known[cur] = true
			t.current[npc] = cur
			t.mu.Unlock()
			select {
			case menus <- [2]uint64{npc, uint64(cur)}:
			default:
			}
		case smsgNewTaxiPath: // a flight point discovered: the nearest one
			x, y, _, _, m := w.Position()
			loadTaxi()
			best, bestD := uint32(0), math.MaxFloat64
			for id, nd := range taxi.nodes {
				if d := math.Hypot(float64(nd.x-x), float64(nd.y-y)); nd.mapID == m && d < bestD {
					best, bestD = id, d
				}
			}
			if best != 0 {
				t.mu.Lock()
				t.known[best] = true
				t.mu.Unlock()
				b.emit("flight_learned", types.FlightLearned{Node: b.taxiNode(best, true)})
			}
		case smsgBindPointUpdate: // after binding at an inn (the login one is read in setupTravel)
			h := parseBindPoint(data)
			t.mu.Lock()
			changed := t.home == nil || *t.home != h
			t.home = &h
			t.mu.Unlock()
			if changed {
				b.emit("home_changed", h)
			}
		case 0x1B3: // SMSG_TRAINER_BUY_SUCCEEDED: npc, spell; an innkeeper's bind confirms with the Bind spell
			if len(data) >= 12 && binary.LittleEndian.Uint32(data[8:]) == 3286 {
				t.mu.Lock()
				t.bound = time.Now()
				t.mu.Unlock()
			}
		case smsgActivateTaxiReply:
			if code := u32(); code != 0 {
				names := map[uint32]string{1: "server_error", 2: "no_such_path", 3: "not_enough_money", 4: "too_far",
					5: "no_flight_master", 6: "not_visited", 7: "busy", 8: "mounted", 9: "shapeshifted", 10: "moving",
					11: "same_node", 12: "not_standing"}
				b.emit("error", types.ErrorEvent{Op: "flights.take", Code: name(names, code),
					Message: "the flight master refused: " + name(names, code)})
			}
		}
	})

	e.Func("flights.nodes", "([map]) → TaxiNode[]", "every flight point for your faction (on one map: 0 Eastern Kingdoms, 1 Kalimdor, 530 Outland, 571 Northrend). known is only accurate after you've opened a flight map",
		func(m any) []types.TaxiNode {
			if m != nil {
				v, ok := m.(float64)
				if !ok || v < 0 || v > 4294967295 || v != math.Trunc(v) {
					panic("flights.nodes: map must be an unsigned integer")
				}
			}

			loadTaxi()
			t.mu.Lock()
			defer t.mu.Unlock()
			out := []types.TaxiNode{}
			ally := b.alliance()
			for id, nd := range taxi.nodes {
				if (ally && !nd.alliance) || (!ally && !nd.horde) {
					continue
				}
				if f, ok := m.(float64); ok && uint32(f) != nd.mapID {
					continue
				}
				out = append(out, b.taxiNode(id, t.known[id]))
			}
			sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
			return out
		})
	e.Func("flights.map", "(npc_guid) → Request<FlightMap>", "talk to a flight master within ~5 yd (role flight_master): discovers its flight point the first time, and lists the known points you can fly to from there, with costs",
		func(npc uint64) map[string]any {
			args := map[string]any{"npc_guid": gs(npc)}
			if u := b.GetUnitInfo(npc); u == nil || u.NPCFlags&0x2000 == 0 {
				return b.refuse("flights.map", args, "not_a_flight_master", "that's not a flight master in sight")
			}
			for len(menus) > 0 {
				<-menus
			}
			ask := func() { w.SendPacketRaw(cmsgTaxiQueryAvailable, binary.LittleEndian.AppendUint64(nil, npc)) }
			ask()
			learned, asked := false, time.Now()
			return b.request("flights.map", args, 6*time.Second, func() *script.Outcome {
				select {
				case m := <-menus:
					if m[0] != npc {
						return nil
					}
					t.mu.Lock()
					fm := types.FlightMap{Current: b.taxiNode(uint32(m[1]), true), Destinations: []types.TaxiNode{}, Learned: learned}
					for id, k := range t.known {
						if !k || id == uint32(m[1]) {
							continue
						}
						if nodes, cost := route(uint32(m[1]), id, t.known); nodes != nil {
							nd := b.taxiNode(id, true)
							nd.Cost, nd.Hops = cost, len(nodes)-1
							fm.Destinations = append(fm.Destinations, nd)
						}
					}
					t.mu.Unlock()
					sort.Slice(fm.Destinations, func(i, j int) bool { return fm.Destinations[i].Cost < fm.Destinations[j].Cost })
					return script.Ok(fm)
				default:
				}
				return nil
			}, func(ev script.Event) *script.Outcome {
				if ev.Type == "flight_learned" && time.Since(asked) < 5*time.Second {
					learned = true
					ask() // the server only teaches the node the first time: ask again for the map
				}
				return nil
			})
		})
	e.Func("flights.take", "(npc_guid, node) → Request<Flight>", "fly from this flight master to a known node (by id; via other known nodes if there's no direct path). Resolves when you land; await it with a long timeout (flights take minutes). You can't move while flying",
		func(npc uint64, to uint32) map[string]any {
			args := map[string]any{"npc_guid": gs(npc), "node": to}
			t.mu.Lock()
			from, ok := t.current[npc]
			var nodes []uint32
			var cost uint32
			if ok {
				nodes, cost = route(from, to, t.known)
			}
			t.mu.Unlock()
			switch {
			case !ok:
				return b.refuse("flights.take", args, "map_not_loaded", "open the flight map first: flights.map(npc)")
			case from == to:
				return b.refuse("flights.take", args, "same_node", "you're already there")
			case nodes == nil:
				return b.refuse("flights.take", args, "no_route", "no route over the flight points you know (flights.map lists them)")
			case cost > w.Money():
				return b.refuse("flights.take", args, "not_enough_money", fmt.Sprintf("it costs %dc; you have %dc", cost, w.Money()))
			}
			if len(nodes) == 2 {
				body := binary.LittleEndian.AppendUint64(nil, npc)
				body = binary.LittleEndian.AppendUint32(body, from)
				w.SendPacketRaw(cmsgActivateTaxi, binary.LittleEndian.AppendUint32(body, to))
			} else {
				body := binary.LittleEndian.AppendUint64(nil, npc)
				body = binary.LittleEndian.AppendUint32(body, uint32(len(nodes)))
				for _, n := range nodes {
					body = binary.LittleEndian.AppendUint32(body, n)
				}
				w.SendPacketRaw(cmsgActivateTaxiExpress, body)
			}
			fl := &types.Flight{From: b.taxiNode(from, true), To: b.taxiNode(to, true), Cost: cost}
			t.mu.Lock()
			t.flight = fl
			t.mu.Unlock()
			took, settled, asked := false, false, time.Now()
			return b.request("flights.take", args, 30*time.Minute, func() *script.Outcome {
				flying := w.MovementLock() == "in_flight"
				switch {
				case flying:
					took = true
				case took && !settled: // landed: drop from the path's last point to the floor, then report
					settled = true
					w.Settle()
				case took && w.Falling():
				case took:
					f := *fl
					f.Seconds = int(time.Since(asked).Seconds())
					return script.Ok(f)
				case time.Since(asked) > 8*time.Second:
					return script.Fail("no_takeoff", "the flight didn't start", nil)
				}
				return nil
			}, func(ev script.Event) *script.Outcome { return failOn(ev, "flights.take") })
		})
	e.Func("me.home", "() → Home?", "nil until the server bind-point packet is observed; otherwise where your hearthstone takes you (use the Hearthstone item: items.use(6948), a 10 s cast with a 1 hour cooldown)", func() any {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.home == nil {
			return nil
		}
		return *t.home
	})
	e.Func("innkeeper.bind", "(npc_guid) → Request<Home>", "make this inn your home: your hearthstone brings you back here (talk to an innkeeper within ~5 yd)", func(npc uint64) map[string]any {
		args := map[string]any{"npc_guid": gs(npc)}
		if u := b.GetUnitInfo(npc); u == nil || u.NPCFlags&0x10000 == 0 {
			return b.refuse("innkeeper.bind", args, "not_an_innkeeper", "that's not an innkeeper in sight")
		}
		asked := time.Now()
		w.SendPacketRaw(cmsgBinderActivate, binary.LittleEndian.AppendUint64(nil, npc))
		return b.request("innkeeper.bind", args, 5*time.Second, func() *script.Outcome {
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.bound.After(asked) && time.Since(t.bound) > 500*time.Millisecond && t.home != nil { // the bind point follows
				return script.Ok(*t.home)
			}
			return nil
		}, nil)
	})
	e.Func("me.movement", "() → Movement", "who's moving you: free, or the server (in_flight, teleporting, feared, confused, charmed: move.to is refused), or stunned / rooted; with where the server is taking you",
		func() types.Movement {
			mv := types.Movement{State: w.MovementLock()}
			if b.moves != nil {
				mv.Owner, mv.Queue = b.moves.snapshot()
			}
			if mv.State == "" {
				mv.State = "free"
			}
			if d, left, ok := w.SelfSplineDestination(); ok {
				mv.Destination = &types.Position{X: round1(d[0]), Y: round1(d[1]), Z: round1(d[2])}
				mv.SecondsLeft = math.Round(left.Seconds())
			}
			t.mu.Lock()
			if t.flight != nil && mv.State == "in_flight" {
				f := *t.flight
				mv.Flight = &f
			}
			t.mu.Unlock()
			return mv
		})
}

// travelEvents (each tick, before scripts): movement_changed, flight_start / flight_end, teleported.
func (b *Bot) travelEvents(teleported bool) {
	t := b.travel
	if t == nil {
		return
	}
	w := b.world
	lock := w.MovementLock()
	x, y, z, o, m := w.Position()
	pos := types.Position{X: round1(x), Y: round1(y), Z: round1(z), O: math.Round(float64(o)*1000) / 1000, Map: m}
	t.mu.Lock()
	prev, from := t.lock, t.lastPos
	t.lock, t.lastPos = lock, pos
	fl := t.flight
	t.mu.Unlock()
	if teleported {
		b.emit("teleported", types.Teleport{From: from, To: pos, Far: from.Map != pos.Map})
	}
	if lock == prev {
		return
	}
	state := func(s string) string {
		if s == "" {
			return "free"
		}
		return s
	}
	b.emit("movement_changed", types.MovementChanged{State: state(lock), Previous: state(prev)})
	switch {
	case lock == "in_flight":
		b.StopMoving() // the local walker must not fight the server's path
		f := types.Flight{}
		if fl != nil {
			f = *fl
		}
		if _, left, ok := w.SelfSplineDestination(); ok {
			f.Seconds = int(left.Seconds())
		}
		t.mu.Lock()
		t.started = time.Now()
		t.mu.Unlock()
		b.emit("flight_start", f)
	case prev == "in_flight":
		f := types.Flight{}
		if fl != nil {
			f = *fl
		}
		t.mu.Lock()
		f.Seconds = int(time.Since(t.started).Seconds())
		t.flight = nil
		t.mu.Unlock()
		b.emit("flight_end", f)
	}
}
