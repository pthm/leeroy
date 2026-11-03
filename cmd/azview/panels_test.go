package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"strings"
	"testing"
)

func render(t *testing.T, s *Store, name string, q Query) string {
	t.Helper()
	tmpl, data := s.Panel(name, q)
	var b bytes.Buffer
	if err := templates().ExecuteTemplate(&b, tmpl, data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "ZgotmplZ") { // html/template refused a value in an attribute or style
		t.Fatalf("%s: unsafe value replaced: %s", name, b.String())
	}
	return b.String()
}

const party = `{"char":"A","t":1,"type":"login","data":{"class":"warrior"}}
{"char":"B","t":1,"type":"login","data":{"class":"priest"}}
{"char":"A","t":900,"type":"group","data":{"group":7,"leader":"B","members":["A","B"]}}
{"char":"A","t":1000,"type":"state","data":{"map":0,"x":0,"y":0,"health":{"current":1234,"max":2000},"power":{"type":"rage","current":30,"max":100},"level":12,"money":10203,"group":7,"alive":true,"in_combat":true}}
{"char":"B","t":1000,"type":"state","data":{"map":0,"x":30,"y":40,"health":{"current":0,"max":100},"level":11,"group":7,"alive":false,"ghost":true}}
{"char":"C","t":1000,"type":"state","data":{"map":0,"x":500,"y":0,"health":{"current":10,"max":10},"level":3,"alive":true}}
{"char":"A","t":1000,"type":"units","data":{"map":0,"units":[[6,3,5,0,40,0]]}}
{"char":"A","t":1100,"type":"state","data":{"map":0,"x":0,"y":0,"health":{"current":1234,"max":2000},"power":{"type":"rage","current":30,"max":100},"level":12,"money":10203,"group":7,"alive":true,"in_combat":true,"target":{"name":"Kobold <Miner>","entry":6,"level":3}}}
{"char":"A","t":1100,"type":"say","data":{"text":"pulling now\nsecond line"}}
{"char":"C","t":1100,"type":"score","data":{"value":10,"score":2.5,"pace":1,"recent_rate":3}}`

