-- Minimal hand-written agent: fight the nearest attackable mob, loot it, repeat. Load with: wow -f example.lua
-- It runs as a task (task.spawn): a loop that waits without blocking the game. Watch it with wow tasks / wow events.

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

kills = kills or 0 -- a global: wow 'return kills'

task.stop("grind") -- reloading this file replaces the running loop
task.spawn("grind", function()
  while true do
    if not bot.is_alive() then wait(5) -- death handling is left to you
    else
      local mob = nearest_mob(80)
      if not mob then
        task.status("no targets")
        wait(3)
      else
        task.status(string.format("engaging %s (lvl %d) at %.0fy", mob.name, mob.level, mob.distance))
        local r = act.approach(mob.guid, 4) -- waits until in range; pathfinds via navmesh
        if not r.ok then
          bot.log("can't reach " .. mob.name .. ": " .. r.error.code)
          wait(1)
        else
          while true do -- swing until it dies or leaves
            local u = bot.get_unit(mob.guid)
            if not u or not u.is_alive or not bot.is_alive() then break end
            if u.distance > 5 then act.approach(mob.guid, 4) end
            bot.face_target(mob.guid)
            bot.attack(mob.guid)
            wait(0.5)
          end
          local u = bot.get_unit(mob.guid)
          if u and not u.is_alive then
            kills = kills + 1
            wait(0.5)
            local loot = await(bot.loot_all(mob.guid))
            bot.log(string.format("kill #%d: %s%s", kills, mob.name, loot.ok and (", looted " .. #loot.data.items .. " items") or ""))
          end
        end
      end
    end
  end
end)
