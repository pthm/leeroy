package session

import (
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

// task.keep reruns a script that ends or fails, and task.stop ends it for good.
func TestTaskKeep(t *testing.T) {
	e := script.NewEngine(func(string, ...any) {}, nil)
	if err := e.DoString(`me = {alive = function() return true end}`); err != nil {
		t.Fatal(err)
	}
	if err := e.DoString(actLua); err != nil {
		t.Fatal(err)
	}
	run := func(d time.Duration) {
		for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			e.CallTick()
		}
	}
	if _, err := e.Eval(`runs = 0 task.keep("k", function() runs = runs + 1 if runs == 1 then error("boom") end end)`); err != nil {
		t.Fatal(err)
	}
	run(3500 * time.Millisecond) // a failed run, a 2 s backoff, a second run
	if v, _ := e.Eval(`return runs`); v.(float64) < 2 {
		t.Fatalf("runs = %v, want a rerun after the failure", v)
	}
	e.Eval(`task.stop("k")`)
	before, _ := e.Eval(`return runs`)
	run(5 * time.Second)
	if after, _ := e.Eval(`return runs`); after != before {
		t.Fatalf("still running after task.stop: %v -> %v", before, after)
	}
}

// events.watch: a stall once XP stops, again only after progress resumes, and the progress digest.
func TestWatchStall(t *testing.T) {
	b := &Bot{lua: script.NewEngine(func(string, ...any) {}, nil)}
	b.setupWatch()
	if _, err := b.lua.Eval(`return events.watch{stall_s = 1, every_s = 1}`); err != nil {
		t.Fatal(err)
	}
	wait := func(typ string, since uint64) []script.Event {
		return b.lua.Bus.Wait(script.Query{Since: since, Types: []string{typ}}, 7*time.Second)
	}
	if evs := wait("stall", 0); len(evs) == 0 || evs[0].Data.(types.Stall).Reason != "no_xp" {
		t.Fatalf("no stall: %v", evs)
	}
	b.emit("xp", types.XPGain{Amount: 100})
	since := b.lua.Bus.Seq()
	if evs := wait("progress", since); len(evs) == 0 || evs[0].Data.(types.Progress).XP != 100 {
		t.Fatalf("progress: %v", evs)
	}
	if evs := wait("stall", since); len(evs) == 0 {
		t.Fatal("no second stall after progress resumed and stopped again")
	}
}
