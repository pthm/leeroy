package session

import (
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/script"
)

const fastClock = `clock = 0; now = function() return clock end; wait = function(s) clock = clock + s * 1000 end; `

func TestActConsumeAndDestroyAwaitConfirmation(t *testing.T) {
	for _, helper := range []string{`act.consume("Food")`, `act.make_room()`} {
		t.Run(helper, func(t *testing.T) {
			e := script.NewEngine(func(string, ...any) {}, nil)
			if err := e.DoString(actBase + `
              items.list = function() return {{guid = "7", name = "Food", use_spell = 1, quality = "poor", sell_price = 1}} end
              spells.info = function() return {name = "Food"} end`); err != nil {
				t.Fatal(err)
			}
			for _, op := range []string{"items.use", "items.destroy"} {
				e.Func(op, "(guid) → Request", "", func(string) map[string]any {
					return e.Reqs.Start(script.Spec{Op: op, Timeout: time.Minute})
				})
			}
			if err := e.DoString(actLua); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Eval(`task.spawn("item", function() r = ` + helper + ` end)`); err != nil {
				t.Fatal(err)
			}
			e.CallTick()
			if v, _ := e.Eval(`return r`); v != nil {
				t.Fatalf("reported completion before server response: %v", v)
			}
			e.Reqs.Resolve(1, script.Fail("refused", "server refused", nil))
			e.CallTick()
			if _, err := e.Eval(`assert(not r.ok and r.error.code == "refused" and r.data.item.guid == "7" and r.id == nil)`); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestActMakeRoomProtectsGearAndUnknownItems(t *testing.T) {
	actWorld(t, actBase+`
      items.list = function() return {
        {guid = "unknown", quality = "poor", sell_price = 0},
        {guid = "upgrade", name = "Sword", slot = "main_hand", usable = true, quality = "poor", sell_price = 1},
        {guid = "uncompared", name = "Boots", slot = "feet", usable = true, quality = "poor", sell_price = 1},
        {guid = "junk", name = "Tooth", quality = "poor", sell_price = 2}} end
      items.compare = function(g) if g == "upgrade" then return {ok=true,data={replaces = {}, item_level = 1, armor = 0, dps = 0}} end return {ok=false,error={code="not_available",message="missing"}} end
      items.destroy = function(g) destroyed = g return {ok = true} end`, `
      r = act.make_room()
      assert(r.ok and destroyed == "junk" and r.data.item.guid == "junk")
      items.list = function() return {{name = "Unknown", quality = "poor"}} end
      r = act.make_room()
      assert(not r.ok and r.error.code == "no_disposable_item")`)
}

func TestActRestPostconditions(t *testing.T) {
	for _, tc := range []struct{ name, stubs, body string }{
		{"timeout", `me.health = function() return {current = 50, max = 100} end`, `
          r = act.rest{max_s = 2}
          assert(not r.ok and r.error.code == "rest_timeout" and r.data.hp == 0.5 and clock == 2000)`},
		{"mana required", `me.power = function() return {type = "mana", current = 10, max = 100} end`, `
          r = act.rest{hp = 0.5, mana = 0.9, max_s = 2}
          assert(not r.ok and r.data.mana == 0.1)`},
		{"threshold reached", `me.health = function() return {current = clock < 1000 and 50 or 100, max = 100} end`, `
          r = act.rest{max_s = 2}
          assert(r.ok and r.data.hp == 1)`},
		{"item refused", `me.health = function() return {current = 50, max = 100} end
          items.list = function() return {{guid = "food", use_spell = 1}} end
          spells.info = function() return {name = "Food"} end
          items.use = function() return {ok = false, error = {code = "not_ready", message = "refused"}} end`, `
          r = act.rest{max_s = 2}
          assert(not r.ok and r.error.code == "not_ready" and not r.steps[2].ok and clock == 0)`},
	} {
		t.Run(tc.name, func(t *testing.T) { actWorld(t, actBase+fastClock+tc.stubs, tc.body) })
	}
}

const objectWorld = actBase + fastClock + `
  q = {id = 42, complete = false, objectives = {{kind = "object", entry = 5, have = 0, need = 1}}}
  quests = {log = function() return q and {q} or {} end}
  uses = 0
  objects = {find = function() return {guid = "crate", distance = 2, x = 0, y = 0, z = 0} end,
    use = function() uses = uses + 1 return {ok = true} end}
`

func TestActObjectPostconditions(t *testing.T) {
	for _, tc := range []struct{ name, stubs, body string }{
		{"absent quest", `q = nil`, `r = act.use_object{entry = 5, quest_id = 42}
          assert(not r.ok and r.error.code == "not_in_log" and uses == 0)`},
		{"quest disappears", `objects.use = function() uses = uses + 1; q = nil; return {ok = true} end`, `
          r = act.use_object{entry = 5, quest_id = 42}
          assert(not r.ok and r.error.code == "not_in_log" and r.data.used == 1)`},
		{"no progress", ``, `r = act.use_object{entry = 5, quest_id = 42, max_attempts = 2}
          assert(not r.ok and r.error.code == "no_progress" and uses == 2 and r.data.used == 2)`},
		{"deadline", ``, `r = act.use_object{entry = 5, quest_id = 42, timeout_s = 0.5}
          assert(not r.ok and r.error.code == "timeout" and clock == 500 and uses == 1)`},
		{"progress", `objects.use = function() uses = uses + 1; q.objectives[1].have = 1; return {ok = true} end`, `
          r = act.use_object{entry = 5, quest_id = 42}
          assert(r.ok and r.data.status == "quest_progress" and uses == 1)`},
		{"already complete", `q.complete = true`, `r = act.use_object{entry = 5, quest_id = 42}
          assert(r.ok and r.data.status == "already_complete" and uses == 0)`},
		{"explicit count", ``, `r = act.use_object{entry = 5, count = 2}
          assert(r.ok and r.data.used == 2)`},
		{"partial failure", `objects.use = function() uses = uses + 1; return {ok = uses == 1,
          error = uses > 1 and {code = "locked", message = "locked"}} end`, `
          r = act.use_object{entry = 5, count = 2}
          assert(not r.ok and r.error.code == "locked" and r.data.used == 1 and r.data.attempts == 2)`},
	} {
		t.Run(tc.name, func(t *testing.T) { actWorld(t, objectWorld+tc.stubs, tc.body) })
	}
}

func TestActObjectMovementFailure(t *testing.T) {
	for _, tc := range []struct{ name, movement, code string }{
		{"wrong floor", `move.to = function() return {ok = true} end`, "unreachable"},
		{"movement deadline", `move.to = function() return {id = 999, op = "move.to", pending = true} end`, "wait_timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actWorld(t, actBase+tc.movement+`
              objects = {find = function() return {guid = "crate", x = 0, y = 0, z = 10, distance = 10} end,
                use = function() error("must not use an unreachable object") end}`,
				`r = act.use_object{entry = 5, timeout_s = 0.02}
                  assert(not r.ok and r.error.code == "`+tc.code+`" and r.data.used == 0)`)
		})
	}
}

func TestActLootRequiredSteps(t *testing.T) {
	const lootWorld = actBase + `
      mobs = {{guid = "a", alive = false, lootable = true, distance = 2},
              {guid = "b", alive = false, lootable = true, distance = 2}}
      loots = 0
      items.loot = function(g)
        loots = loots + 1
        if g == "a" then return {ok = true, data = {items = {{entry = 1}}, money = 7}} end
        return {ok = false, error = {code = "inventory_full", message = "full"}, data = {items = {}, money = 2}}
      end
    `
	actWorld(t, lootWorld, `r = act.loot_nearby()
      assert(not r.ok and r.error.code == "inventory_full" and r.data.money == 9 and #r.data.items == 1 and loots == 2)`)
	actWorld(t, lootWorld, `mobs[1].distance = 8
      act.approach = function() return act.fail("act.approach", "unreachable", "no path") end
      r = act.loot_nearby()
      assert(not r.ok and r.error.code == "unreachable" and loots == 0)`)
	actWorld(t, lootWorld, `rooms = 0
      act.make_room = function() rooms = rooms + 1 return act.ok("act.make_room", {}) end
      r = act.loot_nearby{make_room = true}
      assert(not r.ok and r.error.code == "inventory_full" and rooms == 1 and loots == 3)
      assert(r.data.money == 11 and #r.steps == 4)`)
}

func TestActPostconditionOptions(t *testing.T) {
	actWorld(t, actBase, `
      for _, args in ipairs({{hp = 2}, {mana = -1}, {max_s = 0}, {max_s = false}, {max_s = 0/0}, {hps = 0.9}}) do
        local ok = pcall(act.rest, args); assert(not ok)
      end
      for _, args in ipairs({{}, {entry = 5, quest = 42}, {entry = 5, count = 4}, {entry = 5, max_attempts = 33}, {entry = 5, timeout_s = false}}) do
        local ok = pcall(act.use_object, args); assert(not ok)
      end
      assert(not pcall(act.make_room, {force = true}))
      assert(not pcall(act.loot_nearby, {make_room = "true"}))
    `)
}
