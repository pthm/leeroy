-- Minimal hand-written agent: fight the nearest attackable mob, loot it, repeat. Load with: wow -f example.lua
-- It runs as a task: a loop that waits without blocking the game. Watch it with wow tasks / wow events.

local function pick_target(dist)
  for _, u in ipairs(units.nearby(dist)) do -- nearest first
    -- ponytail: naive filter; doesn't avoid elites, camps or mobs far above your level: refine it
    if u.alive and u.attackable and u.reaction ~= "friendly" and not u.tapped_by_other and not u.roles then return u end
  end
end

kills = kills or 0 -- a global: wow 'return kills'

task.stop("grind") -- reloading this file replaces the running loop
task.spawn("grind", function()
  while true do
    local mob = me.alive() and pick_target(80)
    if not mob then
      task.status(me.alive() and "no targets" or "dead") -- death handling is left to you
      wait(3)
    else
      task.status(string.format("fighting %s (level %d) at %.0f yd", mob.name, mob.level, mob.distance))
      local r = act.approach{ unit = mob.guid, range = 4 } -- waits until in range; pathfinds via the navmesh
      if not r.ok then
        log("can't reach " .. mob.name .. ": " .. r.error.code)
        wait(1)
      else
        while true do -- swing until it dies or leaves
          local u = units.get(mob.guid)
          if not u or not u.alive or not me.alive() then break end
          if u.distance > 5 then act.approach{ unit = mob.guid, range = 4 } end
          move.face(mob.guid)
          combat.attack(mob.guid)
          wait(0.5)
        end
        local u = units.get(mob.guid)
        if u and not u.alive then
          kills = kills + 1
          local loot = await(items.loot(mob.guid))
          log(string.format("kill #%d: %s%s", kills, mob.name, loot.ok and (", looted " .. #loot.data.items .. " items") or ""))
        end
      end
    end
  end
end)
