package world

// Roads, safe routes and map images for azworld (roads / route / map).
// Roads come from the game client's road textures (data/roads, extracted by cmd/clientdata: every paved, cobbled or
// stone road as painted on the ground) and from the patrol paths of friendly NPCs (guards, patrollers, travelling
// merchants walk the roads). Map images use the client's minimap where it was extracted.
// Routes: A* on an 8-yard grid over the server's terrain (no slopes over ~45 degrees, swimming is slow), cheaper on
// roads and more expensive near hostile mobs around or above your level. Terrain only: buildings, caves and
// bridges aren't modelled, so follow the route with move_to, which uses the real navmesh between waypoints.
//
// The arithmetic mirrors the TypeScript original (float32 grids, JS rounding, products kept out of FMA with
// float64(...)) so routes come out the same.

import (
	"fmt"
	"math"
	"os"
	"sort"
)

const navCell = 8

func roadPaths(m, x0, x1, y0, y1 float64) [][][2]float64 {
	rows := q(fmt.Sprintf(`SELECT w.id, w.point, ROUND(w.position_x) x, ROUND(w.position_y) y FROM waypoint_data w
    WHERE w.id IN (SELECT a.path_id FROM creature c JOIN creature_addon a ON a.guid = c.guid JOIN creature_template t ON t.entry = c.id
      JOIN wowbench.reaction r ON r.faction_template = t.faction
      WHERE c.map = %s AND a.path_id > 0 AND r.to_alliance = 'friendly' AND r.to_horde <> 'friendly'
        AND c.position_x BETWEEN %s AND %s AND c.position_y BETWEEN %s AND %s)
    ORDER BY w.id, w.point`, jsStr(m), jsStr(x0-300), jsStr(x1+300), jsStr(y0-300), jsStr(y1+300)))
	byID := map[float64]int{}
	var paths [][][2]float64
	for _, r := range rows {
		id := r.n("id")
		k, ok := byID[id]
		if !ok {
			k = len(paths)
			byID[id] = k
			paths = append(paths, nil)
		}
		paths[k] = append(paths[k], [2]float64{r.n("x"), r.n("y")})
	}
	// keep travelling paths (not an NPC pacing in a room), clipped loosely to the box
	out := [][][2]float64{}
	for _, p := range paths {
		lx, hx, ly, hy := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
		in := false
		for _, v := range p {
			lx, hx, ly, hy = math.Min(lx, v[0]), math.Max(hx, v[0]), math.Min(ly, v[1]), math.Max(hy, v[1])
			in = in || (v[0] >= x0 && v[0] <= x1 && v[1] >= y0 && v[1] <= y1)
		}
		if hx-lx+hy-ly > 120 && in {
			out = append(out, p)
		}
	}
	return out
}

type threat struct {
	entry float64
	name  string
	level float64
	elite bool
	x, y  float64
}

// threats: hostile spawns (to an Alliance character; horde flips it) with their levels
func threats(m, x0, x1, y0, y1 float64, horde bool) []threat {
	side := "to_alliance"
	if horde {
		side = "to_horde"
	}
	var out []threat
	for _, r := range q(fmt.Sprintf("SELECT s.id entry, t.name, t.minlevel lo, t.maxlevel hi, t.`rank` rk, ROUND(s.x) x, ROUND(s.y) y FROM wowbench.creature_spawn s\n"+
		"    JOIN creature_template t ON t.entry = s.id JOIN wowbench.reaction r ON r.faction_template = t.faction\n"+
		"    WHERE s.map = %s AND r.%s = 'hostile'\n"+
		"      AND t.flags_extra & 128 = 0 AND t.unit_flags & 0x2000000 = 0 -- not invisible triggers / unselectable dummies\n"+
		"      AND s.guid NOT IN (SELECT guid FROM game_event_creature) -- holiday spawns\n"+
		"      AND s.x BETWEEN %s AND %s AND s.y BETWEEN %s AND %s", jsStr(m), side, jsStr(x0), jsStr(x1), jsStr(y0), jsStr(y1))) {
		out = append(out, threat{entry: r.n("entry"), name: jsStr(r.get("name")), level: (r.n("lo") + r.n("hi")) / 2,
			elite: r.n("rk") > 0, x: r.n("x"), y: r.n("y")})
	}
	return out
}

