-- act: utilities built into every session, so scripts spend their effort on strategy, not on the plumbing of
-- talking to the game. There is deliberately no combat AI here: no rotations, target picking, grinding loops, assist
-- or healing logic. How to fight, what to kill, when to rest and how to work with others is yours to build.
--
-- A job is a table with tick(self) returning "running", "done" or "failed" (+ a message). act.run{job, job, ...} runs
-- jobs in order from on_tick; act.status() says where it is. Your own logic fits in with act.fn(function(self) ... end).
--
--   act.run{ act.go_to(-9460, 70), act.accept{ npc = 240, quest = 62 }, act.fn(my_fight_step), act.turn_in{ npc = 240, quest = 62 } }
--
-- Everything here uses the public bot API; read session/act.lua to see or copy how it works.

act = act or {}
local A = act
A.log = A.log or {}

local function now() return bot.now_ms() end
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
-- cast(id [, target]): face the target and cast if ready; true if sent. Range: bot.spell_info(id).range
function A.cast(id, target)
  target = target or bot.get_own_guid()
  if not A.ready(id, target) then return false end
  if target ~= bot.get_own_guid() then bot.face_target(target) end
  bot.cast_spell(id, target)
  return true
end
-- buff(id [, target]): cast it unless the target already has that aura
function A.buff(id, target)
  target = target or bot.get_own_guid()
  if not id or bot.has_aura_on(target, id) then return false end
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
-- loot_nearby(dist): loot every lootable corpse within dist (default 8); returns how many
function A.loot_nearby(dist)
  local n = 0
  for _, u in ipairs(bot.get_nearby_units(dist or 8)) do if not u.is_alive and u.lootable then bot.loot_all(u.guid) n = n + 1 end end
  return n
end
-- consume("Food" | "Drink"): use the first item whose use-spell has that name; true if used (sit still after)
function A.consume(kind)
  for _, it in ipairs(bot.bag_items()) do
    if it.use_spell and it.use_spell > 0 and (it.bag ~= 255 or it.slot >= 23) then
      local sp = bot.spell_info(it.use_spell)
      if sp and sp.name == kind then bot.use_item(it.guid) return true end
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

-- ---------- jobs: movement ----------
local function job(name, t) t.name = name return t end

-- go_to(x, y [, z], {tolerance = 4}): walk there (indoors/upstairs: pass z); fails if no progress for 15s or no path
function A.go_to(x, y, z, o)
  o = o or {}
  return job(string.format("go_to %d,%d", x, y), { tick = function(self)
    local d = A.dist(x, y)
    if d <= (o.tolerance or 4) then bot.stop_moving() return "done" end
    if self.best == nil or d < self.best - 2 then self.best, self.progress_at = d, now() end
    if now() - self.progress_at > 15000 then return "failed", "stuck " .. math.floor(d) .. " yd away" end
    -- re-send every 3s (every tick stalls movement), or 2s after a failed/finished move
    if now() - (self.sent or 0) > 3000 or (not bot.is_moving() and now() - (self.sent or 0) > 2000) then
      self.sent = now()
      local tz = (z and bot.ground_z(x, y, z)) or bot.ground_z(x, y) or z or select(3, pos())
      if not bot.move_to(x, y, tz) then
        self.nopath = (self.nopath or 0) + 1
        if self.nopath > 5 then return "failed", "no path" end
      end
    end
    return "running"
  end })
