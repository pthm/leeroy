# The runtime: tasks, actions, events

Your session runs one Lua state. Your scripts and your `wow` calls share it: what a script stores in a global you can
read with `wow`, and what you change with `wow` your scripts see on their next step. There is one world and one
script state, not a copy for you and another for your scripts. Everything that happens to your character lands in
one **event log** that both of you can read at any time.

## Tasks: scripts that wait

A task is a function that runs alongside everything else and can wait without blocking the game:

```lua
task.spawn("grind", function()
  while true do
    local mob = pick_target()                          -- your logic
    if not mob then wait(5) else
      local r = act.approach{ unit = mob.guid }        -- waits until you're there (or it fails)
      if r.ok then fight(mob) end                      -- your logic, can wait too
    end
  end
end)
```

- `task.spawn([name,] fn, ...)` → id. The task starts on the next tick (200 ms).
- Waiting, only inside a task: `wait(seconds)`, `await(request [, timeout_s])` (an action's result, below),
  `events.wait(type_or_types [, timeout_s])` → the next matching event, or nil on timeout.
- `task.list()` → your tasks with their state, status and what each waits for (`wow tasks`). `task.status("text")`
  sets what the current task says it's doing. `task.stop(id or name)`, `task.pause(id)`, `task.resume(id)`.
- A task that raises an error ends `failed`; the others keep running. Its end is a `task_finished` event, and
  failures show up in your next `wow` reply.
- Each evaluation, task step, and `on_tick` call has a 3 second Lua execution deadline. Long-running tasks should
  yield with `wait`, `await`, or `events.wait` so other work can run.
- `dofile` and `loadfile` can read regular Lua files within the configured workspace. `os.time`, `os.date`,
  `os.clock`, and `os.difftime` are available; process, environment, and filesystem operations are unavailable.
- `on_tick()` still works: a plain function called every tick that can't wait.
- `wow` evaluations run between ticks on the same state, so they never wait: start a task instead. Reloading a
  file doesn't stop the tasks it started: `task.stop("name")` first.

### Scripts that keep running

A script that ends, fails or dies leaves your character standing still until you notice. Make the loop the
script's job, not yours:

```lua
events.on("death", function() act.recover() end, "recover")   -- release, corpse run, rest
task.keep("grind", function()                                 -- runs again whenever it ends or fails
  while true do ... end
end)
```

- `task.keep(name, fn, ...)` runs `fn` as a task and runs it again each time it finishes or fails, after a pause
  that grows while it keeps failing fast (up to 30 s), and not while you're dead. Calling it again replaces it;
  `task.stop(name)` ends it for good.
- `act.recover{[spirit = false, rest = true]}`: release, run back to the corpse (the spirit healer if that fails),
  then rest.

**Tune, don't restart.** Restarting a script throws away what it was doing: the target it picked, the path it was
walking, the pull it was half-way through. Keep what you tune in a table the loop reads on every pass, and change
it while the script runs:

```lua
G = G or { area = {x = -9460, y = 70, radius = 80}, rest_hp = 0.5, avoid = {} }   -- keeps its values on reload
task.keep("grind", function()
  while true do grind_once(G) end                             -- reads G every pass
end)
```

```sh
wow 'G.rest_hp = 0.6; G.avoid[475] = true'                    # takes effect on the next pass
wow -f grind.lua                                              # redefines grind_once; the running loop calls the new one
```

## Actions: requests and results

Every action returns a **request** at once, which later resolves to a **result**:

```lua
local r = await(spells.cast(133, target))      -- in a task: waits for the result
-- {id = 7, op = "spells.cast", ok = true, data = {spell = 133}}
-- {id = 7, op = "spells.cast", ok = false, error = {code = "out_of_range", message = "out_of_range"}}
local req = items.loot(guid)                   -- anywhere: {id = 8, op = "items.loot", pending = true}
result(req.id)                                 -- later: the result once it's in
```

Refusals the client can detect up front (an unknown spell, an item you don't have, a living mob to loot) resolve
at once. If the server stays silent, the request ends with `timeout` and a hint about the usual cause. Every
failure and every result that took time is also a `result` event, so failures reach your event log (and your next
`wow` reply) even when no script checks them. `await` gives up after 30 s by default (`await(r, 120)`): it returns
`error.code = "wait_timeout"` then, though the request itself may still finish. What each action's `data` holds is
in the reference ("Action results").

## Events

Everything that happens is an event `{seq, t, type, data}`: `seq` counts up for the whole run, `t` is unix ms,
`data` is the type's struct (the reference's "Events" table lists every type: damage, kills, xp, casts, loot, quest
progress, chat, errors, results, task output...). Events come from what the server sends your client: what you'd
see on screen and in a real client's combat log.

**In scripts:**

- `events.on(type_or_types, fn [, name])` → id: runs `fn(event)` as its own task for each matching event (so it can
  wait and act). Registering again under the same name (default `"on <types>"`) replaces the handler, so reloading
  a script doesn't stack them. `events.off(id)`, `events.handlers()`.
- `events.wait(types [, timeout_s])` inside a task: the next matching event after now.
- Types match an event's type; `"quest_*"` matches a prefix, and `"chat:<kind>"` one kind of chat
  (`"chat:whisper"`, `"chat:npc_*"` for NPC lines and emotes: a boss's yell often announces its next ability).
- `events.watch{[stall_s = 180, below = 0, window_s = 300, every_s = 0]}`: when to emit `stall` (no XP for
  `stall_s` seconds, or fewer than `below` XP per hour over the last `window_s`) and `progress` (a digest of XP, XP
  per hour, kills and deaths every `every_s`; off by default). A stall is reported once, until progress resumes.
- `events.query{since = seq, types = {...}, match = "text", last = 50}` → past events (`limit = n`: the first n
  after `since`). Types can end in `*` (`"quest_*"`, `"cast_*"`); `match` is a case-insensitive search of the
  event's data. `events.seq()` → the latest seq.
- `events.emit(name, data)` → seq: an event of your own, `script.<name>`.

**From the shell:**

```sh
wow events                                       # the last 50 events, one per line
wow events --types chat --last 100               # chat history
wow events --types damage,heal --last 200        # your combat log
wow events --types result --match '"ok":false'   # failed actions
wow events --since 1200                          # everything after seq 1200
wow wait 120                                     # block until something you subscribed to happens (or 120 s)
wow wait --types quest_complete,death 600        # block for specific events
wow subscribe                                    # what wakes you
wow subscribe +chat:channel,progress             # also wake for channel chat and progress digests
wow subscribe -task_finished                     # stop waking when a task ends
wow tasks                                        # what your tasks are doing
```

**Your subscription** is what wakes you: `wow wait` without `--types` waits for it. You choose it. The default is
deaths, stalls, script errors, tasks ending, questions from your scripts, invites, trades, broken gear, and chat
from players to you (whispers, party, raid and say). NPC lines and emotes, channel chat, progress digests and the
rest are there when you ask for them: wake for what needs you, and let your scripts handle the rest
(`events.on`). A session restart resets it to the default.

Every `wow` evaluation's reply also carries `"events"`: what happened since your previous `wow` call, counted by
type, with these in full: what you subscribe to, failed actions and tasks, channel chat, and level-ups,
resurrections, errors, notices, group changes and quest completions. Nothing is lost if you don't look: the log keeps the whole run,
through restarts, and reading it never consumes anything.

## Scripts asking you

Routine play belongs in scripts, and decisions can come back to you:

```lua
local choice = ask("Bags full, 30 min from a vendor. Destroy greys or go sell?", {"destroy", "sell"},
                   { default = "destroy", timeout = 120 })
```

`ask` (inside a task) emits an `attention` event and waits. You see it in your next `wow` reply or with
`wow wait`, and answer with `wow answer <seq> sell` (seq is the attention event's). After `timeout` seconds it
continues with `default`. It returns the choice and `"answered"` or `"timeout"`.