// aggro: how far a mob of lvl notices a player of pl (roughly the server's aggro radius: 20 yd at equal level,
// +-1 per level)
func aggro(lvl, pl float64) float64 { return math.Max(5, math.Min(40, 20+(lvl-pl))) }

func route(m float64, from, to [2]float64, level float64, horde bool) *obj {
	const pad = 200
	x0, x1 := math.Min(from[0], to[0])-pad, math.Max(from[0], to[0])+pad
	y0, y1 := math.Min(from[1], to[1])-pad, math.Max(from[1], to[1])+pad
	W, H := int(math.Ceil((y1-y0)/navCell))+1, int(math.Ceil((x1-x0)/navCell))+1
	if W*H > 400_000 {
		panic(qerr{fmt.Errorf("route too long for one query (max ~5000 yd): split it into legs")})
	}
	idx := func(x, y float64) int { return int(jsRound((x-x0)/navCell))*W + int(jsRound((y-y0)/navCell)) }
	cx := func(i int) float64 { return x0 + float64(float64(i/W)*navCell) }
	cy := func(i int) float64 { return y0 + float64(float64(i%W)*navCell) }
	h, cost := make([]float32, W*H), make([]float32, W*H)
	mapID := uint32(m)
	for i := range h {
		g, wl, _ := Sample(mapID, cx(i), cy(i))
		h[i], cost[i] = float32(g), 1
		if !math.IsNaN(wl) && wl > g+1.5 {
			cost[i] = 3 // swimming
		}
	}
	road := make([]uint8, W*H)
	for _, p := range roadPaths(m, x0, x1, y0, y1) {
		for k := 1; k < len(p); k++ {
			a, b := p[k-1], p[k]
			n := math.Ceil(math.Hypot(b[0]-a[0], b[1]-a[1]) / (navCell / 2))
			for s := 0.0; s <= n; s++ {
				x, y := a[0]+float64((b[0]-a[0])*s)/n, a[1]+float64((b[1]-a[1])*s)/n
				if x >= x0 && x <= x1 && y >= y0 && y <= y1 {
					road[idx(x, y)] = 1
				}
			}
		}
	}
	for i := range road {
		if RoadAt(mapID, cx(i), cy(i)) > 100 { // the client's road textures
			road[i] = 1
		}
	}
	for i := range road {
		if road[i] != 0 {
			cost[i] = float32(float64(cost[i]) * 0.6)
		}
	}
	var mobs []threat
	for _, t := range threats(m, x0, x1, y0, y1, horde) {
		if t.level >= level-2 {
			mobs = append(mobs, t)
		}
	}
	for _, t := range mobs {
		r, weight := aggro(t.level, level), 4+float64(2*math.Max(0, t.level-level))
		if t.elite {
			r += 10
			weight += 10
		}
		for dx := -r; dx <= r; dx += navCell {
			for dy := -r; dy <= r; dy += navCell {
				d, x, y := math.Hypot(dx, dy), t.x+dx, t.y+dy
				if d <= r && x >= x0 && x <= x1 && y >= y0 && y <= y1 {
					k := idx(x, y)
					cost[k] = float32(float64(cost[k]) + float64(weight*(1-d/r)))
				}
			}
		}
	}
	// A* over 8 neighbours; a step is blocked if steeper than ~45 degrees or off the map
	start, goal := idx(from[0], from[1]), idx(to[0], to[1])
	dist, prev, closed := make([]float32, W*H), make([]int32, W*H), make([]bool, W*H)
	for i := range dist {
		dist[i], prev[i] = float32(math.Inf(1)), -1
	}
	type entry struct {
		f float64
		i int
	}
	var heap []entry
	push := func(f float64, i int) {
		heap = append(heap, entry{f, i})
		for k := len(heap) - 1; k > 0; {
			p := (k - 1) >> 1
			if heap[p].f <= heap[k].f {
				break
			}
			heap[p], heap[k] = heap[k], heap[p]
			k = p
		}
	}
	pop := func() entry {
		top, last := heap[0], heap[len(heap)-1]
		heap = heap[:len(heap)-1]
		if len(heap) > 0 {
			heap[0] = last
			for k := 0; ; {
				l, r, m := 2*k+1, 2*k+2, k
				if l < len(heap) && heap[l].f < heap[m].f {
					m = l
				}
				if r < len(heap) && heap[r].f < heap[m].f {
					m = r
				}
				if m == k {
					break
				}
				heap[m], heap[k] = heap[k], heap[m]
				k = m
			}
		}
		return top
	}
	hEst := func(i int) float64 { return float64(math.Hypot(cx(i)-to[0], cy(i)-to[1]) * 0.6) }
	dist[start] = 0
	push(hEst(start), start)
	steps := [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1}}
	for len(heap) > 0 {
		i := pop().i
		// each cell is expanded once: without this, stale heap entries (a cell queued again with a shorter distance,
		// which mob costs make common) were expanded every time, and a 500-yard route took minutes
		if closed[i] {
			continue
		}
		closed[i] = true
		if i == goal {
			break
		}
		r, c := i/W, i%W
		for _, s := range steps {
			nr, nc := r+s[0], c+s[1]
			if nr < 0 || nr >= H || nc < 0 || nc >= W {
				continue
			}
			j, step := nr*W+nc, float64(math.Hypot(float64(s[0]), float64(s[1]))*navCell)
			if closed[j] {
				continue
			}
			if math.IsNaN(float64(h[j])) || math.Abs(float64(h[j])-float64(h[i])) > step {
				continue
			}
			d := float64(dist[i]) + float64(step*(float64(cost[i])+float64(cost[j])))/2
			if d < float64(dist[j]) {
				dist[j], prev[j] = float32(d), int32(i)
				push(d+hEst(j), j)
			}
		}
	}
	if prev[goal] < 0 && goal != start {
		return newObj("error", "no walkable route on the terrain (water, cliffs, or the target is indoors/underground): try a nearer point")
	}
	var cells []int
	for i := goal; i >= 0; i = int(prev[i]) {
		cells = append([]int{i}, cells...)
	}
	// waypoints: a point every ~40 yd or where the direction turns
	pts := make([][3]float64, len(cells))
	for k, i := range cells {
		pts[k] = [3]float64{cx(i), cy(i), jsRound(float64(h[i])*10) / 10}
	}
	way := [][3]float64{pts[0]}
	for k := 1; k < len(pts)-1; k++ {
		a, b, c := way[len(way)-1], pts[k], pts[k+1]
		turn := math.Abs(math.Atan2(c[1]-b[1], c[0]-b[0]) - math.Atan2(b[1]-a[1], b[0]-a[0]))
		if math.Hypot(b[0]-a[0], b[1]-a[1]) >= 40 || (turn > 0.5 && turn < 2*math.Pi-0.5) {
			way = append(way, b)
		}
	}
	way = append(way, [3]float64{to[0], to[1], pts[len(pts)-1][2]})
	length, onRoad := 0.0, 0.0
	for k := 1; k < len(cells); k++ {
		s := math.Hypot(cx(cells[k])-cx(cells[k-1]), cy(cells[k])-cy(cells[k-1]))
		length += s
		if road[cells[k]] != 0 {
			onRoad += s
		}
	}
	type pass struct {
		o     *obj
		level float64
	}
	byEntry := map[float64]*obj{}
	var near []pass
	for _, t := range mobs {
		d := math.Inf(1)
		for _, i := range cells {
			d = math.Min(d, math.Hypot(cx(i)-t.x, cy(i)-t.y))
		}
		if d > aggro(t.level, level)+5 {
			continue
		}
		e := byEntry[t.entry]
		if e == nil {
			e = newObj("entry", t.entry, "name", t.name, "level", t.level, "elite", t.elite, "closest", d, "n", 0.0)
			byEntry[t.entry] = e
			near = append(near, pass{e, t.level})
		}
		e.set("n", e.n("n")+1)
		e.set("closest", jsRound(math.Min(e.n("closest"), d)))
	}
	sort.SliceStable(near, func(i, j int) bool { return near[i].level > near[j].level })
	passes := make([]*obj, len(near))
	for i, p := range near {
		passes[i] = p.o
	}
	return newObj("length", jsRound(length), "on_road", jsStr(jsRound(onRoad/math.Max(1, length)*100))+"%", "waypoints", way,
		"passes_near", passes, "note", "walk it with move_to(x, y, ground_z(x, y)) leg by leg; terrain only, so buildings and caves aren't modelled")
}

