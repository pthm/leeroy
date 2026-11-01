package main

import "testing"

func TestServerEventsTargetChanges(t *testing.T) {
	log := `{"t":1,"ev":"pos","p":1,"n":"A","tgt":{"id":6,"n":"Kobold","lvl":2}}
{"t":2,"ev":"pos","p":1,"n":"A","tgt":{"id":6,"n":"Kobold","lvl":2}}
{"t":3,"ev":"pos","p":2,"n":"Other","tgt":{"id":6,"n":"Kobold","lvl":2}}
{"t":4,"ev":"pos","p":1,"n":"A"}
`
	last := map[string]string{}
	got := ServerEvents(log, map[string]bool{"A": true}, last)
	if len(got) != 2 || S(got[0], "ev") != "tgt" || got[0]["tgt"] == nil || got[1]["tgt"] != nil || N(got[1], "t") != 4 {
		t.Fatalf("want a target then none, got %v", got)
	}
	// read in pieces: the same target again is no change
	if more := ServerEvents(`{"t":5,"ev":"pos","p":1,"n":"A"}`+"\n", map[string]bool{"A": true}, last); len(more) != 0 {
		t.Fatalf("no change expected, got %v", more)
	}
}
