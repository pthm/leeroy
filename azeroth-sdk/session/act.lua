-- act: utilities built into every session, so scripts spend their effort on strategy, not on the plumbing of
-- talking to the game. There is deliberately no combat AI here: no rotations, target picking, grinding loops, assist
-- or healing logic. How to fight, what to kill, when to rest and how to work with others is yours to build.
--
-- The doing functions (go_to, accept, vendor, rest, ...) wait until they're finished, so call them inside a task.
-- Each returns a result like an action request: {ok = true, data = ...} or {ok = false, error = {code, message}}.
--
--   task.spawn("quest 62", function()
--     act.must(act.accept{ npc = 240, quest = 62 })
--     ... your fighting ...
--     act.must(act.turn_in{ npc = 240, quest = 62 })
--   end)
--
-- Everything here uses the public bot API; read session/act.lua to see or copy how it works.

act = act or {}
local A = act
A.log = A.log or {}

local function now() return bot.now_ms() end
function A.ok(data) return { ok = true, data = data } end
function A.fail(code, message) return { ok = false, error = { code = code, message = message } } end
-- must(result): the result if it's ok, else raise its error (ends the task with it)
function A.must(r)
  if r and r.ok == false then error((r.op or "act") .. ": " .. r.error.code .. ": " .. tostring(r.error.message), 2) end
  return r
