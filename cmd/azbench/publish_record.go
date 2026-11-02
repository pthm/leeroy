package main

// What the website shows of a run beyond the judge: each player's route and deaths (from their recording, with killer
// and place names from the world database), the party's chat volume and time together, and maps behind the routes
// (the viewer's detail map in greyscale; instances: their navmesh floor plan).

import (
	"bufio"
	"bytes"
	"cmp"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pthm/leeroy/azsdk/metrics"
	"github.com/pthm/leeroy/azsdk/types"
	"github.com/pthm/leeroy/world"
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
	track  [][5]float64   // t, map, x, y, group id: every 15 s; map -1 marks a teleport, -2 a ride (boat, zeppelin)
	raw    [][5]float64   // the same every second, for drawing (simplify)
	class  string         // from the login event: runs that kept no meta.json
	casts  map[string]int // spell name -> times cast
	spells map[string]int // spell name -> spell id (the last cast), for its icon
	deaths []recDeath

	milestones []milestone     // quests turned in, achievements, levels, rare and better loot
	loot       map[string]int  // items received by quality
	places     []place2        // the zones it was in, in order
	kills      map[int]int     // killing blows by creature entry
	damage     map[int]float64 // damage dealt by creature entry
	heard      map[string]int  // players' chat it heard, by speaker
	samples    int             // state records, and those with a pet out and alive
	petUp      int
}

// milestone: [s from the start, kind (quest, achievement, level, loot), what, item quality (loot)]
type milestone struct {
	T       float64 `json:"t"`
	Kind    string  `json:"kind"`
	What    string  `json:"what"`
	Quality string  `json:"quality,omitempty"`
	id      int     // achievement: named at the end
}

// place2: a zone the character entered, s from the start
type place2 struct {
	T    float64 `json:"t"`
	Zone string  `json:"zone"`
}

// lootQualities: what counts as a milestone
var lootQualities = map[string]bool{"rare": true, "epic": true, "legendary": true}

// chatKinds: the chat counted as the players' own talk (the SDK's kinds), from the speaker's own game log (self)
var chatKinds = map[string]string{"party": "party", "whisper_sent": "whisper", "channel": "channel", "say": "say"}

// readRecording reads char's game log; its chat (every kind in chatKinds) goes into chat.
func readRecording(f, char string, start int64, chat map[string]chatMsg) (*recording, error) {
	fh, err := os.Open(f)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	rec := &recording{casts: map[string]int{}, spells: map[string]int{}, loot: map[string]int{}, kills: map[int]int{}, damage: map[int]float64{}, heard: map[string]int{}}
	last := -1e9
	var prev place
	speed := 0.0 // yd/s over the last second
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		readEvent(rec, sc.Bytes(), char, start, chat, &last, &prev, &speed)
	}
	return rec, sc.Err()
}

type place struct {
	t    float64
	m    int
	x, y float64
}

