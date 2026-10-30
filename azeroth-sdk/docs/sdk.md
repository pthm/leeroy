# The SDK

Your character runs in a **session**: a headless game client that stays logged in and runs one Lua state. Your
scripts and your `wow` calls share that state: a global a script sets, `wow` reads, and what `wow` changes, a script
sees on its next step. Everything that happens to the character goes to one **event log** that both read.

There is no built-in AI. With no script running the character does nothing at all, not even fight back, and
nothing moves or turns it for you.

This page is the runtime and the Lua API: how scripts run, act, and react, and how to see what they do. How the
API behaves in the game is in a file per topic (the table at the end), and api-reference.md has every function,
type and event.

## Tasks: scripts that wait

A task is a function that runs alongside everything else and can wait without blocking the game:

```lua
task.keep("main", function()
  while true do
    local r = act.go_to{ x = -9460, y = 70 }          -- waits until you're there, or it fails
    if not r.ok then log("go_to: " .. r.error.code) end
    wait(5)
  end
end)
```

- `task.spawn([name,] fn, ...)` → id: runs `fn` once, starting on the next tick (200 ms).
- `task.keep(name, fn, ...)` → id: runs `fn` and runs it again each time it finishes or fails, after a pause that
  grows while it keeps failing fast (up to 30 s), and not while you're dead. Calling it again replaces it.
