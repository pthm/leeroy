package session

import (
	"github.com/pthm/leeroy/azeroth-sdk/script"
	"testing"
	"time"
)

// Real scope wrappers and controller, with deterministic game action stubs.
func leaseEngine(t *testing.T) (*script.Engine, *movementFixture) {
	t.Helper()
	f := newMovementFixture(t, false)
	e := script.NewEngine(func(string, ...any) {}, nil)
	if err := e.DoString(actBase); err != nil {
		t.Fatal(err)
	}
	f.control.reqs = e.Reqs
	f.control.registerLeases(e)
	e.OnPoll = f.control.poll
	e.OnTaskInactive = f.control.cancelTask
	e.OnClose = f.control.close
	if err := e.DoString(actLua); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e, f
}
func evalMovement(t *testing.T, e *script.Engine, code string) {
	t.Helper()
	if _, err := e.Eval(code); err != nil {
		t.Fatalf("%v; tasks: %+v", err, e.TasksHeld())
	}
}
func TestMovementLuaScopePreemptionAndCallbackFailure(t *testing.T) {
	e, f := leaseEngine(t)
	defer func() {
		if t.Failed() {
			t.Logf("tasks: %+v", e.TasksHeld())
		}
	}()
	evalMovement(t, e, `
  task.spawn("normal", function()
   old_result = move.with_control{fn=function()
    return move.with_control{fn=function()
     events.wait("script.continue", 2)
     return act.ok("callback")
    end}
   end}
  end)
 `)
	e.CallTick()
	o, _ := f.control.snapshot()
	if o == nil || o.TaskID != 1 {
		t.Fatalf("scope not held: %v", o)
	}
	evalMovement(t, e, `
  task.spawn("urgent", function()
   urgent_result=move.with_control{priority="urgent",fn=function()
    events.emit("continue", {})
    return act.ok("callback")
   end}
  end)
 `)
	e.CallTick()
	waitMovement(t, func() bool {
		e.CallTick()
		v, _ := e.Eval(`return old_result ~= nil and urgent_result ~= nil`)
		return v == true
	})
	evalMovement(t, e, `assert(urgent_result.ok); assert(not old_result.ok and old_result.error.code=="preempted")`)
	if o, _ := f.control.snapshot(); o != nil {
		t.Fatalf("leaked scope: %v", o)
	}
	evalMovement(t, e, `
  task.spawn("caught",function()
   local ok, err=pcall(function() move.with_control{fn=function() wait(0.01); error("callback fault") end} end)
   assert(not ok and tostring(err):find("callback fault"));caught=true
  end)
 `)
	e.CallTick()
	time.Sleep(20 * time.Millisecond)
	e.CallTick()
	evalMovement(t, e, `assert(caught)`)
	if o, _ := f.control.snapshot(); o != nil {
		t.Fatalf("caught exception leaked ownership: %v", o)
	}
}
func TestMovementHelperLeaseSpansWaypoints(t *testing.T) {
	e, f := leaseEngine(t)
	evalMovement(t, e, `
  local old_to=move.to
  move.to=function(x,y,z)
   if #moves==0 then events.wait("next",2) end
   return old_to(x,y,z)
  end
  task.spawn("travel",function() travel_result=act.travel{points={{100,0,0},{200,0,0}}} end)
 `)
	e.CallTick()
	o, _ := f.control.snapshot()
	if o == nil || o.TaskID != 1 {
		t.Fatalf("travel lost ownership while waiting: %v", o)
	}
	queued := leaseRequest(f, 99, "normal", 5, 600)
	e.Emit("next", nil)
	e.CallTick()
	evalMovement(t, e, `assert(travel_result.ok and #moves==2)`)
	grantedLease(t, f, queued)
}
func TestMovementLuaLeaseExpiresWhileCallbackSleeps(t *testing.T) {
	e, f := leaseEngine(t)
	evalMovement(t, e, `
  task.spawn("sleeping",function()
   expired=move.with_control{max_s=0.1,fn=function() events.wait("wake",2);return act.ok("callback") end}
  end)
 `)
	e.CallTick()
	f.clock.Add(int64(100 * time.Millisecond))
	e.CallTick()
	if o, _ := f.control.snapshot(); o != nil {
		t.Fatalf("sleeping callback retained expired lease: %v", o)
	}
	e.Emit("wake", nil)
	e.CallTick()
	evalMovement(t, e, `assert(not expired.ok and expired.error.code=="lease_timeout")`)
}
func TestActApproachAwaitsFacingAndKeepsPendingWalk(t *testing.T) {
	actWorld(t, actBase+`
  reads=0
  units.get=function() reads=reads+1;return {x=100,y=0,z=0,distance=reads<3 and 100 or 2,in_sight=true} end
  move.to=function() moves[#moves+1]={};return {id=999,pending=true} end
  move.face=function() return {ok=false,error={code="failed",message="turn failed"}} end
 `, `r=act.approach{unit="123"};assert(not r.ok and r.error.code=="failed" and #moves==1)`)
}
func TestActFollowRequiresBoundAndBoundsAbsence(t *testing.T) {
	actWorld(t, actBase+`
  units.players=function() return {} end
  group={info=function() return nil end}
  clock=0;now=function() clock=clock+1000;return clock end
 `, `
  local ok=pcall(act.follow,{name="missing"});assert(not ok)
  r=act.follow{name="missing",timeout_s=60};assert(not r.ok and r.error.code=="player_not_visible")
 `)
}
