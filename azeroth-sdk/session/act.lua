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
  if task.current() then await(f, 2) end
end
-- cast(id [, guid]): face the target and cast if ready; in a task it waits for the result
function A.cast(id, guid)
  guid = guid or me.guid()
  if not A.ready(id, guid) then
    if task.current() then wait(0.2) end -- a loop that retries can't spin past the execution limit
    return result("act.cast", false, nil, "not_ready", "on cooldown, casting, not enough power, or its condition isn't met (spells.usable)")
  end
  if guid == me.guid() then return await(spells.cast(id, guid)) end
  face(guid)
  local r = await(spells.cast(id, guid))
  if task.current() and r.ok == false and r.error and r.error.code == "unit_not_infront" then -- it moved: once more
    face(guid)
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
  while now() < deadline do
    local u = units.get(o.unit)
    if not u then return result("act.approach", false, nil, "unit_gone", "the unit is gone (dead and despawned, or out of sight)") end
    -- in range and in sight: a mob behind a wall or round a tunnel bend is in range but can't be hit from here
    if u.distance <= range and (u.in_sight ~= false or u.distance <= 3) then move.stop() move.face(o.unit) return result("act.approach", true, u) end
    move.to(u.x, u.y, u.z)
    wait(0.5)
  end
  return result("act.approach", false, nil, "timeout", "couldn't get within " .. range .. " yd")
end
function A.follow(o)
  while not (o["until"] and o["until"]()) do
    local p = A.player(o.name)
    if p and p.distance > (o.distance or 5) + 2 then move.to(p.x, p.y, p.z) end
    wait(1)
  end
  return result("act.follow", true)
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
-- safe_spot: of the points on a ring around (x, y), the one farthest from danger; nil when nothing hostile is near
local function safe_spot(x, y, radius)
  local foes = A.danger(x, y, radius + 30)
  if #foes == 0 then return nil end
  local best, far = nil, -1
  for k = 0, 11 do
    local a = k * math.pi / 6
    local px, py = x + radius * math.cos(a), y + radius * math.sin(a)
    local near = math.huge
    for _, u in ipairs(foes) do near = math.min(near, math.sqrt((u.x - px) ^ 2 + (u.y - py) ^ 2)) end
    if near > far then best, far = { x = px, y = py }, near end
  end
  return best
end
-- corpse_run: walk to the corpse and reclaim it; with hostiles around the corpse (usually what killed you), reclaim
-- from the side of the reclaim circle farthest from them instead of on top of them (safe = false: on the spot)
function A.corpse_run(o)
  o = o or {}
  local moved = false
  while not me.alive() do
    local c = death.corpse()
    if not c then wait(1) -- asked the server
    elseif A.dist(c.x, c.y) > 25 and not moved then
      local r = A.go_to{ x = c.x, y = c.y, z = c.z, tolerance = 15 }
      if not r.ok then return wrapped("act.corpse_run", r) end
    elseif o.safe ~= false and not moved then
      moved = true
      -- the edge of the reclaim circle (the server allows ~39 yd; 32 leaves room): at 24 yd a mob five levels up
      -- still had you in its ~25 yd aggro radius
      local spot = safe_spot(c.x, c.y, 32)
      if spot then
        log("act.corpse_run: hostiles at the corpse, reclaiming from " .. math.floor(spot.x) .. "," .. math.floor(spot.y))
        A.go_to{ x = spot.x, y = spot.y, tolerance = 3 }
      end
    elseif c.reclaim_in > 0 then wait(c.reclaim_in)
    else
      local r = await(death.reclaim())
      if r.ok == false then wait(1) end
    end
  end
  return result("act.corpse_run", true)
end
function A.spirit_res()
  while not me.alive() do
    local healer
    for _, u in ipairs(units.nearby(60)) do
      for _, role in ipairs(u.roles or {}) do if role == "spirit_healer" then healer = healer or u end end
    end
    if not healer and graveyard and A.dist(graveyard.x, graveyard.y) > 20 then -- walked off: back to where you released
      local r = A.go_to{ x = graveyard.x, y = graveyard.y, z = graveyard.z, tolerance = 10 }
      if not r.ok then return wrapped("act.spirit_res", r) end
    elseif not healer then
      return result("act.spirit_res", false, nil, "not_found", "no spirit healer in sight (they stand at graveyards: azworld can find one)")
    end
    if healer and healer.distance > 5 then A.approach{ unit = healer.guid, range = 5 }
    elseif healer then await(death.spirit_res(healer.guid)) end
  end
  return result("act.spirit_res", true)
end
-- recover: back to life however it takes: release, run to the corpse (the spirit healer if that fails, or with
-- spirit = true), then rest unless rest = false. Pair it with a death handler:
--   events.on("death", function() act.recover() end, "recover")
-- the spirit healer brings you back where you stand: no use when hostiles are camping the graveyard
local function graveyard_camped()
  local p = me.position()
  local foes = A.danger(p.x, p.y, 30)
  return #foes > 0, foes
