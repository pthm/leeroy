package script

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func TestDiagnosticsImmediateOutcomesAndPrivacy(t *testing.T) {
	e := NewEngine(nolog, nil)
	var events []types.Diagnostic
	e.SetDiagnosticSink("a1", func(d types.Diagnostic) { events = append(events, d) })
	e.Func("chat.whisper", "(name, msg) → Request", "", func(name, msg string) map[string]any {
		return e.Reqs.Now(Spec{Op: "chat.whisper", Args: map[string]any{"name": name, "msg": msg}}, Fail("not_available", msg, map[string]any{"secret": msg}))
	})
	if _, err := e.Eval(`for i = 1, 3 do chat.whisper("secret recipient", "secret message") end`); err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("instant failures were suppressed: %+v", events)
	}
	for i, ev := range events {
		if ev.Seq != uint64(i+1) || ev.Session == "" || ev.Agent != "a1" {
			t.Fatalf("identity: %+v", ev)
		}
		if i > 0 && (ev.Code != "not_available" || ev.Call == "" || ev.Request != i) {
			t.Fatalf("outcome: %+v", ev)
		}
	}
	b, _ := json.Marshal(events)
	if strings.Contains(string(b), "secret") {
		t.Fatalf("sensitive payload copied to diagnostics: %s", b)
	}
	if got := len(e.Bus.Query(Query{Types: []string{"result"}})); got != 1 {
		t.Fatalf("user event throttle changed: %d", got)
	}
}

func TestDiagnosticContextSurvivesYieldAndStop(t *testing.T) {
	e := NewEngine(nolog, nil)
	var events []types.Diagnostic
	e.SetDiagnosticSink("a1", func(d types.Diagnostic) { events = append(events, d) })
	e.Func("move.to", "(x, y, z) → Request", "", func(x, y, z float64) map[string]any {
		return e.Reqs.Start(Spec{Op: "move.to", Timeout: time.Minute})
	})
	_, err := e.Eval(`
      task.spawn("travel", function()
        local outer = task._action_begin("act.travel", {x = 1, y = 2, z = 3})
        local inner = task._action_begin("act.go_to", {x = 1, y = 2, z = 3})
        req = move.to(1, 2, 3)
        await(req)
        task._action_end(inner, {ok = true})
        task._action_end(outer, {ok = true})
      end)
      task.spawn("other", function() other_req = move.to(4, 5, 6) end)`)
	if err != nil {
		t.Fatal(err)
	}
	e.CallTick()
	e.Eval(`task.stop("travel")`)
	e.Reqs.Resolve(1, Fail("stopped", "stopped", nil))
	e.Reqs.Resolve(2, Ok(nil))
	if len(events) != 5 {
		t.Fatalf("events: %+v", events)
	}
	inner, outer, raw, other := events[1], events[2], events[3], events[4]
	if inner.Code != "task_stopped" || outer.Code != "task_stopped" || raw.ParentCall != inner.Call || inner.ParentCall != outer.Call {
		t.Fatalf("correlation: %+v", events)
	}
	if other.ParentCall != "" || other.Task != "other" || raw.Task != "travel" || raw.Details.Destination.X != 1 {
		t.Fatalf("cross-task attribution: %+v", events)
	}
}

func TestDiagnosticTaskStatusAndSessions(t *testing.T) {
	var events []types.Diagnostic
	e := NewEngine(nolog, nil)
	e.SetDiagnosticSink("a1", func(d types.Diagnostic) { events = append(events, d) })
	e.Eval(`task.spawn("main", function() task.status("accepting quest") task.status("accepting quest") task.status(string.rep("x", 1000)) task.status("") end)`)
	e.CallTick()
	if len(events) != 4 || !events[1].Details.AgentClaim || len(events[2].Details.Status) != 512 || events[3].Details.Status != "" {
		t.Fatalf("status changes: %+v", events)
	}
	if events[1].Details.Source != "wow" || events[1].Details.Line == 0 {
		t.Fatalf("source missing: %+v", events[1])
	}
	other := NewEngine(nolog, nil)
	if e.diag.session == other.diag.session {
		t.Fatal("new session reused event identity")
	}
}

func TestRequestObserverRace(t *testing.T) {
	r := NewRequests(NewBus(""))
	var mu sync.Mutex
	observations := 0
	for i := 0; i < 100; i++ {
		h := r.Start(Spec{Op: "test"})
		id := h["id"].(int)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			r.Observe(id, func(map[string]any) { mu.Lock(); observations++; mu.Unlock() })
		}()
		go func() { defer wg.Done(); r.Resolve(id, Ok(nil)); r.Resolve(id, Ok(nil)) }()
		wg.Wait()
	}
	if observations != 100 {
		t.Fatalf("observations = %d", observations)
	}
}
