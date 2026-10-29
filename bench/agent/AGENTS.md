# Playing WoW as an agent

You control one World of Warcraft (3.3.5a) character on a local AzerothCore server. The character stays
logged in for your whole run, in a **session**. You can query and act on it live, and you load **Lua
scripts** into it that keep playing while you think. Your prompt says what you're scored on (reaching a goal
such as `level:10` fast, or making as much progress as you can in the time), so build up scripts that get you there
and reuse them.

## Your workspace and session

Your working directory is your **script library**. It persists between tasks (anything from previous runs
is still there), and it's the only place you can write. `session.log` in it is the live log of your
character: `log()` output, script errors, kills, level-ups. `example.lua` is a complete
fight-loot-repeat loop to start from.

```sh
wow 'return me.level(), me.health()'   # query -> {"result":[3,72,72], "events": {...}}
wow 'return units.nearby(40)'            # what's around you
wow status                                       # what you would see on screen: you, your fight, what is around you
wow -f grind.lua                                 # load a script file (it starts its tasks)
wow tasks                                        # your running tasks: state, status, what each waits for
wow 'task.stop("grind")'                         # stop one
wow wait 300                                     # block until something you subscribed to happens (or 300 s)
wow subscribe +stall,chat:npc_*                  # choose what wakes you: show, replace, +add or -remove types
wow events --types chat,death --last 20          # the event log: what happened, in order, for the whole run
wow answer 812 sell                              # answer a script's ask()
tail -20 session.log
wow 'return group.report(10)'                    # your party over the last 10 min: each member's time fighting/travelling/idle/dead/with the group, kills, deaths
standings                                        # outside the game: where everyone stands (score, rates, level) and the minutes left in the run
```

- `wow` runs Lua in the session and prints the returned values as JSON (`{"error": ...}` on failure). It runs
  between your scripts' steps on the same state, so it sees exactly what they see, and it can't wait: start a
  task for anything that takes time. Every reply carries `"events"`: what happened since your last call (counts,
  plus what you subscribe to, errors, failed actions and tasks in full), and `"script_errors"` if a script failed.
  Subscribed chat also goes to stderr. The run's clock isn't in the game: `standings` has `minutes_left` on its
  last line.
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
task.keep("grind", function()                        -- runs again if it ends or fails; reloading replaces it
  while true do
    local mob = combat.pick_target(80)
    if not mob then wait(3) else
      task.status("fighting " .. mob.name)
      local r = act.approach{ unit = mob.guid, range = 4 }
      if r.ok then combat.fight(mob) else log("can't reach " .. mob.name .. ": " .. r.error.code) end
    end
  end
end)
events.on("death", function() act.recover() end, "recover")
```

While it runs, you're watching, and your time is the scarce part: your character keeps playing only as long as a
script is running. Let scripts handle what happens every few seconds (deaths, a finished errand, a full bag) and
wake for what needs a decision. `wow subscribe` chooses what wakes you (`wow wait`). The default is deaths, stalls
(no XP for 3 minutes: `events.watch` tunes it), script errors, tasks ending, your scripts' questions, invites,
trades and chat from players to you. Waking for NPC lines and emotes (`chat:npc_*`), channel chat (`chat:channel`,
which your replies show anyway) and progress digests (`progress`) is yours to add. `wow events` shows what led up to a wake, and a script can hand a
decision to you with `ask(question, options)`. Tune a running script by changing the globals it reads rather than
restarting it ("Scripts that keep running" in the runtime section).

**Staying reachable.** Other players reach you only through chat, and you only see chat in a reply's `"events"`,
in `wow wait`, or in `wow events --types chat`. Three easy ways to miss it: filtering replies down to `.result`
(`wow` also prints subscribed chat and invites to stderr, so keep stderr visible); unsubscribing from the chat
kinds players use to reach you (`chat:whisper`, `chat:party`); long stretches without any `wow` call. A script can also react to messages itself
while you're busy, for example to log them, pause a task or reply:

```lua
events.on("chat", function(e)
  local c = e.data
  if c.kind == "whisper" or c.kind == "party" then
    log(c.from .. " (" .. c.kind .. "): " .. c.text)   -- or task.pause a grind, or chat.whisper(c.from, "...")
  end
end, "chat watch")
```

The rest of this file: a guide to playing well with the SDK (bags, vendors, trainers, death, unreachable
targets, other players), then the runtime (tasks, actions, events), the Lua API and its full reference, then the world data tools
(`azworld`, with `azworld sql` for raw SQL).
