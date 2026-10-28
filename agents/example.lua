-- Minimal hand-written agent: fight the nearest hostile mob, loot it, repeat.
-- on_tick() runs every 200ms. Paths in dofile() are relative to ghost/ (the runner's cwd).
local targeting = dofile("scripts/lib/targeting.lua")

local target, kills = nil, 0

function on_tick()
  if not bot.is_alive() then return end
  if bot.consume_teleport() then target = nil end

  -- Loot anything lootable nearby once out of combat.
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
    local best = targeting.find_best_hostile({ max_dist = 80 })
    if not best then return end
    target = best.guid
    t = best
    bot.log(string.format("engaging entry %d (lvl %d) at %.0fy", best.entry, best.level, best.distance))
  end

  if t.distance > 4 then
    bot.move_to(t.x, t.y, t.z) -- pathfinds via mmaps
  else
    if bot.is_moving() then bot.stop_moving() end
    bot.face_target(target)
    bot.attack(target)
  end
end
