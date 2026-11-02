package session

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/pthm/leeroy/azsdk/client"
	"github.com/pthm/leeroy/azsdk/script"
	"github.com/pthm/leeroy/azsdk/types"
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

// casting() ends with the cast bar or a SPELL_FAILURE; SMSG_CANCEL_AUTO_REPEAT for us records when auto-repeat stopped;
// spells.stop_casting with nothing cast is not_casting, and sends CMSG_CANCEL_CAST with the spell (the server reads it)
func TestStopCasting(t *testing.T) {
	const me = 0x2A
	s := &agentState{castSpell: 133, castUntil: time.Now().Add(time.Second)}
	if s.casting() != 133 {
		t.Fatal("cast in progress not seen")
	}
	s.onPacket(me, smsgSpellFailure, append(append(packGUID(me), 1), 133, 0, 0, 0, 2))
	if s.casting() != 0 {
		t.Fatal("cast still in progress after SPELL_FAILURE")
	}
	s.onPacket(me, smsgCancelAutoRepeat, packGUID(0x99))
	if !s.repeatEnded.IsZero() {
		t.Fatal("another unit's auto-repeat cancel counted")
	}
	s.onPacket(me, smsgCancelAutoRepeat, packGUID(me))
	if time.Since(s.repeatEnded) > time.Second {
		t.Fatal("auto-repeat cancel not recorded")
	}

	b := testBot()
	b.agent = &agentState{}
	b.setupAgentAPI()
	if _, err := b.lua.Eval(`r = result(spells.stop_casting().id) assert(r.ok == false and r.error.code == "not_casting", json.encode(r))`); err != nil {
		t.Fatal(err)
	}
	var sent []byte
	b.world.OnPacketSend = func(op uint16, d []byte) {
		if op == client.CmsgCancelCast {
			sent = d
		}
	}
	b.agent.castSpell, b.agent.castUntil = 116, time.Now().Add(time.Second)
	b.lua.Eval(`spells.stop_casting()`)
	if !bytes.Equal(sent, []byte{0, 116, 0, 0, 0}) {
		t.Fatalf("CMSG_CANCEL_CAST %x", sent)
	}
}
