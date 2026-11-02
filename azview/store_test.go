package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pthm/leeroy/azbench/gamelog"
	"github.com/pthm/leeroy/azbench/runfiles"
)

func evs(t *testing.T, text string) []Ev {
	t.Helper()
	out := ParseLines(text)
	if len(out) == 0 {
		t.Fatal("no events")
	}
	return out
}

func storeOf(t *testing.T, text string) *Store {
	s := NewStore()
	for _, e := range evs(t, text) {
		s.add(e)
	}
	return s
}

func TestStoreKeepsTimeOrderAndInterpolates(t *testing.T) {
	s := storeOf(t, `{"t":2000,"ev":"pos","n":"A","x":10,"y":0,"m":0,"alive":1}
{"t":1000,"ev":"pos","n":"A","x":0,"y":0,"m":0,"alive":1}
{"t":9000,"ev":"pos","n":"A","x":100,"y":0,"m":0,"alive":1}
{"t":1500,"ev":"kill","n":"A","victim":{"id":6,"n":"Kobold"}}
{"t":1200,"ev":"death","n":"A","killer":{"id":6,"n":"Kobold"}}`)
	p := s.byName["A"]
	if len(p.Track) != 3 || p.Track[0].T != 1000 || p.Track[1].T != 2000 {
		t.Fatalf("track not sorted: %+v", p.Track)
	}
	if got, _ := p.At(1500); got.X != 5 {
		t.Fatalf("want x 5 halfway, got %v", got.X)
	}
	if got, _ := p.At(5000); got.X != 10 { // a 7 s gap: hold
		t.Fatalf("want x held at 10 over a gap, got %v", got.X)
	}
	if _, ok := p.At(500); ok {
		t.Fatal("no position before the first sample")
	}
	if len(s.events) != 2 || S(s.events[0], "ev") != "death" || len(s.marks) != 1 || len(s.feed) != 2 {
		t.Fatalf("events %v marks %d feed %d", s.events, len(s.marks), len(s.feed))
	}
}

func TestStoreMergesRepeats(t *testing.T) {
	s := storeOf(t, `{"t":1,"ev":"chat","n":"A","from":"Marshal","type":12,"msg":"hi"}
{"t":2,"ev":"chat","n":"B","from":"Marshal","type":12,"msg":"hi"}
{"t":3,"ev":"group","n":"A","grp":5,"leader":"A","members":["A","B"]}
{"t":4,"ev":"group","n":"A","grp":5,"leader":"A","members":["A","B"]}`)
	if n := len(s.byEv["chat"]); n != 1 {
		t.Fatalf("NPC speech heard twice should be one event, got %d", n)
	}
	if h := s.byEv["chat"][0]["heard"].([]string); len(h) != 2 || !s.Hears(s.byEv["chat"][0], "B") || s.Hears(s.byEv["chat"][0], "C") {
		t.Fatalf("heard %v", h)
	}
	if len(s.groups) != 1 {
		t.Fatalf("a repeated group list should be dropped, got %d", len(s.groups))
	}
}

func TestStoreTargetsAndGroups(t *testing.T) {
	s := storeOf(t, `{"t":1000,"ev":"pos","n":"A","x":0,"y":0,"m":0,"alive":1,"grp":7,"hp":100,"hpmax":100}
{"t":1000,"ev":"pos","n":"B","x":3,"y":4,"m":0,"alive":1,"grp":7,"hp":50,"hpmax":100}
{"t":1000,"ev":"pos","n":"C","x":50,"y":0,"m":0,"alive":1}
{"t":900,"ev":"group","n":"A","grp":7,"leader":"B","members":["A","B"]}
{"t":1000,"ev":"units","n":"A","m":0,"u":[[6,3,40,0,80,0],[6,3,10,0,30,0]]}
{"t":1100,"ev":"tgt","n":"A","tgt":{"id":6,"n":"Kobold","lvl":3}}
{"t":1100,"ev":"dmg","n":"C","src":{"id":3,"n":"C","pl":1},"dst":{"id":6,"n":"Kobold"},"amount":5}
{"t":1100,"ev":"tgt","n":"B","tgt":{"id":1,"n":"A","pl":1}}`)
	a := s.byName["A"]
	tg := s.TargetOf(a, 1200)
	if tg == nil || tg.Name != "Kobold" || *tg.X != 10 || *tg.HP != 30 {
		t.Fatalf("want the nearer kobold at x 10 with 30%%, got %+v", tg)
	}
	if tg := s.TargetOf(s.byName["C"], 1200); tg == nil || tg.Name != "Kobold" { // no target from the server: its last hit
		t.Fatalf("want C's last hit as its target, got %+v", tg)
	}
	if tg := s.TargetOf(s.byName["C"], 5000); tg != nil {
		t.Fatalf("a hit older than 3 s isn't a target, got %+v", tg)
	}
	if tg := s.TargetOf(s.byName["B"], 1200); tg == nil || !tg.Player || *tg.HP != 100 {
		t.Fatalf("want player A at full health, got %+v", tg)
	}
	groups, solo := s.GroupsAt(1200)
	if len(groups) != 1 || groups[0].Leader != "B" || len(groups[0].Members) != 2 || len(solo) != 1 {
		t.Fatalf("groups %+v solo %v", groups, solo)
	}
}

