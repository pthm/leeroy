# The SDK

Your character runs in a **session**: a headless game client that stays logged in and runs one Lua state. Your
scripts and your `wow` calls share that state: a global a script sets, `wow` reads, and what `wow` changes, a script
sees on its next step. Everything that happens to the character goes to one **event log** that both read.

There is no built-in AI. With no script running the character does nothing at all, not even fight back, and
nothing moves or turns it for you.

This page covers the runtime, the Lua API, how the API behaves in the game, and the world data tool, `azworld`.
api-reference.md has every function, type and event.

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
  A stopped task that was waiting for a walk (`move.to`, `act.go_to`) stops walking; anything else it started (an
  auto-attack, a cast) carries on.
- `task.pause(id)` takes a waiting task off the schedule (false if it isn't waiting) and `task.resume(id)` puts
  it back on the same wait: a result or an event that came in meanwhile is still delivered.
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

Every action returns a **request** at once, which later resolves to a **result**:

```lua
local r = await(spells.cast(133, target))      -- in a task: waits for the result
-- {id = 7, op = "spells.cast", ok = true, data = {spell = 133}}
-- {id = 7, op = "spells.cast", ok = false, error = {code = "out_of_range", message = "out_of_range"}}
local req = items.loot(guid)                   -- anywhere: {id = 8, op = "items.loot", pending = true}
result(req.id)                                 -- later: the result once it's in
```

A pending result is a table too (`{id, op, pending = true}`), so test `r.ok` or `r.pending`, never just `r`.

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
  each task is without reading logs.
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
| `task`, `events`, `wait`, `await`, `ask` | the runtime (above) |
| `act` | utilities that wait: walking with retries, NPC errands, resting, recovering from death |
| `client` | the raw protocol, for what the API doesn't cover |

**Conventions:**

- **Queries** are nouns and return one value or one table: `me.level()`, `me.health()` → `{current, max}`,
  `units.nearby(40)` → creatures, nearest first. A `?` on a result type (`me.casting() → Casting?`) means nil when
  there's nothing: out of sight, none, or not loaded yet. Names of creatures, items and quests fill in a moment
  after you first see them (the client asks the server): call again.
- **Actions** are verbs and return a request (above). Error codes are stable strings (`out_of_range`,
  `not_known`, `bags_full`, `stuck`, `timeout`...).
- **One shape per thing:** a creature, item, quest or spell is the same table wherever it appears.
- **GUIDs are strings** (64-bit numbers don't fit in a Lua number): pass them back unchanged. An `item` argument is
  an item entry (number) or the GUID of one you carry; `npc` and `guid` are GUIDs, `entry` is a template id.
  Anywhere a GUID goes you can pass the table itself (a unit, item or object): its `guid` is used.
- **Options tables:** `act` functions and a few others take one table: `act.go_to{x = 1, y = 2}`.
- **`act`** covers plumbing, not play: walking with stuck handling, following, NPC errands (accept, turn in, sell,
  repair, train), objects, resting, flying, and recovering from death (`act.recover`). Its source is
  `session/act.lua`.

**The Lua** is 5.1 (gopher-lua) with `goto`, `bit32`, `table.unpack` and `load(string)` from 5.2. Numbers are
doubles; there's no `//`, `&` or `|` (use `bit32.band` and friends). A function's multiple returns are cut to the
first when it isn't last in a list (`return ask(...), me.level()` loses ask's second value: `return {ask(...)},
me.level()`). `goto` is reserved, so it can't name a function.

## How the API behaves

### Moving

- `move.to` with no walkable path walks a straight line (the request has `no_path = true`) and usually ends `stuck`.
  `world.path(x, y, z)` returns the walking distance and the gap between where the path ends and the point: a gap
  over a few yards means the point can't be reached.
- `units.nearby` gives each living creature `in_sight`, and for the nearest `path_distance` (the walk) and
  `reachable`; `distance` is a straight line. A mob in a mine below you can be 30 yards away and a 300-yard walk.
- Indoors and upstairs, pass the point's z to get the right floor: `move.to(x, y, world.ground_z(x, y, z))`.
  `act.go_to` without a z uses your own height for points within 80 yards, so it stays on your floor in a cave or a
  building; for anywhere else, pass the z (azworld gives spawn and quest points with theirs).
- Walking doesn't fight: `move.to`, `act.go_to` and `act.travel` keep walking while something attacks you, and drag
  it into your next fight. Give `act.go_to` and `act.travel` an `on_attacked = function(attackers) ... end` (your
  fighting) and they stop for whoever attacks you on the way, then carry on.
- Two tasks moving you at once cancel each other's walks: the older `move.to` resolves `superseded`, naming the
  task that took over. Let one task own movement (stop the other, or pause it, while a death handler runs).
- `units.nearby` leaves out units nobody can select: the invisible triggers the server uses for events and scripts.
- Entering a place triggers it as in the game: exploration objectives complete, inns and cities make you rest.
- While the server moves you (a flight, a teleport, fear, confusion) the client sends no movement of its own:
  `move.to` is refused with the reason (`in_flight`, `feared`...), `me.movement()` says who moves you, and
  `movement_changed`, `flight_start` and `flight_end` events mark the changes.
- Teleports are `teleported` events (`far = true`: a different map, and the world around you reloads). Portals are
  objects (`type = "portal"`): `objects.use(portal)`. Boats and zeppelins aren't supported.

### Casting and fighting

- `me.casting()` is set from the moment you call `spells.cast` (or `items.use`) until the cast ends. Moving
  interrupts a cast.
- `spells.usable(id, guid)` checks cooldown, power and the aura state a spell needs (Victory Rush only after a
  kill, Execute below 20% health, Revenge after a block or dodge: `me.states()`, a unit's `states`) before a cast;
  the result says whether it went off or why not (`out_of_range`, `not_ready`, `no_power`, `line_of_sight`,
  `bad_targets`, `caster_aurastate`...).
- `spells.cooldown(id)` and `items.cooldown(item)` say how long until a spell or item is ready, including
  cooldowns still running from before you logged in (the Hearthstone) and shared ones (all potions). A potion used
  in combat reads `-1`: its cooldown only starts when you leave combat.
- `cast_failed` events carry the server's reason. A cast bar that ends without the spell and without one is
  `cancelled`. A failed cast's code is in its request's result too, so check `r.ok` and branch on `r.error.code`:
  retrying the same call unchanged gets the same answer.
- `combat.attack(guid)` starts auto-attack. A refused swing is an `attack_failed` event (`not_in_range`,
  `bad_facing`: `move.face`).
- Each spell rank is its own id: `spells.known`, `act.best({rank ids})` for the highest you know.
- Every fight ends with a `fight_end` event: duration, damage dealt and taken, abilities used, outcomes. A unit's
  `casting`, a `unit_cast_start` event, `units.auras`, `threat_pct` and `combat.threat(guid)` show what the game
  shows a player.
- The combat log: `damage` and `heal` events (`periodic` for DoT and HoT ticks), `energize` (mana, rage, energy
  given), `dispel` (auras dispelled or stolen). A `cast_done` event says how many targets it `hits` and which it
  `misses`, and why.

### NPCs

- NPCs have `roles` (`quest_giver`, `vendor`, `trainer`, `repair`, `flight_master`, `innkeeper`, `spirit_healer`...);
  quest givers have `quest_status` (the marker a client draws). `azworld near x y --kind <role>` finds the nearest.
- NPC calls need you within ~5 yards. `gossip.open(npc)` returns what an NPC says, its options and its quests;
  `gossip.select(index)` picks an option.
- Vendors: `vendor.items(npc)` (call it twice: the first call asks the vendor), `vendor.buy(npc, entry, count)`,
  `vendor.sell(npc, item)`, `vendor.sell_junk(npc)` (every grey), `vendor.repair(npc)`. `act.vendor{...}` walks
  there and does all of it.
- Trainers: `trainer.list(npc)` (what it teaches you, cost, level, available now), `trainer.learn(npc, spell)`. The
  level-70 "<Class> Trainer" NPCs in Northshire are placeholders that teach nothing.
- Quests: `quests.accept(npc, quest)`, `quests.turn_in(npc, quest [, reward index])` (0 = the first choice;
  `quests.info(id)` lists the choices), `quests.log()`, `quests.info(id)` (the quest giver's text), `quests.abandon`.
  `act.accept` and `act.turn_in` walk there first. A kill objective also counts the creatures whose
  `kill_credit` lists its entry (`units.nearby`, `units.get`). A turn-in fails at once with `incomplete` when the objectives
  aren't done; a `quest_failed` event means an escort died or a timer ran out: abandon it and take it again.

### Items

- `items.list()` has each item's name, quality and vendor price once its info has loaded (a second after it first
  appears). `items.free_slots()`, `items.destroy(item)`, `items.use(entry)`, `items.equip(item)`.
- `items.compare(item)` shows the change against what you wear in that slot (item level, stats, armor, dps) and
  `usable = false` with the reason when you can't use it. `items.info(entry)` has the details; `me.gear()` every
  slot; `items.broken()` what's at 0 durability.
- Group loot: a `loot_roll_start` event per item up for rolls; `items.roll(roll, "need"|"greed"|"disenchant"|"pass")`
  before the countdown ends (`items.rolls()` lists the open ones), then `loot_roll` and `loot_roll_won` events
  (`loot_roll_all_passed` when nobody wants it).
- Full bags: loot stays on the corpse and turn-ins with an item reward fail with `bags_full` (and an "Inventory is
  full" notice).
- `objects.use(guid)` within ~5 yards uses a chest, node or quest object. `objects.nearby` says what each takes
  (`gather`, `skill`, `can_use`, `key`), `lit` when one of your quests needs it, and how far the walk is.

### Travel and death

- `flights.map(npc)` at a flight master discovers its point (`learned`) and lists where you can fly;
  `flights.take(npc, node)` resolves when you land. `act.fly{to = "Sentinel Hill"}` from near a flight master.
- `innkeeper.bind(npc)` sets your home; the hearthstone is item 6948 (`items.use(6948)`); `me.home()`.
- `death.release()`, `death.corpse()` (where it is and when you can reclaim), `death.reclaim()` within ~30 yards,
  `death.spirit_res(npc)`. `act.recover()` does release, corpse run (spirit healer if that fails) and rest. A
  `gear_broken` event follows a revive with broken gear.
- Coming back to life where you died puts you next to what killed you. `act.corpse_run` and `act.recover` reclaim
  from the far side of the reclaim circle (32 yards out) when hostiles stand by the corpse, and `act.recover{spirit =
  true}` runs to the corpse instead when hostiles camp the graveyard (the spirit healer revives you where you stand).
  `act.danger(x, y [, r])` lists the hostile creatures near any point. The wait before you can reclaim grows with
  deaths in quick succession.
- A `travel_refused` event: a portal or instance turned you away (`reason`, `message`: level, group, full, lockout).
- `me.reputation()`: your standing with each faction (`rank`: hated ... exalted).

### Other players

- Sending: `chat.say(msg)`, `chat.yell(msg)`, `chat.whisper(name, msg)`, `chat.party(msg)`,
  `chat.channel(name, msg)`. Every message is at most 255 bytes (`message_too_long`). Whispers and channel
  messages resolve once the server has delivered them, or fail (`player_not_found`, `not_member`, `muted`,
  `throttled`); your own messages aren't echoed back as chat events.
- Channels: you start in your zone's General channel, and leave it when you change zone until you
  `chat.join("General - <Zone>", 1)` (zone channel ids: General 1, Trade 2, LocalDefense 22). `chat.join("name", 0)`
  joins a custom channel, in every zone (`wrong_password`, `banned`).
- Receiving: every message is a `chat` event with `kind`, `from` and `text` (`ago_s` in `wow events` says how old).
  `events.on("chat", fn)` reacts in a script. Messages sent while your session restarts are lost.
- Groups: `group.invite(name)` (fails with `bad_player_name`, `already_in_group`, `group_full`...), the invitee gets
  a `party_invite` event and calls `group.accept()`; `group.info()`, `group.leave()`. A `ready_check` event:
  answer with `group.ready(true)`. A `summon_request` event: `summon.accept()` resolves when you arrive, or `summon.decline()`.
  `group.report([minutes])`: how each member spent the last minutes (fighting, travelling, idle, dead, with the
  group), from what you've seen of them.
- The server's message of the day comes as a `motd` event; battleground and arena queue changes as `battleground`.
- `units.players(dist)`, `units.inspect(guid)`: who's around, their level, class and gear.
- Trading: `trade.start(name)`, both sides `trade.offer{items = {...}, money = copper}`, both `trade.accept()`.
  A refused trade fails the call with the reason (`too_far`, `busy`, `not_enough_money`...), also in `Trade.error`.

### When something doesn't work

| You see | It usually means | Check |
|---|---|---|
| `move.to` with `no_path`, or ending `stuck` | no walkable route there (inside a building, wrong floor, under the map) | `world.path`, the point's z, `azworld route` |
| distance to a target not dropping | blocked by terrain or out of reach | `path_distance`, `reachable`, `in_sight` |
| cast `line_of_sight` | a wall, rock or rise in between (in caves, often the tunnel's bend) | `units.in_sight(guid)`; `act.approach` closes in until it's in sight |
| `out_of_range` | too far for that spell | `spells.info(id).range` |
| `bad_targets` | not a valid target (dead, friendly, tapped by someone else) | the unit's `alive`, `reaction`, `tapped_by_other` |
| `equipped_item_class*` | your weapon is broken or missing | `items.broken()` |
| `not_ready` / `no_power` | cooldown, or not enough mana, rage or energy | `spells.cooldown(id)`, `items.cooldown(item)`, `spells.usable(id, guid)` |
| `caster_aurastate` / `target_aurastate` | the spell's condition isn't met (Victory Rush without a kill, Execute above 20%) | `me.states()`, the target's `states`, `spells.usable` |
| `unit_not_infront` | the target isn't in front of you | `move.face(guid)` before the cast (`act.cast` faces for you) |
| casting while dead (`caster_dead`) | a script still running after you died | `me.alive()`, or wait in your loop while dead |
| `interrupted` | you moved or were hit mid-cast | `me.casting()` |
| `miss`, `resist`, `immune`, `evade`, `reflect`, `dodge`, `parry`... | the spell went off but didn't land: power and cooldown are spent, so a retry may hit `not_ready` | `spells.cooldown(id)`; `immune`/`evade`: try another spell or target |
| `bags_full`, loot left on corpses | no bag space | `items.free_slots()` |
| `turn_in` times out | the quest isn't complete, or a different NPC takes it | `quests.log()`, `azworld quest <id>` |
| accepting does nothing | level too low, a prerequisite missing, or already done | `azworld quest <id>` (min level, chain) |
| a mob takes no damage | evading, or tagged by someone else | `evading`, `tapped_by_other` |

## World data: `azworld`

`azworld` reads the game's static world database: what drops where, who gives which quest and where its targets
are, which NPCs are hostile to you. It prints JSON, with relations resolved:

```sh
azworld search "Hogger"                          # items, quests, npcs, objects, spells, zones, achievements by name
azworld item 1931                                # stats and sources: drops, chests and nodes, vendors, quest rewards, crafting
azworld quest 176                                # giver and turn-in with coordinates, objectives with spawns, rewards, chain
azworld npc 448                                  # level, elite, reaction to you, spawns, loot, vendor stock, trainer spells
azworld spell 6673                               # cost, cast time, range, cooldowns, which trainers teach it
azworld zone "Elwynn Forest"                     # sub-areas, mobs by level, graveyards, inns, flight masters, vendors, trainers
azworld near -9460 70 --kind trainer             # nearest trainer|vendor|questgiver|flightmaster|innkeeper|repair|banker|graveyard|object
azworld near -9460 70 --kind mob --hostile --level 9-11   # nearest hostile mobs, levels 9-11
azworld quests --level 10 --race 1 --class 1 --near -9460,70   # quests you can pick up, nearest givers first
azworld area -9460 70                            # which area and zone a point is in
azworld route -9460 70 -9660 690 --level 7       # walking waypoints that prefer roads and avoid hostile mobs near or above level 7
azworld map -9460 70 --level 7 [--to -9660,690]  # writes map.png (roads, buildings, water, hostile mobs, the route): view it with Read
azworld roads -9460 70                           # guard patrol paths near a point
azworld upgrades --class priest --level 7 --near -9460,70 [--slot chest]   # gear you can use, by improvement, and where it comes from
```

`reaction` is relative to a new character of each side (`hostile` attacks on sight; `neutral` only fights back).
Races: 1 Human, 3 Dwarf, 4 Night Elf, 7 Gnome; classes: 1 Warrior, 2 Paladin, 4 Rogue, 5 Priest, 8 Mage. Lists are
capped at 15; counts like `drops_total` give the full size. Coordinates match `me.position()`.

**Raw SQL:** `azworld sql "SELECT ..."` runs read-only SQL against the world database (MySQL; TSV output, at most
300 rows):

| Table | Useful columns |
|---|---|
| `creature` (spawns) | `guid, id` (= template entry), `map, position_x, position_y, position_z, spawntimesecs` |
| `creature_template` | `entry, name, subname, minlevel, maxlevel, faction, npcflag, rank` |
| `creature_loot_template` | `Entry` (= creature entry), `Item, Chance` |
| `item_template` | `entry, name, Quality` (0 grey ... 4 epic), `ItemLevel, RequiredLevel, InventoryType, SellPrice` |
| `quest_template` | `ID, LogTitle, QuestLevel, MinLevel, AllowableRaces, ObjectiveText1, RequiredNpcOrGo1, RequiredNpcOrGoCount1, RequiredItemId1, RequiredItemCount1, RewardItem1` |
| `creature_queststarter` / `creature_questender` | `id` (creature entry), `quest` |
| `npc_vendor` | `entry` (vendor creature), `item` |
| `gameobject`, `gameobject_template`, `gameobject_loot_template` | spawns, names and loot of chests, herbs, ore |

The `wowbench` schema has derived data (qualify the table names): `creature_spawn` and `gameobject_spawn` (spawns
with `area` and `zone`), `reaction` (`creature_template.faction` to hostility), `spell` (names, costs,
cooldowns), and the achievements:

| Table | Useful columns |
|---|---|
| `wowbench.achievement` | `id, name, description, points, category, faction` (-1 any, 1 Alliance), `flags` (0x1 = a statistic, not earnable), `count`/`ref_achievement` (meta achievements) |
| `wowbench.achievement_criteria` | `id, achievement, type, asset, quantity, description`: all of an achievement's criteria count unless `achievement.count` says how many |
| `wowbench.criteria_type` | `id, name` (`explore_area`, `complete_quest_count`, `kill_creature`, `learn_skill_level`...); `asset` is a creature, quest, spell, item or map overlay id depending on the type |
| `wowbench.achievement_category` | `id, parent, name` |
| `wowbench.area` | `id, map, zone` (parent zone, 0 = a zone itself), `level, name` |
| `wowbench.map_overlay` | `id` (= `asset` of `explore_area` criteria), `area1..area4` (the areas that reveal it) |
| `wowbench.area_location` | `area, map, x, y` (a point inside the area), `min_x, max_x, min_y, max_y` |

```sh
# level 3-5 mobs near Northshire (map 0 = Eastern Kingdoms), by spawn count
azworld sql "SELECT t.entry, t.name, t.minlevel, t.maxlevel, COUNT(*) n, ROUND(AVG(c.position_x)) x, ROUND(AVG(c.position_y)) y
  FROM creature c JOIN creature_template t ON t.entry = c.id
  WHERE c.map = 0 AND t.maxlevel BETWEEN 3 AND 5 AND t.npcflag = 0
    AND c.position_x BETWEEN -9500 AND -8500 AND c.position_y BETWEEN -800 AND 200
  GROUP BY t.entry ORDER BY n DESC LIMIT 10"
```

`rank` is a MySQL reserved word: write it as `` t.`rank` ``.

## The raw protocol

The client doesn't implement mail or the auction house. `client.watch(opcode)` captures a server opcode,
`client.packets()` drains what was captured as `{opcode, hex, time_ms}`, `client.send(opcode, "hex body")` sends a
client packet (header and encryption handled), and `client.call("Method", ...)` calls any exported method of the
underlying `WorldClient` (`client/world.go`). Opcode numbers are in the client's `opcodes.go` and the server's
`Server/Protocol/Opcodes.h`; packet layouts in the server's `Handlers/*.cpp`; enums in `shared/SharedDefines.h`.
For example the mailbox: `CMSG_GET_MAIL_LIST` (0x23A, body = mailbox guid as u64 LE), reply
`SMSG_MAIL_LIST_RESULT` (0x23B). All numbers are little-endian.

`chat.command(".cmd")` sends a server command; GM commands only work on GM accounts, and scored runs don't use one.
