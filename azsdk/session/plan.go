package session

// Journey planning: the fastest way from here to a point, walking, riding transports (transports.json) and taking the
// portals that are part of getting around (the Deeprun Tram's). A time-dependent search over the transports' stops:
// a ride's cost is the wait for it (its live timetable once it's been seen, else half a round) and the trip to the
// stop you leave at; a walk's is its navmesh length at run speed, found lazily (the straight line is its lower bound,
// so only walks that could be on the best journey are routed, and a walk the navmesh can't make isn't taken).
// world.plan plans with every transport, for the agent to follow; act.go_to plans with lifts, trams and portals only
// (world._plan_local) and follows the plan itself when walking all the way isn't faster.

import (
	"container/heap"
	"context"
	"math"
	"time"

	"github.com/pthm/leeroy/azsdk/navigation"
	"github.com/pthm/leeroy/azsdk/pathfinding"
	"github.com/pthm/leeroy/azsdk/script"
	"github.com/pthm/leeroy/azsdk/types"
)

// crossLinks: area triggers that take you to another map and back as part of getting around a continent (the Deeprun
// Tram, between Stormwind and Ironforge); where each puts you is the server's areatrigger_teleport.
var crossLinks = []struct {
	trigger        uint32
	name           string
	fromMap, toMap uint32
	exit           [3]float32
}{
	{2173, "Deeprun Tram (from Stormwind)", 0, 369, [3]float32{68.3006, 2490.91, -4.29647}},
	{2175, "Deeprun Tram (from Ironforge)", 0, 369, [3]float32{69.2542, 10.257, -4.29664}},
	{2171, "Deeprun Tram (to Stormwind)", 369, 0, [3]float32{-8364.57, 535.981, 91.7969}},
	{2166, "Deeprun Tram (to Ironforge)", 369, 0, [3]float32{-4838.95, -1318.46, 501.868}},
}

const (
	portalSeconds = 5   // walking into a portal and the loading screen after it
	stepSeconds   = 3   // stepping on and off a transport
	planReach     = 6.0 // a walk that ends further than this from where it was going didn't get there
)

type planLink struct {
	name           string
	fromMap, toMap uint32
	enter, exit    [3]float32
}

// planLinks: the portals a journey can take: the Deeprun Tram's, and the teleports within a map (Darnassus and
// Rut'theran, the Wizard's Sanctum), each from its trigger (AreaTrigger.dbc) to where it puts you
func planLinks(triggers []areaTrigger) []planLink {
	at := func(id uint32) (areaTrigger, bool) {
		for _, t := range triggers {
			if t.id == id {
				return t, true
			}
		}
		return areaTrigger{}, false
	}
	var out []planLink
	for _, l := range crossLinks {
		if t, ok := at(l.trigger); ok {
			out = append(out, planLink{l.name, l.fromMap, l.toMap, [3]float32{t.x, t.y, t.z}, l.exit})
		}
	}
	for _, s := range sameMapTeleports {
		if t, ok := at(s.trigger); ok {
			out = append(out, planLink{s.name, t.mapID, t.mapID, [3]float32{t.x, t.y, t.z}, [3]float32{s.tx, s.ty, s.tz}})
		}
	}
	return out
}

type planNode struct {
	m     uint32
	p     [3]float32
	stop  int // a transport stop: its transport's index in the table, else -1
	stopI int
	link  int // a portal's entry (crossLinks index), else -1
}

type planLabel struct {
	node     int
	t        float64 // seconds from now it gets there
	est      float64 // t plus the straight line to the target, at run speed (the queue's order)
	prev     int     // the label it came from
	kind     string  // walk, ride, portal
	verified bool    // a walk whose navmesh route was found (else its straight line, a lower bound)
	ride     [3]int  // table index, from stop, to stop
	wait     float64
}

type planQueue []*planLabel

func (q planQueue) Len() int           { return len(q) }
func (q planQueue) Less(i, j int) bool { return q[i].est < q[j].est }
func (q planQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *planQueue) Push(x any)        { *q = append(*q, x.(*planLabel)) }
func (q *planQueue) Pop() any          { o := *q; x := o[len(o)-1]; *q = o[:len(o)-1]; return x }

// phaseOf: how far into its round transport tt will be at seconds from now, if it's been seen
func (b *Bot) phaseOf(tt *tableTransport, in float64) (uint32, bool) {
	b.trs.mu.Lock()
	defer b.trs.mu.Unlock()
	for _, r := range b.trs.recs {
		if r.table == tt {
			at := time.Now().Add(time.Duration(in * float64(time.Second)))
			return (r.pp + uint32(at.Sub(r.at).Milliseconds())) % tt.period(), true
		}
	}
	return 0, false
}

