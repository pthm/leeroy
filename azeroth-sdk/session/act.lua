-- act: utilities built into every session, so scripts spend their effort on strategy, not on the plumbing of
-- talking to the game. There is deliberately no combat AI here: no rotations, target picking, grinding loops, assist
-- or healing logic. How to fight, what to kill, when to rest and how to work with others is yours to build.
--
-- The doing functions take one options table, wait until they're finished (so call them inside a task), and return
-- a result like an action's: {ok = true, op = "act.go_to", data = ...} or {ok = false, op, error = {code, message}}.
-- act.must(r) raises the error instead. The helpers at the top don't wait.
--
--   task.spawn("quest 62", function()
--     act.must(act.accept{ npc_entry = 240, quest = 62 })
--     ... your fighting ...
--     act.must(act.turn_in{ npc_entry = 240, quest = 62 })
--   end)
--
-- Everything here uses the public API; read session/act.lua to see or copy how it works.

act = {}
local A = act

local function result(op, ok, data, code, message)
  if ok then return { ok = true, op = op, data = data } end
  return { ok = false, op = op, data = data, error = { code = code, message = message } }
end
function A.ok(op, data) return result(op, true, data) end
function A.fail(op, code, message) return result(op, false, nil, code, message) end
-- Copy the envelope: a helper must not rename or leak a raw request's result.
local function wrapped(op, r)
  if type(r) ~= "table" or type(r.ok) ~= "boolean" or (not r.ok and not r.error) then
    error(op .. ": expected a completed action result", 2)
  end
  return {op = op, ok = r.ok, data = r.data, error = r.error, steps = r.steps}
end
function A.must(r)
  if r and r.ok == false then error((r.op or "act") .. ": " .. r.error.code .. ": " .. tostring(r.error.message), 2) end
  return r
end

-- A scope retains its lease across waits and nested helpers. Go owns cancellation
-- and expiry, since a stopped coroutine cannot run a Lua finally block.
local function movement_scope(priority, queue_s, max_s, fn, ...)
  local acquired = await(move._acquire(priority, queue_s, max_s), queue_s + 2)
  if not acquired.ok then return acquired end
  local ok, r = pcall(fn, ...)
  local released = move._release(acquired.data.lease_id)
  if not ok then error(r, 0) end
  if not released.ok then return released end
  return r
end
move = move or {}
function move.with_control(o)
  if not task.current() then error("move.with_control requires a task", 2) end
  if type(o) ~= "table" then error("move.with_control requires an options table", 2) end
  for k in pairs(o) do
    if k ~= "priority" and k ~= "queue_timeout_s" and k ~= "max_s" and k ~= "fn" then error("move.with_control: unknown option " .. tostring(k), 2) end
  end
  if type(o.fn) ~= "function" then error("move.with_control: fn must return a completed action result", 2) end
  local priority, queue_s, max_s = o.priority or "normal", o.queue_timeout_s or 5, o.max_s or 600
  if priority ~= "normal" and priority ~= "urgent" and priority ~= "recovery" then error("move.with_control: invalid priority", 2) end
  if type(queue_s) ~= "number" or not (queue_s >= 0 and queue_s <= 60) then error("move.with_control: queue_timeout_s must be 0..60", 2) end
  if type(max_s) ~= "number" or not (max_s > 0 and max_s <= 600) then error("move.with_control: max_s must be >0 and <=600", 2) end
  local call = task._action_begin("move.with_control", o)
  local ok, r = pcall(movement_scope, priority, queue_s, max_s, function() return wrapped("move.with_control", o.fn()) end)
  if not ok then
    task._action_end(call, A.fail("move.with_control", "script_error", "callback raised a Lua error"))
    error(r, 0)
  end
  r = wrapped("move.with_control", r)
  task._action_end(call, r)
  return r
end
doc("move.with_control", "{fn [, priority = normal, queue_timeout_s = 5, max_s = 600]} → ActResult",
  "run fn() under a movement lease; fn must return a completed action result. Priorities recovery > urgent > normal; queue_timeout_s is 0..60, max_s is >0..600. Nested scopes share the outer deadline; higher nested priority promotes the lease. Preemption/expiry cancels movement and makes the scope fail even if fn later returns success; it does not interrupt arbitrary callback code")

