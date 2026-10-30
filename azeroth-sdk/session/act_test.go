package session

import (
	"fmt"
	"strings"
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
	defer b.lua.Close()
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
	// The initial stall and digest are a batch. XP can arrive after that digest
	// was computed but before it is published; wait for the next nonempty one.
	seen := since
	for deadline := time.Now().Add(7 * time.Second); ; {
		evs := b.lua.Bus.Wait(script.Query{Since: seen, Types: []string{"progress"}}, time.Until(deadline))
		if len(evs) == 0 {
			t.Fatal("no progress digest containing XP")
		}
		found := false
		for _, ev := range evs {
			seen = ev.Seq
			if ev.Data.(types.Progress).XP == 100 {
				found = true
			}
		}
		if found {
			break
		}
	}
	if evs := wait("stall", since); len(evs) == 0 {
		t.Fatal("no second stall after progress resumed and stopped again")
	}
}

// actWorld: act.lua over a stubbed game (each stub returns a finished result, so await returns at once), running
// body as a task until it sets done.
func actWorld(t *testing.T, stubs, body string) *script.Engine {
	t.Helper()
	e := script.NewEngine(func(string, ...any) {}, nil)
	if err := e.DoString(stubs); err != nil {
		t.Fatal(err)
	}
	installTestRecovery(t, e)
	if err := e.DoString(actLua); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Eval(`task.spawn("t", function() ` + body + ` done = true end)`); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		e.CallTick()
		if v, _ := e.Eval(`return done`); v == true {
			return e
		}
	}
	tasks, _ := e.Eval(`return task.list and task.list() or nil`)
	t.Fatalf("script didn't finish: %v", tasks)
	return e
}

const actBase = `
  log = function() end
  pos = {x = 0, y = 0, z = 0}
  me = {guid = function() return "me" end, position = function() return pos end, alive = function() return alive end,
    ghost = function() return not alive end, in_combat = function() return false end, casting = function() return nil end,
    health = function() return {current = 100, max = 100} end, power = function() return {type = "mana", current = 100, max = 100} end}
  alive = true
  me.gear = function() return {broken={}} end
  me.money = function() return 100 end
  world = {ground_z = function(x, y, z) return z or 0 end, path = function() return {ok=true,op="world.path",data={reachable=true,gap=0,distance=6}} end, in_sight = function() return true end}
  world.danger = function(x,y,z,r)
    local d={state="clear",hostiles={}}
    if z==nil or world.ground_z(x,y,z)==nil or math.abs(world.ground_z(x,y,z)-z)>3 then d.state="unknown" end
    for _,u in ipairs(units.nearby(80)) do
      if u.alive and u.reaction=="hostile" and math.sqrt((u.x-x)^2+(u.y-y)^2)<=r then d.hostiles[#d.hostiles+1]=u;d.state="hostile" end
    end
    return {ok=true,data=d}
  end
  world.route = function(points, opts) return {ok=true,op="world.route",data={status="verified",from=pos,completed_legs=#points,waypoints=points}} end
  moves = {}
  move = {to = function(x, y, z) moves[#moves + 1] = {x = x, y = y, z = z} pos = {x = x, y = y, z = z or 0} return {ok = true} end,
    stop = function() return {ok = true} end, face = function() faced = (faced or 0) + 1 return {ok = true} end}
  move._acquire = function() return {ok=true, data={lease_id=1}} end
  move._release = function() return {ok=true} end
  move._check = function() return {ok=true} end
  mobs = {}
  units = {nearby = function() return mobs end, attackers = function() return {} end, get = function() return nil end}
  spells = {check = function() return {usable=true} end, usable = function() return true end, known = function() return true end, info = function() return {range = 30} end}
  items = {list = function() return {} end}
`

// act.cast faces the target and casts again once when the first cast fails for facing.
func TestActCastRefaces(t *testing.T) {
	e := actWorld(t, actBase+`
  casts = 0
  spells.cast = function() casts = casts + 1
    if casts == 1 then return {ok = false, error = {code = "unit_not_infront", message = "face it"}} end
    return {ok = true} end`, `r = act.cast(133, "mob")`)
	if v, _ := e.Eval(`return r.ok, casts, faced`); fmt.Sprint(v) != "[true 2 2]" {
		t.Fatalf("ok, casts, faces = %v, want a second cast after facing again", v)
	}
}

// act.go_to with on_attacked stops for whoever attacks you on the way, hands them over, then carries on.
func TestActGoToOnAttacked(t *testing.T) {
	e := actWorld(t, actBase+`
  attacked = true
  move.to = function(x, y, z) moves[#moves + 1] = {x = x, y = y}
    if attacked then return {id = 424242, pending = true} end -- still walking
    pos = {x = x, y = y, z = 0} return {ok = true} end
  units.attackers = function() if attacked then return {{name = "Kobold Tunneler"}} end return {} end`,
		`r = act.go_to{x = 100, y = 0, on_attacked = function(foes) fought = foes[1].name attacked = false end}`)
	if v, _ := e.Eval(`return r.ok, fought, #moves`); fmt.Sprint(v) != "[true Kobold Tunneler 2]" {
		t.Fatalf("ok, fought, moves = %v", v)
	}
}