// rideSeconds: arriving at stop i of transport tt in seconds from now, how long until it leaves with you aboard, and
// how long it then takes to reach stop j
func (b *Bot) rideSeconds(tt *tableTransport, i, j int, in float64) (wait, ride float64) {
	period := tt.period()
	if period == 0 {
		return math.Inf(1), 0
	}
	si, sj := tt.Stops[i], tt.Stops[j]
	ride = float64((sj.Arrive+period-si.Depart)%period)/1000 + stepSeconds
	phase, seen := b.phaseOf(tt, in)
	if !seen {
		return float64(period) / 2000, ride
	}
	d, a := (si.Depart+period-phase)%period, (si.Arrive+period-phase)%period // until it leaves, arrives
	if d < a && float64(d)/1000 < walkMargin+stepSeconds {                   // there, but leaving: the next time round
		d += period
	}
	return float64(d) / 1000, ride
}

// plan: the fastest journey from (m, from) to (tm, to); local: lifts, trams and portals only (no boats or zeppelins)
func (b *Bot) plan(ctx context.Context, m uint32, from [3]float32, tm uint32, to [3]float32, local bool) (*types.Plan, error) {
	nodes := []planNode{{m: m, p: from, stop: -1, link: -1}, {m: tm, p: to, stop: -1, link: -1}}
	for ti := range transportTable {
		tt := &transportTable[ti]
		if local && len(tt.Anim) == 0 {
			continue
		}
		for si, s := range tt.Stops {
			nodes = append(nodes, planNode{m: s.Map, p: s.Wait, stop: ti, stopI: si, link: -1})
		}
	}
	links := planLinks(b.areaTriggers)
	exitNode := map[int]int{} // a portal's exit
	for li, l := range links {
		nodes = append(nodes, planNode{m: l.fromMap, p: l.enter, stop: -1, link: li})
		nodes = append(nodes, planNode{m: l.toMap, p: l.exit, stop: -1, link: -1})
		exitNode[li] = len(nodes) - 1
	}
	dist := func(a, c [3]float32) float64 { return math.Hypot(float64(a[0]-c[0]), float64(a[1]-c[1])) }
	lower := planLowerBounds(nodes, exitNode, dist)
	h := func(n int) float64 { return lower[n] }
	walks := map[[2]int]float64{}
	walk := func(a, c int) float64 { // navmesh route length at run speed; +Inf if it can't get there
		if v, ok := walks[[2]int{a, c}]; ok {
			return v
		}
		pa, pc := nodes[a].p, nodes[c].p
		t0 := time.Now()
		defer func() { planQueryTime += time.Since(t0) }()
		res, err := b.routePath(ctx, nodes[a].m, navigation.Point3D{X: pa[0], Y: pa[1], Z: pa[2]}, navigation.Point3D{X: pc[0], Y: pc[1], Z: pc[2]})
		v := math.Inf(1)
		if err == nil && res != nil && res.Found && len(res.Points) > 0 {
			end := res.Points[len(res.Points)-1]
			if math.Hypot(float64(end.X-pc[0]), float64(end.Y-pc[1])) <= planReach && math.Abs(float64(end.Z-pc[2])) <= planReach {
				v = b.walkSeconds(nodes[a].m, navigation.Point3D{X: pa[0], Y: pa[1], Z: pa[2]}, res.Points)
			}
		}
		walks[[2]int{a, c}] = v
		if planTrace != nil {
			planTrace(nodes[a], nodes[c], v)
		}
		return v
	}

	labels := []*planLabel{{node: 0, prev: -1, verified: true}}
	q := &planQueue{labels[0]}
	labels[0].est = h(0)
	best := map[int]float64{}
	push := func(l *planLabel) {
		l.est = l.t + h(l.node)
		labels = append(labels, l)
		heap.Push(q, l)
	}
	for q.Len() > 0 {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		l := heap.Pop(q).(*planLabel)
		if !l.verified { // its walk: the real route, and back in the queue at its real time
			from := labels[l.prev]
			w := walk(from.node, l.node)
			if math.IsInf(w, 1) {
				continue
			}
			l.t, l.verified = from.t+w, true
			l.est = l.t + h(l.node)
			heap.Push(q, l)
			continue
		}
		if t, ok := best[l.node]; ok && t <= l.t {
			continue
		}
		best[l.node] = l.t
		if l.node == 1 {
			return b.planOf(labels, l, nodes, links), nil
		}
		n := nodes[l.node]
		if n.link >= 0 { // walking into a portal takes it: nowhere to walk on to
			push(&planLabel{node: exitNode[n.link], t: l.t + portalSeconds, prev: indexOf(labels, l), kind: "portal", verified: true, ride: [3]int{n.link, 0, 0}})
			continue
		}
		for c := range nodes { // walking to the target, a stop or a portal on this map
			if c == l.node || nodes[c].m != n.m || c == 0 {
				continue
			}
			if _, done := best[c]; done {
				continue
			}
			if d := dist(n.p, nodes[c].p); d > float64(16384) {
				continue
			}
			push(&planLabel{node: c, t: l.t + dist(n.p, nodes[c].p)/walkSpeed, prev: indexOf(labels, l), kind: "walk"})
		}
		if n.stop >= 0 && l.kind != "ride" { // riding from this stop to each other stop
			tt := &transportTable[n.stop]
			for sj := range tt.Stops {
				if sj == n.stopI {
					continue
				}
				wait, ride := b.rideSeconds(tt, n.stopI, sj, l.t)
				for c := range nodes {
					if nodes[c].stop == n.stop && nodes[c].stopI == sj {
						push(&planLabel{node: c, t: l.t + wait + ride, prev: indexOf(labels, l), kind: "ride", verified: true,
							ride: [3]int{n.stop, n.stopI, sj}, wait: wait})
					}
				}
			}
		}
	}
	return nil, nil
}

