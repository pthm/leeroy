package world

import (
	"bytes"
	"encoding/json"
	"testing"
)

// worldDB skips a test when the world database isn't reachable (these tests read the live AzerothCore DB).
func worldDB(t *testing.T) {
	t.Helper()
	if err := World().Ping(); err != nil {
		t.Skipf("world database not reachable: %v", err)
	}
}

func lookup(t *testing.T, args ...string) map[string]any {
	t.Helper()
	var out bytes.Buffer
	if code := Run(args, &out); code != 0 {
		t.Fatalf("azworld %v exited %d: %s", args, code, out.String())
	}
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("azworld %v: %v: %s", args, err, out.String())
	}
	return v
}

// The Defias Squallshaper's script comment calls spell 4979 Frost Nova; Spell.dbc says Quick Flame Ward.
func TestNpcCastsNamesSpellsFromDBC(t *testing.T) {
	worldDB(t)
	casts, _ := lookup(t, "npc", "1732")["casts"].([]any)
	for _, c := range casts {
		if c := c.(map[string]any); c["spell"] == 4979.0 {
			if c["name"] != "Quick Flame Ward" || c["when"] != "in_combat" {
				t.Fatalf("spell 4979 = %v", c)
			}
			return
		}
	}
	t.Fatalf("no spell 4979 in %v", casts)
}

// Red Leather Bandana (quest 153) drops from more creatures than the 5 listed.
func TestQuestDroppedByTotal(t *testing.T) {
	worldDB(t)
	for _, it := range lookup(t, "quest", "153")["items"].([]any) {
		it := it.(map[string]any)
		if it["item"] == 829.0 {
			if n := len(it["dropped_by"].([]any)); n != 5 || it["dropped_by_total"].(float64) <= 5 {
				t.Fatalf("dropped_by %d of %v", n, it["dropped_by_total"])
			}
			return
		}
	}
	t.Fatal("no item 829")
}

// Goblin Commoners spawn only for world events.
func TestSpawnsFlagEventOnly(t *testing.T) {
	worldDB(t)
	sp := lookup(t, "npc", "20102")["spawns"].(map[string]any)
	if sp["event_only"] != sp["count"] || sp["points"].([]any)[0].(map[string]any)["event"] == nil {
		t.Fatalf("spawns %v", sp)
	}
	if sp := lookup(t, "npc", "1732")["spawns"].(map[string]any); sp["event_only"] != nil {
		t.Fatalf("Squallshaper spawns flagged event-only: %v", sp)
	}
}
