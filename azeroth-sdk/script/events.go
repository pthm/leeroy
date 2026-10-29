package script

// The event stream: everything that happens to a character, in order, kept for the whole run. Readers never drain
// it: each keeps its own position (a sequence number). Events also go to an append-only JSONL file, so a session
// restart (or another process) can read the history.

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

// Event is one thing that happened. Data holds JSON-able values only.
type Event struct {
	Seq  uint64         `json:"seq"`
	T    int64          `json:"t"` // unix ms
	Type string         `json:"type"`
	Data map[string]any `json:"data,omitempty"`
}

const busKeep = 50000 // ponytail: in-memory window; the file has everything

type Bus struct {
	mu        sync.Mutex
	seq       uint64
	buf       []Event
	file      *os.File
	changed   chan struct{} // closed and replaced on every emit: a broadcast to waiters
	listeners []func(Event)
}

// NewBus opens (or creates) the event file and loads its tail, so sequence numbers continue across restarts.
// An empty path keeps events in memory only.
func NewBus(path string) *Bus {
	b := &Bus{changed: make(chan struct{})}
	if path == "" {
		return b
	}
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var e Event
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				b.buf = append(b.buf, e)
				if len(b.buf) > busKeep {
					b.buf = b.buf[len(b.buf)-busKeep:]
				}
				b.seq = max(b.seq, e.Seq)
			}
		}
		f.Close()
	}
	b.file, _ = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	return b
}

// Listen registers fn for every new event. fn runs on the emitter's goroutine and must not block or emit.
func (b *Bus) Listen(fn func(Event)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.listeners = append(b.listeners, fn)
}

// Emit records an event and wakes everyone waiting for one.
func (b *Bus) Emit(typ string, data map[string]any) Event {
	b.mu.Lock()
	b.seq++
	e := Event{Seq: b.seq, T: time.Now().UnixMilli(), Type: typ, Data: data}
	b.buf = append(b.buf, e)
	if len(b.buf) > busKeep+busKeep/10 {
		b.buf = append([]Event(nil), b.buf[len(b.buf)-busKeep:]...)
	}
	if b.file != nil {
		if line, err := json.Marshal(e); err == nil {
			b.file.Write(append(line, '\n'))
		}
	}
	close(b.changed)
	b.changed = make(chan struct{})
	ls := b.listeners
	b.mu.Unlock()
	for _, fn := range ls {
		fn(e)
	}
	return e
}

// Seq is the latest sequence number.
func (b *Bus) Seq() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

// Query selects events: after Since, of Types (any if empty), whose JSON contains Match, the Last n of them
// (or the first Limit after Since).
type Query struct {
	Since uint64   `json:"since"`
	Types []string `json:"types"`
	Match string   `json:"match"`
	Last  int      `json:"last"`
	Limit int      `json:"limit"`
}

func (q Query) wants(e Event) bool {
	if e.Seq <= q.Since {
		return false
	}
	if len(q.Types) > 0 && !typeIn(e.Type, q.Types) {
		return false
	}
	if q.Match != "" {
		raw, _ := json.Marshal(e.Data)
		if !strings.Contains(strings.ToLower(string(raw)), strings.ToLower(q.Match)) {
			return false
		}
	}
	return true
}

// typeIn: "chat" matches "chat"; a pattern ending in "*" matches a prefix ("quest_*")
func typeIn(t string, types []string) bool {
	for _, p := range types {
		if p == t || p == "*" || (strings.HasSuffix(p, "*") && strings.HasPrefix(t, strings.TrimSuffix(p, "*"))) {
			return true
		}
	}
	return false
}

func (b *Bus) Query(q Query) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []Event{}
	for _, e := range b.buf {
		if q.wants(e) {
			out = append(out, e)
		}
	}
	if q.Last > 0 && len(out) > q.Last {
		out = out[len(out)-q.Last:]
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out
}

// Wait returns the events matching q, waiting up to timeout for the first one to arrive.
func (b *Bus) Wait(q Query, timeout time.Duration) []Event {
	deadline := time.After(timeout)
	for {
		if out := b.Query(q); len(out) > 0 {
			return out
		}
		b.mu.Lock()
		ch := b.changed
		b.mu.Unlock()
		select {
		case <-ch:
		case <-deadline:
			return []Event{}
		}
	}
}