end
function A.recover(o)
  o = o or {}
  if me.alive() then return result("act.recover", true) end
  if not me.ghost() then
    local r = A.release()
    if not r.ok then return wrapped("act.recover", r) end
  end
  local spirit = o.spirit
  if spirit and o.safe ~= false then
    local camped, foes = graveyard_camped()
    if camped then
      log("act.recover: " .. #foes .. " hostile creature(s) at the graveyard (" .. tostring(foes[1].name) .. ", level " ..
        tostring(foes[1].level) .. "): running to the corpse instead of the spirit healer")
      spirit = false
    end
  end
  local r = spirit and A.spirit_res() or A.corpse_run{safe = o.safe}
  if not r.ok and not spirit then r = A.spirit_res() end
  r = wrapped("act.recover", r)
  if r.ok and o.rest ~= false then
    local rested = A.rest()
    if not rested.ok then return wrapped("act.recover", rested) end
  end
  return r
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
  recover = true, loot_nearby = true, fly = true, make_room = true }
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
  rest = "hp mana max_s",
  make_room = "",
  go_to = "x y z tolerance tries unstuck on_attacked",
  travel = "points tolerance unstuck on_attacked",
  approach = "unit range timeout",
  follow = "name distance until",
  corpse_run = "safe",
  recover = "spirit rest safe",
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
  if n == "follow" and (type(o.name) ~= "string" or o.name == "") then bad("name must be a nonempty player name") end
  for _, k in ipairs({"unstuck", "safe", "spirit", "rest"}) do
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
  approach = { "{unit [, range = 4, timeout = 60]} → ActResult<Creature>", "move within range of a unit and face it" },
  follow = { "{name [, distance = 5, until = fn]} → ActResult", "stay near a player until until() is true" },
  reach_npc = { "{npc_entry|npc_guid [, x, y, z]} → ActResult<Creature>", "find an NPC by exactly one of npc_entry or npc_guid (walking to x, y, z first) and stand next to it" },
  accept = { "{npc_entry|npc_guid, quest [, x, y, z]} → ActResult", "walk to a quest giver and accept the quest" },
  turn_in = { "{npc_entry|npc_guid, quest [, reward = 0, x, y, z]} → ActResult", "walk to the quest ender and turn the quest in" },
  vendor = { "{npc_entry|npc_guid [, x, y, z, sell_junk = false, repair = false, buy = {{entry, count}, ...}]} → ActResult<VendorErrand>", "perform only requested operations (at least one required, at most 64 purchases); stop on failure and return VendorErrand partial data and steps" },
  train = { "{npc_entry|npc_guid, spells [, x, y, z, continue_on_error = false]} → ActResult<TrainErrand>", "learn 1..64 unique requested spells; stop on the first failure unless continue_on_error; return TrainErrand with learned, already_known, failed and remaining IDs" },
  talk = { "{npc_entry|npc_guid, fn [, x, y, z]} → ActResult", "walk to an NPC and return fn(npc) as ActResult; fn must return a completed action result" },
  use_object = { "{entry [, count = 1, quest_id, x, y, z, radius = 80, max_attempts = 3, timeout_s = 60]} → ActResult<ObjectUse>", "use objects count times, or until observed progress/completion of quest_id; absent quest, missing progress, attempt limit or deadline fails with partial counts; at most 32 attempts" },
  loot_nearby = { "{[dist = 8, make_room = false]} → ActResult<Looted>", "loot at most 32 nearby corpses per call; fail on approach or loot errors with partial items/money. Destruction is opt-in with make_room = true, with one retry per corpse" },
  rest = { "{[hp = 0.95, mana = 0.9, max_s = 40]} → ActResult<Rested>", "wait for both requested fractions (0..1); no consumable allows passive regeneration. Item-use failures propagate; rest_timeout includes achieved fractions" },
  fly = { "{to = node id or name [, npc_entry or npc_guid]} → ActResult<Flight>", "walk to a flight master nearby, fly to a known destination and wait until you land" },
  release = { "() → ActResult", "release your spirit" },
  corpse_run = { "{[safe = true]} → ActResult", "as a ghost, walk to your corpse and reclaim it; with hostiles at the corpse it reclaims from the far side of the reclaim circle, 32 yd out (safe = false: on the spot)" },
  spirit_res = { "() → ActResult", "as a ghost, resurrect at the nearest spirit healer in sight (costs durability)" },
  recover = { "{[spirit = false, rest = true, safe = true]} → ActResult", "back to life: release, corpse run (reclaiming away from hostiles at the corpse), the spirit healer if that fails or with spirit = true (unless hostiles camp the graveyard: then the corpse run), then rest" },
}) do doc("act." .. name, d[1], d[2]) end
