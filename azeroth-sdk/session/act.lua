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
  return { ok = false, op = op, error = { code = code, message = message } }
end
function A.ok(data) return { ok = true, data = data } end
function A.fail(code, message) return { ok = false, error = { code = code, message = message } } end
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
function A.make_room()
  local cheapest
  for _, it in ipairs(items.list()) do
    if not it.equipped and it.quality == "poor" and (not cheapest or it.sell_price < cheapest.sell_price) then cheapest = it end
  end
  if cheapest then items.destroy(cheapest.guid) log("act: destroyed " .. cheapest.name .. " for bag space") return true end
  return false
end
-- cast(id [, guid]): face the target and cast if ready; in a task it waits for the result
function A.cast(id, guid)
  guid = guid or me.guid()
  if not A.ready(id, guid) then return result("act.cast", false, nil, "not_ready", "on cooldown, casting, or not enough power") end
  if guid ~= me.guid() then move.face(guid) end
  return await(spells.cast(id, guid))
end
function A.buff(id, guid)
  guid = guid or me.guid()
  if not id or units.has_aura(guid, id) then return nil end
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

function A.go_to(o)
  local tol = o.tolerance or 4
  local z = (o.z and world.ground_z(o.x, o.y, o.z)) or world.ground_z(o.x, o.y) or o.z or me.position().z
  local r
  for n = 1, o.tries or 3 do
    if A.dist(o.x, o.y) <= tol then move.stop() return result("act.go_to", true, { distance = A.dist(o.x, o.y) }) end
    r = await(move.to(o.x, o.y, z), 600)
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
    local r = A.go_to{ x = p[1] or p.x, y = p[2] or p.y, z = p[3] or p.z, tolerance = o.tolerance or 6, unstuck = o.unstuck }
    if not r.ok then return result("act.travel", false, { waypoint = i }, r.error.code, "waypoint " .. i .. ": " .. tostring(r.error.message)) end
  end
  return result("act.travel", true)