-- ---------- helpers (don't wait) ----------
function A.dist(x, y, z)
  local p = me.position()
  return math.sqrt((p.x - x) ^ 2 + (p.y - y) ^ 2 + ((z and (p.z - z)) or 0) ^ 2)
end
local function frac(r) return (r.max and r.max > 0) and r.current / r.max or 1 end
function A.hp() return frac(me.health()) end
function A.mana() local p = me.power() return p.type == "mana" and frac(p) or 1 end
function A.ready(id, guid) return id ~= nil and me.casting() == nil and spells.usable(id, guid) end
function A.in_range(id, guid)
  local sp, u = spells.info(id), units.get(guid)
  return sp ~= nil and u ~= nil and u.distance <= math.max(sp.range or 5, 5)
end
function A.npc(entry, dist)
  for _, u in ipairs(units.nearby(dist or 60)) do -- nearest first
    if u.entry == entry and u.alive then return u end
  end
end
function A.player(name)
  for _, p in ipairs(units.players(100)) do if p.name == name then return p end end
  local g = group.info()
  for _, m in ipairs(g and g.members or {}) do if m.name == name then return units.get(m.guid) end end
end
local function consume(kind, timeout_s)
  for _, it in ipairs(items.list()) do
    if it.use_spell and not it.equipped then
      local sp = spells.info(it.use_spell)
      if sp and sp.name == kind then
        local r = wrapped("act.consume", await(items.use(it.guid), timeout_s))
        r.data = {item = it, action = r.data}
        return r
      end
    end
  end
  return A.fail("act.consume", "no_consumable", "no " .. tostring(kind) .. " item in your bags")
end
function A.consume(kind) return consume(kind, 30) end
local function disposable(it)
  -- Missing template information is not evidence that an item is junk.
  if it.equipped or it.quality ~= "poor" or not it.name or it.name == "" or it.sell_price == nil then return false end
  if it.slot and it.slot ~= "" and it.usable ~= false then
    local c = items.compare(it.guid)
    if not c or not c.replaces then return false end
    if c.item_level > 0 or c.armor > 0 or c.dps > 0 then return false end
    for _, v in pairs(c.stats or {}) do if v > 0 then return false end end
  end
  return true
end
function A.make_room(o)
  local cheapest
  for _, it in ipairs(items.list()) do
    if disposable(it) and (not cheapest or it.sell_price < cheapest.sell_price) then cheapest = it end
  end
  if not cheapest then return A.fail("act.make_room", "no_disposable_item", "no loaded poor-quality item can be discarded without losing a possible gear upgrade") end
  local r = wrapped("act.make_room", await(items.destroy(cheapest.guid), 5))
  r.data = {item = cheapest, action = r.data}
  return r
end
-- face: turn to a unit, waiting for the turn in a task (a cast sent mid-turn fails with unit_not_infront)
local function face(guid)
  local f = move.face(guid)
  if task.current() then return await(f, 2) end
  return f
end
-- cast(id [, guid]): face the target and cast if ready; in a task it waits for the result
function A.cast(id, guid)
  guid = guid or me.guid()
  if not A.ready(id, guid) then
    if task.current() then wait(0.2) end -- a loop that retries can't spin past the execution limit
    return result("act.cast", false, nil, "not_ready", "on cooldown, casting, not enough power, or its condition isn't met (spells.usable)")
  end
  if guid == me.guid() then return await(spells.cast(id, guid)) end
  local faced = face(guid)
  if not faced.ok then return faced end
  local r = await(spells.cast(id, guid))
  if task.current() and r.ok == false and r.error and r.error.code == "unit_not_infront" then -- it moved: once more
    faced = face(guid)
    if not faced.ok then return faced end
    r = await(spells.cast(id, guid))
  end
  return r
end
function A.buff(id, guid)
  guid = guid or me.guid()
  if not id then return A.fail("act.buff", "not_known", "a spell ID is required") end
  if units.has_aura(guid, id) then return A.ok("act.buff", {status = "already_active"}) end
  return A.cast(id, guid)
end

-- ---------- movement ----------
-- sidestep: a few yards to the side of the way you're heading, with a jump (what gets a stuck character free)
local function sidestep(o, n)
  local p = me.position()
  local dx, dy = o.x - p.x, o.y - p.y
  local len = math.sqrt(dx * dx + dy * dy)
  if len < 0.1 then return end
  local side = (n % 2 == 0) and 1 or -1
  local sx, sy = p.x - dy / len * 5 * side, p.y + dx / len * 5 * side
  move.to(sx, sy, world.ground_z(sx, sy, p.z) or p.z)
  move.jump()
  wait(1.5)
end

-- walk: move.to and wait; with o.on_attacked, stop whenever something targets you on the way, call
-- o.on_attacked(attackers) (your fighting), then carry on
local function walk(o, x, y, z)
  local req = move.to(x, y, z)
  if not o.on_attacked then return await(req, 600) end
  local deadline = now() + 600000
  while now() < deadline do
    local r = await(req, 0.5)
    if not (r.error and r.error.code == "wait_timeout") then return r end
    local foes = units.attackers(30)
    if #foes > 0 then
      move.stop()
      o.on_attacked(foes)
      req = move.to(x, y, z)
    end
  end
  return await(req, 1)
end

function A.go_to(o)
  local tol = o.tolerance or 4
  -- no z given: the floor nearest your own height when it's close by (in a cave or a building, not the ground above)
  local hint = o.z or (A.dist(o.x, o.y) < 80 and me.position().z or nil)
  local z = (hint and world.ground_z(o.x, o.y, hint)) or world.ground_z(o.x, o.y) or o.z or me.position().z
  local r
  for n = 1, o.tries or 3 do
    if A.dist(o.x, o.y) <= tol then move.stop() return result("act.go_to", true, { distance = A.dist(o.x, o.y) }) end
    r = walk(o, o.x, o.y, z)
    if not r.ok and (r.error.code == "preempted" or r.error.code == "lease_timeout" or r.error.code == "cancelled" or r.error.code == "task_paused" or r.error.code == "movement_busy" or r.error.code == "replaced_by_owner") then return r end
    if A.dist(o.x, o.y) <= tol then return result("act.go_to", true, { distance = A.dist(o.x, o.y) }) end
    if r.ok and r.data and r.data.stopped then return result("act.go_to", false, nil, "stopped", "move.stop() was called on the way") end
    if not r.ok and r.error.code == "superseded" then return result("act.go_to", false, nil, "superseded", r.error.message) end
    if not r.ok and (r.error.code == "in_flight" or r.error.code == "teleporting") then return result("act.go_to", false, nil, r.error.code, r.error.message) end
    if not r.ok and r.error.code == "stuck" then sidestep(o, n) else wait(1) end
  end
  -- the failsafe: still stuck after every try -> the client's "Character stuck" (hearths you home)
  if not r.ok and r.error.code == "stuck" and o.unstuck == true and not me.in_combat() then
    log("act.go_to: stuck at " .. math.floor(me.position().x) .. "," .. math.floor(me.position().y) .. " heading for " ..
      math.floor(o.x) .. "," .. math.floor(o.y) .. ": using the Stuck spell (hearth home)")
    local u = await(move.unstuck(), 30)
    if u.ok then return result("act.go_to", false, u.data, "unstuck_home", "stuck: the Stuck spell took you home (me.home()); try another route") end
  end
  return result("act.go_to", false, r and r.data, r and r.error and r.error.code or "stuck", r and r.error and r.error.message)
end
function A.travel(o)
  for i, p in ipairs(o.points) do
    local r = A.go_to{ x = p[1] or p.x, y = p[2] or p.y, z = p[3] or p.z, tolerance = o.tolerance or 6, unstuck = o.unstuck,
      on_attacked = o.on_attacked }
    if not r.ok then return result("act.travel", false, { waypoint = i }, r.error.code, "waypoint " .. i .. ": " .. tostring(r.error.message)) end
  end
  return result("act.travel", true)
end
function A.approach(o)
  local range, deadline = o.range or 4, now() + (o.timeout or 60) * 1000
  local req, dest
  while now() < deadline do
    local u = units.get(o.unit)
    if not u then return A.fail("act.approach", "unit_gone", "the unit is no longer visible") end
    if u.distance <= range and (u.in_sight ~= false or u.distance <= 3) then
      local stopped = await(move.stop(), 2)
      if not stopped.ok then return stopped end
      local faced = await(move.face(o.unit), 2)
      if not faced.ok then return faced end
      return A.ok("act.approach", u)
    end
    if not req or ((u.x-dest.x)^2 + (u.y-dest.y)^2 + (u.z-dest.z)^2 > 9) then
      req, dest = move.to(u.x, u.y, u.z), u
    end
    local r = await(req, math.min(0.5, (deadline-now())/1000))
    if not r.ok and r.error.code ~= "wait_timeout" then return r end
    if r.ok then
      local current = units.get(o.unit)
      if current and current.distance > range and current.x == dest.x and current.y == dest.y and current.z == dest.z then
        return A.fail("act.approach", "unreachable", "walk ended outside the requested range")
      end
      req = nil
      wait(0.1)
    end
  end
  return A.fail("act.approach", "timeout", "couldn't get within " .. range .. " yd")
end
function A.follow(o)
  local deadline = now() + (o.timeout_s or 600) * 1000
  local missing, req, dest
  while now() < deadline do
    if o["until"] and o["until"]() then return A.ok("act.follow") end
    local p = A.player(o.name)
    if not p then
      missing = missing or now()
      if now() - missing >= 5000 then return A.fail("act.follow", "player_not_visible", "player has not been visible for 5 seconds") end
    else
      missing = nil
      if p.distance <= (o.distance or 5) + 2 then
        local r = await(move.stop(), 2); if not r.ok then return r end
        req = nil
      elseif not req or ((p.x-dest.x)^2 + (p.y-dest.y)^2 + (p.z-dest.z)^2 > 9) then
        req, dest = move.to(p.x, p.y, p.z), p
      end
    end
    if req then
      local r = await(req, math.min(0.5, (deadline-now())/1000))
      if not r.ok and r.error.code ~= "wait_timeout" then return r end
      if r.ok then req = nil; wait(0.1) end
    else wait(0.2) end
  end
  return A.fail("act.follow", "timeout", "follow deadline expired")
end

-- ---------- NPCs and objects ----------
function A.reach_npc(o)
  local function find_npc()
    if o.npc_guid then
      local u = units.get(o.npc_guid)
      return u and u.alive and u or nil
    end
    return A.npc(o.npc_entry)
  end
  local npc = find_npc()
  if not npc and o.x and A.dist(o.x, o.y) > 8 then
    local r = A.go_to{ x = o.x, y = o.y, z = o.z }
    if not r.ok then return result("act.reach_npc", false, nil, r.error.code, r.error.message) end
    npc = find_npc()
  end
  if not npc then return result("act.reach_npc", false, nil, "npc_not_found", "npc " .. tostring(o.npc_guid or o.npc_entry) .. " not found nearby (pass its x, y, z from azworld)") end
  if npc.distance > 4 then
    local r = A.go_to{ x = npc.x, y = npc.y, z = npc.z, tolerance = 3 }
    if not r.ok then return result("act.reach_npc", false, nil, r.error.code, r.error.message) end
  end
  move.stop()
  return result("act.reach_npc", true, units.get(npc.guid) or npc)
end
local function at_npc(op, o, fn)
  local r = A.reach_npc{npc_entry = o.npc_entry, npc_guid = o.npc_guid, x = o.x, y = o.y, z = o.z}
  if not r.ok then return wrapped(op, r) end
  local out = fn(r.data)
  return wrapped(op, out)
end
local function in_log(id) for _, q in ipairs(quests.log()) do if q.id == id then return q end end end
function A.accept(o)
  if in_log(o.quest) then return result("act.accept", true, { quest = o.quest }) end
  return at_npc("act.accept", o, function(npc) return await(quests.accept(npc.guid, o.quest)) end)
end
function A.turn_in(o)
  if not in_log(o.quest) then return result("act.turn_in", false, nil, "not_in_log", "quest " .. o.quest .. " isn't in your quest log") end
  return at_npc("act.turn_in", o, function(npc) return await(quests.turn_in(npc.guid, o.quest, o.reward or 0)) end)
end
local function has_role(npc, ...)
  for _, role in ipairs(npc.roles or {}) do
    for _, want in ipairs({ ... }) do if role == want then return true end end
  end
end
-- Every requested step is required. Preserve its error and previously completed work.
local function step(steps, op, r)
  steps[#steps + 1] = {op = op, ok = r.ok, request_id = r.id, code = r.error and r.error.code}
end
local function composite(op, data, steps, failure)
  return {op = op, ok = failure == nil, data = data, steps = steps, error = failure and failure.error}
end
function A.vendor(o)
  return at_npc("act.vendor", o, function(npc)
    local buy = o.buy or {}
    if ((o.sell_junk or #buy > 0) and not has_role(npc, "vendor")) or
       (o.repair and not has_role(npc, "repair")) then
      return A.fail("act.vendor", "wrong_npc", tostring(npc.name) .. " lacks a requested vendor or repair role")
    end
    local out, steps = {bought = {}, completed = {}, remaining = {}}, {}
    local plan = {}
    if o.sell_junk then plan[#plan + 1] = {op = "vendor.sell_junk"} end
    if o.repair then plan[#plan + 1] = {op = "vendor.repair"} end
    if #buy > 0 then plan[#plan + 1] = {op = "vendor.items"} end
    for _, b in ipairs(buy) do plan[#plan + 1] = {op = "vendor.buy", item_entry = b[1], count = b[2] or 1} end
    for i, action in ipairs(plan) do
      local r
      if action.op == "vendor.buy" then r = await(vendor.buy(npc.guid, action.item_entry, action.count))
      elseif action.op == "vendor.items" then r = await(vendor.items(npc.guid))
      elseif action.op == "vendor.sell_junk" then r = await(vendor.sell_junk(npc.guid))
      else r = await(vendor.repair(npc.guid)) end
      step(steps, action.op, r)
      if action.op == "vendor.sell_junk" then out.sold = r.data
      elseif action.op == "vendor.repair" then out.repaired = r.data
      elseif action.op == "vendor.buy" and r.ok then out.bought[#out.bought + 1] = r.data end
      if not r.ok then
        for j = i, #plan do out.remaining[#out.remaining + 1] = plan[j] end
        return composite("act.vendor", out, steps, r)
      end
      out.completed[#out.completed + 1] = action
    end
    return composite("act.vendor", out, steps)
  end)
end
function A.train(o)
  -- Snapshot known spells before any learning; do not claim later successes were known at entry.
  local known = {}
  for _, sp in ipairs(o.spells) do known[sp] = spells.known(sp) end
  return at_npc("act.train", o, function(npc)
    if not has_role(npc, "trainer") then return A.fail("act.train", "wrong_npc", "the NPC is not a trainer") end
    local out, steps, failure = {learned = {}, already_known = {}, failed = {}, remaining = {}}, {}, nil
    for _, sp in ipairs(o.spells) do
      if known[sp] then out.already_known[#out.already_known + 1] = sp end
    end
    for _, sp in ipairs(o.spells) do
      if not known[sp] then
        if failure and not o.continue_on_error then out.remaining[#out.remaining + 1] = sp
        else
          local r = await(trainer.learn(npc.guid, sp))
          step(steps, "trainer.learn", r)
          if r.ok then out.learned[#out.learned + 1] = sp
          else out.failed[#out.failed + 1] = sp; failure = failure or r end
        end
      end
    end
    return composite("act.train", out, steps, failure)
  end)
end
function A.talk(o) return at_npc("act.talk", o, function(npc) return o.fn(npc) end) end
function A.use_object(o)
  local used, attempts, deadline, steps = 0, 0, now() + (o.timeout_s or 60) * 1000, {}
  local initial = o.quest_id and in_log(o.quest_id)
  local before = {}
  if o.quest_id and not initial then return result("act.use_object", false, {used = 0, attempts = 0}, "not_in_log", "the requested quest is not in your log") end
  for _, obj in ipairs(initial and initial.objectives or {}) do
    before[obj.kind .. ":" .. obj.entry] = {have = obj.have, done = obj.done}
  end
  local function finish(failure, status)
    return composite("act.use_object", {used = used, attempts = attempts, status = status}, steps, failure)
  end
  local function failed(code, message) return finish(A.fail("act.use_object", code, message)) end
  local function left() return math.max(0, (deadline - now()) / 1000) end
  local function walk_to(x, y, z)
    if left() <= 0 then return A.fail("act.use_object", "timeout", "object-use deadline reached") end
    local r = await(move.to(x, y, z), left())
    step(steps, "move.to", r)
    if not r.ok then move.stop() return r end
    if A.dist(x, y, z) > 5 then return A.fail("act.use_object", "unreachable", "movement ended outside object interaction range") end
    return r
  end
  while true do
    if o.quest_id then
      local q = in_log(o.quest_id)
      if not q then return failed("not_in_log", "quest disappeared before progress was verified") end
      if q.failed then return failed("quest_failed", "the requested quest failed") end
      if q.complete then return finish(nil, used == 0 and "already_complete" or "quest_complete") end
      for _, obj in ipairs(q.objectives or {}) do
        local prior = before[obj.kind .. ":" .. obj.entry]
        if prior and ((obj.have or 0) > (prior.have or 0) or (obj.done and not prior.done)) then
          return finish(nil, "quest_progress")
        end
      end
    elseif used >= (o.count or 1) then return finish(nil, "count_reached") end
    if left() <= 0 then return failed("timeout", "object-use deadline reached") end
    if attempts >= (o.max_attempts or 3) then return failed("no_progress", "attempt limit reached before the requested postcondition") end
    local ob = objects.find(o.entry, o.radius or 80)
    if not ob then
      if o.x and A.dist(o.x, o.y) > 5 then
        local r = walk_to(o.x, o.y, o.z or me.position().z)
        if not r.ok then return finish(r) end
        ob = objects.find(o.entry, o.radius or 80)
      end
      if not ob then return failed("not_found", "no object " .. o.entry .. " in range") end
    end
    if ob.distance > 4 then
      local r = walk_to(ob.x, ob.y, ob.z)
      if not r.ok then return finish(r) end
    end
    local stopped = await(move.stop(), left())
    step(steps, "move.stop", stopped)
    if not stopped.ok then return finish(stopped) end
    if left() <= 0 then return failed("timeout", "object-use deadline reached") end
    attempts = attempts + 1
    local r = await(objects.use(ob.guid), left())
    step(steps, "objects.use", r)
    if not r.ok then return finish(r) end
    used = used + 1
    if o.quest_id then wait(math.min(1, left())) end -- allow the quest update to arrive
  end
end
-- Every approach and loot operation is checked. Destruction requires the caller's explicit opt-in.
function A.loot_nearby(o)
  o = o or {}
  local got, steps, corpses = {items = {}, money = 0}, {}, 0
  local function finish(r) return composite("act.loot_nearby", got, steps, r) end
  local function collect(r)
    if r.data then
      for _, it in ipairs(r.data.items or {}) do got.items[#got.items + 1] = it end
      got.money = got.money + (r.data.money or 0)
    end
  end
  for _, u in ipairs(units.nearby(o.dist or 8)) do
    if not u.alive and u.lootable then
      if corpses >= 32 then return finish(A.fail("act.loot_nearby", "limit_reached", "looted 32 corpses; call again for the remainder")) end
      corpses = corpses + 1
      if u.distance > 4 then
        local r = A.approach{ unit = u.guid, range = 4, timeout = 10 }
        step(steps, "act.approach", r)
        if not r.ok then return finish(r) end
      end
      local r = await(items.loot(u.guid))
      step(steps, "items.loot", r)
      collect(r)
      if not r.ok and (r.error.code == "inventory_full" or r.error.code == "bags_full") and o.make_room == true then
        local room = A.make_room()
        step(steps, "act.make_room", room)
        if not room.ok then return finish(room) end
        r = await(items.loot(u.guid))
        step(steps, "items.loot", r)
        collect(r)
      end
      if not r.ok then return finish(r) end
    end
  end
  return finish()
end

-- fly{to = node id or name [, npc_entry or npc_guid]}: walk to the nearest flight master (or that one), open its map, fly
-- to the destination and wait until you land
function A.fly(o)
  local master
  for _, u in ipairs(units.nearby(80)) do
    for _, role in ipairs(u.roles or {}) do
      if role == "flight_master" and u.alive and (not o.npc_entry or u.entry == o.npc_entry) and (not o.npc_guid or u.guid == o.npc_guid) then master = master or u end
    end
  end
  if not master then return result("act.fly", false, nil, "no_flight_master", "no flight master within 80 yd (azworld near x y --kind flightmaster)") end
  if master.distance > 4 then
    local r = A.approach{ unit = master.guid, range = 4 }
    if not r.ok then return wrapped("act.fly", r) end
  end
  local m = await(flights.map(master.guid))
  if not m.ok then return wrapped("act.fly", m) end
  local dest
  for _, n in ipairs(m.data.destinations) do
    if n.id == o.to or (type(o.to) == "string" and n.name:lower():find(o.to:lower(), 1, true)) then dest = dest or n end
  end
  if not dest then return result("act.fly", false, m.data, "unknown_destination", "not a known destination from here: " .. tostring(o.to)) end
  local r = await(flights.take(master.guid, dest.id), 1800)
  r = wrapped("act.fly", r)
  return r
end

-- ---------- recovery ----------
local recovery_danger
function A.rest(o)
  o = o or {}
  local deadline, attempted, steps = now() + (o.max_s or 40) * 1000, {}, {}
  local function finish(r)
    return composite("act.rest", {hp = A.hp(), mana = A.mana()}, steps, r)
  end
  local stopped = await(move.stop(), math.min(o.max_s or 40, 5))
  step(steps, "move.stop", stopped)
  if not stopped.ok then return finish(stopped) end
  while A.hp() < (o.hp or 0.95) or A.mana() < (o.mana or 0.9) do
    if not me.alive() or me.ghost() then return finish(A.fail("act.rest", "not_alive", "cannot rest while dead")) end
    if o.safe then local control=move._check(); if not control.ok then return finish(control) end end
    if o.safe and recovery_danger(me.position()).state ~= "clear" then return finish(A.fail("act.rest", "unsafe_recovery", "resting location became hostile or unverified")) end
    if me.in_combat() then return finish(A.fail("act.rest", "attacked", "attacked while resting")) end
    if now() >= deadline then return finish(A.fail("act.rest", "rest_timeout", "requested health and mana thresholds were not reached")) end
    for _, need in ipairs({{kind = "Food", low = A.hp() < (o.hp or 0.95)}, {kind = "Drink", low = A.mana() < (o.mana or 0.9)}}) do
      if need.low and not attempted[need.kind] then
        if now() >= deadline then return finish(A.fail("act.rest", "rest_timeout", "requested health and mana thresholds were not reached")) end
        attempted[need.kind] = true
        local r = consume(need.kind, math.min(30, math.max(0, (deadline - now()) / 1000)))
        step(steps, "act.consume", r)
        if not r.ok and r.error.code ~= "no_consumable" then return finish(r) end
      end
    end
    wait(math.max(0, math.min(1, (deadline - now()) / 1000)))
  end
  return finish()
end
local graveyard -- where releasing put you: the spirit healer is there
function A.release()
  local r = await(death.release())
  r = wrapped("act.release", r)
  if r.ok then graveyard = me.position() end
  return r
end
-- danger: living hostile creatures within r yards of a point (what attacks you the moment you come back there)
function A.danger(x, y, r)
  local out = {}
  for _, u in ipairs(units.nearby(80)) do
    if u.alive and u.reaction == "hostile" and u.attackable ~= false and math.sqrt((u.x - x) ^ 2 + (u.y - y) ^ 2) <= (r or 25) then
      out[#out + 1] = u
    end
  end
  return out
end
-- Recovery has one deadline and one resurrection-attempt budget across methods.
local function recovery_context(o, id)
  local c = {deadline = now() + (o.timeout_s or 180) * 1000, id = id,
    limit = o.max_attempts or 2, data = {state = "dead", attempts = 0, steps = {}}}
  function c:state(state)
    if self.data.state ~= state or self.reported_attempts ~= self.data.attempts then
      self.reported_attempts = self.data.attempts
      self.data.state = state
      death._recovery_state(state, self.data.attempts)
    end
  end
  function c:fail(code, message, cause)
    if cause then self.data.last_error = cause.error; self.data.failed_step = cause.op; self.data.action = cause.data end
    return {op = "act.recover", ok = false, data = self.data, error = {code = code, message = message}}
  end
  function c:guard()
    if now() >= self.deadline then return self:fail("recovery_timeout", "recovery deadline expired") end
    local control = move._check()
    if not control.ok then return self:fail(control.error.code, control.error.message, control) end
    if self.id then
      local r = death._recovery_check(self.id)
      if not r.ok then return self:fail(r.error.code, r.error.message, r) end
    end
  end
  function c:sleep(seconds)
    local r = self:guard(); if r then return r end
    wait(math.max(0, math.min(seconds, 1, (self.deadline-now())/1000)))
    return self:guard()
  end
  function c:action(op, fn, seconds)
    local r = self:guard(); if r then return r end
    r = await(fn(), math.max(0.001, math.min(seconds or 10, (self.deadline-now())/1000)))
    self.data.steps[#self.data.steps+1] = {op = op, ok = r.ok, request_id = r.id, error = r.error}
    local ended = self:guard(); if ended then return ended end
    if not r.ok then
      local failed = self:fail(r.error.code, r.error.message, r)
      self.data.failed_step = op
      return failed
    end
    return r
  end
  function c:finish(r)
    self.data.alive = me.alive() and not me.ghost()
    if r.ok and not self.data.alive then r = self:fail("not_alive", "resurrection did not leave the character alive") end
    self:state(r.ok and "done" or (r.error.code == "needs_repair" and "needs_repair" or "attention"))
    if not r.ok and r.data ~= self.data then self.data.control = r.data end
    if self.data.steps and #self.data.steps == 0 then self.data.steps = nil end
    r.data = self.data
    return r
  end
  return c
end

-- Conservative local evidence only: a hostile on an uncertain floor is not
-- discarded as safe. Shared arbitrary-point floor/LOS queries remain route work.
recovery_danger = function(p)
  if not p.z then return {state = "unknown", reason = "missing_floor"} end
  local ground = world.ground_z(p.x, p.y, p.z)
  if ground == nil or math.abs(ground-p.z) > 3 then return {state = "unknown", reason = "unverified_floor"} end
  if A.dist(p.x, p.y) > 45 then return {state = "unknown", reason = "outside_local_observation"} end
  local hostiles = {}
  for _, u in ipairs(units.nearby(80)) do
    if u.alive and u.reaction == "hostile" and u.attackable ~= false then
      local gap = math.sqrt((u.x-p.x)^2 + (u.y-p.y)^2)
      if gap <= math.max(25, u.aggro_radius or 0) + 2 then hostiles[#hostiles+1] = u end
    end
  end
  return {state = #hostiles > 0 and "hostile" or "clear", hostiles = hostiles}
end
local function recovery_move(c, p, tolerance)
  local ended = c:guard(); if ended then return ended end
  if p.map and me.position().map and p.map ~= me.position().map then return c:fail("unreachable", "recovery point is on another map") end
  local ground = world.ground_z(p.x, p.y, p.z)
  if ground == nil or math.abs(ground-p.z) > 3 then return c:fail("wrong_floor", "recovery point's floor could not be confirmed") end
  local path = world.path(p.x, p.y, p.z)
  if not path or path.gap > 3 then return c:fail("unreachable", "no complete path to the recovery point") end
  c:state("ghost_travel")
  local r = c:action("move.to", function() return move.to(p.x, p.y, p.z) end, 120)
  if not r.ok then return r end
  local here = me.position()
  if math.abs(here.z-p.z) > 3 then return c:fail("wrong_floor", "walk ended on another floor") end
  if A.dist(p.x, p.y) > (tolerance or 4) or (r.data and r.data.no_path) then return c:fail("unreachable", "walk did not confirm the recovery point") end
  return r
end
local function corpse_recovery(c, o, attempt_limit)
  if me.alive() and not me.ghost() then return A.ok("act.corpse_run") end
  if not me.ghost() then return c:fail("not_a_ghost", "release before running to the corpse") end
  local corpse
  while not corpse do
    local ended = c:guard(); if ended then return ended end
    corpse = death.corpse()
    if not corpse then ended = c:sleep(0.5); if ended then return ended end end
  end
  if A.dist(corpse.x, corpse.y, corpse.z) > 35 then
    local r = recovery_move(c, corpse, 4); if not r.ok then return r end
  end
  local target = {x = corpse.x, y = corpse.y, z = corpse.z, map = corpse.map}
  if o.safe ~= false and recovery_danger(target).state ~= "clear" then
    local best, margin
    for k=0,11 do
      local a = k*math.pi/6
      local p = {x=corpse.x+32*math.cos(a), y=corpse.y+32*math.sin(a), z=corpse.z, map=corpse.map}
      if recovery_danger(p).state == "clear" then
        local nearest = math.huge
        for _, u in ipairs(units.nearby(80)) do
          if u.alive and u.reaction == "hostile" then nearest = math.min(nearest, math.sqrt((u.x-p.x)^2+(u.y-p.y)^2)) end
        end
        if not margin or nearest > margin then best, margin = p, nearest end
      end
    end
    if not best then return c:fail("unsafe_recovery", "no observed clear reclaim point on the corpse floor") end
    target = best
  end
  if A.dist(target.x,target.y,target.z) > 3 then
    local r = recovery_move(c,target,3); if not r.ok then return r end
  end
  while c.data.attempts < attempt_limit do
    local ended = c:guard(); if ended then return ended end
    if me.alive() and not me.ghost() then return A.ok("act.corpse_run") end
    local current = death.corpse()
    if not current then return c:fail("corpse_unavailable", "corpse state became unavailable") end
    local here = me.position()
    if (current.map and here.map and current.map ~= here.map) or math.abs(here.z-current.z)>3 or A.dist(current.x,current.y,current.z)>35 then
      return c:fail("wrong_floor", "not within reclaim range on the corpse floor")
    end
    if world.in_sight(current.x,current.y,current.z) ~= true then return c:fail("unsafe_recovery", "line of sight to the corpse is blocked or unknown") end
    if o.safe ~= false and recovery_danger(here).state ~= "clear" then return c:fail("unsafe_recovery", "reclaim location is hostile or unverified") end
    if current.reclaim_in > 0 then
      c:state("reclaim_wait"); ended = c:sleep(current.reclaim_in); if ended then return ended end
    else
      c.data.attempts = c.data.attempts + 1; c:state("reclaiming")
      local r = c:action("death.reclaim", death.reclaim, 5)
      if r.ok then
        if me.alive() and not me.ghost() then return r end
        r = c:fail("not_alive", "reclaim response did not confirm resurrection")
      end
      if r.error.code == "recovery_timeout" or r.error.code == "task_paused" then return r end
      c.data.last_error = r.error
      if c.data.attempts < attempt_limit then ended = c:sleep(1); if ended then return ended end end
    end
  end
  return c:fail("retry_exhausted", "corpse reclaim attempt limit reached")
end
local function spirit_recovery(c)
  if me.alive() and not me.ghost() then return A.ok("act.spirit_res") end
  if not me.ghost() then return c:fail("not_a_ghost", "release before spirit resurrection") end
  local healer
  for _, u in ipairs(units.nearby(60)) do
    for _, role in ipairs(u.roles or {}) do if role == "spirit_healer" then healer = healer or u end end
  end
  if not healer then return c:fail("not_found", "no spirit healer is visible") end
  c.data.spirit_option = {npc_guid=healer.guid, durability_loss=true, cost_known=false}
  if recovery_danger(healer).state ~= "clear" then return c:fail("unsafe_recovery", "spirit healer location is hostile or unverified") end
  if healer.distance > 4 then
    local r = recovery_move(c,healer,4); if not r.ok then return r end
  end
  while c.data.attempts < c.limit do
    if me.alive() and not me.ghost() then return A.ok("act.spirit_res") end
    local ended = c:guard(); if ended then return ended end
    if recovery_danger(me.position()).state ~= "clear" then return c:fail("unsafe_recovery", "spirit healer location is hostile or unverified") end
    if world.in_sight(healer.x,healer.y,healer.z) ~= true then return c:fail("unsafe_recovery", "spirit healer line of sight is blocked or unknown") end
    c.data.attempts = c.data.attempts + 1; c:state("spirit_healer")
    local r = c:action("death.spirit_res",function() return death.spirit_res(healer.guid) end,5)
    if r.ok and me.alive() and not me.ghost() then return r end
    if not r.ok and (r.error.code == "recovery_timeout" or r.error.code == "task_paused") then return r end
    c.data.last_error = r.error or {code="not_alive",message="spirit resurrection did not leave the character alive"}
    if c.data.attempts < c.limit then ended = c:sleep(1); if ended then return ended end end
  end
  return c:fail("retry_exhausted", "spirit resurrection attempt limit reached")
end
local function recovery_snapshot(c)
  c.data.health,c.data.power,c.data.gear,c.data.money = me.health(),me.power(),me.gear(),me.money()
  if not c.data.health or not c.data.power or not c.data.gear or c.data.money == nil then return c:fail("state_unavailable", "post-resurrection state is unavailable") end
  if c.data.gear.weapon and c.data.gear.weapon.broken then
    c.data.repair_options = {}
    for _, u in ipairs(units.nearby(80)) do
      for _, role in ipairs(u.roles or {}) do if role=="repair" and #c.data.repair_options<20 then c.data.repair_options[#c.data.repair_options+1]=u end end
    end
    if #c.data.repair_options == 0 then c.data.repair_options = nil end
    return c:fail("needs_repair", "main-hand weapon is broken")
  end
end
local function recovery_postconditions(c,o)
  local ended = c:guard(); if ended then return ended end
  if not me.alive() or me.ghost() then return c:fail("not_alive", "character died again; recovery will not repeat the death cycle") end
  local snapshot_error = recovery_snapshot(c); if snapshot_error then return snapshot_error end
  if o.rest ~= false then
    if recovery_danger(me.position()).state ~= "clear" then return c:fail("unsafe_recovery", "cannot rest at a hostile or unverified location") end
    c:state("resting")
    local r = A.rest{safe=true,max_s=math.min(40,math.max(0.001,(c.deadline-now())/1000))}
    if not r.ok then return c:fail(r.error.code,r.error.message,r) end
    ended=c:guard();if ended then return ended end
    if not me.alive() or me.ghost() then return c:fail("not_alive", "character died while resting") end
    snapshot_error = recovery_snapshot(c); if snapshot_error then return snapshot_error end
  end
  return A.ok("act.recover")
end
function A.corpse_run(o)
  o=o or {};local c=recovery_context(o)
  local r=corpse_recovery(c,o,c.limit)
  if r.ok then r=recovery_postconditions(c,{rest=false}) end
  return c:finish(r)
end
function A.spirit_res(o)
  o=o or {};local c=recovery_context(o)
  -- Calling this helper directly is explicit permission for the healer cost.
  local r=spirit_recovery(c)
  if r.ok then r=recovery_postconditions(c,{rest=false}) end
  return c:finish(r)
end
function A.recover(o)
  o=o or {}
  local attempt=death._recovery_begin(o.timeout_s or 180)
  if attempt.error then error(attempt.error,2) end
  if not attempt.owner then return await(attempt.request,601) end
  local c=recovery_context(o,attempt.id)
  if not me.alive() or me.ghost() then c:state(me.ghost() and "ghost_travel" or "dead") end
  local ok,r=pcall(movement_scope,"recovery",5,o.timeout_s or 180,function()
    local ended=c:guard();if ended then return ended end
    if not me.alive() or me.ghost() then
      if not me.ghost() then
        c:state("releasing")
        local released=c:action("death.release",death.release,10)
        if not released.ok then return released end
        if not me.ghost() then return c:fail("not_a_ghost","release did not produce a ghost") end
        graveyard=me.position()
      end
      local recovered
      if o.spirit then recovered=spirit_recovery(c)
      else recovered=corpse_recovery(c,o,o.allow_spirit_fallback and math.max(1,c.limit-1) or c.limit) end
      if not recovered.ok and not o.spirit and o.allow_spirit_fallback and c.data.attempts<c.limit then
        c.data.last_error = recovered.error
        c.data.failed_step = c.data.failed_step or "act.corpse_run"
        local ended=c:guard();if ended then return ended end
        recovered=spirit_recovery(c)
      end
      if not recovered.ok then
        for _,u in ipairs(units.nearby(60)) do for _,role in ipairs(u.roles or {}) do
          if role=="spirit_healer" then c.data.spirit_option={npc_guid=u.guid,durability_loss=true,cost_known=false,requires_permission=not(o.spirit or o.allow_spirit_fallback)} end
        end end
        return recovered
      end
    end
    return recovery_postconditions(c,o)
  end)
  if not ok then
    local failed=c:finish(c:fail("script_error","recovery raised a Lua error"))
    death._recovery_finish(attempt.id,failed)
    error(r,0)
  end
  r=c:finish(r)
  return death._recovery_finish(attempt.id,r)
end

-- task.keep: a script that doesn't stop when it ends. It runs fn as a task called name and runs it again each time
-- it finishes or fails (after a pause that grows while it keeps failing fast, up to 30 s), waiting while you're
-- dead. Calling it again replaces it; task.stop(name) stops it for good.
function task.keep(name, fn, ...)
  local args, n = { ... }, select("#", ...)
  task.stop(name)
  return task.spawn(name, function()
    local quick = 0
    while true do
      while not me.alive() do events.wait("resurrected", 5) end
      local started = now()
      local id = task.spawn(name, fn, unpack(args, 1, n))
      local ev
      repeat ev = events.wait("task_finished") until ev and ev.data.task == id
      if ev.data.state == "stopped" then return end
      quick = (now() - started < 10000) and quick + 1 or 0
      wait(math.min(2 ^ quick, 30))
    end
  end)
end
doc("task.keep", "(name, fn, ...) → id", "run fn as a task and run it again whenever it finishes or fails (backing off while it fails fast, waiting while you're dead); task.stop(name) ends it")

-- the functions above that wait need a task: say so plainly instead of failing deep inside
-- and the ones that take an options table check they got one (act.loot_nearby(10) means act.loot_nearby{dist = 10})
local takes_options = { go_to = true, travel = true, approach = true, follow = true, reach_npc = true, accept = true,
  turn_in = true, vendor = true, train = true, talk = true, use_object = true, rest = true, corpse_run = true,
  recover = true, spirit_res = true, loot_nearby = true, fly = true, make_room = true }
local npc_options = {
  reach_npc = "npc_entry npc_guid x y z",
  accept = "npc_entry npc_guid x y z quest",
  turn_in = "npc_entry npc_guid x y z quest reward",
  vendor = "npc_entry npc_guid x y z sell_junk repair buy",
  train = "npc_entry npc_guid x y z spells continue_on_error",
  talk = "npc_entry npc_guid x y z fn",
  fly = "npc_entry npc_guid to",
}
local function positive_int(v) return type(v) == "number" and v > 0 and v < math.huge and v == math.floor(v) end
local function validate_npc_options(n, o)
  local fields = npc_options[n]
  if not fields then return end
  local function bad(why) error("act." .. n .. ": " .. why .. "; accepted fields: " .. fields, 3) end
  if type(o) ~= "table" then bad("an options table is required") end
  local allowed = {}
  for field in fields:gmatch("%S+") do allowed[field] = true end
  for k in pairs(o) do if not allowed[k] then bad("unknown option " .. tostring(k)) end end
  if (o.npc_entry ~= nil and o.npc_guid ~= nil) or (n ~= "fly" and o.npc_entry == nil and o.npc_guid == nil) then
    bad("supply exactly one of npc_entry or npc_guid")
  end
  if o.npc_entry ~= nil and not positive_int(o.npc_entry) then bad("npc_entry must be a positive integer") end
  if o.npc_guid ~= nil and (type(o.npc_guid) ~= "string" or not o.npc_guid:match("^%d+$")) then bad("npc_guid must be a decimal GUID string") end
  for _, k in ipairs({"sell_junk", "repair", "continue_on_error"}) do
    if o[k] ~= nil and type(o[k]) ~= "boolean" then bad(k .. " must be boolean") end
  end
  for _, k in ipairs({"x", "y", "z"}) do
    if o[k] ~= nil and (type(o[k]) ~= "number" or o[k] ~= o[k] or math.abs(o[k]) == math.huge) then bad(k .. " must be a finite coordinate") end
  end
  if (o.x == nil) ~= (o.y == nil) then bad("coordinates require both x and y") end
  local function array(v, field, min)
    if type(v) ~= "table" or #v < min or #v > 64 then bad(field .. " must contain " .. min .. "..64 entries") end
    local count = 0
    for k in pairs(v) do
      if not positive_int(k) or k > #v then bad(field .. " must be a dense array") end
      count = count + 1
    end
    if count ~= #v then bad(field .. " must be a dense array") end
  end
  if n == "vendor" then
    local buy = o.buy or {}
    array(buy, "buy", 0)
    if not o.sell_junk and not o.repair and #buy == 0 then bad("request sell_junk, repair, or a purchase") end
    for _, b in ipairs(buy) do
      if type(b) ~= "table" or not positive_int(b[1]) or (b[2] ~= nil and not positive_int(b[2])) then
        bad("buy entries must be {item_entry, count = 1} with positive integers")
      end
      for k in pairs(b) do if k ~= 1 and k ~= 2 then bad("buy entries accept only item_entry and count positions") end end
    end
  elseif n == "train" then
    array(o.spells, "spells", 1)
    local seen = {}
    for _, sp in ipairs(o.spells) do
      if not positive_int(sp) or seen[sp] then bad("spells must contain unique positive IDs") end
      seen[sp] = true
    end
  elseif n == "accept" or n == "turn_in" then
    if not positive_int(o.quest) then bad("quest is required and must be a positive ID") end
  elseif n == "talk" and type(o.fn) ~= "function" then bad("fn must return a completed action result")
  elseif n == "fly" and not (positive_int(o.to) or (type(o.to) == "string" and o.to ~= "")) then bad("to must be a node ID or name") end
end
local helper_options = {
  use_object = "entry count quest_id x y z radius max_attempts timeout_s",
  loot_nearby = "dist make_room",
  rest = "hp mana max_s safe",
  make_room = "",
  go_to = "x y z tolerance tries unstuck on_attacked",
  travel = "points tolerance unstuck on_attacked",
  approach = "unit range timeout",
  follow = "name distance until timeout_s",
  corpse_run = "safe timeout_s max_attempts",
  spirit_res = "timeout_s max_attempts",
  recover = "spirit rest safe allow_spirit_fallback timeout_s max_attempts",
}
local function validate_helper_options(n, o)
  if n == "consume" then
    if o ~= "Food" and o ~= "Drink" then error('act.consume(kind): kind must be "Food" or "Drink"', 3) end
    return
  end
  local fields = helper_options[n]
  if fields == nil then return end
  local function bad(why) error("act." .. n .. ": " .. why .. "; accepted fields: " .. fields, 3) end
  o = o or {}
  local allowed = {}
  for field in fields:gmatch("%S+") do allowed[field] = true end
  for k in pairs(o) do if not allowed[k] then bad("unknown option " .. tostring(k)) end end
  if n == "go_to" and (o.x == nil or o.y == nil) then bad("x and y are required") end
  if n == "approach" and (type(o.unit) ~= "string" or not o.unit:match("^%d+$")) then bad("unit must be a GUID string") end
  if n == "follow" and not o["until"] and not o.timeout_s then bad("until or timeout_s is required") end
  if n == "follow" and o.timeout_s and (type(o.timeout_s) ~= "number" or not (o.timeout_s > 0 and o.timeout_s <= 600)) then bad("timeout_s must be >0 and <=600") end
  if n == "follow" and (type(o.name) ~= "string" or o.name == "") then bad("name must be a nonempty player name") end
  for _, k in ipairs({"unstuck", "safe", "spirit", "rest", "allow_spirit_fallback"}) do
    if o[k] ~= nil and type(o[k]) ~= "boolean" then bad(k .. " must be boolean") end
  end
  for _, k in ipairs({"on_attacked", "until"}) do
    if o[k] ~= nil and type(o[k]) ~= "function" then bad(k .. " must be a function") end
  end
  if o.tries ~= nil and (not positive_int(o.tries) or o.tries > 32) then bad("tries must be an integer from 1 to 32") end
  if n == "use_object" and not positive_int(o.entry) then bad("entry is required and must be a positive ID") end
  for _, k in ipairs({"entry", "count", "quest_id", "max_attempts"}) do
    if o[k] ~= nil and not positive_int(o[k]) then bad(k .. " must be a positive integer") end
  end
  for _, k in ipairs({"radius", "dist", "timeout_s", "max_s", "timeout", "tolerance", "range"}) do
    if o[k] ~= nil and (type(o[k]) ~= "number" or not (o[k] > 0 and o[k] < math.huge)) then bad(k .. " must be positive and finite") end
  end
  if o.distance ~= nil and (type(o.distance) ~= "number" or not (o.distance >= 0 and o.distance < math.huge)) then bad("distance must be nonnegative and finite") end
  for _, k in ipairs({"hp", "mana"}) do
    if o[k] ~= nil and (type(o[k]) ~= "number" or not (o[k] >= 0 and o[k] <= 1)) then bad(k .. " must be a fraction from 0 to 1") end
  end
  for _, k in ipairs({"x", "y", "z"}) do
    if o[k] ~= nil and (type(o[k]) ~= "number" or o[k] ~= o[k] or math.abs(o[k]) == math.huge) then bad(k .. " must be a finite coordinate") end
  end
  if (o.x == nil) ~= (o.y == nil) then bad("coordinates require both x and y") end
  if o.make_room ~= nil and type(o.make_room) ~= "boolean" then bad("make_room must be boolean") end
  if n == "recover" or n == "corpse_run" or n == "spirit_res" then
    if o.timeout_s and o.timeout_s > 600 then bad("timeout_s must be at most 600") end
    if o.max_attempts and o.max_attempts > 2 then bad("max_attempts must be 1 or 2") end
  end
  if n == "use_object" then
    if (o.max_attempts or 3) > 32 then bad("max_attempts must be at most 32") end
    if not o.quest_id and (o.count or 1) > (o.max_attempts or 3) then bad("count cannot exceed max_attempts") end
  end
  if n == "travel" then
    if type(o.points) ~= "table" or #o.points < 1 or #o.points > 4096 then bad("points must contain 1..4096 waypoints") end
    local count = 0
    for k, p in pairs(o.points) do
      count = count + 1
      if not positive_int(k) or k > #o.points or type(p) ~= "table" then bad("points must be a dense array of waypoints") end
      local positional = p[1] ~= nil or p[2] ~= nil or p[3] ~= nil
      for key in pairs(p) do
        if positional then
          if key ~= 1 and key ~= 2 and key ~= 3 then bad("positional waypoints accept only {x, y, z} positions") end
        elseif key ~= "x" and key ~= "y" and key ~= "z" then bad("named waypoints accept only x, y, z") end
      end
      local x, y, z = p[1] or p.x, p[2] or p.y, p[3] or p.z
      local function finite(v) return type(v) == "number" and v == v and math.abs(v) < math.huge end
      if not finite(x) or not finite(y) or (z ~= nil and not finite(z)) then bad("each waypoint requires finite x, y and optional z") end
    end
    if count ~= #o.points then bad("points must be a dense array") end
  end
end
local movement_helpers = {}
for _, n in ipairs({"go_to", "travel", "approach", "follow", "reach_npc", "accept", "turn_in", "vendor", "train", "talk", "use_object", "rest", "corpse_run", "spirit_res", "loot_nearby", "fly"}) do movement_helpers[n] = true end
for _, n in ipairs({ "go_to", "travel", "approach", "follow", "reach_npc", "accept", "turn_in", "vendor", "train", "talk",
  "use_object", "rest", "release", "corpse_run", "spirit_res", "recover", "loot_nearby", "fly", "cast", "buff", "consume", "make_room" }) do
  local f = A[n]
  A[n] = function(o, ...)
    if not task.current() then
      error("act." .. n .. " waits for the game, so it runs inside a task: task.spawn(\"" .. n .. "\", function() act." .. n .. "{...} end)", 2)
    end
    local call = task._action_begin("act." .. n, o, ...)
    local ok, r = pcall(function(...)
      if takes_options[n] and select("#", ...) > 0 then error("act." .. n .. " accepts one options table", 2) end
      if takes_options[n] and o ~= nil and type(o) ~= "table" then
        error("act." .. n .. " takes an options table, act." .. n .. "{...}, not a " .. type(o) .. ": help(\"act." .. n .. "\") lists its fields", 2)
      end
      validate_npc_options(n, o)
      validate_helper_options(n, o)
      if movement_helpers[n] then
        local priority = (n == "recover" or n == "corpse_run" or n == "spirit_res") and "recovery" or "normal"
        local r = wrapped("act." .. n, movement_scope(priority, 5, ((n == "corpse_run" or n == "spirit_res") and o and o.timeout_s) or 600, f, o, ...))
        if (n == "corpse_run" or n == "spirit_res") and (not r.data or not r.data.state) then
          r.data = {state = r.ok and "done" or "attention", attempts = 0, control = r.data}
        end
        return r
      end
      return wrapped("act." .. n, f(o, ...))
    end, ...)
    if not ok then
      task._action_end(call, A.fail("act." .. n, "script_error", "helper raised a Lua error"))
      error(r, 0)
    end
    task._action_end(call, r)
    return r
  end
end

-- reference (help("act"))
for name, d in pairs({
  ok = { "(op, data) → ActResult", "an ok result for your own act-style functions" },
  fail = { "(op, code, message) → ActResult", "a failed result" },
  must = { "(result) → ActResult", "the result if ok, else raise its error (ends the task)" },
  dist = { "(x, y [, z]) → yards", "distance from you (2D without z)" },
  hp = { "() → 0..1", "your health fraction" },
  mana = { "() → 0..1", "your mana fraction (1 for rage/energy users)" },
  ready = { "(id [, guid]) → bool", "known, off cooldown, affordable, and you're not casting" },
  in_range = { "(id, guid) → bool", "within the spell's range of the unit" },
  npc = { "(entry [, dist = 60]) → Creature?", "the nearest living observed creature with that entry, or nil when none is visible" },
  player = { "(name) → Player?", "an observed player by name, or nil when not visible (including distant group members)" },
  consume = { "(\"Food\" | \"Drink\") → ActResult<Consumed>", "wait up to 30 seconds for the first matching item to be used; no_consumable if absent (sit still after)" },
  make_room = { "{} → ActResult<Consumed>", "destroy the cheapest loaded poor-quality item that is not a possible usable gear upgrade; wait up to 5 seconds for confirmation, or no_disposable_item; options may be omitted" },
  cast = { "(id [, guid]) → ActResult", "face the target and cast if ready (waits for the result in a task)" },
  buff = { "(id [, guid]) → ActResult", "cast it unless the aura is already active (ok with data.status = already_active then)" },
  go_to = { "{x, y [, z, tolerance = 4, tries = 3, unstuck = false, on_attacked = nil]} → ActResult", "walk there; tries must be 1..32; when stuck it sidesteps and jumps between tries. unstuck = true adds a last resort (out of combat): the Stuck spell, which hearths you home (error unstuck_home). Pass z indoors and upstairs. Without on_attacked it walks on while mobs hit you and drags them along; with it, it stops whenever something targets you, calls on_attacked(attackers) (your fighting) and then carries on" },
  travel = { "{points = {{x, y [, z]}, ...} [, tolerance = 6, unstuck = false, on_attacked = nil]} → ActResult", "go_to each of 1..4096 waypoints (e.g. from azworld route); all waypoint fields are checked before movement starts" },
  danger = { "(x, y [, r = 25]) → Creature[]", "living hostile creatures within r yards of a point: check a spot before you go there or come back to life there" },
  approach = { "{unit [, range = 4, timeout = 60]} → ActResult<Creature>", "move within range and verify facing; reuse a pending walk until the target moves; bounded by timeout" },
  follow = { "{name [, distance = 5, until = fn, timeout_s = 600]} → ActResult", "requires until or timeout_s; check until each pass, fail timeout at the deadline (at most 600 seconds), or player_not_visible after 5 seconds of absence" },
  reach_npc = { "{npc_entry|npc_guid [, x, y, z]} → ActResult<Creature>", "find an NPC by exactly one of npc_entry or npc_guid (walking to x, y, z first) and stand next to it" },
  accept = { "{npc_entry|npc_guid, quest [, x, y, z]} → ActResult", "walk to a quest giver and accept the quest" },
  turn_in = { "{npc_entry|npc_guid, quest [, reward = 0, x, y, z]} → ActResult", "walk to the quest ender and turn the quest in" },
  vendor = { "{npc_entry|npc_guid [, x, y, z, sell_junk = false, repair = false, buy = {{entry, count}, ...}]} → ActResult<VendorErrand>", "perform only requested operations (at least one required, at most 64 purchases); stop on failure and return VendorErrand partial data and steps" },
  train = { "{npc_entry|npc_guid, spells [, x, y, z, continue_on_error = false]} → ActResult<TrainErrand>", "learn 1..64 unique requested spells; stop on the first failure unless continue_on_error; return TrainErrand with learned, already_known, failed and remaining IDs" },
  talk = { "{npc_entry|npc_guid, fn [, x, y, z]} → ActResult", "walk to an NPC and return fn(npc) as ActResult; fn must return a completed action result" },
  use_object = { "{entry [, count = 1, quest_id, x, y, z, radius = 80, max_attempts = 3, timeout_s = 60]} → ActResult<ObjectUse>", "use objects count times, or until observed progress/completion of quest_id; absent quest, missing progress, attempt limit or deadline fails with partial counts; at most 32 attempts" },
  loot_nearby = { "{[dist = 8, make_room = false]} → ActResult<Looted>", "loot at most 32 nearby corpses per call; fail on approach or loot errors with partial items/money. Destruction is opt-in with make_room = true, with one retry per corpse" },
  rest = { "{[hp = 0.95, mana = 0.9, max_s = 40, safe = false]} → ActResult<Rested>", "wait for both requested fractions (0..1); no consumable allows passive regeneration. Item-use failures propagate; rest_timeout includes achieved fractions. safe=true rechecks local floor/hostile evidence each pass" },
  fly = { "{to = node id or name [, npc_entry or npc_guid]} → ActResult<Flight>", "walk to a flight master nearby, fly to a known destination and wait until you land" },
  release = { "() → ActResult", "release your spirit" },
  corpse_run = { "{[safe = true, timeout_s = 180, max_attempts = 2]} → ActResult<Recovery>", "bounded ghost travel and reclaim; confirm path endpoint, floor, reclaim distance, line of sight and timer. Default safe=true rejects nearby hostiles or unverified floor evidence. timeout_s >0..600, max_attempts 1..2; preserve failed movement and resurrection steps; no healer fallback; check gear after resurrection; needs_repair for broken main hand" },
  spirit_res = { "{[timeout_s = 180, max_attempts = 2]} → ActResult<Recovery>", "explicitly authorize spirit-healer resurrection and its durability penalty. Reject hostile or unverified healer sites, propagate movement failures, require observed resurrection, and check gear. At most 2 attempts within timeout_s >0..600; no rest or automatic farming restart" },
  recover = { "{[spirit = false, allow_spirit_fallback = false, rest = true, safe = true, timeout_s = 180, max_attempts = 2]} → ActResult<Recovery>", "one session recovery attempt: duplicate callers join the first caller's policy and result. Release, reclaim and optionally rest; healer use requires spirit=true or allow_spirit_fallback=true. timeout_s >0..600 and at most 2 total resurrection attempts across methods; fallback reserves the second attempt when possible. Return attention on failure or needs_repair for a broken main hand; never restart another task. Synchronous path query cancellation remains unsupported; exact healer cost is unavailable" },
}) do doc("act." .. name, d[1], d[2]) end