func TestLineReadsLikeTheGame(t *testing.T) {
	s := storeOf(t, `{"t":1,"ev":"login","n":"A","cls":5}
{"t":2,"ev":"chat","n":"A","to":"B","type":7,"msg":"<hi>"}`)
	w := s.byEv["chat"][0]
	if got := string(s.Line(w, "A")); !strings.Contains(got, "To [") || !strings.Contains(got, "&lt;hi&gt;") {
		t.Fatalf("whisper from the sender's side, escaped: %s", got)
	}
	if got := string(s.Line(w, "B")); !strings.Contains(got, "whispers: ") || !strings.Contains(got, "#FFFFFF") { // A is a priest
		t.Fatalf("whisper from the receiver's side: %s", got)
	}
	if Money(10203) != "1g 2s 3c" || Money(0) != "0c" || Fmt(3723000) != "1:02:03" || Fmt(65000) != "1:05" {
		t.Fatal("money or time format")
	}
}

func TestPollTailsFiles(t *testing.T) {
	dir := t.TempDir()
	rec, tr := filepath.Join(dir, "game", "A", "events.jsonl"), filepath.Join(dir, "agent.jsonl")
	os.MkdirAll(filepath.Dir(rec), 0o755)
	os.WriteFile(rec, []byte(`{"t":500,"type":"state","data":{"m":0,"x":1,"y":1}}
{"t":1000,"type":"state","data":{"m":0,"x":1,"y":1}}
{"seq":1,"t":1100,"type":"result","data":{"op":"move.to","ok":true}}
{"seq":2,"t":1200,"type":"xp","data":{"amount":`), 0o644) // the last line is half written
	os.WriteFile(tr, []byte(`{"type":"assistant","timestamp":"1970-01-01T00:00:01.300Z","message":{"content":[{"type":"text","text":"hello"}]}}
`), 0o644)
	s := NewStore()
	src := &Source{Files: []string{rec, tr}, Games: map[string]*gamelog.Reader{}, Scores: map[string]string{}, Transcripts: map[string]string{tr: "A"}, Since: 900}
	s.Poll(src)
	if p := s.byName["A"]; p == nil || len(p.Track) != 1 || len(p.says) != 1 || len(s.events) != 1 {
		t.Fatalf("want one sample after since and one say, got %+v %v", p, s.events)
	}
	f, _ := os.OpenFile(rec, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("10}}\n")
	f.Close()
	s.Poll(src)
	if len(s.byEv["xp"]) != 1 || N(s.byEv["xp"][0], "amount") != 10 {
		t.Fatalf("the finished line should be read: %v", s.byEv["xp"])
	}
}

// A viewer started before every session is up finds the late agents (and the clock's start) on a later poll.
func TestPollFindsLateAgents(t *testing.T) {
	f := runfiles.Run(t.TempDir())
	m := runfiles.Manifest{Players: []runfiles.Player{{Agent: "a1", Char: "A"}, {Agent: "a2", Char: "B"}}, Status: "starting"}
	f.WriteManifest(m)
	os.MkdirAll(f.Game("A"), 0o755)
	os.WriteFile(f.Events("A"), []byte(`{"t":1000,"type":"state","data":{"m":0,"x":1,"y":1}}`+"\n"), 0o644)
	s := NewStore()
	src := &Source{Dir: string(f), Games: map[string]*gamelog.Reader{}, Scores: map[string]string{}, Transcripts: map[string]string{}}
	s.Poll(src)
	if s.byName["A"] == nil || s.byName["B"] != nil {
		t.Fatalf("want only A, got %v", s.byName)
	}
	os.MkdirAll(f.Game("B"), 0o755)
	os.WriteFile(f.Events("B"), []byte(`{"t":2000,"type":"state","data":{"m":0,"x":2,"y":2}}`+"\n"), 0o644)
	os.MkdirAll(f.Agent("a2"), 0o755)
	os.WriteFile(f.Transcript("a2"), nil, 0o644)
	m.Started = 1500
	f.WriteManifest(m)
	src.scanned = time.Time{}
	s.Poll(src)
	if p := s.byName["B"]; p == nil || len(p.Track) != 1 || src.Transcripts[f.Transcript("a2")] != "B" || s.Start != 1500 {
		t.Fatalf("want B with its sample and Start from run.json, got %+v %v %v", p, src.Transcripts, s.Start)
	}
}

// A teleport (the hearthstone, a graveyard) isn't interpolated: the player is where they were until the next sample.
func TestAtHoldsOverTeleport(t *testing.T) {
	s := storeOf(t, `{"t":1000,"ev":"pos","n":"A","x":0,"y":0,"m":0,"alive":1}
{"t":2000,"ev":"pos","n":"A","x":800,"y":0,"m":0,"alive":1}
{"t":3000,"ev":"pos","n":"A","x":805,"y":0,"m":0,"alive":1}`)
	p := s.byName["A"]
	if got, _ := p.At(1500); got.X != 0 {
		t.Fatalf("800 yd in a second is a teleport: held at 0, got %v", got.X)
	}
	if got, _ := p.At(2500); got.X != 802.5 {
		t.Fatalf("5 yd in a second is walking: interpolated, got %v", got.X)
	}
}

// a pet rides in its master's pos: parsed, moved between samples (without changing the stored sample), on the card
func TestStorePet(t *testing.T) {
	s := storeOf(t, `{"t":1000,"ev":"pos","n":"A","x":0,"y":0,"m":0,"alive":1,"hp":10,"hpmax":10,"pet":{"n":"Voidwalker","x":2,"y":0,"hp":50,"hpmax":100,"lvl":60,"alive":1}}
{"t":2000,"ev":"pos","n":"A","x":10,"y":0,"m":0,"alive":1,"hp":10,"hpmax":10,"pet":{"n":"Voidwalker","x":12,"y":0,"hp":50,"hpmax":100,"lvl":60,"alive":1}}`)
	p := s.byName["A"]
	got, _ := p.At(1500)
	if got.Pet == nil || got.Pet.X != 7 || got.Pet.Name != "Voidwalker" || p.Track[0].Pet.X != 2 {
		t.Fatalf("pet halfway: %+v, stored %+v", got.Pet, p.Track[0].Pet)
	}
	if c := s.card(p, 1500, Query{}, false); c.Pet == nil || c.Pet.HPPct != 50 || c.Pet.Dead {
		t.Fatalf("card pet: %+v", c.Pet)
	}
	if m := s.mapSample(p, got); m["pet"] == nil {
		t.Fatalf("map sample has no pet: %v", m)
	}
}

// a player's message in the speaker's record and two listeners' (by: the speaker), in any order: one event, said by
// the speaker, heard by all three
func TestStoreMergesPlayerChat(t *testing.T) {
	s := storeOf(t, `{"t":10,"ev":"chat","n":"B","by":"A","type":2,"msg":"pull"}
{"t":10,"ev":"chat","n":"A","type":2,"msg":"pull"}
{"t":11,"ev":"chat","n":"C","by":"A","type":2,"msg":"pull"}
{"t":20,"ev":"chat","n":"C","by":"A","type":7,"msg":"psst","to":"C"}`)
	if n := len(s.byEv["chat"]); n != 2 {
		t.Fatalf("want 2 messages, got %d: %v", n, s.byEv["chat"])
	}
	e := s.byEv["chat"][0]
	if S(e, "n") != "A" || len(e["heard"].([]string)) != 3 || !s.Hears(e, "C") || !s.Hears(e, "A") {
		t.Fatalf("party message: %v", e)
	}
	if w := s.byEv["chat"][1]; S(w, "n") != "A" || S(w, "to") != "C" {
		t.Fatalf("whisper: %v", w)
	}
}
