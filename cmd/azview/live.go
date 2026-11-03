// azview live [log.jsonl ...] [--run <run or team dir>] [--port 8800] [--host addr] [--chars A,B] [--since 60m]
//
// The run viewer: reads game logs (azsdk -events files, several at once) into a store, follows them as they grow, and serves the viewer at http://localhost:<port>/: the
// map as terrain tiles, the samples around a moment, new data as server-sent events, and the panels as HTML.
// --run <dir> takes a bench run (or team) dir, running or finished: its game logs, the judge's samples and each
// agent's transcript.
package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pthm/leeroy/world"
)

//go:embed templates static
var assets embed.FS

// files are the templates and static files: built in, or from disk with AZVIEW_DEV=<azview dir> (edit, reload).
func files() fs.FS {
	if d := os.Getenv("AZVIEW_DEV"); d != "" {
		return os.DirFS(d)
	}
	return assets
}

func templates() *template.Template {
	return template.Must(template.New("").Funcs(template.FuncMap{
		"fmt": Fmt, "money": Money, "coins": Coins, "comma": comma,
		"deref": func(p *int) int { return *p },
	}).ParseFS(files(), "templates/*.html"))
}

func runLive(args []string) {
	var logs []string
	for i, a := range args {
		if !strings.HasPrefix(a, "--") && (i == 0 || !strings.HasPrefix(args[i-1], "--")) {
			logs = append(logs, a)
		}
	}
	dir, _ := Flag(args, "run")
	sinceMin := 60.0
	if v, ok := Flag(args, "since"); ok {
		sinceMin, _ = strconv.ParseFloat(strings.TrimSuffix(v, "m"), 64)
	}
	s, src := OpenRun(dir, logs, sinceMin)
	if v, ok := Flag(args, "chars"); ok {
		src.Chars = strings.Split(v, ",")
	}
	port, host := "8800", "" // every interface; --host 127.0.0.1: this machine alone (a remote run's, reached through SSH)
	if v, ok := Flag(args, "port"); ok {
		port = v
	}
	if v, ok := Flag(args, "host"); ok {
		host = v
	}
	title := "live"
	if dir != "" {
		title = filepath.Base(strings.TrimSuffix(dir, "/"))
	}
	s.Poll(src)
	debug.FreeOSMemory() // the startup read's garbage
	go func() {
		for range time.Tick(50 * time.Millisecond) { // ponytail: polling a dozen files' sizes; cheaper than a watcher dependency
			s.Poll(src)
		}
	}()
	srv := newServer(s, title)
	s.mu.RLock()
	n := len(s.events)
	s.mu.RUnlock()
	fmt.Printf("viewer on http://localhost:%s/ (%s; %d events)\n", port, strings.Join(src.Files, ", "), n)
	log.Fatal(http.ListenAndServe(host+":"+port, srv))
}

// server is the viewer's HTTP side over a store.
type server struct {
	*http.ServeMux
	s           *Store
	title       string
	maps        map[string]string
	labels      []world.Label
	tileVersion int
	tileMu      sync.Mutex
	tiles       map[string][]byte
}

func newServer(s *Store, title string) *server {
	v := &server{ServeMux: http.NewServeMux(), s: s, title: title, tiles: map[string][]byte{}, tileVersion: 1}
	// map names for the picker: continents by name, anything else by its top-level zone (the newest, for the
	// Deadmines' map that's also "The Great Sea")
	v.maps = map[string]string{"0": "Eastern Kingdoms", "1": "Kalimdor", "530": "Outland", "571": "Northrend"}
	for _, r := range world.MustRows(world.Bench(), "SELECT map, name FROM area WHERE zone = 0 AND map NOT IN (0, 1, 530, 571) ORDER BY id") {
		v.maps[r[0]] = r[1]
	}
	for _, m := range []uint32{0, 1, 530, 571} { // the continents (wowbench.area_location has no instances)
		v.labels = append(v.labels, world.Labels(m, nil)...)
	}
	if world.HasClientData() { // tiles changed from the terrain render to the minimap, then instance floor plans: busts browser caches
		v.tileVersion = 3
	}
	v.HandleFunc("GET /{$}", v.page)
	v.Handle("GET /static/", http.FileServerFS(files()))
	v.HandleFunc("GET /tile/{m}/{gx}/{gy}", v.tile)
	v.HandleFunc("GET /detail/{m}/{level}/{gx}/{gy}/{sx}/{sy}", v.detail)
	v.HandleFunc("GET /samples", v.samples)
	v.HandleFunc("GET /transport-shapes", v.transports)
	v.HandleFunc("GET /heat", v.heat)
	v.HandleFunc("GET /roads/{m}/{gx}/{gy}", v.roadTile)
	v.HandleFunc("GET /patrols", v.patrols)
	v.HandleFunc("GET /nav/{m}/{level}/{gx}/{gy}/{sx}/{sy}", v.nav)
	v.HandleFunc("GET /route", v.route)
	v.HandleFunc("GET /live", v.live)
	v.HandleFunc("GET /panel/{name}", v.panel)
	return v
}