end
local function note(msg)
  A.log[#A.log + 1] = string.format("%d %s", math.floor(now() / 1000), msg)
  if #A.log > 30 then table.remove(A.log, 1) end
  bot.log("act: " .. msg)
end
A.note = note

local function pos() local x, y, z = bot.get_position() return x, y, z end
-- dist(x, y [, z]): yards from you (2D unless z is given)
function A.dist(x, y, z)
  local px, py, pz = pos()
  return math.sqrt((px - x) ^ 2 + (py - y) ^ 2 + ((z and (pz - z)) or 0) ^ 2)
end
local function frac(cur, max) return (max and max > 0) and cur / max or 1 end
function A.hp() local c, m = bot.get_health() return frac(c, m) end
-- mana fraction (1 for rage/energy users)
function A.mana()
  if bot.get_power_type() ~= 0 then return 1 end
  local c, m = bot.get_power() return frac(c, m)
end

-- ---------- casting helpers ----------
local known = {}
-- knows(id): cached KnowsSpell; call forget_spells() after training
function A.knows(id)
  if known[id] == nil then known[id] = bot.call("KnowsSpell", id) == true end
  return known[id]
end
function A.forget_spells() known = {} end
-- best({rank1, rank2, ...}): the highest rank you know of a spell, or nil
function A.best(ids)
  for i = #ids, 1, -1 do if A.knows(ids[i]) then return ids[i] end end
end
-- ready(id [, target]): off cooldown/GCD, enough power, not mid-cast
function A.ready(id, target)
  return id ~= nil and not bot.is_casting() and bot.is_spell_ready(id) and bot.can_cast(id, target or bot.get_own_guid())
end
-- cast(id [, target]): face the target and cast if ready. In a task it waits for the result ({ok, error}); in on_tick
-- it returns the pending request. Not ready: {ok = false, error = {code = "not_ready"}}. Range: bot.spell_info(id).range
function A.cast(id, target)
  target = target or bot.get_own_guid()
  if not A.ready(id, target) then return A.fail("not_ready", "on cooldown, casting, or not enough power") end
  if target ~= bot.get_own_guid() then bot.face_target(target) end
  return await(bot.cast_spell(id, target))
end
-- buff(id [, target]): cast it unless the target already has that aura (nil when there was nothing to do)
function A.buff(id, target)
  target = target or bot.get_own_guid()
  if not id or bot.has_aura_on(target, id) then return nil end
  return A.cast(id, target)
end
-- in_range(id, guid): within the spell's max range of the unit
function A.in_range(id, guid)
  local sp, u = bot.spell_info(id), bot.get_unit(guid)
  return sp ~= nil and u ~= nil and u.distance <= math.max(sp.range or 5, 5)
end

-- ---------- world helpers ----------
-- attackers(dist): living mobs targeting you
function A.attackers(dist)
  local out = {}
  for _, u in ipairs(bot.get_attackers(dist or 30)) do if not u.is_player then out[#out + 1] = u end end
  return out
end
-- tapped_by_other(unit): someone else (not you or your party) owns this mob: killing it gives you nothing
function A.tapped_by_other(u)
  local f = u.dyn_flags or 0
  return math.floor(f / 4) % 2 == 1 and math.floor(f / 8) % 2 == 0
end
-- npc(entry [, dist]): the nearest living unit with that entry
function A.npc(entry, dist)
  local best
  for _, u in ipairs(bot.get_nearby_units(dist or 60)) do
    if u.entry == entry and u.is_alive and (not best or u.distance < best.distance) then best = u end
  end
  return best
end
-- player(name): a player by name (nearby, or a party member), or nil
function A.player(name)
  for _, p in ipairs(bot.get_nearby_players(100)) do if p.name == name then return p end end
  for _, m in ipairs((bot.group() or {}).members or {}) do if m.name == name then return bot.get_unit(m.guid) end end
end
-- loot_nearby(dist): loot every lootable corpse within dist (default 8), one at a time; returns the items got
function A.loot_nearby(dist)
  local got = {}
  for _, u in ipairs(bot.get_nearby_units(dist or 8)) do
    if not u.is_alive and u.lootable then
      local r = await(bot.loot_all(u.guid))
      for _, it in ipairs((r.data or {}).items or {}) do got[#got + 1] = it end
    end
  end
  return got
end
-- consume("Food" | "Drink"): use the first item whose use-spell has that name; true if used (sit still after)
function A.consume(kind)
  for _, it in ipairs(bot.bag_items()) do
    if it.use_spell and it.use_spell > 0 and (it.bag ~= 255 or it.slot >= 23) then
      local sp = bot.spell_info(it.use_spell)
      if sp and sp.name == kind then return bot.use_item(it.guid).ok ~= false end
    end
  end
  return false
end
-- make_room(): destroy your cheapest grey item to free a bag slot; true if it did
function A.make_room()
  local cheapest
  for _, it in ipairs(bot.bag_items()) do
    if (it.bag ~= 255 or it.slot >= 23) and it.quality == 0 and (not cheapest or (it.sell_price or 0) < (cheapest.sell_price or 0)) then cheapest = it end
  end
  if cheapest then bot.destroy(cheapest.guid) note("destroyed " .. tostring(cheapest.name) .. " for bag space") return true end
  return false
end

-- ---------- movement (in a task: these wait) ----------
-- go_to(x, y [, z], {tolerance = 4, tries = 3}): walk there (indoors/upstairs: pass z). Retries after getting stuck.
function A.go_to(x, y, z, o)
  o = o or {}
  local tz = (z and bot.ground_z(x, y, z)) or bot.ground_z(x, y) or z or select(3, pos())
  local r
  for _ = 1, o.tries or 3 do
    if A.dist(x, y) <= (o.tolerance or 4) then bot.stop_moving() return A.ok({ distance = A.dist(x, y) }) end
    r = await(bot.move_to(x, y, tz), 600)
    if A.dist(x, y) <= (o.tolerance or 4) then return A.ok({ distance = A.dist(x, y) }) end
    if r.ok and r.data and r.data.stopped then return A.fail("stopped", "stop_moving() was called on the way") end
    if r.error.code == "superseded" then return r end
    wait(1)
  end
  return r
end
-- travel(waypoints): go_to each {x, y [, z]} in turn (e.g. the waypoints from `wowdb route`)
function A.travel(points, o)
  for i, p in ipairs(points) do
    local r = A.go_to(p[1], p[2], p[3], o or { tolerance = 6 })
    if not r.ok then r.error.message = "waypoint " .. i .. ": " .. tostring(r.error.message) return r end
  end
  return A.ok()
end
-- approach(guid, range): move until within range yards of a unit and face it; fails if it's gone
function A.approach(guid, range, timeout_s)
  local deadline = now() + (timeout_s or 60) * 1000
  while now() < deadline do
    local u = bot.get_unit(guid)
    if not u then return A.fail("unit_gone", "the unit is gone (dead, despawned or out of sight)") end
    if u.distance <= range then bot.stop_moving() bot.face_target(guid) return A.ok({ distance = u.distance }) end
    bot.move_to(u.x, u.y, u.z)
    wait(0.5)
  end
  return A.fail("timeout", "couldn't get within " .. range .. " yd")
end
-- follow{name, distance = 5, ["until"] = fn}: stay near a player until until() is true (forever without it)
function A.follow(o)
  while not (o["until"] and o["until"]()) do
    local p = A.player(o.name)
    if p and p.distance > (o.distance or 5) + 2 then bot.move_to(p.x, p.y, p.z) end
    wait(1)
  end
  return A.ok()
end

-- ---------- NPCs and objects ----------
-- reach_npc{npc = entry [, x, y, z]}: find the NPC nearby (walking to x, y, z first if given) and stand next to it
function A.reach_npc(o)
  local npc = A.npc(o.npc)
  if not npc and o.x and A.dist(o.x, o.y) > 8 then
    local r = A.go_to(o.x, o.y, o.z)
    if not r.ok then return nil, r end
    npc = A.npc(o.npc)
  end
  if not npc then return nil, A.fail("npc_not_found", "npc " .. tostring(o.npc) .. " not found nearby (pass its x, y, z from wowdb)") end
  if npc.distance > 4 then
    local r = A.go_to(npc.x, npc.y, npc.z, { tolerance = 3 })
    if not r.ok then return nil, r end
  end
  bot.stop_moving()
  return npc
end
local function has_quest(id) for _, q in ipairs(bot.quest_log()) do if q.id == id then return q end end end
-- accept{npc = entry, quest = id [, x, y, z]}
function A.accept(o)
  if has_quest(o.quest) then return A.ok() end
  local npc, err = A.reach_npc(o)
  if not npc then return err end
  local since = events.seq()
  for _ = 1, 3 do
    bot.call("QuestgiverHello", npc.guid)
    bot.call("QuestgiverAcceptQuest", npc.guid, o.quest)
    for _ = 1, 10 do
      wait(0.3)
      if has_quest(o.quest) then return A.ok() end
    end
  end
  local e = events.query{ since = since, types = { "error" }, match = "quest", last = 1 }[1]
  if e then return A.fail(e.data.code, e.data.message) end
  return A.fail("not_offered", "quest not offered (level, prerequisite, already done, or a different NPC)")
end
-- turn_in{npc = entry, quest = id [, reward = index, x, y, z]}
function A.turn_in(o)
  if not has_quest(o.quest) then return A.fail("not_in_log", "quest " .. o.quest .. " isn't in your quest log") end
  local npc, err = A.reach_npc(o)
  if not npc then return err end
  return await(bot.turn_in(npc.guid, o.quest, o.reward or 0))
end
-- vendor{npc = entry [, x, y, z, buy = {{entry, count}, ...}]}: sell grey items, repair if it repairs, buy.
-- Returns {ok, data = {sold, repaired, bought = {results}}}
function A.vendor(o)
  local npc, err = A.reach_npc(o)
  if not npc then return err end
  local out = { sold = bot.sell_junk(npc.guid), bought = {} }
  bot.vendor_items(npc.guid)
  wait(1.5)
  if math.floor(npc.npc_flags / 0x1000) % 2 == 1 then out.repaired = await(bot.repair(npc.guid)) end
  for _, b in ipairs(o.buy or {}) do out.bought[#out.bought + 1] = await(bot.buy(npc.guid, b[1], b[2] or 1)) end
  out.sold = out.sold + bot.sell_junk(npc.guid)
  return A.ok(out)
end
-- train{npc = entry, spells = {ids} [, x, y, z]}: {ok, data = {learned = {ids}, failed = {results}}}
function A.train(o)
  local npc, err = A.reach_npc(o)
  if not npc then return err end
  local learned, failed = {}, {}
  for _, sp in ipairs(o.spells or {}) do
    local r = await(bot.train(npc.guid, sp))
    if r.ok then learned[#learned + 1] = sp else failed[#failed + 1] = r end
  end
  A.forget_spells()
  return A.ok({ learned = learned, failed = failed })
end
-- talk{npc = entry, fn = function(npc) ... end [, x, y, z]}: walk to an NPC and do anything with it
function A.talk(o)
  local npc, err = A.reach_npc(o)
  if not npc then return err end
  return A.ok(o.fn and o.fn(npc))
end
-- use_object{entry, count = 1 [, quest = id (until complete), x, y, z, radius]}: crates, chests, quest objects
function A.use_object(o)
  local used = 0
  while true do
    if o.quest then
      local q = has_quest(o.quest)
      if not q or q.complete then return A.ok({ used = used }) end
    elseif used >= (o.count or 1) then return A.ok({ used = used }) end
    local g = bot.call("FindGameObjectByEntry", o.entry, o.radius or 80)
    if not g or g == 0 or g == "0" then
      if o.x and A.dist(o.x, o.y) > 10 then
        local r = A.go_to(o.x, o.y, o.z)
        if not r.ok then return r end
      else
        return A.fail("not_found", "no object " .. o.entry .. " in range")
      end
    else
      local ob = bot.call("GetObject", g)
      if ob and A.dist(ob.PosX, ob.PosY) > 4 then
        local r = A.go_to(ob.PosX, ob.PosY, ob.PosZ, { tolerance = 3 })
        if not r.ok then return r end
      end
      bot.stop_moving()
      local r = await(bot.use_object(g))
      if not r.ok then return r end
      used = used + 1
      wait(1)
    end
  end
end

-- ---------- recovery ----------
-- rest{hp = 0.95, mana = 0.9, max_s = 40}: eat/drink if you carry food/water, wait until topped up (stops if attacked)
function A.rest(o)
  o = o or {}
  local start, ate, drank = now(), false, false
  bot.stop_moving()
  while A.hp() < (o.hp or 0.95) or A.mana() < (o.mana or 0.9) do
    if bot.in_combat() then return A.fail("attacked", "attacked while resting") end
    if now() - start > (o.max_s or 40) * 1000 then break end
    if not ate and A.hp() < (o.hp or 0.95) then ate = A.consume("Food") end
    if not drank and A.mana() < (o.mana or 0.9) then drank = A.consume("Drink") end
    wait(1)
  end
  return A.ok({ hp = A.hp(), mana = A.mana() })
end
-- release(): release your spirit (ok once you're a ghost)
function A.release()
  for _ = 1, 6 do
    if bot.is_ghost() or bot.is_alive() then return A.ok() end
    bot.call("RepopRequest")
    wait(5)
  end
  return A.fail("timeout", "still not released")
end
-- corpse_run(): as a ghost, walk back to your corpse and reclaim it when allowed
function A.corpse_run()
  while not bot.is_alive() do
    local cx, cy, cz, left = bot.corpse_position()
    if not cx then wait(1) -- asked the server
    elseif A.dist(cx, cy) > 20 then
      local r = A.go_to(cx, cy, cz, { tolerance = 15 })
      if not r.ok then return r end
    elseif (left or 0) > 0 then wait(left)
    else bot.call("ReclaimCorpse") wait(2) end
  end
  return A.ok()
end
-- spirit_res(): as a ghost, walk to the nearest spirit healer in sight and resurrect there (costs durability, and
-- resurrection sickness from level 11)
function A.spirit_res()
  while not bot.is_alive() do
    local healer
    for _, u in ipairs(bot.get_nearby_units(60)) do
      if math.floor(u.npc_flags / 0x4000) % 2 == 1 then healer = u end
    end
    if not healer then return A.fail("not_found", "no spirit healer in sight") end
    if healer.distance > 5 then A.approach(healer.guid, 5) else bot.spirit_res(healer.guid) wait(2) end
  end
  return A.ok()
end

-- the functions above that wait need a task: say so plainly instead of failing deep inside
for _, n in ipairs({ "go_to", "travel", "approach", "follow", "reach_npc", "accept", "turn_in", "vendor", "train", "talk",
  "use_object", "rest", "release", "corpse_run", "spirit_res", "loot_nearby" }) do
  local f = A[n]
  A[n] = function(...)
    if not task.current() then
      error("act." .. n .. " waits for the game, so it runs inside a task: task.spawn(\"" .. n .. "\", function() act." .. n .. "(...) end)", 2)
    end
    return f(...)
  end
end

-- in_los(guid) or in_los(x, y, z): nothing solid between your eyes and the target (as the server checks spells)
function bot.in_los(a, y, z)
  if y then return bot.los_point(a, y, z) end
  return bot.los_unit(a)
end

-- ---------- trade ----------
-- bot.trade_offer{items = {entry or guid, ...}, money = copper}: set your side of an open trade window (replaces it)
function bot.trade_offer(o)
  o = o or {}
  for slot = 0, 5 do
    local item = (o.items or {})[slot + 1]
    local err = bot.trade_item(slot, item or 0)
    if err then return err end
  end
  return bot.trade_money(o.money or 0)
end

-- ---------- status snapshot (`wow status`) ----------
function bot.status()
  local x, y, z = bot.get_position()
  local h, hm = bot.get_health()
  local p, pm = bot.get_power()
  local xp, xpn = bot.get_xp()
  local s = { name = bot.get_name(), level = bot.get_level(), xp = xp, xp_next = xpn, hp = h, hp_max = hm, power = p, power_max = pm,
    money = bot.money(), pos = { math.floor(x), math.floor(y), math.floor(z) }, where = bot.location(),
    alive = bot.is_alive(), ghost = bot.is_ghost(), combat = bot.in_combat(), resting = bot.is_resting(),
    free_slots = bot.free_slots(), quests = {}, stats = bot.stats() }
  local c = bot.is_casting()
  if c then s.casting = c end
  local broken = {}
  for _, it in ipairs(bot.broken_gear()) do broken[#broken + 1] = it.name or it.entry end
  if #broken > 0 then s.broken_gear = broken end
  for _, q in ipairs(bot.quest_log()) do s.quests[#s.quests + 1] = q.complete and (q.id .. " complete") or (q.id .. " " .. table.concat(q.counts, "/")) end
  local t = bot.get_target()
  if t and t ~= "0" then
    local u = bot.get_unit(t)
    if u then s.target = { entry = u.entry, level = u.level, hp = math.floor(100 * frac(u.health, u.max_health)) .. "%", dist = math.floor(u.distance) } end
  end
  local att = A.attackers(30)
  if #att > 0 then s.attackers = #att end
  local g = bot.group()
  if g then
    s.group = { leader = g.leader, members = {} }
    for _, m in ipairs(g.members) do
      local u = bot.get_unit(m.guid)
      s.group.members[#s.group.members + 1] = u and string.format("%s %d%% %dyd", m.name, math.floor(100 * frac(u.health, u.max_health)), math.floor(u.distance)) or (m.name .. " far")
    end
  end
  local near = {}
  for _, pl in ipairs(bot.get_nearby_players(60)) do
    local i = bot.inspect(pl.guid)
    near[#near + 1] = string.format("%s L%d %s %dyd", pl.name ~= "" and pl.name or pl.guid, pl.level, (i and i.class) or "?", math.floor(pl.distance))
  end
  if #near > 0 then s.players = near end
  -- hostile mobs within 30 yd (not just the ones attacking you): how many, the highest level, elites
  local mobs, hi, elites = 0, 0, 0
  for _, u in ipairs(bot.get_nearby_units(30)) do
    local untargetable = math.floor((u.flags or 0) / 0x2000000) % 2 == 1 or math.floor((u.flags or 0) / 2) % 2 == 1 -- triggers, dummies
    if u.is_alive and not u.is_player and not untargetable and u.level >= s.level - 5 and bot.reaction(u.guid) == "hostile" then
      mobs = mobs + 1
      if u.level > hi then hi = u.level end
      if u.max_health > 3 * u.level * 15 then elites = elites + 1 end -- ponytail: elites have several times normal health
    end
  end
  if mobs > 0 then s.mobs_nearby = { count = mobs, max_level = hi, elites = elites > 0 and elites or nil } end
  -- usable items in your bags that beat what you wear (by item level)
  local up = {}
  for _, it in ipairs(bot.bag_items()) do
    if (it.bag ~= 255 or it.slot >= 23) and it.usable then
      local c = bot.compare(it.guid)
      if c and c.usable and c.item_level > 0 then up[#up + 1] = string.format("%s (+%d ilvl %s)", it.name or it.entry, c.item_level, c.slot) end
    end
  end
  if #up > 0 then s.bag_upgrades = up end
  -- your last minute of fighting
  for _, m in ipairs(bot.meter(60)) do
    if m.guid == bot.get_own_guid() and (m.damage > 0 or m.healing > 0) then
      s.last_minute = { dps = m.dps, hps = m.hps, damage = m.damage, healing = m.healing, kills = m.kills }
    end
  end
  for _, m in ipairs(bot.meter(60)) do
    if m.guid == bot.get_own_guid() and m.taken > 0 then s.last_minute = s.last_minute or {} s.last_minute.taken = m.taken end
  end
  if not s.alive then local cx, cy, _, w = bot.corpse_position() if cx then s.corpse = { math.floor(cx), math.floor(cy), w } end end
  local e = bot.last_cast_error()
  if e and now() - e.time_ms < 15000 then s.last_cast_error = e.reason .. (e.hint and (" (" .. e.hint .. ")") or "") end
  local tasks = {}
  for _, t in ipairs(task.list()) do
    if t.state ~= "done" then tasks[#tasks + 1] = t.name .. " " .. t.state .. (t.status and (": " .. t.status) or "") .. (t.error and (" " .. t.error) or "") end
  end
  if #tasks > 0 then s.tasks = tasks end
  return s
end
