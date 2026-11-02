package script

import (
	"fmt"
	"testing"
	"time"
)

func TestImmediateRequestContract(t *testing.T) {
	e := NewEngine(nolog, nil)
	for _, ok := range []bool{true, false} {
		t.Run(fmt.Sprint(ok), func(t *testing.T) {
			o := Ok(map[string]any{"completed": 1})
			if !ok {
				o = Fail("not_enough_money", "cannot buy", o.Data)
			}
			h := e.Reqs.Now(Spec{Op: "vendor.buy"}, o)
			if h["pending"] != true || h["ok"] != nil || len(h) != 3 {
				t.Fatalf("raw call returned an outcome instead of a handle: %v", h)
			}
			r := e.Reqs.Result(h["id"].(int))
			if r["pending"] != nil || r["ok"] != ok || r["op"] != "vendor.buy" || r["data"] == nil {
				t.Fatalf("resolved result: %v", r)
			}
			if !ok && r["error"].(map[string]any)["code"] != o.Code {
				t.Fatalf("lost error: %v", r)
			}
		})
	}
}

func TestAwaitTimeoutDoesNotResolveRequest(t *testing.T) {
	e := NewEngine(nolog, nil)
	e.Func("test.start", "() → Request", "", func() map[string]any {
		return e.Reqs.Start(Spec{Op: "test.start", Timeout: time.Minute})
	})
	if _, err := e.Eval(`task.spawn("waiter", function() req = test.start(); r = await(req, 0) end)`); err != nil {
		t.Fatal(err)
	}
	tick(e, 2)
	v, err := e.Eval(`return r.op, r.ok, r.error.code, r.pending, result(req.id).pending`)
	if err != nil || fmt.Sprint(v) != "[test.start false wait_timeout <nil> true]" {
		t.Fatalf("timeout result: %v, %v", v, err)
	}
	e.Reqs.Resolve(1, Ok("later"))
	v, err = e.Eval(`return result(req.id).ok, result(req.id).data, r.error.code`)
	if err != nil || fmt.Sprint(v) != "[true later wait_timeout]" {
		t.Fatalf("late completion: %v, %v", v, err)
	}
	if _, err := e.Eval(`return await(req)`); err == nil {
		t.Fatal("await outside a task must direct callers to tasks")
	}
}