// mapImage: PNG around (x, y): terrain, roads (light), hostile mobs (red; brighter = higher level than you, big =
// elite), your position (white), optional route (yellow). North (+x) is up, west (+y) is left.
func mapImage(m, x, y, radius, level float64, out string, path [][3]float64) *obj {
	res := math.Max(1, jsRound(radius*2/600))
	x0, x1, y0, y1 := x-radius, x+radius, y-radius, y+radius
	rd, mobs := roadPaths(m, x0, x1, y0, y1), threats(m, x0, x1, y0, y1, false)
	img := RenderMap(uint32(m), x0, x1, y0, y1, res, nil, func(px func(wx, wy float64, c [3]uint8)) {
		dot := func(wx, wy float64, r int, col [3]uint8) {
			for dx := -r; dx <= r; dx++ {
				for dy := -r; dy <= r; dy++ {
					if dx*dx+dy*dy <= r*r {
						px(wx+float64(float64(dx)*res), wy+float64(float64(dy)*res), col)
					}
				}
			}
		}
		line := func(pts [][2]float64, col [3]uint8, w int) {
			for k := 1; k < len(pts); k++ {
				a, b := pts[k-1], pts[k]
				n := math.Ceil(math.Hypot(b[0]-a[0], b[1]-a[1]) / res)
				if n == 0 { // 0/0: JS draws at NaN, i.e. nowhere
					continue
				}
				for s := 0.0; s <= n; s++ {
					dot(a[0]+float64((b[0]-a[0])*s)/n, a[1]+float64((b[1]-a[1])*s)/n, w, col)
				}
			}
		}
		for _, p := range rd {
			line(p, [3]uint8{235, 220, 170}, 1)
		}
		for _, t := range mobs {
			r, col := 2, [3]uint8{150, 110, 90}
			if t.elite {
				r = 4
			}
			if t.level >= level+2 {
				col = [3]uint8{255, 30, 30}
			} else if t.level >= level-1 {
				col = [3]uint8{230, 120, 40}
			}
			dot(t.x, t.y, r, col)
		}
		if path != nil {
			p2 := make([][2]float64, len(path))
			for i, p := range path {
				p2[i] = [2]float64{p[0], p[1]}
			}
			line(p2, [3]uint8{255, 230, 0}, 1)
		}
		dot(x, y, 4, [3]uint8{255, 255, 255})
	})
	if err := os.WriteFile(out, img.PNG, 0o644); err != nil {
		panic(qerr{err})
	}
	legend := "terrain; "
	if HasClientData() {
		legend = "the game's minimap (roads, buildings, water); "
	}
	return newObj("file", out, "width", img.W, "height", img.H, "yards_per_pixel", res, "center", []float64{x, y},
		"legend", legend+"north up; light lines = guard patrol paths; red = hostile 2+ levels above you, orange = around your level, brown = lower; big dot = elite; white = (x, y); yellow = route",
		"roads", len(rd), "hostile_spawns", len(mobs))
}