// page is the viewer with what the client needs to start: the players, map names and labels, marks and ticks.
func (v *server) page(w http.ResponseWriter, r *http.Request) {
	s := v.s
	s.mu.RLock()
	type pl struct {
		Name  string `json:"name"`
		Class string `json:"class"`
	}
	var players []pl
	for _, p := range s.players {
		players = append(players, pl{p.Name, p.Cls})
	}
	marks := make([]any, len(s.marks))
	for i, e := range s.marks {
		marks[i] = s.mapMark(e)
	}
	ticks := make([][2]any, len(s.ticks))
	for i, e := range s.ticks {
		ticks[i] = [2]any{N(e, "t"), S(e, "type")}
	}
	cats := make([]map[string]any, len(feedCats))
	for i, c := range feedCats {
		cats[i] = map[string]any{"key": c.Key, "on": c.On}
	}
	start, lastT := s.Start, s.last
	s.mu.RUnlock()
	if start == 0 {
		start = float64(time.Now().UnixMilli())
	}
	boot := map[string]any{"start": start, "last": math.Max(lastT, start), "players": players, "maps": v.maps, "labels": v.labels,
		"tileVersion": v.tileVersion, "detail": v.detailMaps(), "maxDetail": world.MaxDetail, "detailVersion": world.DetailVersion, "navVersion": world.NavVersion, "roads": world.RoadSquares(), "marks": marks, "ticks": ticks, "cats": cats}
	b, _ := json.Marshal(boot) // escapes < > &, so the data can't close its <script> tag
	data := map[string]any{"Title": v.title, "Boot": map[string]any{"Players": players}, "BootJSON": template.JS(b),
		"Cats": feedCats, "Metrics": metricGroups(), "Ranges": ranges}
	w.Header().Set("content-type", "text/html; charset=utf-8")
	if err := templates().ExecuteTemplate(w, "page.html", data); err != nil {
		log.Print(err)
	}
}

func (v *server) tile(w http.ResponseWriter, r *http.Request) {
	m, _ := strconv.Atoi(r.PathValue("m"))
	gx, _ := strconv.Atoi(r.PathValue("gx"))
	gy, _ := strconv.Atoi(strings.TrimSuffix(r.PathValue("gy"), ".png"))
	key := fmt.Sprint(m, "/", gx, "/", gy)
	v.tileMu.Lock()
	t, ok := v.tiles[key]
	if !ok {
		// the client's minimap for that square if it was extracted (roads, buildings, water); an instance's floor plan
		// from its navmesh (its own minimap is the rock above it); else the terrain render
		t = world.MinimapTile(uint32(m), gx, gy)
		if t == nil && !world.Continent(uint32(m)) {
			t = world.FloorTile(uint32(m), gx, gy, 0.5)
		}
		if t == nil {
			S := world.GridSize
			xMax, yMax := S*float64(32-gx), S*float64(32-gy)
			t = world.RenderMap(uint32(m), xMax-S, xMax, yMax-S, yMax, 2, &[2]float64{-60, 420}, nil).PNG // fixed colour scale so tiles match
		}
		v.tiles[key] = t
	}
	v.tileMu.Unlock()
	w.Header().Set("content-type", "image/png")
	w.Header().Set("cache-control", "max-age=86400")
	w.Write(t)
}

// detailMaps are the maps with detail tiles (the continents, given the game client).
func (v *server) detailMaps() []int {
	out := []int{}
	for _, m := range []uint32{0, 1, 530, 571} {
		if world.HasDetail(m) {
			out = append(out, int(m))
		}
	}
	return out
}