// A corpse run with a hostile mob by the corpse reclaims from the far side of the reclaim circle.
func TestActCorpseRunSafeSpot(t *testing.T) {
	e := actWorld(t, actBase+`
  alive = false
  mobs = {{name = "Kobold Miner", level = 6, alive = true, reaction = "hostile", attackable = true, x = 10, y = 0}}
  death = {corpse = function() return {ok=true,data={x = 0, y = 0, z = 0, reclaim_in = 0}} end,
    reclaim = function() reclaimed_at = {x = pos.x, y = pos.y} alive = true return {ok = true} end}`,
		`r = act.corpse_run()`)
	if v, _ := e.Eval(`return r.ok, math.floor(reclaimed_at.x + 0.5), math.floor(reclaimed_at.y + 0.5)`); fmt.Sprint(v) != "[true -32 0]" {
		t.Fatalf("ok, reclaimed at = %v, want 32 yd on the side away from the mob", v)
	}
	// safe = false: on the spot
	e = actWorld(t, actBase+`
  alive = false
  mobs = {{name = "Kobold Miner", level = 6, alive = true, reaction = "hostile", attackable = true, x = 10, y = 0}}
  death = {corpse = function() return {ok=true,data={x = 0, y = 0, z = 0, reclaim_in = 0}} end,
    reclaim = function() reclaimed_at = {x = pos.x, y = pos.y} alive = true return {ok = true} end}`,
		`r = act.corpse_run{safe = false}`)
	if v, _ := e.Eval(`return reclaimed_at.x, reclaimed_at.y`); fmt.Sprint(v) != "[0 0]" {
		t.Fatalf("safe = false reclaimed at %v", v)
	}
}

// Explicit spirit permission still refuses a camped healer site.
func TestActRecoverRefusesCampedGraveyard(t *testing.T) {
	e := actWorld(t, actBase+`
  alive = false
  mobs = {{name = "Gray Forest Wolf", level = 8, alive = true, reaction = "hostile", attackable = true, x = 5, y = 5}}
  mobs[#mobs+1] = {guid="9",x=0,y=0,z=0,distance=0,roles={"spirit_healer"}}
  death = {corpse = function() return {ok=true,data={x = 0, y = 0, z = 0, reclaim_in = 0}} end,
    reclaim = function() alive = true return {ok = true} end,
    spirit_res = function() spirit = true alive = true return {ok = true} end}`,
		`mobs_at_corpse = false r = act.recover{spirit = true, rest = false}`)
	if v, _ := e.Eval(`return r.ok, spirit == true`); fmt.Sprint(v) != "[false false]" {
		t.Fatalf("ok, used the spirit healer = %v, want refusal", v)
	}
}

// An act helper that takes an options table says so when it gets something else (act.loot_nearby(10)).
func TestActOptionsTable(t *testing.T) {
	e := actWorld(t, actBase, `ok, err = pcall(act.loot_nearby, 10)`)
	if v, _ := e.Eval(`return ok, err`); !strings.Contains(fmt.Sprint(v), "act.loot_nearby takes an options table, act.loot_nearby{...}, not a number") {
		t.Fatalf("pcall(act.loot_nearby, 10) = %v", v)
	}
}

func TestActResultEnvelopes(t *testing.T) {
	for _, tc := range []struct{ name, stubs, body string }{
		{"cast", `raw = {id = 17, op = "spells.cast", ok = true, data = {spell = 133}}
          spells.cast = function() return raw end`, `r = act.cast(133, "me")
          assert(raw.op == "spells.cast" and raw.id == 17)
          assert(r.op == "act.cast" and r.ok and r.data.action.spell == 133 and raw.data.spell == 133)`},
		{"buff active", `units.has_aura = function() return true end`, `r = act.buff(133)
          assert(r.op == "act.buff" and r.ok and r.data.status == "already_active")`},
		{"buff missing spell", ``, `r = act.buff(nil)
          assert(r.op == "act.buff" and not r.ok and r.error.code == "not_known")`},
		{"release failure", `raw = {id = 18, op = "death.release", ok = false,
            data = {attempt = 1}, error = {code = "timeout", message = "not released", next = "inspect state"}}
          death = {release = function() return raw end}`, `r = act.release()
          assert(raw.op == "death.release")
          assert(r.op == "act.release" and not r.ok and r.data.attempt == 1)
          assert(r.error.code == "timeout" and r.error.next == "inspect state")`},
		{"constructors", ``, `r = act.ok("mine", {count = 1})
          assert(r.op == "mine" and act.must(r).data.count == 1)
          r = act.fail("mine", "blocked", "cannot proceed")
          local ok, err = pcall(act.must, r)
          assert(not ok and tostring(err):find("mine: blocked: cannot proceed"))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actWorld(t, actBase+tc.stubs, tc.body+` assert(r.id == nil and r.pending == nil)`)
		})
	}
}
