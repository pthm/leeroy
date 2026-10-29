package script

import (
	"fmt"
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