// detail is a detail tile: part of a grid square finer than its minimap (world.DetailTile).
func (v *server) detail(w http.ResponseWriter, r *http.Request) {
	var n [5]int
	for i, k := range []string{"level", "gx", "gy", "sx", "sy"} {
		n[i], _ = strconv.Atoi(strings.TrimSuffix(r.PathValue(k), ".jpg"))
	}
	m, _ := strconv.Atoi(r.PathValue("m"))
	leaves := r.URL.Query().Get("leaves") != "0" // ?leaves=0: without foliage (the viewer's foliage slider mixes the two)
	key := fmt.Sprint("d", m, n, leaves)
	v.tileMu.Lock()
	t, ok := v.tiles[key]
	v.tileMu.Unlock()
	if !ok {
		t = world.DetailTileLeaves(uint32(m), n[1], n[2], n[0], n[3], n[4], leaves) // rendered outside the lock: tiles render in parallel
		v.tileMu.Lock()
		if len(v.tiles) > 4000 { // ponytail: drop the lot past ~120 MB; an LRU if zooming around a big area makes that churn
			v.tiles = map[string][]byte{}
		}
		v.tiles[key] = t
		v.tileMu.Unlock()
	}
	if t == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("content-type", "image/jpeg")
	w.Header().Set("cache-control", "max-age=86400")
	w.Write(t)
}

func num(r *http.Request, k string) float64 {
	f, err := strconv.ParseFloat(r.URL.Query().Get(k), 64)
	if err != nil || math.IsNaN(f) {
		return 0
	}
	return f
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// samples is every player's samples and units samples in [from, to] (unix ms), as the map draws them.
func (v *server) samples(w http.ResponseWriter, r *http.Request) {
	from, to := num(r, "from"), num(r, "to")
	if to-from > 6*3600_000 { // ponytail: a cap so a bad request can't build a huge reply; the client asks for minutes
		to = from + 6*3600_000
	}
	s := v.s
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]any{}
	units, cls := map[string]any{}, map[string]string{}
	at := func(x Sample) float64 { return x.T }
	uat := func(x UnitsSample) float64 { return x.T }
	for _, p := range s.players {
		var a []any
		for _, x := range p.Track[upto(p.Track, from-1, at):upto(p.Track, to, at)] {
			a = append(a, s.mapSample(p, x))
		}
		var u []any
		for _, x := range p.Units[upto(p.Units, from-1, uat):upto(p.Units, to, uat)] {
			u = append(u, s.mapUnits(x))
		}
		if a != nil {
			out[p.Name] = a
		}
		if u != nil {
			units[p.Name] = u
		}
		cls[p.Name] = p.Cls
	}
	hits := []any{}
	for _, e := range s.meter[uptoEv(s.meter, from-1):uptoEv(s.meter, to)] {
		for _, h := range mapHits(e) {
			hits = append(hits, h)
		}
	}
	trs := map[string]any{} // guid -> {name, entry, track: [[t, map, x, y, o]...]}, from the sample before the window
	tat := func(x TrSample) float64 { return x[0] }
	for id, tr := range s.trs {
		if a := tr.Track[max(0, upto(tr.Track, from-1, tat)-1):upto(tr.Track, to, tat)]; len(a) > 0 {
			trs[id] = map[string]any{"name": tr.Name, "entry": tr.Entry, "track": a}
		}
	}
	writeJSON(w, map[string]any{"samples": out, "units": units, "class": cls, "hits": hits, "transports": trs})
}

// route is a player's whole route: [t, x, y, map] per sample.
func (v *server) route(w http.ResponseWriter, r *http.Request) {
	s := v.s
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.byName[r.URL.Query().Get("n")]
	out := [][4]float64{}
	if p != nil {
		for _, x := range p.Track {
			out = append(out, [4]float64{x.T, math.Round(x.X*10) / 10, math.Round(x.Y*10) / 10, float64(x.M)})
		}
	}
	writeJSON(w, out)
}

// live streams new data as server-sent events: s (a sample), u (units), hit (combat text), mark, tick, data (anything
// else arrived).
func (v *server) live(w http.ResponseWriter, r *http.Request) {
	fl, _ := w.(http.Flusher)
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	c, done := v.s.Subscribe()
	defer done()
	fmt.Fprint(w, ": hello\n\n")
	if fl != nil {
		fl.Flush()
	}
	for {
		select {
		case m, ok := <-c:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m.typ, m.b); err != nil {
				return
			}
			for len(c) > 0 { // what else is queued, in one write
				m = <-c
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m.typ, m.b)
			}
			if fl != nil {
				fl.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

var panelName = regexp.MustCompile(`^(roster|pane)$`)

// panel renders one panel at a moment, for the UI state in the query (see viewerQuery in viewer.js).
func (v *server) panel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !panelName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	q := queryOf(r)
	w.Header().Set("content-type", "text/html; charset=utf-8")
	v.s.mu.RLock()
	defer v.s.mu.RUnlock()
	tmpl, data := v.s.Panel(name, q)
	if err := templates().ExecuteTemplate(w, tmpl, data); err != nil {
		log.Print(err)
	}
}
