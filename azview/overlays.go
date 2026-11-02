package main

// Map overlays: a heatmap of where the players have been, and the road network the SDK's routes use (the painted
// roads from the client's ground textures, and friendly NPCs' patrol paths).

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/pthm/leeroy/azeroth-sdk/pathfinding"
	"github.com/pthm/leeroy/azworld/world"
)

// heatCell is the heatmap's resolution, yd.
const heatCell = 3.0

// Heat is a heatmap image and where it lies: its north-west corner (x0, y0) and yards per pixel.
type Heat struct {
	X0  float64 `json:"x0"`
	Y0  float64 `json:"y0"`
	Res float64 `json:"res"`
	W   int     `json:"w"`
	H   int     `json:"h"`
	PNG string  `json:"png"` // a data: URL; "" when nobody has been on the map
}

// heatKinds are what a heatmap can count: time spent (each step between position samples adds its seconds along its
// line), or events where they happened (the player's position then), each weighted: one, or its amount.
var heatKinds = map[string]struct {
	ev     string
	who    func(Ev) string // whose position; "" skips
	weight func(Ev) float64
}{
	"deaths": {"death", func(e Ev) string { return S(e, "char") }, func(Ev) float64 { return 1 }},
	"kills":  {"kill", func(e Ev) string { return S(e, "char") }, func(Ev) float64 { return 1 }},
	"xp":     {"xp", func(e Ev) string { return S(e, "char") }, func(e Ev) float64 { return N(e, "amount") }},
	"taken":  {"damage", unitField("dst"), func(e Ev) float64 { return N(e, "amount") }},
	"dealt":  {"damage", unitField("src"), func(e Ev) float64 { return N(e, "amount") }},
}

