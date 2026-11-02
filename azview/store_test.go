package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pthm/leeroy/azbench/gamelog"
	"github.com/pthm/leeroy/azbench/runfiles"
)

// evs reads test lines: game log lines with the character beside them ({"char", "t", "type", "data"}).
func evs(t *testing.T, text string) []Ev {
	t.Helper()
	var out []Ev
	for _, l := range lines(text) {
		var c struct{ Char string }
		json.Unmarshal([]byte(l), &c)
		if e := gamelog.Line([]byte(l), c.Char); e != nil {
			out = append(out, e)
		}
	}
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
	s := storeOf(t, `{"char":"A","t":2000,"type":"state","data":{"map":0,"x":10,"y":0,"alive":true}}
{"char":"A","t":1000,"type":"state","data":{"map":0,"x":0,"y":0,"alive":true}}
{"char":"A","t":9000,"type":"state","data":{"map":0,"x":100,"y":0,"alive":true}}
{"char":"A","t":1500,"type":"kill","data":{"victim":{"name":"Kobold","entry":6}}}
{"char":"A","t":1200,"type":"death","data":{"killer":{"name":"Kobold","entry":6}}}`)
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
	if len(s.events) != 2 || S(s.events[0], "type") != "death" || len(s.marks) != 1 || len(s.feed) != 2 {
		t.Fatalf("events %v marks %d feed %d", s.events, len(s.marks), len(s.feed))
	}
}

func TestStoreMergesRepeats(t *testing.T) {
	s := storeOf(t, `{"char":"A","t":1,"type":"chat","data":{"kind":"npc_say","text":"hi","from":"Marshal"}}
{"char":"B","t":2,"type":"chat","data":{"kind":"npc_say","text":"hi","from":"Marshal"}}
{"char":"A","t":3,"type":"group","data":{"group":5,"leader":"A","members":["A","B"]}}
{"char":"A","t":4,"type":"group","data":{"group":5,"leader":"A","members":["A","B"]}}`)
	if n := len(s.byEv["chat"]); n != 1 {
		t.Fatalf("NPC speech heard twice should be one event, got %d", n)
	}
	if h := s.byEv["chat"][0]["heard"].([]string); len(h) != 2 || !s.Hears(s.byEv["chat"][0], "B") || s.Hears(s.byEv["chat"][0], "C") {
		t.Fatalf("heard %v", h)
	}
	if len(s.groups) != 1 {
		t.Fatalf("a repeated group list should be dropped, got %d", len(s.groups))
	}
	s = storeOf(t, `{"char":"A","t":1,"type":"group","data":{"members":[]}}
{"char":"A","t":2,"type":"group","data":{"group":5,"leader":"A","members":["A","B"]}}
{"char":"A","t":3,"type":"group","data":{"members":[]}}`)
	if len(s.groups) != 2 || N(s.groups[0], "group") != 5 || N(s.groups[1], "group") != 0 {
		t.Fatalf("no group at login isn't leaving one; leaving after one is: %v", s.groups)
	}
}

func TestStoreTargetsAndGroups(t *testing.T) {
	s := storeOf(t, `{"char":"A","t":1000,"type":"state","data":{"map":0,"x":0,"y":0,"health":{"current":100,"max":100},"group":7,"alive":true}}
{"char":"B","t":1000,"type":"state","data":{"map":0,"x":3,"y":4,"health":{"current":50,"max":100},"group":7,"alive":true}}
{"char":"C","t":1000,"type":"state","data":{"map":0,"x":50,"y":0,"alive":true}}
{"char":"A","t":900,"type":"group","data":{"group":7,"leader":"B","members":["A","B"]}}
{"char":"A","t":1000,"type":"units","data":{"map":0,"units":[[6,3,40,0,80,0],[6,3,10,0,30,0]]}}
{"char":"A","t":1100,"type":"state","data":{"map":0,"x":0,"y":0,"health":{"current":100,"max":100},"group":7,"alive":true,"target":{"name":"Kobold","entry":6,"level":3}}}
{"char":"C","t":1100,"type":"damage","data":{"src":{"name":"C","guid":"3","player":true},"dst":{"name":"Kobold","entry":6},"amount":5}}
{"char":"B","t":1100,"type":"state","data":{"map":0,"x":3,"y":4,"health":{"current":50,"max":100},"group":7,"alive":true,"target":{"name":"A","guid":"1","player":true}}}`)
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
	s := storeOf(t, `{"char":"A","t":1,"type":"login","data":{"class":"priest"}}
{"char":"A","t":2,"type":"chat","data":{"kind":"whisper_sent","text":"<hi>","from":"B","self":true}}`)
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
	src := &Source{Files: []string{rec, tr}, Games: map[string]string{}, Scores: map[string]string{}, Transcripts: map[string]string{tr: "A"}, Since: 900}
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
	src := &Source{Dir: string(f), Games: map[string]string{}, Scores: map[string]string{}, Transcripts: map[string]string{}}
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
	s := storeOf(t, `{"char":"A","t":1000,"type":"state","data":{"map":0,"x":0,"y":0,"alive":true}}
{"char":"A","t":2000,"type":"state","data":{"map":0,"x":800,"y":0,"alive":true}}
{"char":"A","t":3000,"type":"state","data":{"map":0,"x":805,"y":0,"alive":true}}`)
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
	s := storeOf(t, `{"char":"A","t":1000,"type":"state","data":{"map":0,"x":0,"y":0,"health":{"current":10,"max":10},"alive":true,"pet":{"name":"Voidwalker","x":2,"y":0,"health":{"current":50,"max":100},"level":60,"alive":true}}}
{"char":"A","t":2000,"type":"state","data":{"map":0,"x":10,"y":0,"health":{"current":10,"max":10},"alive":true,"pet":{"name":"Voidwalker","x":12,"y":0,"health":{"current":50,"max":100},"level":60,"alive":true}}}`)
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
	s := storeOf(t, `{"char":"B","t":10,"type":"chat","data":{"kind":"party","text":"pull","from":"A"}}
{"char":"A","t":10,"type":"chat","data":{"kind":"party","text":"pull","from":"A","self":true}}
{"char":"C","t":11,"type":"chat","data":{"kind":"party","text":"pull","from":"A"}}
{"char":"C","t":20,"type":"chat","data":{"kind":"whisper","text":"psst","from":"A"}}`)
	if n := len(s.byEv["chat"]); n != 2 {
		t.Fatalf("want 2 messages, got %d: %v", n, s.byEv["chat"])
	}
	e := s.byEv["chat"][0]
	if S(e, "speaker") != "A" || len(e["heard"].([]string)) != 3 || !s.Hears(e, "C") || !s.Hears(e, "A") {
		t.Fatalf("party message: %v", e)
	}
	if w := s.byEv["chat"][1]; S(w, "speaker") != "A" || !s.Hears(w, "C") || s.Hears(w, "B") {
		t.Fatalf("whisper: %v", w)
	}
}
