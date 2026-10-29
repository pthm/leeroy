// azview replay <run-dir | team-dir> [--out replay.html] [--log server/azerothcore/env/dist/logs/recorder.jsonl]
//
//	[--chars A,B] [--from <ms|ISO>] [--to <ms|ISO>]
//
// Builds a self-contained HTML replay of a run from its own client-side records (or the mod-recorder log): the
// game's minimap when client data is extracted, else a terrain map from data/maps, with every player's route, a seek
// bar, and chat / combat / event panels synced to the playhead. Characters and the time window come from the run's
// judge.json (or team.json), or from --chars/--from/--to.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pthm/leeroy/azworld/world"
)

var n = N

// parseWhen is a --from/--to value: unix ms or an ISO time.
func parseWhen(s string) (float64, bool) {
	if regexp.MustCompile(`^\d+$`).MatchString(s) {
		f, _ := strconv.ParseFloat(s, 64)
		return f, true
	}
	for _, l := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(l, s); err == nil {
			return float64(t.UnixMilli()), true
		}
	}
	return 0, false
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", a...)
	os.Exit(1)
}

func runReplay(args []string) {
	dir := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
		dir = args[0]
	}
	isDir := dir != "" && exists(dir)

	// ---------- which characters, which window ----------
	var chars []string
	if v, ok := Flag(args, "chars"); ok {
		chars = strings.Split(v, ",")
	}
	var from, to *float64
	if v, ok := Flag(args, "from"); ok {
		if t, ok := parseWhen(v); ok {
			from = &t
		}
	}
	if v, ok := Flag(args, "to"); ok {
		if t, ok := parseWhen(v); ok {
			to = &t
		}
	}
	title := "WoW run"
	if isDir {
		title = filepath.Base(strings.TrimSuffix(dir, "/"))
		type judge struct {
			Char     string  `json:"char"`
			At       string  `json:"at"`
			ElapsedS float64 `json:"elapsed_s"`
		}
		var judges []judge
		if b, err := os.ReadFile(filepath.Join(dir, "team.json")); err == nil {
			var t struct{ Agents []judge }
			json.Unmarshal(b, &t)
			judges = t.Agents
		} else if b, err := os.ReadFile(filepath.Join(dir, "judge.json")); err == nil {
			var j judge
			json.Unmarshal(b, &j)
			judges = []judge{j}
		}
		if len(chars) == 0 {
			for _, j := range judges {
				chars = append(chars, j.Char)
			}
		}
		var ends, starts []float64
		for _, j := range judges {
			if t, err := time.Parse(time.RFC3339Nano, j.At); err == nil {
				ends = append(ends, float64(t.UnixMilli()))
				starts = append(starts, float64(t.UnixMilli())-j.ElapsedS*1000)
			}
		}
		if to == nil && len(ends) > 0 && len(ends) == len(judges) {
			t := slices.Max(ends) + 5000
			to = &t
		}
		if from == nil {
			if b, err := os.ReadFile(filepath.Join(dir, "go")); err == nil {
				f, _ := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
				f *= 1000
				from = &f
			} else if len(starts) > 0 && len(starts) == len(judges) {
				f := slices.Min(starts)
				from = &f
			}
		}
	}
	// Sources: --log, else the run's own client-side records (azsdk -record: record.jsonl, <agent>.record.jsonl),
	// else the server's mod-recorder log.
	var logFiles []string
	if v, ok := Flag(args, "log"); ok {
		logFiles = []string{v}
	} else if isDir {
		logFiles = RecordFiles(dir)
	}
	if len(logFiles) == 0 {
		logFiles = []string{ServerLog()}
	}
	var transcripts []Transcript
	if isDir {
		_, ts := RunSources(dir)
		for _, t := range ts {
			if exists(t.Path) {
				transcripts = append(transcripts, t)
			}
		}
	}
	// with transcripts (a bench run): player chat once, from the server log, instead of every agent's received copy
	runChars := map[string]bool{}
	for _, t := range transcripts {
		runChars[t.Name] = true
	}
	keep := ClientChatFilter()
	var all []ev
	for _, f := range logFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			fail("%v", err)
		}
		for _, e := range ParseLines(string(b)) {
			if len(runChars) == 0 || keep(e) {
				all = append(all, e)
			}
		}
	}
	if len(runChars) > 0 {
		if b, err := os.ReadFile(ServerLog()); err == nil {
			all = append(all, ServerChat(string(b), runChars)...)
		}
	}
	for _, t := range transcripts {
		b, _ := os.ReadFile(t.Path)
		all = append(all, TranscriptEvents(string(b), t.Name)...)
	}
	SortByTime(all)
	NameUnits(all)
	var evs []ev
	for _, e := range all {
		t := n(e, "t")
		if (len(chars) == 0 || slices.Contains(chars, S(e, "n"))) && (from == nil || *from == 0 || t >= *from) &&
			(to == nil || *to == 0 || t <= *to) {
			evs = append(evs, e)
		}
	}
	if len(evs) == 0 {
		who := strings.Join(chars, ",")
		if who == "" {
			who = "anyone"
		}
		fail("no recorder events for %s in that window (%s)", who, strings.Join(logFiles, ", "))
	}
	if len(chars) == 0 {
		for _, e := range evs {
			if c := S(e, "n"); c != "" && !slices.Contains(chars, c) {
				chars = append(chars, c)
			}
		}
	}
	t0, t1 := n(evs[0], "t"), n(evs[len(evs)-1], "t")
	if from != nil {
		t0 = *from
	}
	if to != nil {
		t1 = *to
	}

	// ---------- tracks ----------
	var pos []ev
	count, order := map[float64]int{}, []float64{}
	for _, e := range evs {
		if S(e, "ev") == "pos" {
			pos = append(pos, e)
			m := n(e, "m")
			if count[m] == 0 {
				order = append(order, m)
			}
			count[m]++
		}
	}
	mapID, best := 0.0, 0
	for _, m := range order { // most positions; the first seen on a tie
		if count[m] > best {
			mapID, best = m, count[m]
		}
	}
	type player struct {
		Name  string  `json:"name"`
		Cls   string  `json:"cls"`
		Color string  `json:"color"`
		Track [][]any `json:"track"`
	}
	players := []player{}
	for _, name := range chars {
		cls := 0
		for i := len(all) - 1; i >= 0; i-- { // the last login, which may predate the window
			if e := all[i]; S(e, "n") == name && S(e, "ev") == "login" && n(e, "t") <= t1 {
				cls = int(n(e, "cls"))
				break
			}
		}
		p := player{Name: name, Color: "#888", Track: [][]any{}}
		if cls >= 0 && cls < len(Class) {
			p.Cls, p.Color = Class[cls], Color[cls]
		}
		for _, q := range pos {
			if S(q, "n") == name {
				p.Track = append(p.Track, TrackRow(q, t0))
			}
		}
		players = append(players, p)
	}
	events := []ev{}
	for _, e := range evs {
		if S(e, "ev") != "pos" {
			c := make(ev, len(e))
			for k, v := range e {
				c[k] = v
			}
			c["t"] = n(e, "t") - t0
			events = append(events, c)
		}
	}

	// ---------- map image from data/maps ----------
	xMin, xMax, yMin, yMax := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for _, p := range pos {
		if n(p, "m") == mapID {
			xMin, xMax = math.Min(xMin, n(p, "x")), math.Max(xMax, n(p, "x"))
			yMin, yMax = math.Min(yMin, n(p, "y")), math.Max(yMax, n(p, "y"))
		}
	}
	pad := math.Max(250, 0.15*math.Max(xMax-xMin, yMax-yMin))
	xMin, xMax, yMin, yMax = xMin-pad, xMax+pad, yMin-pad, yMax+pad
	res := math.Max(1.5, math.Ceil((math.Max(xMax-xMin, yMax-yMin)/1800)*2)/2) // yards per pixel
	img := world.RenderMap(uint32(mapID), xMin, xMax, yMin, yMax, res, nil, nil)
	labels := world.Labels(uint32(mapID), &[4]float64{xMin, xMax, yMin, yMax})
	zones := map[string]string{}
	for id, name := range world.Areas(uint32(mapID)).Names {
		zones[strconv.Itoa(id)] = name
	}
	var lootIDs []int
	for _, e := range events {
		if S(e, "ev") == "loot" {
			if id := int(n(e, "item")); !slices.Contains(lootIDs, id) {
				lootIDs = append(lootIDs, id)
			}
		}
	}

	data := map[string]any{"title": title, "start": t0, "duration": t1 - t0,
		"map":     map[string]any{"id": mapID, "xMax": xMax, "yMax": yMax, "res": res, "w": img.W, "h": img.H},
		"players": players, "events": events, "labels": labels, "zones": zones, "items": world.ItemNames(lootIDs),
		"agents": AgentMeta(transcripts)}
	html, err := Page(title, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(img.PNG), data)
	if err != nil {
		fail("%v", err)
	}
	out, ok := Flag(args, "out")
	if !ok {
		out = "replay.html"
		if isDir {
			out = filepath.Join(dir, "replay.html")
		}
	}
	if err := os.WriteFile(out, []byte(html), 0o644); err != nil {
		fail("%v", err)
	}
	fmt.Printf("%s: %d players, %d positions, %d events, map %dx%d @ %g yd/px, %.1f MB\n", out, len(players), len(pos),
		len(events), img.W, img.H, res, float64(len(html))/1e6)
}
