package script

// Requests: every game action returns a request at once and resolves later with a structured result, which is also
// emitted as a "result" event. A request resolves through a check polled each tick (state-based), a matcher run on
// every event (packet-based), an explicit Resolve, or its timeout.

import (
	"sync"
	"time"
)

// Outcome is how a request ended. Code is a stable error string ("" when OK).
type Outcome struct {
	OK      bool
	Code    string
	Message string
	Data    any
}

func Ok(data any) *Outcome { return &Outcome{OK: true, Data: data} }
func Fail(code, msg string, data any) *Outcome {
	return &Outcome{Code: code, Message: msg, Data: data}
}

// Spec describes one request: Check is polled each tick, Match sees each event; either returns nil to keep waiting.
type Spec struct {
	Op      string
	Args    map[string]any
	Timeout time.Duration
	Check   func() *Outcome
	Match   func(Event) *Outcome
	Hint    string // added to the timeout message: what usually went wrong
}

type pending struct {
	id        int
	spec      Spec
	created   time.Time
	observers []func(map[string]any)
}

type Requests struct {
	mu       sync.Mutex
	bus      *Bus
	next     int
	pending  map[int]*pending
	done     map[int]map[string]any
	order    []int
	lastFail map[string]time.Time // op + code -> when an instant failure was last logged
}

func NewRequests(bus *Bus) *Requests {
	r := &Requests{bus: bus, pending: map[int]*pending{}, done: map[int]map[string]any{}, lastFail: map[string]time.Time{}}
	bus.Listen(r.onEvent)
	return r
}

// Start registers a request and returns its handle {id, op, pending = true}.
func (r *Requests) Start(s Spec) map[string]any {
	if s.Timeout == 0 {
		s.Timeout = 10 * time.Second
	}
	r.mu.Lock()
	r.next++
	id := r.next
	r.pending[id] = &pending{id: id, spec: s, created: time.Now()}
	r.mu.Unlock()
	return map[string]any{"id": id, "op": s.Op, "pending": true}
}

// Now resolves a request immediately and returns its handle. Only a failure
// becomes a "result" event (instant oks would flood the log).
// A failure repeated within 10 s (same op and code: a script retrying every tick) is logged once.
func (r *Requests) Now(s Spec, o *Outcome) map[string]any {
	h := r.Start(s)
	emit := !o.OK
	if emit {
		key := s.Op + " " + o.Code
		r.mu.Lock()
		if time.Since(r.lastFail[key]) < 10*time.Second {
			emit = false
		} else {
			r.lastFail[key] = time.Now()
		}
		r.mu.Unlock()
	}
	r.resolve(h["id"].(int), o, emit)
	return h
}

// Resolve ends a pending request with o and emits the "result" event. Resolving twice is a no-op.
func (r *Requests) Resolve(id int, o *Outcome) map[string]any { return r.resolve(id, o, true) }

func (r *Requests) resolve(id int, o *Outcome, emit bool) map[string]any {
	r.mu.Lock()
	p := r.pending[id]
	if p == nil {
		res := r.done[id]
		r.mu.Unlock()
		return res
	}
	delete(r.pending, id)
	res := map[string]any{"id": id, "op": p.spec.Op, "ok": o.OK, "ms": time.Since(p.created).Milliseconds()}
	if p.spec.Args != nil {
		res["args"] = p.spec.Args
	}
	if o.Data != nil {
		res["data"] = o.Data
	}
	if !o.OK {
		res["error"] = map[string]any{"code": o.Code, "message": o.Message}
	}
	r.done[id] = res
	r.order = append(r.order, id)
	if len(r.order) > 5000 { // ponytail: keep the last 5000 results; older ones are in the event log
		delete(r.done, r.order[0])
		r.order = r.order[1:]
	}
	r.mu.Unlock()
	for _, observe := range p.observers {
		observe(res)
	}
	if emit {
		r.bus.Emit("result", res)
	}
	return res
}

// Observe attaches a completion observer after an API call returns its handle. It also sees immediate
// completions, bypassing result-event throttling. Registration racing with resolution invokes it exactly once.
func (r *Requests) Observe(id int, fn func(map[string]any)) {
	r.mu.Lock()
	if p := r.pending[id]; p != nil {
		p.observers = append(p.observers, fn)
		r.mu.Unlock()
		return
	}
	res := r.done[id]
	r.mu.Unlock()
	if res != nil {
		fn(res)
	}
}

func (r *Requests) close() {
	r.mu.Lock()
	ids := make([]int, 0, len(r.pending))
	for id := range r.pending {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	for _, id := range ids {
		r.Resolve(id, Fail("session_closed", "session ended before completion was observed", nil))
	}
}

// Result returns the request's result, {id, op, pending = true} while it's pending, or nil if unknown.
func (r *Requests) Result(id int) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	if res, ok := r.done[id]; ok {
		return res
	}
	if p, ok := r.pending[id]; ok {
		return map[string]any{"id": id, "op": p.spec.Op, "pending": true}
	}
	return nil
}

// Poll runs pending checks and timeouts (once per tick).
func (r *Requests) Poll() {
	r.mu.Lock()
	list := make([]*pending, 0, len(r.pending))
	for _, p := range r.pending {
		list = append(list, p)
	}
	r.mu.Unlock()
	for _, p := range list {
		if p.spec.Check != nil {
			if o := p.spec.Check(); o != nil {
				r.Resolve(p.id, o)
				continue
			}
		}
		if time.Since(p.created) > p.spec.Timeout {
			msg := "no result from the server in time"
			if p.spec.Hint != "" {
				msg += ": " + p.spec.Hint
			}
			r.Resolve(p.id, Fail("timeout", msg, nil))
		}
	}
}

func (r *Requests) onEvent(e Event) {
	if e.Type == "result" {
		return
	}
	r.mu.Lock()
	var hits []struct {
		id int
		o  *Outcome
	}
	for id, p := range r.pending {
		if p.spec.Match != nil {
			if o := p.spec.Match(e); o != nil {
				hits = append(hits, struct {
					id int
					o  *Outcome
				}{id, o})
			}
		}
	}
	r.mu.Unlock()
	for _, h := range hits {
		go r.Resolve(h.id, h.o) // Resolve emits; the bus forbids emitting from a listener
	}
}
