-- act: utilities built into every session, so scripts spend their effort on strategy, not on the plumbing of
-- talking to the game. There is deliberately no combat AI here: no rotations, target picking, grinding loops, assist
-- or healing logic. How to fight, what to kill, when to rest and how to work with others is yours to build.
--
-- The doing functions take one options table, wait until they're finished (so call them inside a task), and return
-- a result like an action's: {ok = true, op = "act.go_to", data = ...} or {ok = false, op, error = {code, message}}.
-- act.must(r) raises the error instead. The helpers at the top don't wait.
--
--   task.spawn("quest 62", function()
--     act.must(act.accept{ npc = 240, quest = 62 })
--     ... your fighting ...
--     act.must(act.turn_in{ npc = 240, quest = 62 })
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
function A.best(ids)
  for i = #ids, 1, -1 do if spells.known(ids[i]) then return ids[i] end end
end
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
function A.consume(kind)
  for _, it in ipairs(items.list()) do
    if it.use_spell and not it.equipped then
      local sp = spells.info(it.use_spell)
      if sp and sp.name == kind then return items.use(it.guid).ok ~= false end
    end
  end
  return false
end
local destroyed = {} -- guid -> when: the bags still list an item for a moment after it's destroyed
function A.make_room()
  local cheapest
  for _, it in ipairs(items.list()) do
    local gone = destroyed[tostring(it.guid)]
    if not it.equipped and it.quality == "poor" and not (gone and now() - gone < 5000) and (not cheapest or it.sell_price < cheapest.sell_price) then cheapest = it end
  end
  if cheapest then
    items.destroy(cheapest.guid)
    destroyed[tostring(cheapest.guid)] = now()
    log("act: destroyed " .. cheapest.name .. " for bag space")
    return true
  end
  return false
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
  local npc = A.npc(o.npc)
  if not npc and o.x and A.dist(o.x, o.y) > 8 then
    local r = A.go_to{ x = o.x, y = o.y, z = o.z }
    if not r.ok then return result("act.reach_npc", false, nil, r.error.code, r.error.message) end
    npc = A.npc(o.npc)
  end
  if not npc then return result("act.reach_npc", false, nil, "npc_not_found", "npc " .. tostring(o.npc) .. " not found nearby (pass its x, y, z from azworld)") end
  if npc.distance > 4 then
    local r = A.go_to{ x = npc.x, y = npc.y, z = npc.z, tolerance = 3 }
    if not r.ok then return result("act.reach_npc", false, nil, r.error.code, r.error.message) end
  end
  move.stop()
  return result("act.reach_npc", true, units.get(npc.guid) or npc)
end
local function at_npc(op, o, fn)
  local r = A.reach_npc(o)
  if not r.ok then r.op = op return r end
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
function A.vendor(o)
  return at_npc("act.vendor", o, function(npc)
    if not has_role(npc, "vendor", "repair") then
      return A.fail("act.vendor", "wrong_npc", tostring(npc.name) .. " doesn't sell or repair (roles: " .. table.concat(npc.roles or {}, ", ") .. ")")
    end
    local out = { sold = await(vendor.sell_junk(npc.guid)), bought = {} }
    vendor.items(npc.guid)
    wait(1.5)
    for _, role in ipairs(npc.roles or {}) do if role == "repair" then out.repaired = await(vendor.repair(npc.guid)) end end
    for _, b in ipairs(o.buy or {}) do out.bought[#out.bought + 1] = await(vendor.buy(npc.guid, b[1], b[2] or 1)) end
    return A.ok("act.vendor", out)
  end)
end
function A.train(o)
  return at_npc("act.train", o, function(npc)
    local learned, failed = {}, {}
    for _, sp in ipairs(o.spells or {}) do
      local r = await(trainer.learn(npc.guid, sp))
      if r.ok then learned[#learned + 1] = sp else failed[#failed + 1] = r end
    end
    return A.ok("act.train", { learned = learned, failed = failed })
  end)
end
function A.talk(o) return at_npc("act.talk", o, function(npc) return A.ok("act.talk", o.fn and o.fn(npc)) end) end
function A.use_object(o)
  local used = 0
  while true do
    if o.quest then
      local q = in_log(o.quest)
      if not q or q.complete then return result("act.use_object", true, { used = used }) end
    elseif used >= (o.count or 1) then return result("act.use_object", true, { used = used }) end
    local ob = objects.find(o.entry, o.radius or 80)
    if not ob then
      if o.x and A.dist(o.x, o.y) > 10 then
        local r = A.go_to{ x = o.x, y = o.y, z = o.z }
        if not r.ok then return wrapped("act.use_object", r) end
      else
        return result("act.use_object", false, { used = used }, "not_found", "no object " .. o.entry .. " in range")
      end
    else
      if ob.distance > 4 then
        local r = A.go_to{ x = ob.x, y = ob.y, z = ob.z, tolerance = 3 }
        if not r.ok then return wrapped("act.use_object", r) end
      end
      move.stop()
      local r = await(objects.use(ob.guid))
      if not r.ok then return wrapped("act.use_object", r) end
      used = used + 1
      wait(1)
    end
  end
end
-- loot_nearby: walk to each lootable corpse within dist (looting reaches ~5 yd) and loot it; with full bags it
-- destroys your cheapest grey to make room, and stops (bags_full) when there's none
function A.loot_nearby(o)
  local got = {}
  for _, u in ipairs(units.nearby((o and o.dist) or 8)) do
    if not u.alive and u.lootable then
      if u.distance > 4 then A.approach{ unit = u.guid, range = 4, timeout = 10 } end
      local r = await(items.loot(u.guid))
      if not r.ok and r.error and r.error.code == "inventory_full" then
        if not A.make_room() then return result("act.loot_nearby", false, got, "bags_full", "bags full and no grey item to destroy: sell or destroy something") end
        r = await(items.loot(u.guid))
      end
      for _, it in ipairs(r.ok and r.data.items or {}) do got[#got + 1] = it end
    end
  end
  return result("act.loot_nearby", true, got)
end

-- fly{to = node id or name [, npc = entry]}: walk to the nearest flight master (or that one), open its map, fly
-- to the destination and wait until you land
function A.fly(o)
  local master
  for _, u in ipairs(units.nearby(80)) do
    for _, role in ipairs(u.roles or {}) do
      if role == "flight_master" and u.alive and (not o.npc or u.entry == o.npc) then master = master or u end
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
  local start, ate, drank = now(), false, false
  move.stop()
  while A.hp() < (o.hp or 0.95) or A.mana() < (o.mana or 0.9) do
    if me.in_combat() then return result("act.rest", false, nil, "attacked", "attacked while resting") end
    if now() - start > (o.max_s or 40) * 1000 then break end
    if not ate and A.hp() < (o.hp or 0.95) then ate = A.consume("Food") end
    if not drank and A.mana() < (o.mana or 0.9) then drank = A.consume("Drink") end
    wait(1)
  end
  return result("act.rest", true, { hp = A.hp(), mana = A.mana() })
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
  local r = spirit and A.spirit_res() or A.corpse_run(o)
  if not r.ok and not spirit then r = A.spirit_res() end
  r = wrapped("act.recover", r)
  if r.ok and o.rest ~= false then A.rest() end
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
  recover = true, loot_nearby = true, fly = true }
for _, n in ipairs({ "go_to", "travel", "approach", "follow", "reach_npc", "accept", "turn_in", "vendor", "train", "talk",
  "use_object", "rest", "release", "corpse_run", "spirit_res", "recover", "loot_nearby", "fly", "cast", "buff" }) do
  local f = A[n]
  A[n] = function(o, ...)
    if not task.current() then
      error("act." .. n .. " waits for the game, so it runs inside a task: task.spawn(\"" .. n .. "\", function() act." .. n .. "{...} end)", 2)
    end
    if takes_options[n] and o ~= nil and type(o) ~= "table" then
      error("act." .. n .. " takes an options table, act." .. n .. "{...}, not a " .. type(o) .. ": help(\"act." .. n .. "\") lists its fields", 2)
    end
    return wrapped("act." .. n, f(o, ...))
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
  best = { "({rank ids}) → id", "the highest rank you know of a spell" },
  ready = { "(id [, guid]) → bool", "known, off cooldown, affordable, and you're not casting" },
  in_range = { "(id, guid) → bool", "within the spell's range of the unit" },
  npc = { "(entry [, dist = 60]) → Creature", "the nearest living creature with that entry" },
  player = { "(name) → Player", "a player by name (nearby or in your group)" },
  consume = { "(\"Food\" | \"Drink\") → bool", "eat or drink the first such item you carry (sit still after)" },
  make_room = { "() → bool", "destroy your cheapest grey item to free a bag slot" },
  cast = { "(id [, guid]) → ActResult", "face the target and cast if ready (waits for the result in a task)" },
  buff = { "(id [, guid]) → ActResult", "cast it unless the aura is already active (ok with data.status = already_active then)" },
  go_to = { "{x, y [, z, tolerance = 4, tries = 3, unstuck = false, on_attacked = fn]} → ActResult", "walk there; when stuck it sidesteps and jumps between tries. unstuck = true adds a last resort (out of combat): the Stuck spell, which hearths you home (error unstuck_home). Pass z indoors and upstairs. Without on_attacked it walks on while mobs hit you and drags them along; with it, it stops whenever something targets you, calls on_attacked(attackers) (your fighting) and then carries on" },
  travel = { "{points = {{x, y [, z]}, ...} [, tolerance = 6, unstuck = false, on_attacked = fn]} → ActResult", "go_to each waypoint (e.g. from azworld route)" },
  danger = { "(x, y [, r = 25]) → Creature[]", "living hostile creatures within r yards of a point: check a spot before you go there or come back to life there" },
  approach = { "{unit [, range = 4, timeout = 60]} → ActResult<Creature>", "move within range of a unit and face it" },
  follow = { "{name [, distance = 5, until = fn]} → ActResult", "stay near a player until until() is true" },
  reach_npc = { "{npc [, x, y, z]} → ActResult<Creature>", "find an NPC by entry (walking to x, y, z first) and stand next to it" },
  accept = { "{npc, quest [, x, y, z]} → ActResult", "walk to a quest giver and accept the quest" },
  turn_in = { "{npc, quest [, reward = 0, x, y, z]} → ActResult", "walk to the quest ender and turn the quest in" },
  vendor = { "{npc [, x, y, z, buy = {{entry, count}, ...}]} → ActResult", "sell greys, repair if it can, buy; data = {sold, repaired, bought}" },
  train = { "{npc, spells [, x, y, z]} → ActResult", "learn spells at a trainer; data = {learned, failed}" },
  talk = { "{npc, fn [, x, y, z]} → ActResult", "walk to an NPC and run fn(npc)" },
  use_object = { "{entry [, count = 1, quest, x, y, z, radius = 80]} → ActResult", "use objects (crates, chests, quest objects) count times or until the quest is complete" },
  loot_nearby = { "{[dist = 8]} → ActResult<Item[]>", "walk to and loot every lootable corpse within dist; with full bags it destroys your cheapest grey for room (error bags_full when there's none)" },
  rest = { "{[hp = 0.95, mana = 0.9, max_s = 40]} → ActResult", "eat/drink if you carry food/water and wait until topped up; fails if attacked" },
  fly = { "{to = node id or name [, npc = entry]} → ActResult<Flight>", "walk to a flight master nearby, fly to a known destination and wait until you land" },
  release = { "() → ActResult", "release your spirit" },
  corpse_run = { "{[safe = true]} → ActResult", "as a ghost, walk to your corpse and reclaim it; with hostiles at the corpse it reclaims from the far side of the reclaim circle, 32 yd out (safe = false: on the spot)" },
  spirit_res = { "() → ActResult", "as a ghost, resurrect at the nearest spirit healer in sight (costs durability)" },
  recover = { "{[spirit = false, rest = true, safe = true]} → ActResult", "back to life: release, corpse run (reclaiming away from hostiles at the corpse), the spirit healer if that fails or with spirit = true (unless hostiles camp the graveyard: then the corpse run), then rest" },
}) do doc("act." .. name, d[1], d[2]) end
