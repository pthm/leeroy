package session

import (
	"testing"
	"time"
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