// walkSeconds: how long a route takes, running on land and swimming in water (a stretch under the water's surface)
func (b *Bot) walkSeconds(m uint32, from navigation.Point3D, points []navigation.Point3D) float64 {
	water, _ := b.nav.(interface {
		GetWaterLevel(mapID uint32, x, y float32) (float32, bool)
	})
	secs, prev := 0.0, from
	for _, p := range points {
		d := math.Hypot(float64(p.X-prev.X), float64(p.Y-prev.Y))
		speed := pathfinding.RunSpeed
		if water != nil && d > 0 {
			if wl, ok := water.GetWaterLevel(m, (p.X+prev.X)/2, (p.Y+prev.Y)/2); ok && (p.Z+prev.Z)/2 < wl-1 {
				speed = pathfinding.SwimSpeed
			}
		}
		secs += d / speed
		prev = p
	}
	return secs
}

// routePath: the navmesh's route between two points for weighing a walk (its length), without the collision checks a
// walk needs, one search at a time like findPath
func (b *Bot) routePath(ctx context.Context, m uint32, from, to navigation.Point3D) (*navigation.PathResult, error) {
	raw, ok := b.nav.(interface {
		RoutePathContext(context.Context, uint32, navigation.Point3D, navigation.Point3D) (*navigation.PathResult, error)
	})
	if !ok {
		return b.findPath(ctx, m, from, to)
	}
	b.routeOnce.Do(func() { b.routeGate = make(chan struct{}, 1) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case b.routeGate <- struct{}{}:
	}
	defer func() { <-b.routeGate }()
	return raw.RoutePathContext(ctx, m, from, to)
}

// planTrace, when set (tests), hears each walk the planner routes and its seconds (+Inf: it can't get there)
var planTrace func(from, to planNode, seconds float64)

var planQueryTime time.Duration // tests: time spent routing walks

// planLowerBounds: for each node, a lower bound on the time from it to the target (node 1), never more than the real
// time: walks as the crow flies, rides without waiting. A search guided by it heads for the target across maps instead
// of trying every pier, and still finds the fastest journey.
func planLowerBounds(nodes []planNode, exitNode map[int]int, dist func(a, c [3]float32) float64) []float64 {
	n := len(nodes)
	lb := make([]float64, n)
	for i := range lb {
		lb[i] = math.Inf(1)
	}
	lb[1] = 0
	into := make([][]struct { // edges into each node: from, lower-bound cost
		from int
		cost float64
	}, n)
	for i := range nodes {
		if nodes[i].link >= 0 { // a portal's entry leads only through it
			e := exitNode[nodes[i].link]
			into[e] = append(into[e], struct {
				from int
				cost float64
			}{i, portalSeconds})
			continue
		}
		for j := range nodes {
			if i != j && nodes[i].m == nodes[j].m {
				into[j] = append(into[j], struct {
					from int
					cost float64
				}{i, dist(nodes[i].p, nodes[j].p) / walkSpeed})
			}
		}
		if s := nodes[i].stop; s >= 0 {
			tt := &transportTable[s]
			period := tt.period()
			for j := range nodes {
				if nodes[j].stop == s && j != i && period > 0 {
					ride := float64((tt.Stops[nodes[j].stopI].Arrive+period-tt.Stops[nodes[i].stopI].Depart)%period) / 1000
					into[j] = append(into[j], struct {
						from int
						cost float64
					}{i, ride})
				}
			}
		}
	}
	done := make([]bool, n)
	for { // Dijkstra back from the target (a few hundred nodes)
		u := -1
		for i := range lb {
			if !done[i] && !math.IsInf(lb[i], 1) && (u < 0 || lb[i] < lb[u]) {
				u = i
			}
		}
		if u < 0 {
			return lb
		}
		done[u] = true
		for _, e := range into[u] {
			if v := lb[u] + e.cost; v < lb[e.from] {
				lb[e.from] = v
			}
		}
	}
}

func indexOf(labels []*planLabel, l *planLabel) int {
	for i := len(labels) - 1; i >= 0; i-- {
		if labels[i] == l {
			return i
		}
	}
	return -1
}

// planOf: the journey that reached l, leg by leg
func (b *Bot) planOf(labels []*planLabel, l *planLabel, nodes []planNode, links []planLink) *types.Plan {
	var chain []*planLabel
	for c := l; c.prev >= 0; c = labels[c.prev] {
		chain = append([]*planLabel{c}, chain...)
	}
	pos := func(n int) types.Position {
		p := nodes[n].p
		return types.Position{X: round1f(float64(p[0])), Y: round1f(float64(p[1])), Z: round1f(float64(p[2])), Map: nodes[n].m}
	}
	out := &types.Plan{Seconds: math.Round(l.t), Legs: []types.PlanLeg{}}
	for _, c := range chain {
		from := labels[c.prev]
		leg := types.PlanLeg{Action: c.kind, To: pos(c.node), Seconds: math.Round(c.t - from.t)}
		switch c.kind {
		case "ride":
			tt := &transportTable[c.ride[0]]
			at := pos(from.node)
			leg.From, leg.Name, leg.Kind, leg.Entry = &at, tt.Name, tt.Kind, tt.Entry
			leg.BoardStop, leg.LeaveStop, leg.WaitS = c.ride[1]+1, c.ride[2]+1, math.Round(c.wait)
			b.trs.mu.Lock()
			for g, r := range b.trs.recs {
				if r.table == tt {
					gg := types.GUID(g)
					leg.Transport = &gg
				}
			}
			b.trs.mu.Unlock()
		case "portal":
			at := pos(from.node)
			leg.From, leg.Name = &at, links[c.ride[0]].name
		}
		out.Legs = append(out.Legs, leg)
	}
	return out
}

func (b *Bot) setupPlan(r *routeRequests) {
	e := b.lua
	start := func(op string, local bool, x, y, z float32, tm uint32) map[string]any {
		px, py, pz, _, m := b.world.Position()
		task, _, _ := e.CurrentTaskIdentity()
		args := map[string]any{"x": x, "y": y, "z": z, "map": tm}
		return r.submit(task, op, args, types.Plan{}, func(ctx context.Context, j *routeRequest) *script.Outcome {
			b.scanTransports()
			p, err := b.plan(ctx, m, [3]float32{px, py, pz}, tm, [3]float32{x, y, z}, local)
			if err != nil {
				return script.Fail("route_timeout", err.Error(), nil)
			}
			if p == nil {
				return script.Fail("route_unreachable", "no way there found: walking, or by the transports and portals", nil)
			}
			return script.Ok(*p)
		})
	}
	e.Func("world.plan", "(x, y, z [, map]) → Request<Plan>", "plan the fastest journey from where you are to (x, y, z) on map (default yours): legs to walk (act.go_to), transports to ride (stand at the leg's from, transports.board, then transports.leave{stop = leave_stop}) and portals to walk into, with each leg's estimated seconds and the total. A ride's wait is from the transport's timetable once you've seen it (transports.list), else half its round. Walks are navmesh routes; elevators and the Deeprun Tram on the way are legs too (act.go_to takes those itself). route_unreachable: no way found",
		func(x, y, z float32, m any) any {
			_, _, _, _, here := b.world.Position()
			tm := here
			if v, ok := m.(float64); ok {
				tm = uint32(v)
			}
			return start("world.plan", false, x, y, z, tm)
		})
	e.InternalFunc("world._plan_local", "(x, y, z) → Request<Plan>", func(x, y, z float32) any {
		_, _, _, _, here := b.world.Position()
		return start("world._plan_local", true, x, y, z, here)
	})
}
