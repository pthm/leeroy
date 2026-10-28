# Playing WoW as an agent

You control one World of Warcraft (3.3.5a) character on a local AzerothCore server. The character stays
logged in for your whole run, in a **session**. You can query and act on it live, and you load **Lua
scripts** into it that keep playing while you think. Your score is how long it takes you to reach the goal
you were given (e.g. `level:10`, or owning a rare item), so build up scripts that get you there and reuse
them.

## Your workspace and session

Your working directory is your **script library**. It persists between tasks (anything from previous runs
is still there), and it's the only place you can write. `session.log` in it is the live log of your
character: `bot.log()` output, script errors, kills, level-ups. `example.lua` is a complete
fight-loot-repeat loop to start from.

```sh
wow 'return bot.get_level(), bot.get_health()'   # query -> {"result":[3,72,72]}
wow 'return bot.get_nearby_units(40)'            # what's around you
wow -f grind.lua                                 # load/replace a script; its on_tick() starts running
wow 'on_tick = nil'                              # stop the running script
wow -w 60 'return bot.get_level(), bot.get_xp()' # wait 60s (max 600), then query: use this to poll
wow -c 60 'return bot.read_chat()'               # same, but returns as soon as another player messages you
tail -20 session.log
```

- `wow` runs Lua in the session and prints the returned values as JSON (`{"error": ...}` on failure).
  It shares a lock with `on_tick`, so don't block in it. Return data rather than printing it. If your
  running script raised errors since the last call, the reply also has
  `"script_errors": {"count": n, "last": "..."}`.
- `-w`/`-c` take whole seconds. Create and edit files with your Edit/Write tools: shell redirects,
  heredocs, `sed -i` and `python` are blocked.
- A loaded script stays active until you replace it, and Lua globals persist across calls. Keep shared
  state in globals if you want to inspect it later (`wow 'return my_state'`).
- Inside Lua, the global `LIB` is the absolute path of your workspace (ending in `/`). Load your own
  modules with `dofile(LIB .. "lib/combat.lua")`.

Factor reusable pieces into `lib/` (combat rotation, death handling, navigation, quest and vendor helpers,
packet parsers...), with a top-level script per activity. A script defines `on_tick()`, which is called
every 200ms. Calls like `move_to` don't block: they start an action, and you re-check the world on the next
tick.

```lua
local combat = dofile(LIB .. "lib/combat.lua")
function on_tick()
  if bot.in_combat() then return combat.tick() end
  local mob = combat.pick_target(80)
  if mob then bot.move_to(mob.x, mob.y, mob.z) end
end
```

The rest of this file is the SDK reference: the Lua runtime and `bot` API your scripts use, then the world
data tools (`wowdb`, `worlddb`).
