package session

import (
	"sync"
	"time"
)

// Never reuse a sequence while its request can be pending. Keep a further
// 30-second quarantine for delayed outcomes after cancellation or timeout.
// Sequence zero is reserved for casts initiated outside the request API.
type castSequences struct {
	mu    sync.Mutex
	until [256]time.Time
	next  uint8
}

func (s *castSequences) reserve(now time.Time, timeout time.Duration) (uint8, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < 255; i++ {
		s.next++
		if s.next == 0 {
			s.next = 1
		}
		if !now.Before(s.until[s.next]) {
			s.until[s.next] = now.Add(timeout + 30*time.Second)
			return s.next, true
		}
	}
	return 0, false
}
