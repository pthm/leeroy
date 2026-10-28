-- Minimal hand-written agent: fight the nearest attackable mob, loot it, repeat.
-- on_tick() runs every 200ms. Load with: wow -f example.lua
local target, kills = nil, 0

local function attackable(u)
  -- ponytail: naive filter; doesn't know guards/critters/factions — a real agent should refine this
  return u.is_alive and not u.is_player and u.npc_flags == 0 and u.level > 0
end

local function nearest_mob(max_dist)
  local best
  for _, u in ipairs(bot.get_nearby_units(max_dist)) do
    if attackable(u) and (not best or u.distance < best.distance) then best = u end
  end
  return best
end

function on_tick()
  if not bot.is_alive() then return end -- death handling is left to you
  if bot.consume_teleport() then target = nil end

  if not bot.in_combat() then
    for _, u in ipairs(bot.get_nearby_units(5)) do
      if u.lootable then bot.loot_all(u.guid) end
    end
  end

  local t = target and bot.get_unit(target)
  if t and not t.is_alive then
    kills = kills + 1
    bot.log(string.format("kill #%d: entry %d", kills, t.entry))
    target, t = nil, nil
  end

  if not t then
    t = nearest_mob(80)
    if not t then return end
    target = t.guid
    bot.log(string.format("engaging entry %d (lvl %d) at %.0fy", t.entry, t.level, t.distance))
  end

  if t.distance > 4 then
    bot.move_to(t.x, t.y, t.z) -- pathfinds via navmesh
  else
    if bot.is_moving() then bot.stop_moving() end
    bot.face_target(target)
    bot.attack(target)
  end
end