func TestRosterPanel(t *testing.T) {
	s := storeOf(t, party)
	got := render(t, s, "roster", Query{T: 1200, ByGroup: true, Sort: "score", Sel: "A"})
	for _, want := range []string{
		"Group of 2", "50 yd apart", "1,234 / 2,000", "Kobold &lt;Miner&gt;", "40%", "“pulling now”", `class="pc combat sel"`,
		`class="pc ghost"`, "Solo", "score <b>2.5</b>/h", `1<span class="g">g</span> 2<span class="s">s</span> 3<span class="c">c</span>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("roster lacks %q", want)
		}
	}
	if strings.Index(got, `data-n="B"`) > strings.Index(got, `data-n="A"`) {
		t.Error("the leader's card comes first")
	}
	flat := render(t, s, "roster", Query{T: 1200, Sort: "level"})
	if strings.Contains(flat, "Group of") || strings.Index(flat, `data-n="A"`) > strings.Index(flat, `data-n="C"`) {
		t.Error("flat, by level: A (12) before C (3), no groups")
	}
}

func TestFeedPanel(t *testing.T) {
	s := storeOf(t, party+`
{"char":"C","t":1150,"type":"chat","data":{"kind":"say","text":"far away","from":"C","self":true}}
{"char":"B","t":1150,"type":"chat","data":{"kind":"party","text":"party only","from":"B","self":true}}
{"char":"A","t":1160,"type":"damage","data":{"src":{"name":"A","guid":"1","player":true},"dst":{"name":"Kobold","entry":6},"amount":7}}
{"char":"C","t":1170,"type":"level_up","data":{"level":4}}`)
	all := render(t, s, "pane", Query{T: 2000, Tab: "feed", Cats: map[string]bool{"events": true, "chat": true, "agent": true}})
	for _, want := range []string{"far away", "party only", "reached level <b>4</b>", "“pulling now"} {
		if !strings.Contains(all, want) {
			t.Errorf("feed lacks %q", want)
		}
	}
	if strings.Contains(all, "hit") {
		t.Error("combat is filtered out")
	}
	if strings.Index(all, "party only") > strings.Index(all, "reached level") {
		t.Error("oldest first")
	}
	a := render(t, s, "pane", Query{T: 2000, Tab: "feed", Sel: "A", Cats: map[string]bool{"events": true, "chat": true, "combat": true}})
	if !strings.Contains(a, "party only") || strings.Contains(a, "far away") || strings.Contains(a, "level") || !strings.Contains(a, "for <b>7</b>") || !strings.Contains(a, "data-clear") {
		t.Errorf("A's feed: their party's chat and their hit, not C's say 500 yd away or C's level, with the everyone chip: %s", a)
	}
	if early := render(t, s, "pane", Query{T: 1155, Tab: "feed", Cats: map[string]bool{"events": true}}); strings.Contains(early, "level") {
		t.Error("nothing after the playhead")
	}
}

func TestStatsPanel(t *testing.T) {
	s := storeOf(t, party+`
{"char":"A","t":1300,"type":"damage","data":{"src":{"name":"A","guid":"1","player":true},"dst":{"name":"Kobold","entry":6},"amount":30}}
{"char":"A","t":1400,"type":"damage","data":{"src":{"name":"A","guid":"1","player":true},"dst":{"name":"Wolf","entry":7},"amount":10}}
{"char":"C","t":1500,"type":"damage","data":{"src":{"name":"C","guid":"3","player":true},"dst":{"name":"Kobold","entry":6},"amount":60}}
{"char":"A","t":2000,"type":"state","data":{"map":0,"x":0,"y":3,"health":{"current":1234,"max":2000},"level":12,"group":7,"alive":true,"in_combat":true}}
{"char":"A","t":3000,"type":"state","data":{"map":0,"x":0,"y":7,"health":{"current":1234,"max":2000},"level":12,"group":7,"alive":true,"in_combat":true}}`)
	s.Start = 0
	q := Query{T: 3000, Tab: "stats", Metric: "dmg", Range: "all", Chart: "total", Sel: "A"}
	got := render(t, s, "pane", q)
	for _, want := range []string{"party <b>100</b>", `data-n="C"`, ">60<", ">40<", "60%", "40%", `id="plots"`, `"series":[`, `"rows":[`, "A</span> by target", "Kobold", "Wolf", "75%"} {
		if !strings.Contains(got, want) {
			t.Errorf("stats lacks %q", want)
		}
	}
	if strings.Index(got, `data-n="C"`) > strings.Index(got, `data-n="A"`) {
		t.Error("C (60) ranks above A (40)")
	}
	// A was in combat for 2 s (1000 -> 3000): 40 damage is 20/s; C has no samples in combat: 60/1 s
	if !strings.Contains(got, "20.0/s") && !strings.Contains(got, ">20/s<") {
		t.Errorf("A's DPS over their time in combat: %s", got)
	}
	defs := s.statDefs()
	if r := s.compute(defs["distance"], 0, 3000, nil, false, "", nil); r.tot["A"] != 7 {
		t.Errorf("A walked 3 + 4 yd, got %v", r.tot["A"])
	}
	if r := s.compute(defs["level"], 0, 3000, nil, false, "", nil); r.tot["A"] != 12 || r.tot["C"] != 3 {
		t.Errorf("levels at the playhead: %v", r.tot)
	}
	if lo, hi := s.statRange("fight", 1600); lo != 1300 || hi != 1600 {
		t.Errorf("the fight runs from the first hit: %v %v", lo, hi)
	}
	if lo, hi := s.statRange("fight", 20000); lo != 1300 || hi != 1500 {
		t.Errorf("a fight over 10 s ago ends at its last hit: %v %v", lo, hi)
	}
	rate := render(t, s, "pane", Query{T: 3000, Tab: "stats", Metric: "combat", Range: "all", Chart: "rate"})
	if !strings.Contains(rate, "Share") || !strings.Contains(rate, `data-stat="per"`) {
		t.Errorf("a time metric's rate is a share, with a period picker: %s", rate)
	}
	group := render(t, s, "pane", Query{T: 3000, Tab: "stats", Metric: "dmg", Range: "all", Grp: 7})
	if strings.Contains(group, `data-n="C"`) || !strings.Contains(group, "only group") {
		t.Error("a selected group narrows the stats to its players")
	}
}

// The heatmap covers where the players went, more where they stayed; only the selected players count.
func TestHeatAt(t *testing.T) {
	var lines []string
	for i := 0; i < 60; i++ { // A stands at (0, 0) for a minute, then walks north 30 yd; B is elsewhere
		lines = append(lines, fmt.Sprintf(`{"char":"A","t":%d,"type":"state","data":{"map":0,"x":%d,"y":0,"alive":true}}`, i*1000, max(0, i-30)), fmt.Sprintf(`{"char":"B","t":%d,"type":"state","data":{"map":0,"x":500,"y":500,"alive":true}}`, i*1000))
	}
	s := storeOf(t, strings.Join(lines, "\n"))
	h := s.HeatAt(0, 60000, nil, "time")
	if h.PNG == "" || h.X0 < 530 || h.Y0 < 530 || h.W < 150 {
		t.Fatalf("both players' ground, padded: %+v", h)
	}
	a := s.HeatAt(0, 60000, map[string]bool{"A": true}, "time")
	if a.X0 > 100 || a.W > 30 {
		t.Fatalf("only A's ground: %+v", a)
	}
	if none := s.HeatAt(1, 60000, nil, "time"); none.PNG != "" {
		t.Fatal("nobody on map 1")
	}
}

// A death heatmap is where the deaths were, not the paths.
func TestHeatDeaths(t *testing.T) {
	s := storeOf(t, `{"char":"A","t":0,"type":"state","data":{"map":0,"x":0,"y":0,"alive":true}}
{"char":"A","t":1000,"type":"state","data":{"map":0,"x":300,"y":0,"alive":true}}
{"char":"A","t":2000,"type":"state","data":{"map":0,"x":300,"y":300,"alive":false}}
{"char":"A","t":2000,"type":"death","data":{"killer":{"name":"Kobold","entry":6}}}`)
	h := s.HeatAt(0, 3000, nil, "deaths")
	img, _ := png.Decode(base64.NewDecoder(base64.StdEncoding, strings.NewReader(strings.TrimPrefix(h.PNG, "data:image/png;base64,"))))
	at := func(x, y float64) uint32 { // alpha at a world point
		_, _, _, a := img.At(int((h.Y0-y)/h.Res), int((h.X0-x)/h.Res)).RGBA()
		return a
	}
	if at(300, 300) == 0 || at(0, 0) != 0 {
		t.Fatalf("heat at the death (%d), none where A only walked (%d)", at(300, 300), at(0, 0))
	}
}

// The activity strip has more cells on a wider pane (about one every 8 px), within 4 and 96.
func TestStripResolution(t *testing.T) {
	s := storeOf(t, party)
	s.Start = 0
	narrow, wide, none := s.stripData(0, 3600000, nil, 400), s.stripData(0, 3600000, nil, 900), s.stripData(0, 3600000, nil, 0)
	if (narrow.NB >= wide.NB) || wide.NB > 96 || narrow.NB < 4 || none.NB == 0 || len(wide.Rows) != 3 || len(wide.Rows[0].Cells) != wide.NB {
		t.Fatalf("cells: narrow %d, wide %d, unknown width %d; rows %d", narrow.NB, wide.NB, none.NB, len(wide.Rows))
	}
}
