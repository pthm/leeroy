package main

import (
	"bytes"
	"fmt"
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

const party = `{"t":1,"ev":"login","n":"A","cls":1}
{"t":1,"ev":"login","n":"B","cls":5}
{"t":900,"ev":"group","n":"A","grp":7,"leader":"B","members":["A","B"]}
{"t":1000,"ev":"pos","n":"A","x":0,"y":0,"m":0,"alive":1,"combat":1,"grp":7,"hp":1234,"hpmax":2000,"pt":1,"pw":30,"pwmax":100,"lvl":12,"money":10203}
{"t":1000,"ev":"pos","n":"B","x":30,"y":40,"m":0,"alive":0,"ghost":1,"grp":7,"hp":0,"hpmax":100,"lvl":11}
{"t":1000,"ev":"pos","n":"C","x":500,"y":0,"m":0,"alive":1,"hp":10,"hpmax":10,"lvl":3}
{"t":1000,"ev":"units","n":"A","m":0,"u":[[6,3,5,0,40,0]]}
{"t":1100,"ev":"tgt","n":"A","tgt":{"id":6,"n":"Kobold <Miner>","lvl":3}}
{"t":1100,"ev":"say","n":"A","text":"pulling now\nsecond line"}
{"t":1100,"ev":"score","n":"C","value":10,"score":2.5,"pace":1,"recent_rate":3}`

func TestRosterPanel(t *testing.T) {
	s := storeOf(t, party)
	got := render(t, s, "roster", Query{T: 1200, ByGroup: true, Sort: "score", Sel: "A"})
	for _, want := range []string{"Group of 2", "50 yd apart", "1,234 / 2,000", "Kobold &lt;Miner&gt;", "40%", "“pulling now”", `class="pc combat sel"`,
		`class="pc ghost"`, "Solo", "score <b>2.5</b>/h", `1<span class="g">g</span> 2<span class="s">s</span> 3<span class="c">c</span>`} {
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
{"t":1150,"ev":"chat","n":"C","type":1,"msg":"far away"}
{"t":1150,"ev":"chat","n":"B","type":2,"msg":"party only"}
{"t":1160,"ev":"dmg","n":"A","src":{"id":1,"n":"A","pl":1},"dst":{"id":6,"n":"Kobold"},"amount":7}
{"t":1170,"ev":"level","n":"C","to":4}`)
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
{"t":1300,"ev":"dmg","n":"A","src":{"id":1,"n":"A","pl":1},"dst":{"id":6,"n":"Kobold"},"amount":30}
{"t":1400,"ev":"dmg","n":"A","src":{"id":1,"n":"A","pl":1},"dst":{"id":7,"n":"Wolf"},"amount":10}
{"t":1500,"ev":"dmg","n":"C","src":{"id":3,"n":"C","pl":1},"dst":{"id":6,"n":"Kobold"},"amount":60}
{"t":2000,"ev":"pos","n":"A","x":0,"y":3,"m":0,"alive":1,"combat":1,"grp":7,"hp":1234,"hpmax":2000,"lvl":12}
{"t":3000,"ev":"pos","n":"A","x":0,"y":7,"m":0,"alive":1,"combat":1,"grp":7,"hp":1234,"hpmax":2000,"lvl":12}`)
	s.Start = 0
	q := Query{T: 3000, Tab: "stats", Metric: "dmg", Range: "all", Chart: "total", Sel: "A"}
	got := render(t, s, "pane", q)
	for _, want := range []string{"party <b>100</b>", `data-n="C"`, ">60<", ">40<", "60%", "40%", "<svg class=\"chart\"", "<svg class=\"strip\"", "A</span> by target", "Kobold", "Wolf", "75%"} {
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
		lines = append(lines, fmt.Sprintf(`{"t":%d,"ev":"pos","n":"A","x":%d,"y":0,"m":0,"alive":1}`, i*1000, max(0, i-30)), fmt.Sprintf(`{"t":%d,"ev":"pos","n":"B","x":500,"y":500,"m":0,"alive":1}`, i*1000))
	}
	s := storeOf(t, strings.Join(lines, "\n"))
	h := s.HeatAt(0, 60000, nil)
	if h.PNG == "" || h.X0 < 530 || h.Y0 < 530 || h.W < 150 {
		t.Fatalf("both players' ground, padded: %+v", h)
	}
	a := s.HeatAt(0, 60000, map[string]bool{"A": true})
	if a.X0 > 100 || a.W > 30 {
		t.Fatalf("only A's ground: %+v", a)
	}
	if none := s.HeatAt(1, 60000, nil); none.PNG != "" {
		t.Fatal("nobody on map 1")
	}
}
