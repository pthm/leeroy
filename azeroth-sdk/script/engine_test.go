package script

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nolog(string, ...any) {}

func tick(e *Engine, n int) {
	for i := 0; i < n; i++ {
		e.CallTick()
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLuaWorkspaceAndExecutionDeadline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "module.lua"), []byte("return 42"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.lua")
	if err := os.WriteFile(outside, []byte("return 99"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.lua")); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(nolog, nil)
	if err := e.SetWorkspace(dir); err != nil {
		t.Fatal(err)
	}
	v, err := e.Eval(`return dofile("module.lua"), type(os.time), os.execute, io, package, require`)
	if err != nil || fmt.Sprint(v) != "[42 function <nil> <nil> <nil> <nil>]" {
		t.Fatalf("workspace and libraries: %v, %v", v, err)
	}
	for _, path := range []string{"escape.lua", outside, "../outside.lua"} {
		_, err = e.Eval(`return dofile("` + path + `")`)
		if err == nil {
			t.Fatalf("escaped workspace via %q", path)
		}
	}
	start := time.Now()
	_, err = e.Eval(`while true do end`)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("evaluation did not stop promptly: %v", err)
	}
	if v, err = e.Eval(`return 7`); err != nil || v != 7.0 {
		t.Fatalf("engine remained blocked: %v, %v", v, err)
	}
	_, err = e.Eval(`task.spawn("loop", function() while true do end end)`)
	if err != nil {
		t.Fatal(err)
	}
	e.CallTick()
	tasks := e.TasksHeld()
	if len(tasks) != 1 || tasks[0].State != "failed" || !strings.Contains(tasks[0].Error, "without waiting") {
		t.Fatalf("task was not stopped: %+v", tasks)
	}
	_, err = e.Eval(`task.spawn("nested", function() pcall(function() while true do end end) end)`)
	if err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	e.CallTick()
	if time.Since(start) > 5*time.Second {
		t.Fatal("nested coroutine blocked the session")
	}
}

func TestRuntime(t *testing.T) {
	e := NewEngine(nolog, nil)
	var got map[string]any
	e.Func("t.sum", "", "", func(m map[string]any) float64 { got = m; return m["a"].(float64) + m["b"].(float64) })
	e.Func("t.act", "", "", func() map[string]any { return e.Reqs.Start(Spec{Op: "act", Timeout: time.Second}) })
	v, err := e.Eval(`return t.sum{a = 1, b = 2}`)
	if err != nil || v != 3.0 || got == nil {
		t.Fatalf("table arg: %v %v", v, err)
	}
	_, err = e.Eval(`
		log = {}
		task.spawn("w", function() wait(0.02); log[#log+1] = "waited"
			local r = await(t.act()); log[#log+1] = r.ok and "ok" or r.error.code
			local ev = events.wait("ping", 5); log[#log+1] = ev.data.n
			local c = ask("go?", {"yes","no"}); log[#log+1] = c
		end)
		events.on("ping", function(ev) seen = ev.data.n end)`)
	if err != nil {
		t.Fatal(err)
	}
	tick(e, 10)
	e.Reqs.Resolve(1, Ok(nil))
	tick(e, 3)
	e.Eval(`events.emit("ping", {n = 7})`) // becomes script.ping
	e.Emit("ping", map[string]any{"n": 7})
	tick(e, 3)
	att := e.Bus.Query(Query{Types: []string{"attention"}})
	if len(att) != 1 {
		t.Fatalf("attention events: %v", att)
	}
	e.Eval(`answer(` + itoa(att[0].Seq) + `, "yes")`)
	tick(e, 3)
	v, _ = e.Eval(`return table.concat(log, ","), seen`)
	if r := v.([]any); r[0] != "waited,ok,7,yes" || r[1] != 7.0 {
		t.Fatalf("log: %v", v)
	}
	if v, _ := e.Eval(`return help("task.sp")[1]`); v != "task.spawn([name,] fn, ...) → id  run fn as a task: it can wait without blocking the game" {
		t.Fatalf("help: %v", v)
	}
	e.Func("t.empty", "", "", func() []int { return []int{} })
	if v, _ := e.Eval(`return t.empty()`); fmt.Sprint(v) != "[]" {
		t.Fatalf("empty list: %#v", v)
	}
	// waiting inside pcall (the bug that crashed sessions), and a failing task never takes the engine down
	e.Eval(`pc = {} task.spawn("pc", function()
		local ok, v = pcall(function() wait(0.01) return 42 end) pc.a = ok and v
		local ok2, err = pcall(function() wait(0.01) error("boom") end) pc.b = (not ok2) and tostring(err):find("boom") ~= nil
		local ok3, h = xpcall(function() wait(0.01) error("x") end, function(m) return "handled" end) pc.c = h
	end)`)
	tick(e, 10)
	if v, _ := e.Eval(`return pc.a, pc.b, pc.c`); fmt.Sprint(v) != "[42 true handled]" {
		t.Fatalf("pcall in task: %v", v)
	}
	// API names are protected: reading works, assigning raises a clear error, locals are fine
	e.Eval(`_protect{"t", "task"}`)
	if _, err := e.Eval(`t = {}`); err == nil || !strings.Contains(err.Error(), "part of the game API") {
		t.Fatalf("protect: %v", err)
	}
	if v, err := e.Eval(`local t = 5 return t, type(task.spawn), type(t)`); err != nil || fmt.Sprint(v) != "[5 function number]" {
		t.Fatalf("protect read: %v %v", v, err)
	}
	if _, err := e.Eval(`mything = 1 return mything`); err != nil {
		t.Fatalf("plain globals: %v", err)
	}
	if _, err := e.Eval(`wait(1)`); err == nil {
		t.Fatal("wait outside a task should fail")
	}
	v, _ = e.Eval(`return string.match("Hogger 12", "(%a+) (%d+)")`)
	if r := v.([]any); r[0] != "Hogger" {
		t.Fatalf("patterns: %v", v)
	}
	v, err = e.Eval(`local n = 0
		for i = 1, 3 do if i == 2 then goto skip end n = n + i ::skip:: end
		return {bit32.band(0x1003, 0x1000), bit32.bor(1, 2, 4), bit32.rshift(256, 4), bit32.lshift(1, 31), table.unpack({7, 8}),
			load("return 5")(), n, bit32.btest(6, 2)}`)
	if err != nil || fmt.Sprint(v) != "[4096 7 16 2.147483648e+09 7 5 4 true]" {
		t.Fatalf("compat: %v %v", v, err)
	}
}

func itoa(n uint64) string { return fmt.Sprint(n) }

// task.stop by name skips the caller; with no argument it stops everything else and reports a stopped walk;
// pause/resume keep a task's wait (an action's result or an event that came in while paused is still delivered).
func TestTaskControl(t *testing.T) {
	e := NewEngine(nolog, nil)
	e.Func("t.act", "", "", func() map[string]any { return e.Reqs.Start(Spec{Op: "act", Timeout: time.Minute}) })
	var stopped []int
	e.OnStop = func(req int) { stopped = append(stopped, req) }
	e.Eval(`log = {}
		task.spawn("main", function() task.stop("main") log[#log+1] = "survived" end)
		task.spawn("w", function() local r = await(t.act()) log[#log+1] = r.ok and "w ok" or "w fail" end)
		task.spawn("ev", function() local ev = events.wait("ping", 5) log[#log+1] = ev and "ev " .. ev.data.n or "ev nil" end)`)
	tick(e, 3)
	v, _ := e.Eval(`return task.pause(2), task.pause(3), task.pause(99)`)
	if fmt.Sprint(v) != "[true true false]" {
		t.Fatalf("pause: %v", v)
	}
	e.Reqs.Resolve(1, Ok(nil))
	e.Emit("ping", map[string]any{"n": 5})
	tick(e, 3)
	if v, _ := e.Eval(`return table.concat(log, ",")`); v != "survived" {
		t.Fatalf("paused tasks ran: %v", v)
	}
	e.Eval(`task.resume(2) task.resume(3)`)
	tick(e, 3)
	if v, _ := e.Eval(`return table.concat(log, ",")`); v != "survived,w ok,ev 5" {
		t.Fatalf("after resume: %v", v)
	}
	e.Eval(`task.spawn("a", function() await(t.act()) end) task.spawn("b", function() wait(60) end)
		task.spawn("boss", function() n = task.stop() end)`)
	tick(e, 3)
	if v, _ := e.Eval(`return n`); v != 2.0 || len(stopped) != 1 || stopped[0] != 2 {
		t.Fatalf("stop all: n=%v walks stopped=%v", v, stopped)
	}
	if _, err := e.Eval(`task.spawn("self", function() task.stop(task.current()) end)`); err != nil {
		t.Fatal(err)
	}
	tick(e, 2)
	for _, x := range e.TasksHeld() {
		if x.Name == "self" && x.State != "failed" {
			t.Fatalf("stopping yourself by id should fail the task: %+v", x)
		}
	}
}
