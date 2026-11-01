package main

import (
	"bytes"
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
