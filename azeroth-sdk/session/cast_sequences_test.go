package session

import (
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
	"testing"
	"time"
)

func TestCastSequencesDoNotReusePendingOrLateReplySlots(t *testing.T) {
	var s castSequences
	now := time.Now()
	seen := map[uint8]bool{}
	for i := 0; i < 255; i++ {
		n, ok := s.reserve(now, 10*time.Second)
		if !ok || n == 0 || seen[n] {
			t.Fatalf("duplicate/reserved sequence %d", n)
		}
		seen[n] = true
	}
	if _, ok := s.reserve(now.Add(39*time.Second), time.Second); ok {
		t.Fatal("reused within late reply quarantine")
	}
	if _, ok := s.reserve(now.Add(40*time.Second), time.Second); !ok {
		t.Fatal("expired sequence not reusable")
	}
}
func TestCastMatchRejectsOtherRequestAndUnsolicitedOutcome(t *testing.T) {
	first, second := castMatch(133, 1), castMatch(133, 2)
	for _, typ := range []string{"cast_done", "cast_failed"} {
		ev := script.Event{Type: typ, Data: types.Cast{Spell: 133, Count: 1, Code: "moving"}}
		if first(ev) == nil || second(ev) != nil {
			t.Fatal("same-spell requests shared an outcome")
		}
		ev.Data = types.Cast{Spell: 133, Count: 0}
		if first(ev) != nil {
			t.Fatal("unsolicited cast completed request")
		}
		ev.Data = types.Cast{Spell: 116, Count: 1}
		if first(ev) != nil {
			t.Fatal("wrong spell completed request")
		}
	}
}

// A wand's Shoot: the request's count on cast_start, every shot count 0 (Isolde's 879 failed Shoot requests while
// 1141 shots hit, 2026-10-31)
func TestAutoRepeatShotCompletesTheCast(t *testing.T) {
	match := castMatchRepeating(5019, 124, true)
	if o := match(script.Event{Type: "cast_done", Data: types.Cast{Spell: 5019, Count: 0, Hits: 1}}); o == nil || !o.OK {
		t.Fatalf("shot = %+v", o)
	}
	if o := match(script.Event{Type: "cast_failed", Data: types.Cast{Spell: 5019, Count: 0, Code: "interrupted"}}); o != nil {
		t.Fatalf("another cast's failure matched: %+v", o)
	}
	if o := match(script.Event{Type: "cast_done", Data: types.Cast{Spell: 133, Count: 0, Hits: 1}}); o != nil {
		t.Fatalf("another spell matched: %+v", o)
	}
}
