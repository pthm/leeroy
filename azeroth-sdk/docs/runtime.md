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
    local mob = pick_target()                      -- your logic
    if not mob then wait(5) else
      local r = act.approach(mob.guid, 4)          -- waits until you're there (or it fails)
      if r.ok then fight(mob) end                  -- your logic, can wait too
    end
  end
end)
```

- `task.spawn([name,] fn, ...)` → id. The task starts on the next tick (200 ms).
- Waiting, only inside a task: `wait(seconds)`, `await(request [, timeout_s])` (an action's result, see below),
  `events.wait(type_or_types [, timeout_s])` → the next matching event, or nil on timeout.
- `task.list()` → `{id, name, state, status, waiting_for, seconds, error}` (`wow tasks` shows it).
  `task.status("pulling boars")` sets what the current task says it's doing. `task.stop(id or name)`,
  `task.pause(id)`, `task.resume(id)`, `task.current()`.
- A task that raises an error ends in state `failed` with the message; the others keep running. Its end (done or
  failed) is a `task_finished` event, and failures show up in your next `wow` reply.
- `on_tick()` still works: a plain function called every tick that can't wait. Old scripts keep running.
- `wow` evaluations run between ticks on the same state, so they never wait: start a task instead.

## Actions: requests and results

Every action that the server answers returns a **request** at once, and the request resolves later with a
**result**:

```lua
local r = await(bot.cast_spell(133, target))      -- in a task: waits for the result
-- {id = 7, op = "cast", ok = true, data = {spell = 133}}
-- {id = 7, op = "cast", ok = false, error = {code = "out_of_range", message = "out_of_range"}}
local req = bot.loot_all(guid)                    -- anywhere: {id = 8, op = "loot", pending = true}
bot.result(req.id)                                -- later: the result once it's in
```

Every result is also a `result` event, so a failure reaches your event log (and your next `wow` reply) even when
no script checks it. Refusals the client can detect up front (unknown spell, item not in your bags, corpse not
lootable) resolve immediately. If the server stays silent, the request resolves with `timeout` and a hint.

| Call | `op` | `data` when ok | Error codes |
|---|---|---|---|
| `move_to(x, y, z)` | `move_to` | `distance` (yards short of the point) | `stuck`, `timeout`, `superseded` (a new `move_to` to a different place). Calling it again near the same point (chasing) keeps the same request. `stop_moving()` resolves it ok with `stopped = true`. `no_path = true` on the request: no navmesh path, it walks a straight line |
| `cast_spell(id, guid)` | `cast` | `spell` | the server's reason: `out_of_range`, `line_of_sight`, `not_ready`, `no_power`, `bad_targets`, `moving`, `interrupted`, `not_infront`, `equipped_item_class` (broken or missing weapon)…; `not_known` (refused locally: you don't know that spell or rank) |
| `use_item(item[, target])` | `use_item` | `spell` | cast reasons, `item_not_found`, `no_use_effect`, `not_loaded` |
| `use_object(guid[, spell])` | `use_object` | | cast reasons, `not_found` |
| `loot_all(guid)` | `loot` | `items = {{item, name, count}}`, `money` | `not_lootable`, `out_of_range`, `bags_full`, `nothing_looted` |
| `buy(vendor, entry[, count])` | `buy` | the item received | `not_enough_money`, `out_of_range`, `sold_out`, `cant_carry_more`, `not_sold_here`, `vendor_list_not_loaded`, bag errors |
| `sell(vendor, item[, count])` | `sell` | `money` gained | `item_not_found`, `wont_buy`, `vendor_not_found` |
| `repair(npc)` | `repair` | `fixed`, `cost`, `still_damaged` | `not_repaired` |
| `train(trainer, spell)` | `train` | | `unavailable`, `not_enough_money`, `skill_too_low` |
| `turn_in(npc, quest, reward)` | `turn_in` | `quest`, `xp`, `money` | `bags_full`, `quest_invalid`, `quest_failed`, `timeout` (usually: the quest isn't complete, or the wrong NPC) |
| `learn_talent(id)` | `learn_talent` | `rank` | `max_rank`, `no_such_talent`, `timeout` (points, tier or prerequisites) |
| `destroy(item[, count])` | `destroy` | `count` | `item_not_found` |

`await(r)` on a result that's already in returns it at once; outside a task it returns the result or the pending
request. `await` gives up after 30 s by default (`await(r, 120)` for longer). It returns `error.code = "wait_timeout"`
then, though the request itself may still finish.

## Events

Everything that happens is an event `{seq, t, type, data}`: `seq` counts up for the whole run, `t` is unix ms.
Events come from what the server sends your client: what you'd see on screen and in a real client's combat log.

| Type | `data` |
|---|---|
| `damage`, `heal` | `src`, `dst` (units: `{name, guid, entry, level, player, me}`), `amount`, `spell`, `spell_name`, `taken` (true when you're `dst`); only hits involving you |
| `kill` | `victim`: you got the killing blow (or your party did: `party_kill` with `killer`) |
| `death` | `killer` (who last hit you) |
| `resurrected`, `combat_start`, `combat_end` | |
| `xp` | `amount`, `source` (`kill` with `victim`, or `other`: quests, exploring) |
| `level_up` | `level` |
| `cast_start`, `cast_done`, `cast_failed` | `spell`, `name`, `cast_ms`; failures have `code` (the reason), `interrupted`, `hint` |
| `attack_failed` | `code` (`not_in_range`: move closer; `bad_facing`: `face_target`; `dead_target`; `cant_attack`), `target` |
| `item_received` | `item`, `name`, `quality`, `count`, `from` (`loot` or `npc`: quest rewards and purchases) |
| `money` | `amount` (copper looted) |
| `quest_progress` | `quest`, `entry` (the creature or object), `count`, `required` |
| `quest_complete` | `quest` (objectives done: go turn it in) |
| `quest_turned_in` | `quest`, `xp`, `money` |
| `learned` | `spell` |
| `achievement` | `id` |
| `area_changed` | `zone`, `area`, `zone_name`, `area_name` |
| `gear_broken` | `items`, `message` (after resurrecting with equipped items at 0 durability) |
| `chat` | `kind` (`say`, `yell`, `party`, `whisper`, `whisper_sent`, `channel`, `system`, `emote`, `npc_say`, `npc_yell`, …), `from`, `guid`, `channel`, `text`. Others' messages, plus your own whispers echoed |
| `party_invite` | `from` (`bot.accept_invite()` / `bot.decline_invite()`) |
| `party_changed` | `in_group`, `members` |
| `notice` | `message`: server notices ("Inventory is full", trade and repair messages) |
| `error` | `op` (`inventory`, `buy`, `sell`, `train`, `party`, `quest`), `code`, `message` |
| `result` | an action's result (above) |
| `log` | `msg`, `task`: from `bot.log(msg)` |
| `task_started`, `task_finished`, `script_error` | `name`, `state`, `error`, `where` |
| `attention`, `answer` | a script asking you something (below) |
| `script.<name>` | your own: `events.emit(name, data)` |

**In scripts:**

- `events.on(type_or_types, fn [, name])` → id: runs `fn(event)` as its own task for each matching event (so it
  can wait and act). Registering again under the same name (default `"on <types>"`) replaces the handler, so
  reloading a script doesn't stack them. `events.off(id)`, `events.handlers()`.
- `events.wait(types [, timeout_s])` inside a task: the next matching event after now.
- `events.query{since = seq, types = {...}, match = "text", last = 50}` → past events (`limit = n`: the first n
  after `since`). Types can end in `*` (`"quest_*"`, `"cast_*"`); `match` is a case-insensitive search of the
  event's data. `events.seq()` → the latest seq.
- `events.emit(name, data)` → seq: an event of your own, `script.<name>`.

**From the shell:**

```sh
wow events                                   # the last 50 events, one per line
wow events --types chat --last 100           # chat history
wow events --types damage,heal --last 200    # your combat log
wow events --types result --match '"ok":false'   # failed actions
wow events --since 1200                      # everything after seq 1200
wow wait 120                                 # block until something important happens (or 120 s)
wow wait --types quest_complete,death 600    # block for specific events
wow tasks                                    # what your tasks are doing
```

Every `wow` evaluation's reply also carries `"events"`: what happened since your previous `wow` call, counted by
type (`counts`), with the important ones in full (`important`: chat, deaths, level-ups, errors, notices, failed
actions and tasks, invites, quest completions, questions from your scripts). Nothing is lost if you don't look:
the log keeps the whole run, through restarts, and reading it never consumes anything.

## Scripts asking you

Routine play belongs in scripts, and decisions can come back to you:

```lua
local choice = ask("Bags full, 30 min from a vendor. Destroy greys or go sell?", {"destroy", "sell"},
                   { default = "destroy", timeout = 120 })
```

`ask` (inside a task) emits an `attention` event and waits. You see it in your next `wow` reply or with
`wow wait`, and answer with `wow answer <seq> sell` (seq is the attention event's). After `timeout` seconds it
continues with `default`. It returns the choice and `"answered"` or `"timeout"`.
