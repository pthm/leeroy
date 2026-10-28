-- Bundled grinding AI + a real (non-GM) death handler: release, corpse run, reclaim.
-- Load with: ./wow -f agents/claude1/grind.lua
dofile("scripts/grind.lua")
local ai_tick = on_tick

death = death or { corpse = nil, released_at = 0, reclaim_at = 0 }
last_alive_pos = last_alive_pos

local function is_ghost()
  local auras = bot.call("SelfAuras")
  if type(auras) == "table" then
    for _, a in ipairs(auras) do if a == 8326 then return true end end
  end
  return false
end

function on_tick()
  local now = bot.now_ms()
  local ghost = is_ghost()
  if ghost and death.corpse and death.released_at == 0 then death.released_at = 1 end
  if bot.is_alive() and not ghost then
    local x, y, z = bot.get_position()
    if not death.corpse then
      last_alive_pos = { x, y, z }
    else
      bot.log("claude1: revived")
      death.corpse = nil
    end
    return ai_tick()
  end
  if not death.corpse then
    death.corpse = last_alive_pos or { bot.get_position() }
    death.released_at = 0
  end
  if death.released_at == 0 then
    bot.call("RepopRequest")
    death.released_at = now
    bot.log("claude1: released spirit")
    return
  end
  if now - death.released_at < 3000 then return end
  bot.consume_teleport()
  local x, y, z = bot.get_position()
  local c = death.corpse
  local d = math.sqrt((x - c[1]) ^ 2 + (y - c[2]) ^ 2)
  if d > 5 then
    if not bot.is_moving() then bot.move_to(c[1], c[2], c[3]) end
  end
  if d < 30 and now - death.reclaim_at > 2000 then
    death.reclaim_at = now
    local err = bot.call("ReclaimCorpse")
    if err then bot.log("claude1: reclaim: " .. tostring(err)) end
  end
end
