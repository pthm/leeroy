package session

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/pthm/leeroy/azsdk/navigation"
)

// TestGenerateTransportTable writes transports.json, the stored boarding routines (transporttable.go), from the
// world database (testdata/transportgen: the transports, their crews' spawns in their own axes, the elevators and
// trams) and TransportAnimation.dbc: where each transport stops, where to wait for it there (the nearest walkable
// floor to a deck spot), and where to stand aboard. Candidates: the GM ride tests check them in the game. Run with
// TRANSPORTGEN_DATA (the data dir) and TRANSPORTGEN_DBC (TransportAnimation.dbc, from the server's client data).
func TestGenerateTransportTable(t *testing.T) {
	data, dbc := os.Getenv("TRANSPORTGEN_DATA"), os.Getenv("TRANSPORTGEN_DBC")
	if data == "" || dbc == "" {
		t.Skip("TRANSPORTGEN_DATA and TRANSPORTGEN_DBC not set")
	}
	nav := navigation.NewEmbeddedNavigator(data).(interface {
		FloorHeight(mapID uint32, x, y, hint float32) (float32, bool)
		GetWaterLevel(mapID uint32, x, y float32) (float32, bool)
		RoutePathContext(context.Context, uint32, navigation.Point3D, navigation.Point3D) (*navigation.PathResult, error)
	})
	// onMesh: the navmesh's point nearest p (where a route from p starts), if it's within 4 yd: a wait point a walk can
	// start and end on (a floor query also finds floors the navmesh doesn't cover, a lift's rim)
	onMesh := func(m uint32, p [3]float32) ([3]float32, bool) {
		from := navigation.Point3D{X: p[0], Y: p[1], Z: p[2]}
		res, err := nav.RoutePathContext(context.Background(), m, from, navigation.Point3D{X: p[0] + 1, Y: p[1], Z: p[2]})
		if err != nil || res == nil || len(res.Points) == 0 {
			return p, false
		}
		q := res.Points[0]
		if math.Abs(float64(q.Z-p[2])) > 2 {
			return p, false
		}
		return [3]float32{q.X, q.Y, q.Z}, true
	}
	floor := func(m uint32, at [3]float64, from, to, dz float64) ([3]float32, float64, bool) {
		for r := from; r <= to; r += 1 {
			best, bestD := [3]float32{}, math.Inf(1)
			for k := 0; k < 36; k++ {
				s, c := math.Sincos(float64(k) * math.Pi / 18)
				x, y := at[0]+r*c, at[1]+r*s
				h, ok := nav.FloorHeight(m, float32(x), float32(y), float32(at[2]))
				if wl, wet := nav.GetWaterLevel(m, float32(x), float32(y)); ok && wet && h <= wl+0.5 {
					ok = false // the sea, not a pier
				}
				if ok && math.Abs(float64(h)-at[2]) <= dz {
					if d := math.Abs(float64(h) - at[2]); d < bestD {
						if q, on := onMesh(m, [3]float32{float32(x), float32(y), h}); on && math.Hypot(float64(q[0])-x, float64(q[1])-y) < 1.5 {
							best, bestD = q, d // a floor the navmesh covers: a walk can start and end there
						}
					}
				}
			}
			if !math.IsInf(bestD, 1) {
				return best, r, true
			}
		}
		return [3]float32{}, 0, false
	}
	var table []tableTransport

	// boats, zeppelins, turtles and gunships: a stop is a key frame of the path; aboard, a crew member's spot
	crew := map[string][][3]float32{}
	for _, r := range readTSV(t, "crew.tsv") {
		if r[0] != "0" {
			crew[r[0]] = append(crew[r[0]], [3]float32{f32(r[2]), f32(r[3]), f32(r[4])})
		}
	}
	nodes := loadPathNodes(data)
	mo := readTSV(t, "mo.tsv")
	byModel := map[string][][3]float32{} // a transport without a crew stands where one of the same model has its crew
	for _, r := range mo {
		if len(crew[r[5]]) > len(byModel[r[6]]) {
			byModel[r[6]] = crew[r[5]]
		}
	}
	for _, r := range mo {
		entry, path, speed, accel := u32(r[0]), u32(r[2]), u32(r[3]), u32(r[4])
		tt := tableTransport{Entry: entry, Name: r[1], Kind: kindOf(r[1], 15)}
		if tt.Kind == "gunship" { // they fly a battle over Icecrown, no stop for passengers
			continue
		}
		if len(crew[r[5]]) == 0 {
			crew[r[5]] = byModel[r[6]]
		}
		tp := newTransportPath(nodes[path], float64(speed), float64(accel))
		if tp == nil || tp.period == 0 || len(crew[r[5]]) == 0 {
			t.Logf("%d %s: no path or crew", entry, r[1])
			continue
		}
		tt.Period = tp.period
		for i := range tp.frames {
			if !tp.frames[i].stop() {
				continue
			}
			pose := tp.restingAt(i)
			st := tableStop{
				Frame: i, Map: pose.mapID, At: [3]float32{float32(pose.x), float32(pose.y), float32(pose.z)},
				Arrive: tp.frames[i].arrive, Depart: tp.frames[i].depart,
			}
			bestR := math.Inf(1)
			for _, s := range crew[r[5]] {
				w := pose.toWorld([4]float32{s[0], s[1], s[2], 0})
				p, rr, ok := floor(pose.mapID, [3]float64{float64(w[0]), float64(w[1]), float64(w[2])}, 1, 40, 2.5)
				if ok && rr < bestR {
					bestR, st.Wait, st.Deck = rr, p, s
				}
			}
			if math.IsInf(bestR, 1) {
				t.Logf("%d %s stop %d (map %d %.0f %.0f): no floor near a deck spot", entry, r[1], i, pose.mapID, pose.x, pose.y)
				continue
			}
			tt.Stops = append(tt.Stops, st)
		}
		if len(tt.Stops) >= 2 { // somewhere to go
			table = append(table, tt)
		}
	}

	// elevators and trams: a stop is a pause in the animation (consecutive nodes in one place); aboard, its middle
	anim := loadAnimation(t, dbc)
	for _, r := range readTSV(t, "statics.tsv") {
		entry := u32(r[1])
		a := anim[entry]
		if len(a) < 2 || travel(a) < 3 || !rideable.MatchString(r[2]) || notRideable.MatchString(r[2]) {
			continue
		}
		if r[10] != "0" { // pauseAtTime: it waits at a stop until something sets it off (StaticTransport), not on a loop
			continue
		}
		sign := 1.0
		if f64(r[8]) < 0 {
			sign = -1
		}
		tt := tableTransport{
			Entry: entry, Name: r[2], Kind: kindOf(r[2], 11), Map: u32(r[3]),
			Spawn:   [4]float32{f32(r[4]), f32(r[5]), f32(r[6]), f32(r[7])},
			PathRot: float32(sign * 2 * math.Acos(math.Max(-1, math.Min(1, f64(r[9]))))), Anim: a, Period: uint32(a[len(a)-1][0]),
		}
		for _, p := range pauses(a) {
			at := tt.staticAt(p[0])
			st := tableStop{Frame: -1, Map: tt.Map, At: at, Arrive: p[0], Depart: p[1]}
			look := [3]float64{float64(at[0]), float64(at[1]), float64(at[2])}
			if tt.Kind == "tram" { // a car's origin is its roof: the platform is the station floor, 11 yd down
				look[2] -= 11
			}
			w, _, ok := floor(tt.Map, look, 3, 25, 2.5)

			if tt.Kind == "tram" && ok {
				st.Deck = [3]float32{0, 0, w[2] - at[2]} // aboard: at the platform's height, inside the car
			}
			if !ok {
				t.Logf("%d %s map %d: no landing near %.1f %.1f %.1f", entry, r[2], tt.Map, at[0], at[1], at[2])
				continue
			}
			st.Wait = w
			tt.Stops = append(tt.Stops, st)
		}
		if len(tt.Stops) >= 2 {
			table = append(table, tt)
		}
	}
	var b []byte // a transport a line: diffs read by transport
	for i, tt := range table {
		line, _ := json.Marshal(tt)
		sep := ",\n"
		if i == 0 {
			sep = "[\n"
		}
		b = append(append(b, sep...), line...)
	}
	b = append(b, "\n]\n"...)
	if err := os.WriteFile("transports.json", b, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tt := range table {
		t.Logf("%-9s %6d %-60.60s stops %d", tt.Kind, tt.Entry, tt.Name, len(tt.Stops))
	}
}

// pauses: [arrive, depart] of each stretch of a second or more where the animation stays put
func pauses(a [][4]float32) [][2]uint32 {
	var out [][2]uint32
	var pauseStart []int // the node each pause starts at
	same := func(p, q [4]float32) bool {
		return math.Abs(float64(p[1]-q[1])) < 0.05 && math.Abs(float64(p[2]-q[2])) < 0.05 && math.Abs(float64(p[3]-q[3])) < 0.05
	}
	for i := 0; i+1 < len(a); i++ {
		j := i
		for j+1 < len(a) && same(a[j+1], a[i]) {
			j++
		}
		if j > i && a[j][0]-a[i][0] >= 1000 {
			if n := len(out); n > 0 && uint32(a[i][0])-out[n-1][1] < 1000 && same(a[i], a[pauseStart[n-1]]) {
				out[n-1][1] = uint32(a[j][0]) // a jiggle in the middle of one pause: still the one stop
			} else {
				out, pauseStart = append(out, [2]uint32{uint32(a[i][0]), uint32(a[j][0])}), append(pauseStart, i)
			}
			i = j
		}
	}
	// a pause across the loop's end (at its start and its end, in one place) is one stop, arriving near the end
	if n := len(out); n > 1 && out[0][0] == uint32(a[0][0]) && out[n-1][1] == uint32(a[len(a)-1][0]) && same(a[0], a[len(a)-1]) {
		out = append([][2]uint32{{out[n-1][0], out[0][1]}}, out[1:n-1]...)
	}
	// no pause: it never waits (a pump, a piston), nothing to step on to
	return out
}

func travel(a [][4]float32) float64 {
	var lo, hi [3]float64
	for i, p := range a {
		for k := 0; k < 3; k++ {
			v := float64(p[k+1])
			if i == 0 || v < lo[k] {
				lo[k] = v
			}
			if i == 0 || v > hi[k] {
				hi[k] = v
			}
		}
	}
	return math.Max(hi[0]-lo[0], math.Max(hi[1]-lo[1], hi[2]-lo[2]))
}

// what moves and carries you (lifts, cars, gondolas), and what only moves (doors, gates, pulleys, pistons)
var (
	rideable    = regexp.MustCompile(`(?i)elevator|vator|subway|tram|gondola|scaffold|lift|platform`)
	notRideable = regexp.MustCompile(`(?i)door|gate|pulley|plunger|basestructure`)
)

func kindOf(name string, typ int) string {
	switch {
	case typ == 11 && strings.Contains(name, "Subway"):
		return "tram"
	case typ == 11:
		return "elevator"
	case strings.Contains(name, "Zeppelin"):
		return "zeppelin"
	case strings.Contains(name, "Turtle"):
		return "turtle"
	case strings.Contains(name, "Hammer") || strings.Contains(name, "Skybreaker"):
		return "gunship"
	}
	return "ship"
}

// loadAnimation reads TransportAnimation.dbc: ID, TransportID, TimeIndex, X, Y, Z, SequenceID; by transport, in time order.
func loadAnimation(t *testing.T, path string) map[uint32][][4]float32 {
	b, err := os.ReadFile(path)
	if err != nil || len(b) < 20 || string(b[:4]) != "WDBC" {
		t.Fatalf("%s: %v", path, err)
	}
	le := binary.LittleEndian
	n, rs := le.Uint32(b[4:]), le.Uint32(b[12:])
	out := map[uint32][][4]float32{}
	for i := uint32(0); i < n; i++ {
		r := b[20+i*rs:]
		f := func(c int) float32 { return math.Float32frombits(le.Uint32(r[c*4:])) }
		id := le.Uint32(r[4:])
		out[id] = append(out[id], [4]float32{float32(le.Uint32(r[8:])), f(3), f(4), f(5)})
	}
	for _, a := range out {
		sort.Slice(a, func(i, j int) bool { return a[i][0] < a[j][0] })
	}
	return out
}

func readTSV(t *testing.T, name string) [][]string {
	f, err := os.Open(filepath.Join("testdata", "transportgen", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out [][]string
	sc := bufio.NewScanner(f)
	for first := true; sc.Scan(); first = false {
		if !first {
			out = append(out, strings.Split(sc.Text(), "\t"))
		}
	}
	return out
}

func f64(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
func f32(s string) float32 { return float32(f64(s)) }
func u32(s string) uint32  { v, _ := strconv.ParseUint(s, 10, 32); return uint32(v) }
