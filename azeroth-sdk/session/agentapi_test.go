package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func TestCorpseCacheInvalidatedAcrossLives(t *testing.T) {
	s := &agentState{corpse: []float32{1, 2, 3, 0}, corpseKnown: true, corpseAt: time.Now(), reclaimAt: time.Now().Add(time.Minute)}
	timer := s.reclaimAt
	s.invalidateCorpse()
	if s.corpseKnown || s.corpse != nil || !s.corpseAt.IsZero() {
		t.Fatal("previous corpse survived")
	}
	if s.reclaimAt != timer {
		t.Fatal("lost the server's reclaim deadline")
	}
}

// Logging in dead adds a death only when the character's stream (kept across restarts) doesn't already end in one.
func TestLoginDeathOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Merrin.events.jsonl")
	bus := script.NewBus(path)
	bus.Emit("death", types.Death{})
	bus.Emit("chat", types.Chat{Text: "brb"})
	b := &Bot{lua: script.NewEngine(func(string, ...any) {}, script.NewBus(path))} // a restarted session
	if !b.diedLast() {
		t.Fatal("died before the restart: logging in dead is the same death")
	}
	b.lua.Bus.Emit("resurrected", types.None{})
	if b.diedLast() {
		t.Fatal("resurrected since: logging in dead is a new death")
	}
	if (&Bot{lua: script.NewEngine(func(string, ...any) {}, nil)}).diedLast() {
		t.Fatal("an empty stream")
	}
}
