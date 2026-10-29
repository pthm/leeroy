// azview live [log.jsonl ...] [--run <run or team dir>] [--port 8800] [--chars A,B] [--since 60m]
//
// Live run viewer: tails recorder-format logs (default: the server's mod-recorder log; also azsdk -record files,
// several at once) and serves the replay viewer in live mode at http://localhost:<port>/ with the map as terrain
// tiles rendered on demand. Events stream to the browser as server-sent events; the playhead follows the newest
// data until you seek back. --run <dir> takes a bench run (or team) dir: its record files plus each agent's
// transcript, whose visible messages appear in the Agent tab.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pthm/leeroy/azworld/world"
)


func runLive(args []string) {
	var files []string
	for i, a := range args {
		if !strings.HasPrefix(a, "--") && (i == 0 || !strings.HasPrefix(args[i-1], "--")) {
			files = append(files, a)
		}
	}
	var transcripts []Transcript
	if dir, ok := Flag(args, "run"); ok {
		var records []string
		records, transcripts = RunSources(dir)
		files = append(files, records...)
	}
	serverLog := ServerLog()
	if len(files) == 0 {
		files = append(files, serverLog)
	}
	// transcripts: path -> character name; parsed as "say" events instead of recorder lines
	transcriptOf := map[string]string{}
	for _, t := range transcripts {
		transcriptOf[t.Path] = t.Name
		files = append(files, t.Path)
	}
	// with a run: player chat from the server log (once, with sender names), not each agent's received copy
	runChars := map[string]bool{}
	for _, t := range transcripts {
		runChars[t.Name] = true
	}
	keep := ClientChatFilter()
	if _, err := os.Stat(serverLog); len(runChars) > 0 && err == nil {
		files = append(files, serverLog)
	}
	port := "8800"
	if v, ok := Flag(args, "port"); ok {
		port = v
	}
	chars := []string{}
	if v, ok := Flag(args, "chars"); ok {
		chars = strings.Split(v, ",")
	}
	sinceMin := 60.0
	if v, ok := Flag(args, "since"); ok {
		sinceMin, _ = strconv.ParseFloat(strings.TrimSuffix(v, "m"), 64)
	}
	start := float64(time.Now().UnixMilli()) - sinceMin*60_000

	// ---------- tail the logs ----------
	var (
		mu      sync.Mutex
		events  []ev // everything since start, in arrival order
		clients = map[chan ev]bool{}
		items   = map[int]any{}
		offsets = map[string]int64{}
		partial = map[string]string{}
	)
	poll := func() {
		var fresh []ev
		for _, f := range files {
			st, err := os.Stat(f)
			if err != nil {
				continue
			}
			size, from := st.Size(), offsets[f]
			if size < from { // truncated or rotated: start over
				from = 0
				offsets[f] = 0
			}
			if size <= from {
				continue
			}
			fd, err := os.Open(f)
			if err != nil {
				continue
			}
			buf := make([]byte, size-from)
			n, _ := fd.ReadAt(buf, from)
			fd.Close()
			offsets[f] = from + int64(n)
			text := partial[f] + string(buf[:n])
			cut := strings.LastIndex(text, "\n") + 1
			partial[f] = text[cut:]
			lines := text[:cut]
			var got []ev
			if name, ok := transcriptOf[f]; ok {
				got = TranscriptEvents(lines, name)
			} else if f == serverLog && len(runChars) > 0 {
				got = ServerChat(lines, runChars)
			} else {
				for _, e := range ParseLines(lines) {
					if len(runChars) == 0 || keep(e) {
						got = append(got, e)
					}
				}
			}
			for _, e := range got {
				if N(e, "t") >= start && (len(chars) == 0 || slices.Contains(chars, S(e, "n"))) {
					fresh = append(fresh, e)
				}
			}
		}
		SortByTime(fresh)
		NameUnits(fresh)
		var loot []int
		for _, e := range fresh {
			id := int(N(e, "item"))
			if _, ok := items[id]; S(e, "ev") == "loot" && !ok && !slices.Contains(loot, id) {
				loot = append(loot, id)
			}
		}
		for id, info := range world.ItemNames(loot) {
			i, _ := strconv.Atoi(id)
			items[i] = info
		}
		mu.Lock()
		defer mu.Unlock()
		for _, e := range fresh {
			if info, ok := items[int(N(e, "item"))]; ok && S(e, "ev") == "loot" {
				e["itemInfo"] = info
			}
			events = append(events, e)
			for c := range clients {
				select {
				case c <- e:
				default: // a client that can't keep up is dropped
					delete(clients, c)
					close(c)
				}
			}
		}
	}
	poll()
	go func() {
		for range time.Tick(500 * time.Millisecond) {
			poll()
		}
	}()

	// ---------- viewer data ----------
	zones := map[string]string{}
	for _, m := range []uint32{0, 1} {
		for id, name := range world.Areas(m).Names {
			zones[strconv.Itoa(id)] = name
		}
	}
	labels := append(world.Labels(0, nil), world.Labels(1, nil)...)
	meta := AgentMeta(transcripts)
	tileVersion := 1 // tiles changed from the terrain render to the minimap: busts browser caches
	if world.HasClientData() {
		tileVersion = 2
	}
	page := func() (string, error) {
		now := float64(time.Now().UnixMilli())
		mu.Lock()
		first := now
		if len(events) > 0 {
			first = N(events[0], "t")
		}
		mu.Unlock()
		// the clock starts at the oldest event we have (not --since), so the timeline isn't mostly empty
		return Page("live", "", map[string]any{"live": true, "title": "live", "start": first, "duration": now - first,
			"chars": chars, "players": []any{}, "events": []any{}, "zones": zones, "items": map[string]any{}, "agents": meta,
			"tileVersion": tileVersion, "labels": labels,
			"map": map[string]any{"id": 0, "xMax": world.GridSize * 32, "yMax": world.GridSize * 32, "res": 2}}) // origin at the grid corner, 2 yd/px
	}

	var tileMu sync.Mutex
	tiles := map[string][]byte{}
	tile := func(m uint32, gx, gy int) []byte {
		key := fmt.Sprint(m, "/", gx, "/", gy)
		tileMu.Lock()
		defer tileMu.Unlock()
		if t, ok := tiles[key]; ok {
			return t
		}
		// the client's minimap for that square if it was extracted (roads, buildings, water); else the terrain render
		t := world.MinimapTile(m, gx, gy)
		if t == nil {
			S := world.GridSize
			xMax, yMax := S*float64(32-gx), S*float64(32-gy)
			t = world.RenderMap(m, xMax-S, xMax, yMax-S, yMax, 2, &[2]float64{-60, 420}, nil).PNG // fixed colour scale so tiles match
		}
		tiles[key] = t
		return t
	}

	tilePath := regexp.MustCompile(`^/tile/(\d+)/(\d+)/(\d+)\.png$`)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			s, err := page()
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.Header().Set("content-type", "text/html; charset=utf-8")
			io.WriteString(w, s)
			return
		}
		if t := tilePath.FindStringSubmatch(r.URL.Path); t != nil {
			m, _ := strconv.Atoi(t[1])
			gx, _ := strconv.Atoi(t[2])
			gy, _ := strconv.Atoi(t[3])
			w.Header().Set("content-type", "image/png")
			w.Header().Set("cache-control", "max-age=86400")
			w.Write(tile(uint32(m), gx, gy))
			return
		}
		if r.URL.Path == "/stream" {
			since := start
			if v := r.URL.Query().Get("since"); v != "" {
				var err error
				if since, err = strconv.ParseFloat(v, 64); err != nil || math.IsNaN(since) {
					since = math.Inf(1) // as the TS did with NaN: no backlog
				}
			}
			fl, _ := w.(http.Flusher)
			w.Header().Set("content-type", "text/event-stream")
			w.Header().Set("cache-control", "no-cache")
			send := func(e ev) bool {
				b, _ := json.Marshal(e)
				if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
					return false
				}
				return true
			}
			c := make(chan ev, 4096)
			mu.Lock()
			backlog := slices.Clone(events)
			clients[c] = true
			mu.Unlock()
			defer func() {
				mu.Lock()
				if clients[c] {
					delete(clients, c)
					close(c)
				}
				mu.Unlock()
			}()
			for _, e := range backlog {
				if N(e, "t") >= since && !send(e) {
					return
				}
			}
			if fl != nil {
				fl.Flush()
			}
			for {
				select {
				case e, ok := <-c:
					if !ok || !send(e) {
						return
					}
					if fl != nil {
						fl.Flush()
					}
				case <-r.Context().Done():
					return
				}
			}
		}
		http.Error(w, "not found", 404)
	})
	mu.Lock()
	n := len(events)
	mu.Unlock()
	fmt.Printf("live viewer on http://localhost:%s/ (%s; %d events from the last %g min)\n", port, strings.Join(files, ", "), n, sinceMin)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
