package session

import (
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func installTestRecovery(t *testing.T, e *script.Engine) *recoveryCoordinator {
	t.Helper()
	c := &recoveryCoordinator{reqs: e.Reqs, now: time.Now, cancelMovement: func(int, string) {}}
	c.attach(e)
	c.register(e)
	if err := e.DoString(`death=death or {}; recovery_states={}; death._recovery_state=function(state,attempts) recovery_states[#recovery_states+1]={state=state,attempts=attempts} end`); err != nil {
		t.Fatal(err)
	}
	return c
}

const recoveryWorld = actBase + `
 alive=false
 reclaims,spirits=0,0
 death={corpse=function() return {x=0,y=0,z=0,reclaim_in=0} end,
 reclaim=function() reclaims=reclaims+1;alive=true;return {ok=true} end,
 spirit_res=function() spirits=spirits+1;alive=true;return {ok=true} end}
 healer={guid="42",x=0,y=0,z=0,distance=0,roles={"spirit_healer"}}
`

func TestRecoveryBoundedFailuresAndPostconditions(t *testing.T) {
	for _, tc := range []struct{ name, stubs, body string }{
		{"no automatic healer", `death.corpse=function() return {x=100,y=0,z=0,reclaim_in=0} end;world.path=function() return {ok=false,op="world.path",error={code="route_unreachable",message="no path"}} end;mobs={healer}`,
			`r=act.recover{rest=false};assert(not r.ok and r.error.code=="route_unreachable" and spirits==0 and r.data.spirit_option.requires_permission)`},
		{"explicit fallback", `death.corpse=function() return {x=100,y=0,z=0,reclaim_in=0} end;world.path=function() return {ok=false,op="world.path",error={code="route_unreachable",message="no path"}} end;mobs={healer}`,
			`r=act.recover{rest=false,allow_spirit_fallback=true};assert(r.ok and spirits==1 and reclaims==0 and r.data.state=="done" and r.data.last_error.code=="route_unreachable")`},
		{"two reclaim failures", `death.reclaim=function() reclaims=reclaims+1;return {ok=false,op="death.reclaim",error={code="not_yet",message="timer not ready"}} end`,
			`r=act.recover{rest=false};assert(not r.ok and r.error.code=="retry_exhausted" and reclaims==2 and spirits==0 and r.data.last_error.code=="not_yet")`},
		{"timer never ready", `death.corpse=function() return {x=0,y=0,z=0,reclaim_in=99} end`,
			`r=act.recover{rest=false,timeout_s=2};assert(not r.ok and r.error.code=="recovery_timeout" and reclaims==0 and clock==2000)`},
		{"missing corpse", `death.corpse=function() return nil end`,
			`r=act.corpse_run{timeout_s=2};assert(not r.ok and r.error.code=="recovery_timeout" and reclaims==0)`},
		{"safe spot walk failure", `mobs={{alive=true,reaction="hostile",x=10,y=0,z=0}};move.to=function() return {ok=false,op="move.to",error={code="stuck",message="blocked"}} end`,
			`r=act.corpse_run();assert(not r.ok and r.error.code=="stuck" and reclaims==0 and r.data.failed_step=="move.to")`},
		{"wrong floor", `pos.z=10;move.to=function() return {ok=true} end`,
			`r=act.corpse_run();assert(not r.ok and r.error.code=="wrong_floor" and reclaims==0)`},
		{"unknown collision", `world.in_sight=function() return nil end`,
			`r=act.recover{rest=false};assert(not r.ok and r.error.code=="unsafe_recovery" and reclaims==0)`},
		{"unsafe healer cannot bypass", `mobs={healer,{alive=true,reaction="hostile",x=0,y=0,z=0}}`,
			`r=act.recover{spirit=true,safe=false,rest=false};assert(not r.ok and r.error.code=="unsafe_recovery" and spirits==0)`},
		{"healer move failure", `healer.x=10;healer.distance=10;mobs={healer};move.to=function() return {ok=false,op="move.to",error={code="stuck",message="blocked"}} end`,
			`r=act.spirit_res();assert(not r.ok and r.error.code=="stuck" and spirits==0)`},
		{"healer failure budget", `mobs={healer};death.spirit_res=function() spirits=spirits+1;return {ok=false,op="death.spirit_res",error={code="refused",message="refused"}} end`,
			`r=act.spirit_res();assert(not r.ok and r.error.code=="retry_exhausted" and spirits==2 and r.data.last_error.code=="refused")`},
		{"false resurrection success", `death.reclaim=function() reclaims=reclaims+1;return {ok=true} end`,
			`r=act.corpse_run();assert(not r.ok and r.error.code=="retry_exhausted" and reclaims==2 and not r.data.alive)`},
		{"broken main hand", `me.gear=function() return {weapon={broken=true},broken={"main_hand"}} end;mobs={{guid="8",roles={"repair"}}}`,
			`r=act.recover{rest=false};assert(not r.ok and r.error.code=="needs_repair" and r.data.state=="needs_repair" and #r.data.repair_options==1 and r.data.money==100)`},
		{"failed rest", `me.health=function() return {current=50,max=100} end`,
			`r=act.recover{timeout_s=60};assert(not r.ok and r.error.code=="rest_timeout" and r.data.state=="attention")`},
		{"already alive checks gear", `alive=true;me.gear=function() return {weapon={broken=true},broken={"main_hand"}} end`,
			`r=act.recover();assert(not r.ok and r.error.code=="needs_repair" and reclaims==0)`},
		{"lease invalidated before resurrection", `move._check=function() return {ok=false,error={code="lease_timeout",message="expired"}} end`,
			`r=act.recover{rest=false,allow_spirit_fallback=true};assert(not r.ok and r.error.code=="lease_timeout" and reclaims==0 and spirits==0)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actWorld(t, recoveryWorld+fastClock+tc.stubs, tc.body+`;assert(r.data.state=="done" or r.data.state=="attention" or r.data.state=="needs_repair");assert(r.id==nil and r.pending==nil)`)
		})
	}
}
func TestRecoveryReleaseFailureAndValidation(t *testing.T) {
	actWorld(t, recoveryWorld+`
  me.ghost=function() return false end
  death.release=function() return {ok=false,op="death.release",error={code="refused",message="release refused"}} end
 `, `
  r=act.recover();assert(not r.ok and r.error.code=="refused" and r.data.failed_step=="death.release")
  for _,o in ipairs({{timeout_s=0},{timeout_s=601},{max_attempts=3},{max_attempts=0},{allow_spirit_fallback="yes"}}) do
   local ok=pcall(act.recover,o);assert(not ok)
  end
 `)
}
func TestRecoveryCoordinatorJoiningCancellationAndExpiry(t *testing.T) {
	e := script.NewEngine(func(string, ...any) {}, nil)
	defer e.Close()
	var cancelled int
	current := time.Now()
	c := &recoveryCoordinator{reqs: e.Reqs, now: func() time.Time { return current }, cancelMovement: func(id int, reason string) { cancelled = id }}
	owner := c.begin(1, 10)
	id := owner["id"].(int)
	joined := c.begin(2, 1)
	if owner["owner"] != true || joined["owner"] != false || joined["id"] != id {
		t.Fatalf("owner=%v joined=%v", owner, joined)
	}
	c.abort(2, "task_stopped") // stopping a joiner must not cancel the owner's attempt
	if c.check(1, id)["ok"] != true {
		t.Fatal("joiner stopped recovery")
	}
	c.progress(1, 1)
	current = current.Add(10 * time.Second)
	c.poll()
	result := e.Reqs.Result(id)
	if result["ok"] != false || result["error"].(map[string]any)["code"] != "recovery_timeout" || cancelled != 1 || result["data"].(map[string]any)["attempts"] != 1 {
		t.Fatal(result, cancelled)
	}
	next := c.begin(3, 10)
	newID := next["id"].(int)
	c.finish(1, id, script.Ok(nil)) // old callback cannot complete the new attempt
	if c.check(3, newID)["ok"] != true {
		t.Fatal("old callback completed new recovery")
	}
	c.abort(3, "task_paused")
	if e.Reqs.Result(newID)["error"].(map[string]any)["code"] != "task_paused" {
		t.Fatal("pause not propagated")
	}
	last := c.begin(4, 10)
	c.close()
	if e.Reqs.Result(last["id"].(int))["error"].(map[string]any)["code"] != "session_closed" {
		t.Fatal("close not propagated")
	}
}

func recoveryEngine(t *testing.T) (*script.Engine, *recoveryCoordinator, *movementFixture) {
	t.Helper()
	e, f := leaseEngine(t)
	evalMovement(t, e, `alive=false;death={corpse=function() return {x=0,y=0,z=0,reclaim_in=0} end};spirits=0;death.spirit_res=function() spirits=spirits+1;return {ok=true} end`)
	c := installTestRecovery(t, e)
	c.cancelMovement = f.control.cancelTask
	return e, c, f
}
func TestRecoveryDuplicateHandlersJoinWithoutChangingPolicy(t *testing.T) {
	e, _, f := recoveryEngine(t)
	reclaims, reqID := 0, 0
	e.Func("death.reclaim", "() → Request", "test", func() map[string]any {
		reclaims++
		r := e.Reqs.Start(script.Spec{Op: "death.reclaim", Timeout: time.Minute})
		reqID = r["id"].(int)
		return r
	})
	evalMovement(t, e, `task.spawn("owner",function() owner_result=act.recover{rest=false,max_attempts=1} end)`)
	e.CallTick()
	if reclaims != 1 {
		t.Fatalf("reclaims=%d tasks=%+v", reclaims, e.TasksHeld())
	}
	evalMovement(t, e, `task.spawn("joiner",function() joined_result=act.recover{spirit=true,max_attempts=2} end)`)
	e.CallTick()
	e.Reqs.Resolve(reqID, script.Fail("refused", "reclaim refused", nil))
	e.CallTick()
	e.CallTick()
	evalMovement(t, e, `assert(not owner_result.ok and not joined_result.ok);assert(owner_result.error.code=="retry_exhausted" and joined_result.error.code=="retry_exhausted");assert(owner_result.data.attempts==1 and joined_result.data.attempts==1);assert(spirits==0)`)
	if reclaims != 1 {
		t.Fatalf("joiner dispatched another reclaim: %d", reclaims)
	}
	if o, _ := f.control.snapshot(); o != nil {
		t.Fatalf("recovery leaked ownership: %+v", o)
	}
}
func TestRecoveryOwnerLifecycleTerminatesJoiners(t *testing.T) {
	for _, operation := range []string{"stop", "pause"} {
		t.Run(operation, func(t *testing.T) {
			e, _, f := recoveryEngine(t)
			reqID := 0
			e.Func("death.reclaim", "() → Request", "test", func() map[string]any {
				r := e.Reqs.Start(script.Spec{Op: "death.reclaim", Timeout: time.Minute})
				reqID = r["id"].(int)
				return r
			})
			evalMovement(t, e, `task.spawn("owner",function() owner_result=act.recover{rest=false} end)`)
			e.CallTick()
			evalMovement(t, e, `task.spawn("joiner",function() joined_result=act.recover{rest=false} end)`)
			e.CallTick()
			evalMovement(t, e, `task.`+operation+`(1)`)
			e.CallTick()
			evalMovement(t, e, `assert(not joined_result.ok and joined_result.data.state=="attention")`)
			if o, _ := f.control.snapshot(); o != nil {
				t.Fatalf("inactive recovery retained ownership: %v", o)
			}
			if operation == "pause" {
				// A late server reply may resurrect the player; the paused helper must still fail.
				evalMovement(t, e, `alive=true;task.resume(1)`)
				e.Reqs.Resolve(reqID, script.Ok(nil))
				e.CallTick()
				evalMovement(t, e, `assert(not owner_result.ok and owner_result.error.code=="task_paused")`)
			}
		})
	}
}

func TestRecoveryRestRechecksEquipmentAndDanger(t *testing.T) {
	actWorld(t, recoveryWorld+fastClock+`
  me.health=function() return {current=clock<1000 and 50 or 100,max=100} end
  me.gear=function() return {weapon={broken=clock>=1000},broken=clock>=1000 and {"main_hand"} or {}} end
 `, `r=act.recover();assert(not r.ok and r.error.code=="needs_repair" and r.data.state=="needs_repair")`)
	actWorld(t, recoveryWorld+fastClock+`
  me.health=function() return {current=50,max=100} end
  units.nearby=function() if clock>=1000 then return {{alive=true,reaction="hostile",x=0,y=0,z=0}} end;return {} end
 `, `r=act.recover();assert(not r.ok and r.error.code=="unsafe_recovery" and clock==1000)`)
}

func TestRecoveryTransitionDiagnosticsIncludeTaskAndTermination(t *testing.T) {
	b := testBot()
	e := b.lua
	defer e.Close()
	b.setupActions()
	var evidence []types.Diagnostic
	e.SetDiagnosticSink("test", func(ev types.Diagnostic) { evidence = append(evidence, ev) })
	evalMovement(t, e, `task.spawn("recovery",function()
  task._action_begin("act.recover",{})
  death._recovery_begin(60)
  death._recovery_state("reclaiming",1)
  death._recovery_state("resting",1)
  wait(60)
 end)`)
	e.CallTick()
	evalMovement(t, e, `task.stop(1)`)
	sawProgress, sawGear, sawTermination := false, false, false
	for _, ev := range evidence {
		if ev.Kind == "recovery_transition" && ev.Details.State == "reclaiming" {
			sawProgress = ev.TaskID == 1 && ev.Call != "" && ev.Details.Attempts == 1
		}
		if ev.Kind == "gear_state" {
			sawGear = ev.TaskID == 1 && ev.Call != "" && ev.Details.Gear != nil
		}
		if ev.Kind == "recovery_transition" && ev.Details.State == "attention" {
			sawTermination = ev.TaskID == 1 && ev.Code == "task_stopped" && ev.Details.Attempts == 1
		}
	}
	if !sawProgress || !sawGear || !sawTermination {
		t.Fatalf("missing recovery evidence: %+v", evidence)
	}
}

func TestRecoveryEmptyCollectionsMatchResultContract(t *testing.T) {
	actWorld(t, actBase, `r=act.recover{rest=false};assert(r.ok and r.data.state=="done" and r.data.steps==nil)`)
	actWorld(t, actBase+`me.gear=function() return {weapon={broken=true}} end`,
		`r=act.recover{rest=false};assert(not r.ok and r.data.repair_options==nil and r.data.steps==nil)`)
}
