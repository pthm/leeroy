package api

import (
	"encoding/json"
	"strings"
	"testing"

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
