package main

// What the website shows of a run beyond the judge: each player's route and deaths (from their recording, with killer
// and place names from the world database), the party's chat volume and time together, and relief maps behind the
// routes (from the server's terrain, never the game's minimap art).

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/pthm/leeroy/azworld/world"
)

var continents = map[int]string{0: "Eastern Kingdoms", 1: "Kalimdor", 530: "Outland", 571: "Northrend"}

type recDeath struct {
	T      float64 `json:"t"` // s from the start (wall clock)
	Map    int     `json:"map"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Killer string  `json:"killer"`
	Place  string  `json:"place"`
	entry  int
}

type recording struct {
	track  [][5]float64 // t, map, x, y, group id: every 15 s
	deaths []recDeath
}

// chatKinds: chat types counted as the players' own talk (each message once, however many heard it)
var chatKinds = map[int]string{2: "party", 51: "party", 9: "whisper", 17: "channel", 1: "say"}

// readRecording reads char's own events from one recording; chat (every kind in chatKinds) goes into chat, keyed so a
// message heard by several players counts once.
func readRecording(f, char string, start int64, chat map[string]chatMsg) (*recording, error) {
	fh, err := os.Open(f)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	rec := &recording{}
	last := -1.0
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var e struct {
			Ev, N, Msg string
			T          int64
			M, Grp     int
			Type       int
			X, Y       float64
			Killer     struct{ ID int }
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.N != char {
			continue
		}
		t := float64(e.T-start) / 1000
		switch e.Ev {
		case "pos":
			if t-last >= 15 || len(rec.track) > 0 && rec.track[len(rec.track)-1][1] != float64(e.M) {
				rec.track = append(rec.track, [5]float64{math.Round(t), float64(e.M), math.Round(e.X), math.Round(e.Y), float64(e.Grp)})
				last = t
			}
		case "death":
			d := recDeath{T: math.Round(t), entry: e.Killer.ID}
			if n := len(rec.track); n > 0 {
				p := rec.track[n-1]
				d.Map, d.X, d.Y = int(p[1]), p[2], p[3]
			}
			rec.deaths = append(rec.deaths, d)
		case "chat":
			if kind, ok := chatKinds[e.Type]; ok {
				chat[fmt.Sprintf("%d|%d|%s", e.Type, e.T/1000, e.Msg)] = chatMsg{t, kind}
			}
		}
	}
	return rec, sc.Err()
}

type chatMsg struct {
	t    float64
	kind string
}

// placeName: the zone at a point on a continent, or the instance's own name.
func placeName(m int, x, y float64) string {
	a := world.Areas(uint32(m))
	if _, ok := continents[m]; ok {
		if _, _, area := world.Sample(uint32(m), x, y); area != 0 {
			return a.Names[a.ZoneOf[int(area)]]
		}
		return continents[m]
	}
	return mapName(m)
}

func mapName(m int) string {
	if n, ok := continents[m]; ok {
		return n
	}
	// an instance's own name is its zone with sub-areas (map 36: The Deadmines, not The Great Sea or an unused area)
	a := world.Areas(uint32(m))
	best := 0
	for id, z := range a.ZoneOf {
		if id != z && (best == 0 || z < best) {
			best = z
		}
	}
	if best == 0 {
		return fmt.Sprintf("Map %d", m)
	}
	return a.Names[best]
}

// thinTrack keeps a point every `every` s at most, and only once a player has moved 10 yd, plus every map change:
// the routes without the hours spent standing still.
func thinTrack(t [][5]float64, every float64) [][5]float64 {
	var out [][5]float64
	for _, p := range t {
		if n := len(out); n > 0 {
			q := out[n-1]
			if q[1] == p[1] && (p[0]-q[0] < every || math.Hypot(p[2]-q[2], p[3]-q[3]) < 10) {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

type mapImage struct {
	Map  int     `json:"map"`
	Name string  `json:"name"`
	File string  `json:"file,omitempty"` // under site/src/assets/runs: relief (continents) or the navmesh floor plan (instances)
	XMin float64 `json:"x_min"`
	XMax float64 `json:"x_max"`
	YMin float64 `json:"y_min"`
	YMax float64 `json:"y_max"`
}

// mapImages: one box per map the players spent time on, around every point of every route; continents get a relief
// image written to assetDir.
func mapImages(id string, tracks [][][5]float64, assetDir string) ([]mapImage, error) {
	box := map[int]*[4]float64{}
	n := map[int]int{}
	for _, tr := range tracks {
		for _, p := range tr {
			m := int(p[1])
			b := box[m]
			if b == nil {
				b = &[4]float64{p[2], p[2], p[3], p[3]}
				box[m] = b
			}
			b[0], b[1], b[2], b[3] = math.Min(b[0], p[2]), math.Max(b[1], p[2]), math.Min(b[2], p[3]), math.Max(b[3], p[3])
			n[m]++
		}
	}
	var out []mapImage
	for m, b := range box {
		if n[m] < 4 {
			continue
		}
		// a margin, at least 600 yd across, and the image's aspect no taller than 4:3 or wider than 2:1
		cx, cy := (b[0]+b[1])/2, (b[2]+b[3])/2
		h, w := math.Max(600, b[1]-b[0]+200), math.Max(600, b[3]-b[2]+200)
		w = math.Max(w, h*0.75*4/3)
		h = math.Max(h, w/2)
		mi := mapImage{Map: m, Name: mapName(m), XMin: cx - h/2, XMax: cx + h/2, YMin: cy - w/2, YMax: cy + w/2}
		if _, ok := continents[m]; ok {
			img := world.RenderRelief(uint32(m), mi.XMin, mi.XMax, mi.YMin, mi.YMax, math.Max(1, w/1400))
			mi.File = fmt.Sprintf("%s-%d.jpg", id, m)
			if err := writeJPEG(filepath.Join(assetDir, mi.File), img.PNG); err != nil {
				return nil, err
			}
		} else if plan := world.FloorBox(uint32(m), mi.XMin, mi.XMax, mi.YMin, mi.YMax, math.Max(0.5, w/1400)); plan != nil {
			// an instance: its navmesh floor plan, transparent around it
			mi.File = fmt.Sprintf("%s-%d.png", id, m)
			if err := os.MkdirAll(assetDir, 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(assetDir, mi.File), plan, 0o644); err != nil {
				return nil, err
			}
		}
		out = append(out, mi)
	}
	sort.Slice(out, func(i, j int) bool { return n[out[i].Map] > n[out[j].Map] })
	return out, nil
}

// together: for each 5-minute window, the share of minutes in which every player was on the same map within 40 yd of
// each other. Nil for one player.
func together(tracks [][][5]float64, end float64) []float64 {
	if len(tracks) < 2 {
		return nil
	}
	at := func(tr [][5]float64, t float64) ([5]float64, bool) { // the last point at or before t
		i := sort.Search(len(tr), func(i int) bool { return tr[i][0] > t }) - 1
		if i < 0 {
			return [5]float64{}, false
		}
		return tr[i], true
	}
	var out []float64
	for w := 0.0; w < end; w += 300 {
		in := 0
		for t := w; t < w+300; t += 60 {
			var ps [][5]float64
			for _, tr := range tracks {
				if p, ok := at(tr, t); ok {
					ps = append(ps, p)
				}
			}
			close := len(ps) == len(tracks)
			for i := 0; close && i < len(ps); i++ {
				for j := i + 1; close && j < len(ps); j++ {
					close = ps[i][1] == ps[j][1] && math.Hypot(ps[i][2]-ps[j][2], ps[i][3]-ps[j][3]) <= 40
				}
			}
			if close {
				in++
			}
		}
		out = append(out, math.Round(float64(in)/5*100)/100)
	}
	return out
}

// chatPerHour: the players' messages per hour of the run, by kind.
func chatPerHour(chat map[string]chatMsg) []map[string]int {
	var out []map[string]int
	for _, c := range chat {
		h := int(c.t / 3600)
		if h < 0 {
			continue
		}
		for len(out) <= h {
			out = append(out, map[string]int{})
		}
		out[h][c.kind]++
	}
	return out
}

// nameDeaths fills in killers (creature names) and places.
func nameDeaths(recs []*recording) {
	var ids []int
	for _, r := range recs {
		for _, d := range r.deaths {
			ids = append(ids, d.entry)
		}
	}
	names := world.CreatureNames(ids)
	for _, r := range recs {
		for i := range r.deaths {
			d := &r.deaths[i]
			d.Killer = names[d.entry]
			d.Place = placeName(d.Map, d.X, d.Y)
		}
	}
}

func writeJPEG(f string, pngData []byte) error {
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 80}); err != nil {
		return err
	}
	return os.WriteFile(f, b.Bytes(), 0o644)
}
