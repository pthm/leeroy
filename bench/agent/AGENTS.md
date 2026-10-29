# Playing WoW as an agent

You control one World of Warcraft (3.3.5a) character on a local AzerothCore server. The character stays
logged in for your whole run, in a **session**. You can query and act on it live, and you load **Lua
scripts** into it that keep playing while you think. Your score is how long it takes you to reach the goal
you were given (e.g. `level:10`, or owning a rare item), so build up scripts that get you there and reuse
them.

## Your workspace and session

Your working directory is your **script library**. It persists between tasks (anything from previous runs
is still there), and it's the only place you can write. `session.log` in it is the live log of your
character: `log()` output, script errors, kills, level-ups. `example.lua` is a complete
fight-loot-repeat loop to start from.

```sh
wow 'return me.level(), me.health()'   # query -> {"result":[3,72,72], "events": {...}}
wow 'return units.nearby(40)'            # what's around you
wow status                                       # one-call snapshot of your character
wow -f grind.lua                                 # load a script file (it starts its tasks)
wow tasks                                        # your running tasks: state, status, what each waits for
wow 'task.stop("grind")'                         # stop one
wow wait 300                                     # block until something important happens (chat, death, failure...)
wow events --types chat,death --last 20          # the event log: what happened, in order, for the whole run
wow answer 812 sell                              # answer a script's ask()
tail -20 session.log
standings                                        # where everyone in this run stands (rank, score, recent rate)
party [10]                                       # your party over the last 10 min: each member's XP/hour, time fighting/travelling/idle/dead/with the group
```

- `wow` runs Lua in the session and prints the returned values as JSON (`{"error": ...}` on failure). It runs
  between your scripts' steps on the same state, so it sees exactly what they see, and it can't wait: start a
  task for anything that takes time. Every reply carries `"events"`: what happened since your last call (counts,
  plus chat, deaths, errors, failed actions and tasks in full), and `"script_errors"` if a script failed.
- `wow wait`/`-w` take whole seconds (max 600). Create and edit files with your file-editing tools; your working
  directory is the only place you can write (some setups block shell redirects, heredocs and `sed -i`).
- Scripts and Lua globals persist until you replace them. Keep state you want to inspect in globals
  (`wow 'return my_state'`), and change a running script's behaviour by setting them.
- Inside Lua, the global `LIB` is the absolute path of your workspace (ending in `/`). Load your own
  modules with `dofile(LIB .. "lib/combat.lua")`.

`NOTES.md` is your notebook: read it at the start, and add what you learn (what worked, what killed you, good
spots, NPC and quest gotchas) as you go. It's kept between runs, so your next run starts from it.

Factor reusable pieces into `lib/` (combat, death handling, navigation, quest and vendor helpers, packet
parsers...), with a top-level script per activity. A script starts **tasks**: functions that run alongside
everything else and wait without blocking (`wait(2)`, `await(action)`, `events.wait("kill", 30)`). Actions return
requests that resolve to `{ok, data}` or `{ok = false, error = {code, message}}`, so a script knows whether a move,
cast, loot or turn-in worked and why not. Event handlers react to what happens (`events.on("death", fn)`).

```lua
local combat = dofile(LIB .. "lib/combat.lua")
task.stop("grind")                                   -- reloading: replace the old one
task.spawn("grind", function()
  while true do
    local mob = combat.pick_target(80)
    if not mob then wait(3) else
      task.status("fighting " .. mob.name)
      local r = act.approach{ unit = mob.guid, range = 4 }
      if r.ok then combat.fight(mob) else log("can't reach " .. mob.name .. ": " .. r.error.code) end
    end
  end
end)
events.on("death", function() act.release() act.corpse_run() end)
```

While it runs, you're watching: `wow wait` wakes you for anything important, `wow events` shows what led up to it,
and a script can hand a decision to you with `ask(question, options)`.

The rest of this file: a guide to playing well with the SDK (bags, vendors, trainers, death, unreachable
targets, other players), then the runtime (tasks, actions, events), the Lua API and its full reference, then the world data tools
(`wowdb`, `worlddb`).