end
-- travel(waypoints): go_to each {x, y [, z]} in turn (e.g. the waypoints from `wowdb route`)
function A.travel(points, o)
  local jobs = {}
  for _, p in ipairs(points) do jobs[#jobs + 1] = A.go_to(p[1], p[2], p[3], o or { tolerance = 6 }) end
  return A.seq(jobs, "travel")
end
-- approach(guid, range): move until within range yards of a unit and face it (done), fails if it's gone
function A.approach(guid, range)
  return job("approach", { tick = function(self)
    local u = bot.get_unit(guid)
    if not u then return "failed", "unit gone" end
    if u.distance <= range then bot.stop_moving() bot.face_target(guid) return "done" end
    if now() - (self.sent or 0) > 800 then self.sent = now() bot.move_to(u.x, u.y, u.z) end
    return "running"
  end })
end
-- follow{name, distance = 5, ["until"] = fn}: stay near a player; done when until() is true (runs forever otherwise)
function A.follow(o)
  return job("follow " .. o.name, { tick = function(self)
    if o["until"] and o["until"]() then return "done" end
    local p = A.player(o.name)
    if p and p.distance > (o.distance or 5) + 2 and now() - (self.sent or 0) > 1000 then
      self.sent = now()
      bot.move_to(p.x, p.y, p.z)
    end
    return "running"
  end })
end

-- ---------- jobs: NPCs and objects ----------
-- with_npc: find the NPC by entry nearby (walking to x, y, z first if given), stand next to it, then fn(self, npc)
local function with_npc(name, o, fn)
  return job(name, { tick = function(self)
    if self.sub then
      local st, msg = self.sub:tick()
      if st == "running" then return "running" end
      self.sub = nil
      if st == "failed" then return "failed", msg end
    end
    local npc = A.npc(o.npc)
    if not npc then
      if o.x and A.dist(o.x, o.y) > 8 then self.sub = A.go_to(o.x, o.y, o.z) return "running" end
      return "failed", "npc " .. tostring(o.npc) .. " not found nearby (pass its x, y, z from wowdb)"
    end
    if npc.distance > 4 then self.sub = A.go_to(npc.x, npc.y, npc.z, { tolerance = 3 }) return "running" end
    bot.stop_moving()
    return fn(self, npc)
  end })
end
local function has_quest(id) for _, q in ipairs(bot.quest_log()) do if q.id == id then return q end end end
-- accept{npc = entry, quest = id [, x, y, z]}
function A.accept(o)
  return with_npc("accept " .. o.quest, o, function(self, npc)
    if has_quest(o.quest) then return "done" end
    self.tries = (self.tries or 0) + 1
    if self.tries > 25 then return "failed", "quest not offered (level, prerequisite, or already done?)" end
    if self.tries % 5 == 1 then bot.call("QuestgiverHello", npc.guid) bot.call("QuestgiverAcceptQuest", npc.guid, o.quest) end
    return "running"
  end)
end
-- turn_in{npc = entry, quest = id [, reward = index, x, y, z]}
function A.turn_in(o)
  return with_npc("turn_in " .. o.quest, o, function(self, npc)
    if not has_quest(o.quest) then return "done" end
    self.tries = (self.tries or 0) + 1
    if self.tries > 25 then return "failed", "turn-in refused (incomplete, wrong NPC, or bags full)" end
    if self.tries % 5 == 1 then bot.turn_in(npc.guid, o.quest, o.reward or 0) end
    return "running"
  end)
end
-- vendor{npc = entry [, x, y, z, buy = {{entry, count}, ...}]}: sell grey items, repair if it repairs, buy
function A.vendor(o)
  return with_npc("vendor", o, function(self, npc)
    if not self.at then bot.sell_junk(npc.guid) bot.vendor_items(npc.guid) self.at = now() return "running" end
    if now() - self.at < 1500 then return "running" end
    if math.floor(npc.npc_flags / 0x1000) % 2 == 1 then bot.repair(npc.guid) end
    for _, b in ipairs(o.buy or {}) do bot.buy(npc.guid, b[1], b[2] or 1) end
    bot.sell_junk(npc.guid)
    return "done"
  end)
end
-- train{npc = entry, spells = {ids} [, x, y, z]}
function A.train(o)
  return with_npc("train", o, function(self, npc)
    for _, sp in ipairs(o.spells or {}) do bot.train(npc.guid, sp) end
    A.forget_spells()
    return "done"
  end)
end
-- talk{npc = entry, fn = function(npc) ... end [, x, y, z]}: walk to an NPC and do anything with it
function A.talk(o) return with_npc("talk " .. tostring(o.npc), o, function(_, npc) if o.fn then o.fn(npc) end return "done" end) end
-- use_object{entry, count = 1 [, quest = id (until complete), x, y, z, radius]}: crates, chests, quest objects
function A.use_object(o)
  return job("use_object " .. o.entry, { used = 0, tick = function(self)
    if self.sub then
      local st = self.sub:tick()
      if st == "running" then return "running" end
      self.sub = nil
    end
    if o.quest then local q = has_quest(o.quest) if not q or q.complete then return "done" end
    elseif self.used >= (o.count or 1) then return "done" end
    if bot.is_casting() or (self.wait or 0) > now() then return "running" end
    local g = bot.call("FindGameObjectByEntry", o.entry, o.radius or 80)
    if not g or g == 0 or g == "0" then
      if o.x and A.dist(o.x, o.y) > 10 then self.sub = A.go_to(o.x, o.y, o.z) return "running" end
      return "failed", "no object " .. o.entry .. " in range"
    end
    local ob = bot.call("GetObject", g)
    if ob and A.dist(ob.PosX, ob.PosY) > 4 then self.sub = A.go_to(ob.PosX, ob.PosY, ob.PosZ, { tolerance = 3 }) return "running" end
    bot.stop_moving()
    bot.use_object(g)
    self.used, self.wait = self.used + 1, now() + 3500
    return "running"
  end })
end

-- ---------- jobs: recovery ----------
-- rest{hp = 0.95, mana = 0.9, max_s = 40}: eat/drink if you carry food/water, wait until topped up (stops if attacked)
function A.rest(o)
  o = o or {}
  return job("rest", { tick = function(self)
    self.start = self.start or now()
    if (A.hp() >= (o.hp or 0.95) and A.mana() >= (o.mana or 0.9)) or now() - self.start > (o.max_s or 40) * 1000 or bot.in_combat() then return "done" end
    if bot.is_moving() then bot.stop_moving() end
    if not self.ate and A.hp() < (o.hp or 0.95) then self.ate = A.consume("Food") end
    if not self.drank and A.mana() < (o.mana or 0.9) then self.drank = A.consume("Drink") end
    return "running"
  end })
end
-- release(): release your spirit (done once you're a ghost)
function A.release()
  return job("release", { tick = function(self)
    if bot.is_ghost() or bot.is_alive() then return "done" end
    if now() - (self.sent or 0) > 5000 then self.sent = now() bot.call("RepopRequest") end
    return "running"
  end })
end
-- corpse_run(): as a ghost, walk back to your corpse and reclaim it when allowed
function A.corpse_run()
  return job("corpse_run", { tick = function(self)
    if bot.is_alive() then return "done" end
    local cx, cy, cz, wait = bot.corpse_position()
    if not cx then return "running" end -- asked the server; known next tick
    if A.dist(cx, cy) > 20 then
      if now() - (self.sent or 0) > 3000 or not bot.is_moving() then self.sent = now() bot.move_to(cx, cy, cz) end
    elseif (wait or 0) <= 0 then bot.call("ReclaimCorpse") end
    return "running"
  end })
end
-- spirit_res(): as a ghost, walk to the nearest spirit healer in sight and resurrect there (costs durability, and
-- resurrection sickness from level 11)
function A.spirit_res()
  return job("spirit_res", { tick = function()
    if bot.is_alive() then return "done" end
    for _, u in ipairs(bot.get_nearby_units(60)) do
      if math.floor(u.npc_flags / 0x4000) % 2 == 1 then
        if u.distance > 5 then bot.move_to(u.x, u.y, u.z) else bot.spirit_res(u.guid) end
        return "running"
      end
    end
    return "failed", "no spirit healer in sight"
  end })
end

-- ---------- control flow ----------
function A.seq(jobs, name)
  return job(name or "seq", { i = 1, tick = function(self)
    local j = jobs[self.i]
    if not j then return "done" end
    local st, msg = j:tick()
    if st == "done" then self.i = self.i + 1 elseif st == "failed" then return "failed", j.name .. ": " .. tostring(msg) end
    return "running"
  end, current = function(self) return jobs[self.i] end })
end
function A.wait(seconds) return job("wait", { tick = function(self) self.t = self.t or now() return now() - self.t >= seconds * 1000 and "done" or "running" end }) end
-- fn(f [, name]): your own step; f(self) returns true when done, false/nil to keep going, or "failed", msg
function A.fn(f, name)
  return job(name or "fn", { tick = function(self)
    local r, msg = f(self)
    if r == true then return "done" elseif r == false or r == nil then return "running" end
    return r, msg
  end })
end
-- try(job): never fails (logs and moves on)
function A.try(j) return job(j.name, { inner = j, tick = function() local st, msg = j:tick() if st == "failed" then note(j.name .. " skipped: " .. tostring(msg)) return "done" end return st end }) end

-- ---------- runner ----------
-- run(plan [, {loop = true, on_fail = "skip" | "stop"}]): plan is a job, a list of jobs, or a function returning
-- one (needed for loop = true: jobs keep state, so each lap needs fresh ones). Failed steps are logged and skipped
-- unless on_fail = "stop". Replaces on_tick; call act.tick() from your own on_tick to wrap it instead.
-- The runner does nothing about combat or death: put that in your own steps or on_tick.
function A.run(plan, o)
  o = o or {}
  A.source = nil
  if type(plan) == "function" then A.source = plan plan = plan() end
  if plan.tick == nil then
    local jobs = {}
    for _, j in ipairs(plan) do jobs[#jobs + 1] = (o.on_fail == "stop") and j or A.try(j) end
    plan = A.seq(jobs, "plan")
  end
  A.plan, A.opts, A.state, A.result = plan, o, "running", nil
  note("run " .. plan.name)
  on_tick = A.tick
end
function A.stop() A.plan, A.state = nil, "stopped" if on_tick == A.tick then on_tick = nil end bot.stop_moving() end
function A.tick()
  if not A.plan then return end
  local st, msg = A.plan:tick()
  if st ~= "running" then
    note(A.plan.name .. " " .. st .. (msg and (": " .. tostring(msg)) or ""))
    A.state, A.result = st, msg
    A.plan = nil
    if st == "done" and A.opts and A.opts.loop and A.source then A.run(A.source, A.opts) end
  end
end
function A.status()
  local cur = A.plan and A.plan.current and A.plan:current()
  cur = cur and (cur.inner or cur)
  return { state = A.state, plan = A.plan and A.plan.name, step = cur and cur.name, step_index = A.plan and A.plan.i,
    sub = cur and cur.sub and cur.sub.name, result = A.result, log = { table.unpack(A.log, math.max(1, #A.log - 5)) } }
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
  local s = { level = bot.get_level(), xp = xp, xp_next = xpn, hp = h, hp_max = hm, power = p, power_max = pm,
    money = bot.money(), pos = { math.floor(x), math.floor(y), math.floor(z) }, where = bot.location(),
    alive = bot.is_alive(), ghost = bot.is_ghost(), combat = bot.in_combat(), resting = bot.is_resting(),
    free_slots = bot.free_slots(), chat_pending = bot.chat_pending(), quests = {} }
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
  if A.plan or A.state then s.act = A.status() end
  return s
end