// readEvent files one line of the character's game log in rec.
func readEvent(rec *recording, line []byte, char string, start int64, chat map[string]chatMsg, last *float64, prevp *place, speedp *float64) {
	prev, speed := *prevp, *speedp
	defer func() { *prevp, *speedp = prev, speed }()
	l, ok := metrics.Decode(line)
	if !ok {
		return
	}
	t := float64(l.T-start) / 1000
	switch e := l.Data.(type) {
	case types.State:
		rec.samples++
		if e.Pet != nil && e.Pet.Alive {
			rec.petUp++
		}
		m, grp := int(e.Map), float64(e.Group)
		// a jump: over 100 yd within 3 s. A teleport (a portal, a hearthstone, a spirit healer) comes at walking
		// pace; a boat or zeppelin's path skips stretches too, but the player was moving at its speed (about 30
		// yd/s) just before
		d := math.Hypot(e.X-prev.x, e.Y-prev.y)
		if prev.t > 0 && prev.m == m && t-prev.t <= 3 && d > 100 {
			mark := -1.0
			if speed > 15 {
				mark = -2
			}
			rec.track = append(rec.track, [5]float64{math.Round(t), mark, 0, 0, 0})
			rec.raw = append(rec.raw, [5]float64{math.Round(t), mark, 0, 0, 0})
			*last = -1e9
		}
		if prev.t > 0 && prev.m == m && d <= 100 && t > prev.t {
			speed = d / (t - prev.t)
		}
		prev.t, prev.m, prev.x, prev.y = t, m, e.X, e.Y
		rec.raw = append(rec.raw, [5]float64{math.Round(t), float64(m), math.Round(e.X*10) / 10, math.Round(e.Y*10) / 10, grp})
		if t-*last >= 15 || len(rec.track) > 0 && rec.track[len(rec.track)-1][1] != float64(m) {
			rec.track = append(rec.track, [5]float64{math.Round(t), float64(m), math.Round(e.X), math.Round(e.Y), grp})
			*last = t
		}
	case types.Cast:
		if l.Type == "cast_done" && e.Name != "" {
			rec.casts[e.Name]++
			rec.spells[e.Name] = int(e.Spell)
		}
	case types.Login:
		rec.class = strings.ReplaceAll(e.Class, "_", " ")
	case types.Death:
		d := recDeath{T: math.Round(t)}
		if e.Killer != nil {
			d.entry = int(e.Killer.Entry)
		}
		if n := len(rec.track); n > 0 {
			p := rec.track[n-1]
			d.Map, d.X, d.Y = int(p[1]), p[2], p[3]
		}
		rec.deaths = append(rec.deaths, d)
	case types.Chat:
		if e.Self {
			if kind, ok := chatKinds[e.Kind]; ok {
				chat[fmt.Sprintf("%s|%d|%s", char, l.T, e.Text)] = chatMsg{t, kind}
			}
		} else if _, ok := chatKinds[e.Kind]; (ok || e.Kind == "whisper" || e.Kind == "raid") && e.From != "" {
			rec.heard[e.From]++ // a player's line this character heard
		}
	case types.QuestDone:
		if l.Type == "quest_turned_in" {
			rec.milestones = append(rec.milestones, milestone{T: math.Round(t), Kind: "quest", What: e.Title})
		}
	case types.AchievementEarned:
		rec.milestones = append(rec.milestones, milestone{T: math.Round(t), Kind: "achievement", id: int(e.ID)})
	case types.LevelUp:
		rec.milestones = append(rec.milestones, milestone{T: math.Round(t), Kind: "level", What: fmt.Sprint(e.Level)})
	case types.ItemReceived:
		if e.From == "loot" {
			rec.loot[e.Item.Quality] += max(1, int(e.Item.Count))
			if lootQualities[e.Item.Quality] {
				rec.milestones = append(rec.milestones, milestone{T: math.Round(t), Kind: "loot", What: e.Item.Name, Quality: e.Item.Quality})
			}
		}
	case types.Location:
		if n := len(rec.places); e.Zone != "" && (n == 0 || rec.places[n-1].Zone != e.Zone) {
			rec.places = append(rec.places, place2{T: math.Round(t), Zone: e.Zone})
		}
	case types.Kill:
		if l.Type == "kill" && !e.Victim.Player {
			rec.kills[int(e.Victim.Entry)]++
		}
	case types.Damage:
		if l.Type == "damage" && e.Src.Me && !e.Dst.Player && e.Dst.Entry != 0 {
			rec.damage[int(e.Dst.Entry)] += float64(e.Amount)
		}
	}
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

// simplify draws a route with few points: each stretch between map changes, teleports and rides (marks, kept) is
// reduced with Ramer-Douglas-Peucker to within eps yd of the full one-second track (finer inside instances, where
// rooms are small), and points rounded to the yard.
func simplify(raw [][5]float64) [][5]float64 {
	var out [][5]float64
	flush := func(seg [][5]float64) {
		if len(seg) == 0 {
			return
		}
		eps := 8.0
		if _, ok := continents[int(seg[0][1])]; !ok {
			eps = 2.5
		}
		keep := make([]bool, len(seg))
		keep[0], keep[len(seg)-1] = true, true
		var rdp func(a, b int)
		rdp = func(a, b int) {
			ax, ay, bx, by := seg[a][2], seg[a][3], seg[b][2], seg[b][3]
			l := math.Hypot(bx-ax, by-ay)
			worst, at := 0.0, -1
			for i := a + 1; i < b; i++ {
				var d float64
				if l < 1e-9 {
					d = math.Hypot(seg[i][2]-ax, seg[i][3]-ay)
				} else {
					d = math.Abs((bx-ax)*(ay-seg[i][3])-(ax-seg[i][2])*(by-ay)) / l
				}
				if d > worst {
					worst, at = d, i
				}
			}
			if worst > eps {
				keep[at] = true
				rdp(a, at)
				rdp(at, b)
			}
		}
		rdp(0, len(seg)-1)
		for i, p := range seg {
			if keep[i] {
				out = append(out, [5]float64{p[0], p[1], math.Round(p[2]), math.Round(p[3]), p[4]})
			}
		}
	}
	var seg [][5]float64
	for _, p := range raw {
		if p[1] < 0 || len(seg) > 0 && seg[0][1] != p[1] {
			flush(seg)
			seg = nil
			if p[1] < 0 {
				out = append(out, p)
				continue
			}
		}
		seg = append(seg, p)
	}
	flush(seg)
	return out
}

type mapImage struct {
	Map  int     `json:"map"`
	Name string  `json:"name"`
	File string  `json:"file,omitempty"` // under site/src/assets/runs: the detail map in grey (continents) or the navmesh floor plan (instances)
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
			if m < 0 {
				continue
			}
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
		// a margin, at least 600 yd across, and always 16:10 (west-east : north-south) so every map is the same shape
		cx, cy := (b[0]+b[1])/2, (b[2]+b[3])/2
		h, w := math.Max(375, b[1]-b[0]+200), math.Max(600, b[3]-b[2]+200)
		w = math.Max(w, h*1.6)
		h = w / 1.6
		mi := mapImage{Map: m, Name: mapName(m), XMin: cx - h/2, XMax: cx + h/2, YMin: cy - w/2, YMax: cy + w/2}
		if f := mapFile(id, m); fileExists(filepath.Join(assetDir, f)) { // already drawn: delete the file to redraw
			mi.File = f
		} else if _, ok := continents[m]; ok {
			// the viewer's full-detail map (ground, buildings, trees), in greyscale so the routes' colours stand out
			mi.File = fmt.Sprintf("%s-%d.jpg", id, m)
			img := world.DetailBox(uint32(m), mi.XMin, mi.XMax, mi.YMin, mi.YMax, 2400) // sharp enough to zoom into
			if err := writeJPEG(filepath.Join(assetDir, mi.File), img); err != nil {
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

// together: for each minute of the run, 1 when every player was on the same map within 40 yd of each other, else 0.
// Nil for one player.
func together(tracks [][][5]float64, end float64) []int {
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
	var out []int
	for t := 0.0; t < end; t += 60 {
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
			out = append(out, 1)
		} else {
			out = append(out, 0)
		}
	}
	return out
}

// chatPerMinute: the players' messages in each minute of the run, by kind (the website groups the minutes into an
// interval that suits the run's length).
func chatPerMinute(chat map[string]chatMsg) []map[string]int {
	var out []map[string]int
	for _, c := range chat {
		h := int(c.t / 60)
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

func writeJPEG(f string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 72}); err != nil {
		return err
	}
	return os.WriteFile(f, b.Bytes(), 0o644)
}

// mapFile is the file a run's map is drawn to: a JPEG for a continent, a PNG floor plan for an instance.
func mapFile(id string, m int) string {
	if _, ok := continents[m]; ok {
		return fmt.Sprintf("%s-%d.jpg", id, m)
	}
	return fmt.Sprintf("%s-%d.png", id, m)
}

func fileExists(f string) bool {
	_, err := os.Stat(f)
	return err == nil
}

// gameExtras: what a player's game log adds to its page: milestones (achievements named), loot by quality, the zones
// it went through, its top killing blows and damage by creature ([name, n], most first), whose chat it heard, and the
// share of its time with a pet out and alive (hunters, warlocks; none without a pet).
func gameExtras(rec *recording) map[string]any {
	var ids []int
	for id := range rec.kills {
		ids = append(ids, id)
	}
	for id := range rec.damage {
		ids = append(ids, id)
	}
	names := world.CreatureNames(ids)
	top := func(m map[int]float64) [][2]any {
		var out [][2]any
		for id, v := range m {
			out = append(out, [2]any{cmp.Or(names[id], fmt.Sprint("creature ", id)), math.Round(v)})
		}
		sort.Slice(out, func(i, j int) bool { return out[i][1].(float64) > out[j][1].(float64) })
		return out[:min(10, len(out))]
	}
	kills := map[int]float64{}
	for id, n := range rec.kills {
		kills[id] = float64(n)
	}
	for i := range rec.milestones {
		if m := &rec.milestones[i]; m.Kind == "achievement" {
			m.What = cmp.Or(world.Achievements()[m.id].Title, fmt.Sprint("achievement ", m.id))
		}
	}
	out := map[string]any{"milestones": rec.milestones, "loot": rec.loot, "places": rec.places, "kills_by": top(kills),
		"damage_to": top(rec.damage), "heard_from": rec.heard}
	if rec.petUp > 0 {
		out["pet_pct"] = math.Round(100 * float64(rec.petUp) / float64(rec.samples))
	}
	return out
}