end
function A.approach(o)
  local range, deadline = o.range or 4, now() + (o.timeout or 60) * 1000
  while now() < deadline do
    local u = units.get(o.unit)
    if not u then return result("act.approach", false, nil, "unit_gone", "the unit is gone (dead and despawned, or out of sight)") end
    if u.distance <= range then move.stop() move.face(o.unit) return result("act.approach", true, u) end
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
  out.op = op
  return out
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
function A.vendor(o)
  return at_npc("act.vendor", o, function(npc)
    local out = { sold = await(vendor.sell_junk(npc.guid)), bought = {} }
    vendor.items(npc.guid)
    wait(1.5)
    for _, role in ipairs(npc.roles or {}) do if role == "repair" then out.repaired = await(vendor.repair(npc.guid)) end end
    for _, b in ipairs(o.buy or {}) do out.bought[#out.bought + 1] = await(vendor.buy(npc.guid, b[1], b[2] or 1)) end
    return A.ok(out)
  end)
end
function A.train(o)
  return at_npc("act.train", o, function(npc)
    local learned, failed = {}, {}
    for _, sp in ipairs(o.spells or {}) do
      local r = await(trainer.learn(npc.guid, sp))
      if r.ok then learned[#learned + 1] = sp else failed[#failed + 1] = r end
    end
    return A.ok({ learned = learned, failed = failed })
  end)
end
function A.talk(o) return at_npc("act.talk", o, function(npc) return A.ok(o.fn and o.fn(npc)) end) end
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
        if not r.ok then r.op = "act.use_object" return r end
      else
        return result("act.use_object", false, { used = used }, "not_found", "no object " .. o.entry .. " in range")
      end
    else
      if ob.distance > 4 then
        local r = A.go_to{ x = ob.x, y = ob.y, z = ob.z, tolerance = 3 }
        if not r.ok then r.op = "act.use_object" return r end
      end
      move.stop()
      local r = await(objects.use(ob.guid))
      if not r.ok then r.op = "act.use_object" return r end
      used = used + 1
      wait(1)
    end
  end
end
function A.loot_nearby(o)
  local got = {}
  for _, u in ipairs(units.nearby((o and o.dist) or 8)) do
    if not u.alive and u.lootable then
      local r = await(items.loot(u.guid))
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
    if not r.ok then r.op = "act.fly" return r end
  end
  local m = await(flights.map(master.guid))
  if not m.ok then m.op = "act.fly" return m end
  local dest
  for _, n in ipairs(m.data.destinations) do
    if n.id == o.to or (type(o.to) == "string" and n.name:lower():find(o.to:lower(), 1, true)) then dest = dest or n end
  end
  if not dest then return result("act.fly", false, m.data, "unknown_destination", "not a known destination from here: " .. tostring(o.to)) end
  local r = await(flights.take(master.guid, dest.id), 1800)
  r.op = "act.fly"
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
function A.release() local r = await(death.release()) r.op = "act.release" return r end
function A.corpse_run()
  while not me.alive() do
    local c = death.corpse()
    if not c then wait(1) -- asked the server
    elseif A.dist(c.x, c.y) > 20 then
      local r = A.go_to{ x = c.x, y = c.y, z = c.z, tolerance = 15 }
      if not r.ok then r.op = "act.corpse_run" return r end
    elseif c.reclaim_in > 0 then wait(c.reclaim_in)
    else await(death.reclaim()) end
  end
  return result("act.corpse_run", true)
end
function A.spirit_res()
  while not me.alive() do
    local healer
    for _, u in ipairs(units.nearby(60)) do
      for _, role in ipairs(u.roles or {}) do if role == "spirit_healer" then healer = healer or u end end
    end
    if not healer then return result("act.spirit_res", false, nil, "not_found", "no spirit healer in sight") end
    if healer.distance > 5 then A.approach{ unit = healer.guid, range = 5 } else await(death.spirit_res(healer.guid)) end
  end
  return result("act.spirit_res", true)
end

-- the functions above that wait need a task: say so plainly instead of failing deep inside
for _, n in ipairs({ "go_to", "travel", "approach", "follow", "reach_npc", "accept", "turn_in", "vendor", "train", "talk",
  "use_object", "rest", "release", "corpse_run", "spirit_res", "loot_nearby", "fly" }) do
  local f = A[n]
  A[n] = function(...)
    if not task.current() then
      error("act." .. n .. " waits for the game, so it runs inside a task: task.spawn(\"" .. n .. "\", function() act." .. n .. "{...} end)", 2)
    end
    return f(...)
  end
end

-- reference (help("act"))
for name, d in pairs({
  ok = { "(data) → result", "an ok result for your own act-style functions" },
  fail = { "(code, message) → result", "a failed result" },
  must = { "(result) → result", "the result if ok, else raise its error (ends the task)" },
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
  cast = { "(id [, guid]) → result", "face the target and cast if ready (waits for the result in a task)" },
  buff = { "(id [, guid]) → result", "cast it unless the target already has the aura (nil then)" },
  go_to = { "{x, y [, z, tolerance = 4, tries = 3, unstuck = false]} → result", "walk there; when stuck it sidesteps and jumps between tries. unstuck = true adds a last resort (out of combat): the Stuck spell, which hearths you home (error unstuck_home). Pass z indoors and upstairs" },
  travel = { "{points = {{x, y [, z]}, ...} [, tolerance = 6, unstuck = false]} → result", "go_to each waypoint (e.g. from azworld route)" },
  approach = { "{unit [, range = 4, timeout = 60]} → result<Creature>", "move within range of a unit and face it" },
  follow = { "{name [, distance = 5, until = fn]} → result", "stay near a player until until() is true" },
  reach_npc = { "{npc [, x, y, z]} → result<Creature>", "find an NPC by entry (walking to x, y, z first) and stand next to it" },
  accept = { "{npc, quest [, x, y, z]} → result", "walk to a quest giver and accept the quest" },
  turn_in = { "{npc, quest [, reward = 0, x, y, z]} → result", "walk to the quest ender and turn the quest in" },
  vendor = { "{npc [, x, y, z, buy = {{entry, count}, ...}]} → result", "sell greys, repair if it can, buy; data = {sold, repaired, bought}" },
  train = { "{npc, spells [, x, y, z]} → result", "learn spells at a trainer; data = {learned, failed}" },
  talk = { "{npc, fn [, x, y, z]} → result", "walk to an NPC and run fn(npc)" },
  use_object = { "{entry [, count = 1, quest, x, y, z, radius = 80]} → result", "use objects (crates, chests, quest objects) count times or until the quest is complete" },
  loot_nearby = { "{[dist = 8]} → result<Item[]>", "loot every lootable corpse nearby" },
  rest = { "{[hp = 0.95, mana = 0.9, max_s = 40]} → result", "eat/drink if you carry food/water and wait until topped up; fails if attacked" },
  fly = { "{to = node id or name [, npc = entry]} → result<Flight>", "walk to a flight master nearby, fly to a known destination and wait until you land" },
  release = { "() → result", "release your spirit" },
  corpse_run = { "() → result", "as a ghost, walk to your corpse and reclaim it" },
  spirit_res = { "() → result", "as a ghost, resurrect at the nearest spirit healer in sight (costs durability)" },
}) do doc("act." .. name, d[1], d[2]) end
