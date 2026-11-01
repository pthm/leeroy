package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// a route in an event reply is shortened to its count, first and last two points and where to read it whole; full
// keeps it, short lists stay
func TestCompactEventRoutes(t *testing.T) {
	route := make([]types.Position, 50)
	for i := range route {
		route[i] = types.Position{X: float64(i), Y: 1}
	}
	evs := []script.Event{
		{Seq: 1, Type: "result", Data: map[string]any{"id": 7, "ok": true, "data": types.PathCheck{Waypoints: route}}},
		{Seq: 2, Type: "result", Data: map[string]any{"id": 8, "data": types.PathCheck{Waypoints: route[:3]}}},
	}
	raw, _ := json.Marshal(compactEvents(evs, false))
	s := string(raw)
	if !strings.Contains(s, `"waypoints":{"count":50,"end":[{"map":0,"x":48,"y":1,"z":0},{"map":0,"x":49,"y":1,"z":0}],"start":[{"map":0,"x":0,"y":1,"z":0},{"map":0,"x":1,"y":1,"z":0}],"whole":"result(7)"}`) {
		t.Fatal(s)
	}
	if !strings.Contains(s, `"waypoints":[{"map":0,"x":0,`) {
		t.Fatalf("a short list was shortened: %s", s)
	}
	if raw, _ := json.Marshal(compactEvents(evs, true)); strings.Contains(string(raw), "whole") {
		t.Fatal("full shortened")
	}
}

// chat that is only chat is one wake: five lines 300 ms apart come back together once 2 s pass quietly; a whisper,
// a line naming the character or a non-chat event wakes at once
func TestWaitCoalescesChat(t *testing.T) {
	bus := script.NewBus("")
	q := script.Query{Types: []string{"chat", "death"}, Wake: true}
	go func() {
		for i := range 5 {
			time.Sleep(300 * time.Millisecond)
			bus.Emit("chat", types.Chat{Kind: "party", From: "Corbin", Text: fmt.Sprint("SYNC ", i)})
		}
	}()
	start := time.Now()
	evs := wait(bus, q, 30*time.Second, "Devgm")
	if took := time.Since(start); len(evs) != 5 || took < 3*time.Second || took > 4*time.Second {
		t.Fatalf("%d events after %v", len(evs), took)
	}

	for _, ev := range []struct {
		typ  string
		data any
	}{
		{"chat", types.Chat{Kind: "whisper", From: "Corbin", Text: "hi"}},
		{"chat", types.Chat{Kind: "party", From: "Corbin", Text: "devgm, heal"}},
		{"death", types.Death{}},
	} {
		q.Since = bus.Seq()
		go func() {
			time.Sleep(100 * time.Millisecond)
			bus.Emit(ev.typ, ev.data)
		}()
		start := time.Now()
		if evs := wait(bus, q, 30*time.Second, "Devgm"); len(evs) != 1 || time.Since(start) > time.Second {
			t.Fatalf("%v: %d events after %v", ev, len(evs), time.Since(start))
		}
	}

	// a non-chat event during the collection ends it
	q.Since = bus.Seq()
	go func() {
		bus.Emit("chat", types.Chat{Kind: "party", From: "Corbin", Text: "x"})
		time.Sleep(300 * time.Millisecond)
		bus.Emit("death", types.Death{})
	}()
	start = time.Now()
	if evs := wait(bus, q, 30*time.Second, "Devgm"); len(evs) != 2 || time.Since(start) > time.Second {
		t.Fatalf("%d events after %v", len(evs), time.Since(start))
	}
}