// HeatAt is where the players (all, or only those named) spent their time on map m up to t, or where an event kind
// happened (heatKinds), smoothed, on a log scale, in the inferno colour scheme (transparent where nothing happened,
// through purple and red to pale yellow where the most did).
func (s *Store) HeatAt(m int, t float64, only map[string]bool, kind string) Heat {
	at := func(x Sample) float64 { return x.T }
	x0, x1, y0, y1 := math.Inf(-1), math.Inf(1), math.Inf(-1), math.Inf(1) // north (max x) .. south, west (max y) .. east
	for _, p := range s.players {
		if only != nil && !only[p.Name] {
			continue
		}
		for _, a := range p.Track[:upto(p.Track, t, at)] {
			if a.M == m {
				x0, x1, y0, y1 = math.Max(x0, a.X), math.Min(x1, a.X), math.Max(y0, a.Y), math.Min(y1, a.Y)
			}
		}
	}
	if math.IsInf(x0, -1) {
		return Heat{Res: heatCell}
	}
	const pad = 30
	x0, y0, x1, y1 = x0+pad, y0+pad, x1-pad, y1-pad
	w, h := int((y0-y1)/heatCell)+1, int((x0-x1)/heatCell)+1
	grid := make([][3]float64, w*h) // boxBlur wants three channels; the first holds the seconds
	add := func(x, y, v float64) {
		r, c := int((x0-x)/heatCell), int((y0-y)/heatCell)
		if r >= 0 && r < h && c >= 0 && c < w {
			grid[r*w+c][0] += v
		}
	}
	hk, events := heatKinds[kind]
	if events { // each event at its player's position then
		list := s.byEv[hk.ev]
		for _, e := range list[:uptoEv(list, t)] {
			n := hk.who(e)
			if p := s.byName[n]; p != nil && (only == nil || only[n]) {
				if pp, ok := p.At(N(e, "t")); ok && pp.M == m {
					add(pp.X, pp.Y, hk.weight(e))
				}
			}
		}
	}
	for _, p := range s.players {
		if events || only != nil && !only[p.Name] {
			continue
		}
		tr := p.Track[:upto(p.Track, t, at)]
		for i := 1; i < len(tr); i++ {
			a, b := tr[i-1], tr[i]
			dt := (b.T - a.T) / 1000
			if a.M != m || b.M != m || dt <= 0 || dt > 5 {
				continue
			}
			d := math.Hypot(b.X-a.X, b.Y-a.Y)
			if d/dt > 40 { // a teleport
				continue
			}
			k := max(1, int(d/heatCell*2)) // points along the step, two a cell
			for j := 0; j < k; j++ {
				f := (float64(j) + 0.5) / float64(k)
				add(a.X+(b.X-a.X)*f, a.Y+(b.Y-a.Y)*f, dt/float64(k))
			}
		}
	}
	r := 2
	if events {
		r = 4 // events are points: spread wider than paths
	}
	grid = blur(grid, w, h, r)
	grid = blur(grid, w, h, r) // twice: close to a gaussian
	top := 0.0
	for _, v := range grid {
		top = math.Max(top, v[0])
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i, v := range grid {
		if v[0] <= 0 {
			continue
		}
		f := math.Log1p(v[0]*20) / math.Log1p(top*20)
		if events {
			f = math.Sqrt(v[0] / top) // events are sparse: a log scale would light up only the worst spot
		}
		if f < 0.04 {
			continue
		}
		c := inferno(f)
		c.A = uint8(255 * math.Min(1, 0.25+f)) // fainter where they barely went
		img.SetNRGBA(i%w, i/w, c)
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return Heat{X0: x0, Y0: y0, Res: heatCell, W: w, H: h, PNG: "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())}
}

// blur is a w x h grid's box blur (rectangular: boxBlur is for squares), first channel only.
func blur(a [][3]float64, w, h, r int) [][3]float64 {
	tmp := make([][3]float64, len(a))
	for y := 0; y < h; y++ {
		s := 0.0
		for x := -r; x < w+r; x++ {
			if x+r < w {
				s += a[y*w+x+r][0]
			}
			if x-r-1 >= 0 {
				s -= a[y*w+x-r-1][0]
			}
			if x >= 0 && x < w {
				tmp[y*w+x][0] = s / float64(2*r+1)
			}
		}
	}
	out := make([][3]float64, len(a))
	for x := 0; x < w; x++ {
		s := 0.0
		for y := -r; y < h+r; y++ {
			if y+r < h {
				s += tmp[(y+r)*w+x][0]
			}
			if y-r-1 >= 0 {
				s -= tmp[(y-r-1)*w+x][0]
			}
			if y >= 0 && y < h {
				out[y*w+x][0] = s / float64(2*r+1)
			}
		}
	}
	return out
}

// inferno is matplotlib's inferno colour map at f in 0..1, from a few of its stops.
func inferno(f float64) color.NRGBA {
	stops := [][3]float64{{0, 0, 4}, {40, 11, 84}, {101, 21, 110}, {159, 42, 99}, {212, 72, 66}, {245, 125, 21}, {250, 193, 39}, {252, 255, 164}}
	f = math.Max(0, math.Min(1, f)) * float64(len(stops)-1)
	i := min(len(stops)-2, int(f))
	u := f - float64(i)
	var c [3]uint8
	for k := range c {
		c[k] = uint8(stops[i][k]*(1-u) + stops[i+1][k]*u)
	}
	return color.NRGBA{c[0], c[1], c[2], 255}
}

// heat serves HeatAt for ?m, t (unix ms), kind (time, deaths, kills, xp, taken, dealt) and the selection (sel, grp) as
// in the panels.
func (v *server) heat(w http.ResponseWriter, r *http.Request) {
	q := queryOf(r)
	m, _ := strconv.Atoi(r.URL.Query().Get("m"))
	v.s.mu.RLock()
	var only map[string]bool
	if f := v.s.focus(q); f != nil {
		only = map[string]bool{}
		for _, n := range f {
			only[n] = true
		}
	}
	h := v.s.HeatAt(m, q.T, only, r.URL.Query().Get("kind"))
	v.s.mu.RUnlock()
	writeJSON(w, h)
}

// roadTile is a grid square's painted roads as a 128x128 PNG, amber, as opaque as the cell is road.
func (v *server) roadTile(w http.ResponseWriter, r *http.Request) {
	m, _ := strconv.Atoi(r.PathValue("m"))
	gx, _ := strconv.Atoi(r.PathValue("gx"))
	gy, _ := strconv.Atoi(r.PathValue("gy"))
	t := world.RoadTile(uint32(m), gx, gy)
	if t == nil {
		http.NotFound(w, r)
		return
	}
	img := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	for i, a := range t {
		if a > 0 {
			img.SetNRGBA(i%128, i/128, color.NRGBA{255, 190, 60, uint8(min(255, int(a)*3/2))})
		}
	}
	w.Header().Set("content-type", "image/png")
	w.Header().Set("cache-control", "max-age=86400")
	png.Encode(w, img)
}

var (
	patrolMu sync.Mutex
	patrols  = map[[3]int][][][2]float64{}
)

// patrols are the friendly NPCs' patrol paths crossing grid square gx, gy of map m (?m&gx&gy), cached.
func (v *server) patrols(w http.ResponseWriter, r *http.Request) {
	g := r.URL.Query().Get
	m, _ := strconv.Atoi(g("m"))
	gx, _ := strconv.Atoi(g("gx"))
	gy, _ := strconv.Atoi(g("gy"))
	k := [3]int{m, gx, gy}
	patrolMu.Lock()
	p, ok := patrols[k]
	patrolMu.Unlock()
	if !ok {
		S := world.GridSize
		xN, yW := S*float64(32-gx), S*float64(32-gy)
		p, _ = world.RoadPaths(uint32(m), xN-S, xN, yW-S, yW)
		if p == nil {
			p = [][][2]float64{}
		}
		patrolMu.Lock()
		patrols[k] = p
		patrolMu.Unlock()
	}
	writeJSON(w, p)
}

// transportShapes: each transport's outline from above, by entry, in its own axes (rotate by its orientation): the
// convex hull of its collision model (the server's vmaps), from its display.
var transportShapes = sync.OnceValue(func() map[string][][2]float32 {
	out := map[string][][2]float32{}
	vmaps := filepath.Join(world.DataDir(), "vmaps")
	models, err := pathfinding.GameObjectModels(vmaps)
	if err != nil {
		return out
	}
	rows, err := world.Rows(world.World(), "SELECT t.entry, g.displayId FROM transports t JOIN gameobject_template g ON g.entry = t.entry")
	if err != nil {
		return out
	}
	for _, r := range rows {
		d, _ := strconv.Atoi(r[1])
		if name := models[uint32(d)]; name != "" {
			if hull, err := pathfinding.ModelOutline(filepath.Join(vmaps, name+".vmo")); err == nil {
				out[r[0]] = hull
			}
		}
	}
	return out
})

func (v *server) transports(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("cache-control", "max-age=86400")
	writeJSON(w, transportShapes())
}

// nav is a navmesh tile (world.NavTile): the walkable floors of a continent's square, or a sub-square at a level.
func (v *server) nav(w http.ResponseWriter, r *http.Request) {
	var n [5]int
	for i, k := range []string{"level", "gx", "gy", "sx", "sy"} {
		n[i], _ = strconv.Atoi(strings.TrimSuffix(r.PathValue(k), ".png"))
	}
	m, _ := strconv.Atoi(r.PathValue("m"))
	key := fmt.Sprint("n", m, n)
	v.tileMu.Lock()
	t, ok := v.tiles[key]
	v.tileMu.Unlock()
	if !ok {
		t = world.NavTile(uint32(m), n[1], n[2], n[0], n[3], n[4])
		v.tileMu.Lock()
		v.tiles[key] = t
		v.tileMu.Unlock()
	}
	if t == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("content-type", "image/png")
	w.Header().Set("cache-control", "max-age=86400")
	w.Write(t)
}