- Waiting, only inside a task: `wait(seconds)`, `await(request [, timeout_s])` (an action's result, below),
  `events.wait(type_or_types [, timeout_s])` (the next matching event, or nil on timeout).
- `task.list()` → your tasks with their state, status and what each waits for (`wow tasks`). `task.status("text")`
  sets what the current task says it's doing.
- `task.stop(id or name)` stops tasks; `task.stop()` stops every task. By name or with no argument it skips the
  task that calls it, so a script can stop its own name (task.keep won't restart it) and carry on until it returns.
  Stopping, pausing, failing, or returning from a task cancels all its active and queued `move.to` requests,
  including walks it started without awaiting. Other actions it started (an auto-attack, a cast) carry on.
- `task.pause(id)` takes a waiting task off the schedule (false if it isn't waiting) and `task.resume(id)` puts
  it back on the same wait: a result or an event that came in meanwhile is still delivered. A walk cancelled
  by pause stays cancelled after resume; its result has `error.code = "task_paused"`.
- A task that raises an error ends `failed`; the others keep running. Each end is a `task_finished` event.
- Every evaluation, task step and `on_tick` call has a 3-second execution limit: a loop must `wait`, `await` or
  `events.wait` on every pass to let the rest run, including passes where nothing happens (a check that returns at
  once, a target that's already in range). A task that doesn't fails with "main.lua:22: ran 3s without waiting", at the line it was running. `on_tick()`, if you define it, is called every tick and can't wait.
- `wow` evaluations run between ticks on the same state and can't wait: start a task for anything that takes time.
- `dofile` and `loadfile` read Lua files within your workspace; `LIB` is its absolute path (ending in `/`).
  `os.time`, `os.date`, `os.clock` and `os.difftime` exist; process, environment and other file access don't.

**Reloading and changing a running script.** `wow -f file.lua` runs the file again. Globals keep their values
across reloads, and functions defined in the file are replaced, so a loop that calls a global function picks up
the new one on its next call, and a loop that reads a settings table sees changes on its next pass:

```lua
G = G or {}                                                   -- keeps its values on reload
G.rest_hp = G.rest_hp or 0.5                                  -- a default per field: new fields arrive on reload
function step() ... end                                       -- replaced on reload
task.keep("main", function() while true do step() end end)
```

Give defaults field by field. `G = G or {rest_hp = 0.5, ...}` keeps whatever `G` already is, so a field you add
later (or a `G` you created earlier with `wow 'G = {}'`) never gets its default; the same goes for `state = {...}`,
which on the other hand wipes your progress on every reload.

```sh
wow 'G.rest_hp = 0.6'                                         # seen on the next pass
wow -f main.lua                                               # step() replaced; task.keep restarts "main"
```

A table written as `lib = lib or {}` keeps its old functions on reload (set `lib = nil` first). Reloading a file
that uses `task.spawn` starts a second copy unless the file stops the first (`task.stop("name")`); `task.keep`
replaces its own.

## Actions: requests and results

Every raw action returns a **Request** at once, including an immediate refusal. It resolves to an
**ActionResult**; use `await` inside a task, or poll `result(id)` outside one:

```lua
local r = await(spells.cast(133, target))      -- in a task: waits for the result
-- {id = 7, op = "spells.cast", ok = true, data = {spell = 133}}
-- {id = 7, op = "spells.cast", ok = false, error = {code = "out_of_range", message = "out_of_range"}}
local req = items.loot(guid)                   -- anywhere: {id = 8, op = "items.loot", pending = true}
result(req.id)                                 -- later: the result once it's in
```

A request handle is `{id, op, pending = true}` and has no `ok` field. `pending` identifies the handle,
not whether the action is still running. `result(id)` returns the handle while unresolved, the
`ActionResult` once resolved, or nil for an unknown or expired ID. Test `r.ok`, never just `r`.

Waiting helpers return an **ActResult** with `{op, ok, data?, error?, steps?}` and no `id` or `pending`.
Both result types retain partial `data` on failure and use `error = {code, message, next?}`.
`next`, when present, names one concrete corrective action. Composite helpers use `steps` to identify
nested requests. `act.ok(op, data)` and `act.fail(op, code, message)` construct helper results;
`act.must(r)` raises a failed result as a script error.

Refusals the client can detect (an unknown spell, an item you don't have) resolve at once. If the server stays
silent, the request ends with `timeout` and a hint about the usual cause. `await` gives up after 30 s by default
(`await(r, 120)`), returning `error.code = "wait_timeout"`; the request may still finish. Every failure, and every
result that took time, is also a `result` event. What each action's `data` holds is in api-reference.md ("Action
results").

## Events

Every event is `{seq, t, type, data}`: `seq` counts up for the whole run, `t` is unix ms, and `data` is the type's
struct (api-reference.md, "Events": damage, kills, xp, casts, loot, quest progress, chat, errors, results, task
output...). Events come from what the server sends the client: what a player would see on screen and in the combat
log.

**Types** match an event's type exactly; `"quest_*"` matches a prefix; `"chat:<kind>"` matches one kind of chat
(`"chat:whisper"`, `"chat:npc_*"`).

**In scripts:**

- `events.on(types, fn [, name])` → id: runs `fn(event)` as its own task for each matching event. A handler
  registered again under the same name (default `"on <types>"`) replaces the old one. `events.off(id or name)`,
  `events.handlers()`.
- `events.wait(types [, timeout_s])`, inside a task: the next matching event after now.
- `events.query{since = seq, types = {...}, match = "text", last = 50}` → past events (`limit = n`: the first n
  after `since`); `match` searches the event's data, case-insensitively. `events.seq()` → the latest seq.
- `events.emit(name, data)` → seq: an event of your own, of type `script.<name>`.
- `events.watch{[stall_s = 180, below = 0, window_s = 300, every_s = 0]}`: when the session emits `stall` (no XP
  for `stall_s` seconds, or fewer than `below` XP per hour over the last `window_s`; once, until XP comes again)
  and `progress` (XP, XP per hour, kills and deaths every `every_s`; off by default).

**From the shell:**

```sh
wow events                                       # the last 50 events, one per line
wow events --types chat --last 100               # chat history
wow events --types damage,heal --last 200        # your combat log
wow events --types result --match '"ok":false'   # failed actions
wow events --since 1200                          # everything after seq 1200
wow wait 120                                     # block until an event you subscribe to (or 120 s)
wow wait --types quest_complete,death 600        # block for these
wow subscribe                                    # your subscription
wow subscribe +chat:channel,progress             # add types
wow subscribe -task_finished                     # remove types
wow subscribe death,stall,chat:whisper           # replace it
```

**Your subscription** decides what `wow wait` (without `--types`) waits for. The default is `attention`, `death`,
`stall`, `script_error`, `task_finished`, `party_invite`, `trade`, `gear_broken`, and chat from players to you
(`chat:whisper`, `chat:party`, `chat:raid`, `chat:say`). A session restart resets it to the default.

**Every `wow` reply** carries `"events"`: what happened since your previous call, counted by type, with these in
full: the types you subscribe to, failed actions and tasks, channel chat, level-ups, resurrections, errors, notices,
group changes, quest completions and slow ticks. Subscribed chat, invites, trades, questions from scripts, script
errors and slow ticks also go to stderr, so piping a reply through `cut` or `jq` doesn't hide them.
The log keeps the whole run, through session restarts, and reading it consumes nothing.

**Scripts asking you.** `ask(question, options [, {default, timeout}])`, inside a task, emits an `attention` event
and waits for `wow answer <seq> <choice>` (seq is the attention event's). After `timeout` seconds it continues with
`default`. It returns the choice and `"answered"` or `"timeout"`:

```lua
local choice = ask("Bags full. Destroy greys or go sell?", {"destroy", "sell"}, { default = "destroy", timeout = 120 })
```

## Seeing what your scripts do

- **`log(msg)`** writes a `log` event tagged with the task that wrote it (`wow events --types log --last 20`: each
  with its time and task) and a line in `session.log` (`12:04:31 [Bot Name] [Lua main] msg`).
- **`task.status(text)`** is what the task says it's doing, in `wow tasks`; keep it current and you can see where
  each task is without reading logs. Changes are also recorded as diagnostic `task_status` events, limited to
  512 bytes and marked as agent claims, with a source file and line when available.
- **Globals** are readable from the shell: keep what a script knows in a table (`state`) and `wow 'return state'`.
- **`events.emit(name, data)`** records structured data of your own as a `script.<name>` event: one per pull with
  its target, time and outcome, say. `wow events --types script.pull --last 30` or `events.query` reads them back.
- **Errors** say where: `main.lua:22: attempt to compare number with nil`, and a traceback (innermost call first)
  when the error was deeper than one call. A file's lines are named by its path in your workspace; `act.lua:`
  is the SDK's `act` library (its source is readable, the paths are in your task prompt) and `wow:` is a `wow`
  evaluation. The full error is in `wow tasks` (failed tasks keep theirs), the `script_error` event and
  `session.log`; replies carry the latest as `"script_errors"`.
- **Cost.** The game waits while Lua runs: a tick over 300 ms delays movement, combat and replies. Each one is a
  line in `session.log` naming the task that took longest, and a `slow_tick` event (at most every 30 s, with a
  count) shows in replies. `wow tasks` shows each task's `busy_ms`, its Lua time so far. Path queries are the
  usual cost: `path_distance` on every unit of a wide `units.nearby` each pass.
- **`session.log`**, in your workspace, has each line stamped with the time of day (the clock of events' `t`):
  `log()` lines, script errors, slow ticks, kills, deaths and level-ups. It's the session's own output, so it
  also has its start-up and the odd warning from the client.

When a session recorder is configured, its JSONL file also contains versioned diagnostic records (`kind`,
`schema = 1`). Every raw request and waiting helper has an `action_finished` record with a session-unique
`call`; nested actions reference `parent_call`, and raw actions also identify `request`. These records include
immediate successes and repeated refusals that the ordinary `result` event stream suppresses. Helper IDs stay
out of `ActResult`. A new session gets a new identity; task stop, script failure and session shutdown terminate
open helper traces explicitly. An `await` timeout does not finish its underlying request's trace.

Diagnostic action fields include only selected identifiers, coordinates, outcome, code and timing. They do
not copy command text, chat, arbitrary arguments, result payloads or error messages. Task status remains an
agent-authored observation, not proof that an action succeeded.

The recorder also samples the complete observed quest log once a second and emits `quest_state` on change,
including an initial snapshot. It records IDs, completion/failure bits and raw objective counters, without
quest text. Inventory objectives require item observations. A missing character snapshot is marked unavailable;
an empty available log means no quests were observed. Removal does not establish turn-in. `group_state` records
the initial roster and server group updates using GUIDs, without names, chat, readiness or position claims.

## The Lua API

`help()` lists every function from inside the game (`wow 'return help("items")'`).

| Namespace | What it's for |
|---|---|
| `me` | your character: level, xp, health, power, position, money, stats, state, target; `me.status()`, what you'd see on screen |
| `units` | creatures and players around you: `nearby`, `players`, `get`, `attackers`, auras, line of sight, inspect |
| `objects` | game objects: chests, ore veins and herbs, doors, quest objects (`nearby`, `find`, `use`) |
| `world` | terrain: ground heights, walking paths, line of sight to a point |
| `move` | walking (`to`, `stop`, `unstuck`) and facing (`face`, `turn`) |
| `combat` | targeting, auto-attack, your pet, threat tables, the combat log and meters |
| `spells` | casting (`cast`, `cast_at`), cooldowns, usability, what a spell does |
| `items` | your bags and gear: list, info, compare, use, equip, destroy, loot |
| `gossip` | talking to NPCs: what they say, their dialogue options and quests |
| `vendor`, `trainer`, `innkeeper` | buying, selling, repairing; learning spells; your home inn |
| `quests` | your quest log, quest text, accepting, turning in, abandoning |
| `flights` | flight masters and flight points |
| `death` | your corpse, releasing, reclaiming, spirit healers |
| `chat`, `group`, `trade`, `summon` | other players |
| `talents` | your talent trees and spending points |
| `skills` | learned skill IDs, names, base values, maxima and categories (`list`, `get`) |
| `task`, `events`, `wait`, `await`, `ask` | the runtime (above) |
| `act` | utilities that wait: walking with retries, NPC errands, resting, recovering from death |
| `client` | the raw protocol, for what the API doesn't cover |

**Conventions:**

- **Local queries** return one value or one table: `me.level()`, `me.health()` → `{current, max}`,
  `units.nearby(40)` → creatures, nearest first. A `?` on a result type (`me.casting() → Casting?`) means nil when
  there's nothing: out of sight, none, or not loaded yet. Names of creatures, items and quests fill in a moment
  after you first see them (the client asks the server): call again.
- **Server-backed lookups** return requests even when cached: `items.info(item_entry)`, `quests.info(quest_id)`,
  `vendor.items(npc_guid)` and `me.achievements()`. Use `await` inside a task and check `ok` before reading `data`.
  Item/quest/achievement lookups have a five-second deadline: `not_found` requires an explicit negative server
  answer, `not_available` means the query could not be sent, and `timeout` means no answer arrived. This server
  currently ignores invalid quest IDs, so they time out; an invalid item ID receives `not_found`.
- **Actions** are verbs and return a request (above). Error codes are stable strings (`out_of_range`,
  `not_known`, `bags_full`, `stuck`, `timeout`...).
- **One shape per thing:** a creature, item, quest or spell is the same table wherever it appears.
- **GUIDs are strings** (64-bit numbers don't fit in a Lua number): pass them back unchanged. An `item` argument is
  an item entry (number) or the GUID of one you carry; `npc_guid` and `guid` are GUIDs, `npc_entry` and `entry` are template IDs. Raw NPC calls
  require a GUID string and never reinterpret an entry as a GUID.
  Parameters named `guid` or ending in `_guid` require decimal GUID strings. Explicit item selector unions
  still accept an entry or an item table carrying a GUID.
- **Arguments are checked before native actions run.** Missing required values, extra arguments, wrong scalar
  types, fractional IDs, non-finite coordinates and overflowing integers raise signature errors. Omitted or nil
  optional scalar arguments use their documented literal defaults. Symbolic defaults such as `you` or `last`
  are resolved by the operation. Options reject unknown fields; travel validates every waypoint before moving.

- **Options tables:** `act` functions and a few others take one table: `act.go_to{x = 1, y = 2}`.
- **`act`** covers plumbing, not play: walking with stuck handling, following, NPC errands (accept, turn in, sell,
  repair, train), objects, resting, flying, and recovering from death (`act.recover`). Its source is
  `session/act.lua`.

```lua
task.spawn("inspect", function()
  local item = await(items.info(117))
  if item.ok then log(item.data.name) else log(item.error.code) end
  local quest = await(quests.info(62))
  if quest.ok then log(quest.data.summary) else log(quest.error.code) end
end)
local mining = skills.get(186) -- nil if not in your observed learned skills
if mining then log(mining.value .. "/" .. mining.max) end
```

Skill ranks exclude temporary/equipment bonuses and correspond to the judge's saved `character_skills` values.
Missing client DBC labels set `metadata_known=false`; the observed ranks are still available. Use
`spells.find(name)` for the highest known spell rank; the redundant `act.best` helper has been removed.


**The Lua** is 5.1 (gopher-lua) with `goto`, `bit32`, `table.unpack` and `load(string)` from 5.2. Numbers are
doubles; there's no `//`, `&` or `|` (use `bit32.band` and friends). A function's multiple returns are cut to the
first when it isn't last in a list (`return ask(...), me.level()` loses ask's second value: `return {ask(...)},
me.level()`). `goto` is reserved, so it can't name a function.

## How the API behaves: the topic files

| File | Read it when you're |
|---|---|
| moving.md | walking anywhere, judging what's around you (`wow look`, aggro ranges, `danger` in `wow status`), finding a path, getting stuck |
| fighting.md | casting, attacking, pulling, choosing targets, or looking back at a fight (`wow fights`, `wow fight`) |
| npcs-and-items.md | taking or turning in quests, buying, selling, repairing, training, using objects, managing bags and gear |
| death-and-travel.md | dying, getting back to life, flying, using your hearthstone |
| players.md | grouping, chatting, trading, or working with other players |
| troubleshooting.md | stuck on something that doesn't work as you expect |
| azworld.md | looking up the world: NPCs, quests, spawns, drops, routes, maps |
