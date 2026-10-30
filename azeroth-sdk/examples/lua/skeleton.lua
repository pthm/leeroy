-- The shape of a script: load it with  wow -f example.lua
-- It plays nothing: step() is where your play goes. Everything else here is the plumbing most scripts share.

G = G or { every = 5 }          -- settings the loop reads on every pass: change them live, wow 'G.every = 2'
state = state or { passes = 0 } -- what the script is doing, for you to read: wow 'return state'

-- One pass of play. Reloading this file replaces the function; the running loop calls the new one next time.
function step()
  state.passes = state.passes + 1
  local hp, near = me.health(), units.nearby(40)
  state.last = string.format("%d/%d hp, %d creatures within 40 yd", hp.current, hp.max, #near)
  task.status(state.last)       -- shown by wow tasks

  -- An action returns a request; in a task, await waits for its result:
  --   local r = await(spells.cast(id, guid))
  --   if not r.ok then log("cast: " .. r.error.code) end   -- {ok = false, error = {code, message}}
  -- Helpers already wait and return ActResult; verify it before advancing your own state:
  --   local r = act.accept{npc_entry = quest_giver_entry, quest = quest_id}
  --   if r.ok then state.accepted = true else log(r.error.message) end
  wait(G.every)
end

events.on("death", function() act.recover() end, "recover") -- release, run back to the corpse, rest
task.keep("main", function()                                -- runs again if it ends or fails
  while true do step() end
end)
