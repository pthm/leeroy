# Agent tooling reliability and API contract

## Purpose and scope

Agents can complete early quests and keep Lua scripts running, but a failed route, competing movement tasks, or an unsafe death recovery can consume much of a run. The tools must make these failures observable and bounded, then handle routine movement and casting corrections without choosing the agent's strategy.

This spec covers `azbench` diagnostics and runtime configuration, `azworld` routes, and the session's Lua and `wow` interfaces. It defines consistent signatures, identifiers, results, postconditions, and query behavior alongside execution reliability. It applies to Claude Code, Codex, and OpenCode runs. The judge continues to score game outcomes from server state; tool diagnostics do not affect scores. `wow look`, danger summaries, fight logs, `units.in_sight`, `units.reachable`, and the existing action helpers are inputs to this work, not replacements for it.

An agent still chooses its goal, targets, spells, party behavior, risk tolerance, and whether to abandon an objective. The SDK may reject an operation whose mechanical preconditions are false and explain why. It reports estimated danger; it does not turn that estimate into an automatic strategy choice. It must not silently choose a new quest, target, route destination, or resurrection method with a material cost.

The mage and Hogger runs also show agent-authored state machines advancing after failed quest actions, competing task loops, and repeated pulls after poor results. Tooling should make the underlying outcomes, ownership, and evidence reliable. Quest progression rules, a party phase protocol, readiness thresholds, wipe budgets, and encounter feasibility decisions remain agent-authored. The SDK must not supply a quest state machine, group commander, or automatic retreat policy.

The target API and report schemas are a clean break. Compatibility with old workspaces and report consumers is not required. Existing workspace scripts do not constrain names or return shapes. Update the skeleton and examples, regenerate the reference, and use fresh workspaces after implementation. A paused run must remain on its original build or be ended before switching to this API.

## Behavioral requirements

1. A tool call must either complete or return a typed failure within a stated deadline. Long-lived behavior such as follow requires an explicit completion condition or timeout and remains cancellable; each nested route query, movement request, recovery attempt, or cast retry is bounded.
2. At most one task controls character movement at a time. The current owner and queued or preempted tasks must be visible through `wow tasks` and `me.movement()`.
3. A higher-priority recovery operation may preempt travel. Preemption must be explicit in the displaced action's result and trace; ordinary tasks of equal priority must not repeatedly supersede each other.
4. Helpers may correct routine mechanics, such as stopping before a cast or facing a target. Corrections have fixed attempt and time limits and appear in the action result and trace.
5. Reports must distinguish a game loss, an observable script error, an SDK failure, and a harness failure as far as the evidence allows. A failed action followed by continued play does not prove that the script advanced the wrong state or that its strategy was poor. Unknown causes remain `unknown`, with the relevant event IDs; the report must not guess.
6. Pause, resume, and agent restart must preserve diagnostics and avoid treating the paused interval as active play or a completed judge result.

## API findings and required remedies

| Current issue | Evidence | Required remedy |
|---|---|---|
| `npc` means a GUID in raw calls but an entry ID in `act` errands | `quests.accept(npc, quest)` versus `act.reach_npc{npc=...}` | Name the raw argument `npc_guid`; name the helper selector `npc_entry` or `npc_guid`, with exactly one supplied. Never infer one from the other. |
| The reference's `Result` requires `id`, but `act` constructs results without one | `types.Result`, `act.lua`'s local `result()` | Define and document `Request`, `ActionResult`, and `ActResult` separately. Normalize every action helper's return to `ActResult`. |
| Several composite helpers return `ok=true` after a required step failed | `act.vendor`, `act.train`, `act.loot_nearby`, `act.recover` | Propagate failure, include completed steps and partial data, and stop dependent steps. |
| Some helpers report an operation before the server confirms it | `act.consume`, `act.make_room` | Make them waiting helpers that return `ActResult` only after confirmation. |
| Completion can mean the wrong floor, a missing quest, or elapsed time | `act.go_to`, `act.use_object`, `act.rest` | Use operation-specific postconditions; return a typed incomplete or timeout result when they are unmet. |
| A missing query value can mean absent, loading, or inaccessible | `items.info`, `quests.info`, `vendor.items`, `gossip.open` | Make server-backed lookups awaitable and distinguish `not_found`, `not_available`, and `timeout`. |
| Readiness is spread across overlapping booleans | `spells.usable`, `act.ready`, `act.in_range` | Add one structured `spells.check` and use it inside `act.cast`. Keep simple booleans only as named convenience predicates. |
| The benchmark can score skill goals, but agents cannot inspect skill values | `skill:<id>:<n>` versus no `skills` API | Add `skills.list()` and `skills.get(id)` before running skill-goal scenarios. |

## API contract

### Names and selectors

- Use `_guid` for a specific observed unit or object instance and `_entry` for a template ID. GUIDs remain strings; entries remain numbers. A parameter named only `npc`, `unit`, `item`, or `object` is acceptable solely when its documented type is an explicit selector union and the implementation validates it.
- Raw NPC actions take a nearby `npc_guid`: `quests.accept(npc_guid, quest_id)`, `trainer.learn(npc_guid, spell_id)`, `vendor.buy(npc_guid, item_entry, count)`, and similar calls. `act` errands take exactly one of `npc_entry` or `npc_guid`, plus optional `x, y, z` for finding an entry. A GUID is never treated as an entry after a lookup fails.
- Document whether a coordinate is 2D or 3D, its map, units, and its tolerance. A requested `z` is part of the destination; it is not just a pathfinding hint.
- All options tables reject unknown keys and missing required keys with a signature error naming the function and accepted fields. Optional fields state their defaults in `help()` and the reference. A `nil` option does not silently change an explicitly documented default.

### Raw requests and helper results

Raw actions return `Request<T> = {id, op, pending=true}`. `await(request)` (inside a task) and a resolved `result(id)` return `ActionResult<T> = {id, op, ok, data?, error?, args?, ms?}`. `ok=true` means the operation's documented postcondition was observed; a sent packet or an elapsed wait alone is insufficient. `result(id)` returns the request handle while unresolved and `nil` for an unknown or expired ID. `await` timeouts return `ActionResult` with `wait_timeout`, without a `pending` field, and remain distinguishable from the underlying request's later result.

Every `act` helper that performs an action returns this separate shape after it finishes:

```lua
ActResult<T> = {
  op = "act.vendor", ok = false,
  data = {completed = {...}, remaining = {...}},
  error = {code = "not_enough_money", message = "...", next = "..."},
  steps = {{op = "vendor.sell_junk", ok = true, request_id = 18},
           {op = "vendor.buy", ok = false, request_id = 19, code = "not_enough_money"}}
}
```

Both result types use `error = {code, message, next?}` on failure; `next` is present only when one concrete corrective action is known. `ActResult` has no `id` or `pending`. The `steps` array is present for composite helpers and omitted for a simple helper. It has a bounded size; repeated identical attempts may be summarized with a count. A skipped operation is `ok=true` only when the skip is the helper's documented postcondition, and its `data.status` says why (`already_active`, `already_at_destination`, or similar). Helpers return neither a raw `ActionResult` nor `nil`. `act.ok(op, data)` and `act.fail(op, code, message)` create this shape for agent-written helpers; `act.must(result)` raises on `ok=false` and returns the result otherwise.

`act` functions that only inspect state retain ordinary query return types. `act.npc` and `act.player` explicitly return `Creature?` and `Player?`. `act.player` may return a distant group member only when its position source and freshness are known; otherwise the group roster remains available through `group.info()` and the helper returns `nil`. Do not substitute a stale position without an age field.

### Failure propagation and partial work

A composite helper stops when a required step fails. Its top-level result is `ok=false`, preserves the failing step's code, and includes what succeeded so a script can decide whether to retry. It does not automatically replay a non-idempotent step. An optional step must be explicitly declared optional in the call; a failure there is reported in `steps` and `data.warnings`, while the top-level result may be `ok=true` only if the stated postcondition still holds.

Specific contracts:

- `act.vendor{npc_entry|npc_guid, sell_junk?, repair?, buy?}` performs only requested operations. At least one operation is required. `sell_junk` and `repair` default to false; `buy` defaults to an empty list. Check the NPC's roles before starting. A failed sale, repair, or purchase makes the overall call fail and records earlier successful operations. Selling poor-quality items does not happen as a side effect of buying or repairing.
- `act.train{npc_entry|npc_guid, spells={...}}` requires a nonempty spell list. It returns `ok=true` only if every requested spell was learned or was already known at the start; `data.learned`, `data.already_known`, and `data.failed` list each spell. Stop on the first failure unless `continue_on_error=true` is set, and still return `ok=false` if any failed.
- `act.loot_nearby{dist, make_room=false}` fails on approach or loot failure and returns the items and money already looted. It destroys nothing unless `make_room=true`. If a retry after freeing space fails, propagate that result.
- `act.consume(kind)` waits for `items.use` and returns `ActResult` with the item used. `act.make_room{...}` waits for `items.destroy` and returns the destroyed item. The default selection protects usable gear upgrades and items whose information has not loaded; if none qualifies, return `no_disposable_item`.
- `act.buff(id, guid)` returns `ok=true, data.status="already_active"` when the aura is present. `act.cast` always wraps the underlying request in `ActResult`; it never returns a raw action result on one branch and a helper result on another.

### Completion and bounded execution

- `act.use_object{entry,...,quest_id?}` requires the quest to be in the log when `quest_id` is supplied. Success requires the requested count or observed quest progress/completion, not merely an `objects.use` success or a quest disappearing from the log. It has `max_attempts` and `timeout_s` defaults and returns partial use count on failure.
- `act.rest{hp,mana,max_s}` succeeds only if both requested thresholds were reached. On expiry it returns `rest_timeout` with achieved fractions. An item-use failure is included in the result, not treated as consumption.

### Lookups

Local cached queries (`me.level`, `units.nearby`, `spells.known`, and similar calls) remain synchronous. A lookup that must ask the server returns a request even when cached, so scripts can consistently `await` it: `items.info(item_entry) → Request<Item>`, `quests.info(quest_id) → Request<QuestInfo>`, `vendor.items(npc_guid) → Request<VendorItem[]>`, and `me.achievements() → Request<Achievements>`. A cached answer resolves immediately. `not_found`, `not_available`, and `timeout` are distinct failures. An interaction that opens a different window returns a discriminated result, for example `gossip.open` with `{kind="gossip"|"quest"|"vendor"|"trainer"|"flight", value?}`, rather than successful `nil`. Keep `units.get` as a synchronous visibility query; its `nil` means the unit is not currently observed.

### API overlap and coverage

Keep raw and helper pairs (`spells.cast`/`act.cast`, `move.to`/`act.go_to`, `death.*`/`act.recover`, `vendor.*`/`act.vendor`). Their different control levels are useful once the result and identifier contracts are explicit. Keep `units.attackers` (targeting you) and `units.threats` (targeting you, your pet, or your group) as separate, distinctly named queries. Keep `me.status`, `world.look`, combat events, and fight summaries: they answer different questions at different detail levels.

Use `spells.find(name, rank)` for rank lookup and remove `act.best({rank_ids})`; scripts with an explicit ID list can use `spells.known` in a loop. `act.hp` and `act.mana` are harmless shortcuts but should be grouped as convenience queries, not implied to be more authoritative than `me.health()` and `me.power()`.

Add `skills.list() → Skill[]` and `skills.get(skill_id) → Skill?`, each with ID, name, current value, maximum, and category. The values must match what the judge reads for `skill:<id>:<n>` after the server saves the character. Test gathering and a known crafting spell through the existing `objects.use` and `spells.cast` paths. Add a dedicated crafting action only if those paths cannot execute the profession workflow; do not introduce a second way to cast the same recipe without a concrete need. Mail, auction house, and banking remain outside this spec.

## Bounded three-dimensional routes

`azworld route` supplies a strategic route: roads, static spawn danger, and broad waypoints. The session's embedded navmesh supplies the authoritative walkability and floor check for each leg. A route returned as executable must connect the requested start and destination on the requested map and floor. A two-dimensional terrain path alone cannot certify a cave, bridge, building, or upstairs destination.

Add an explicit destination form that accepts `x y z map`; keep the current two-dimensional syntax for outdoor planning and label its result `unverified`. An agent can submit a proposed route to `world.route(points, opts)` in the session, or use `act.travel{points=...}` with validation enabled by default. The validator returns the first invalid leg and its endpoint gap before movement starts. `world.path` remains available for one leg. Both `world.path` and `world.route` return awaitable requests so expensive validation cannot block a Lua tick; their typed result data follows the checks below. Do not silently replace a requested cave floor with outdoor terrain height.

The route result has `status` (`verified`, `unverified`, `partial`, or `failed`), `reason`, `map`, 3D waypoints, total walk distance, estimated hostile exposure, validation source, and per-leg endpoint gaps. `partial` includes the reachable prefix and the blocked leg; callers must opt in before walking a partial route. `route_unreachable` names the first disconnected leg. A direct-line fallback is labeled `unverified` and is never handed to `act.travel` as a verified path.

Enforce a 15-second wall-clock deadline per route query and a documented maximum query area. If the deadline is reached, return `route_timeout` with a suggested maximum leg length and any completed prefix. Cap simultaneous route computations and cache identical navmesh leg checks for the session. A cancellation must stop the underlying computation rather than only return early to the caller. Keep road and threat costs separate from walkability: a dangerous but walkable route is not `unreachable`.

Static mob positions are an estimate. The live session recomputes nearby hostile exposure from observed units, their floor, line of sight, and aggro range. `act.travel` reports this exposure before each leg and when new hostiles appear. By default, the estimate is diagnostic and travel continues; `stop_on_danger = true` makes the helper stop and return `danger_ahead` with the threatening units and current position. A caller may use `on_attacked` to handle actual combat. Neither the route service nor the travel helper chooses a different strategic destination or decides whether the objective is worth the risk.

`act.go_to{x,y,z,...}` succeeds only after the character is within horizontal tolerance (default 4 yards) and, when `z` is supplied or a floor was selected, vertical tolerance (default 3 yards) on that floor. Return `wrong_floor` or `unreachable` with the observed position and remaining gap. `act.travel` validates and reports the first failed waypoint; it cannot report success after a wrong-floor leg.

The awaited `world.path` result contains a typed path check with `reachable`, endpoint gap, floor, and a reason instead of `nil` for every failure.

`act.danger{x, y, z, radius=25}` returns `DangerCheck = {state="clear"|"hostile"|"unknown", hostiles=Creature[], reason?}`. It checks whether each observed creature is on the same reachable floor and in line of sight. An empty `hostiles` list cannot mean both clear and insufficient data. Recovery and travel share this check rather than reimplementing it.

## One movement owner

All movement-producing operations (`move.to`, `act.go_to`, `act.travel`, `act.approach`, follow, corpse recovery, and helper calls to them) use one session movement controller. The controller sits below the Lua helpers so direct `move.to` cannot bypass ownership. It tracks the task ID, owning call ID, active raw request ID when present, priority, destination, and phase, and exposes the state through `me.movement()` and `wow tasks`.

Priorities are `recovery` > `urgent` > `normal`. `act.recover` acquires `recovery`; an agent may request `urgent` for a bounded escape or defensive move. All other movement defaults to `normal`. A task retains ownership across waypoints and while its `on_attacked` callback handles an attacker; the callback may move under the same owner. Ownership releases when the operation succeeds, fails, is cancelled, or the task ends.

At equal priority, a new request waits in a FIFO queue for up to its `queue_timeout_s` (default 5 seconds), then returns `movement_busy` with the owner's task and operation. The same task may replace its own destination; the old request returns `replaced_by_owner`. A higher-priority request preempts immediately; the old request returns `preempted` with the new owner and does not resume automatically. When a task is paused, stopped, or fails, the controller cancels its active movement and clears its queued requests. A login reset clears ownership and emits a `runtime_state` event.

Expose `move.owner()` for inspection and `move.cancel(request_id)` for explicit cancellation. `act` helpers manage acquisition automatically. Scripts that need a multi-step critical section may use `move.with_control{priority, queue_timeout_s, fn}`; it cannot hold ownership while waiting indefinitely and has a maximum lease duration. The controller must never pause or stop an unrelated Lua task on its own.

`act.approach` awaits or checks the final facing request before success. It returns `unit_gone`, `unreachable`, or `timeout` within its deadline. It does not repeatedly issue movement requests that supersede one another.

`act.follow{name, until, timeout_s}` requires either `until` or `timeout_s`; `until` is checked on each pass. The helper returns `player_not_visible` after a bounded absence instead of waiting forever.

## Bounded cast correction

`spells.check(spell_id, target_guid?)` returns `{usable, reason?, cooldown_ms?, power_needed?, range_yards?, distance_yards?, in_sight?, facing?, target_valid?}`. Its fields distinguish a known negative from unavailable information; it does not promise that the server will accept a later cast. `spells.usable` may remain a bool convenience backed by `spells.check`. `act.ready` should be removed as a duplicate. `act.in_range` should either be removed or renamed `act.within_nominal_range` and documented as a distance-only test. `act.cast` uses `spells.check` for its initial refusal and returns a specific reason instead of one broad `not_ready` message.

Keep `spells.cast` as the raw request. Extend `act.cast(spell_id, target_guid[, options])` as the mechanical helper. Its default behavior is:

1. Use `spells.check` to inspect spell knowledge, power, aura condition, cooldown, target validity, range, and current casting state. Return the specific known failure reason and remaining cooldown when available.
2. If the character is moving under the calling task's control, stop and wait for movement to cease. If another task owns movement, return `movement_busy` rather than stopping that task.
3. Face the target and wait for the turn to complete; cast once. Retry at most once when the target moved across the facing arc.
4. Return `ActResult` preserving the underlying server outcome and data, plus `attempts` and `corrections`. Never report success from a timeout alone; reconcile it with a matching cast event before returning success.

The helper does not chase a target by default. `options.approach = true` permits one bounded approach only when the target is reachable and line of sight or range is the failure, with `max_move_yards` and `max_time_s` required or defaulted conservatively. Healing a party member never moves the healer unless the caller requests it. A single call has bounded corrections and a deadline. Repeated identical failures across calls generate a diagnostic episode; the SDK does not impose a hidden per-task backoff or choose when the script should try again.

## Supervised recovery

`act.recover` becomes a bounded state machine with states `dead`, `releasing`, `ghost_travel`, `reclaim_wait`, `reclaiming`, `spirit_healer`, `resting`, `needs_repair`, `done`, and `attention`. It acquires recovery movement priority before moving and emits each transition. Duplicate death handlers join the current recovery attempt instead of starting another. Other movement receives `preempted` or `movement_busy`; a script's combat loop is not silently restarted while dead.

Default policy favors corpse reclaim when a validated path exists and a reclaim point lies outside observed hostile aggro. It checks the corpse floor, live hostiles, line of sight, and the server's reclaim timer. It does not assume that a timer rounded to zero guarantees reclaim is ready. Spirit healer resurrection requires `spirit = true` or `allow_spirit_fallback = true`; when corpse travel fails without that option, return `attention` with the available healer's durability cost so the agent can choose. Even with permission, refuse a healer area observed to be camped. If both sites are unsafe or two attempts fail, stop at `attention` with `unsafe_recovery` or `retry_exhausted`. Do not loop through repeated deaths automatically.

After resurrection, check health, power, broken equipment, and money. Rest when safe. If the main-hand weapon is broken, emit a `gear_state` event with a `needs_repair` attention code and return `needs_repair` with known nearby repair options. The SDK does not restart an unrelated farming loop; the agent chooses whether to travel to a vendor or continue. A reconnect resumes from observed game state, not a stale Lua state name.

`act.recover` returns the failure of release, travel, reclaim, spirit resurrection, or rest when that step is required. `act.corpse_run` and `act.spirit_res` check every nested movement and resurrection result. A failed reclaim cannot loop without a deadline or attempt limit. A safe-spot walk that fails cannot be ignored.

`attention` and `needs_repair` are terminal recovery states, not alternate result shapes. Return `ActResult` with `ok=false`, the typed error, and `data.state`; `done` requires the requested recovery postconditions to hold.

## Structured diagnostics

### Event contract

The session and harness emit structured diagnostic events alongside their existing records. Extend the existing `<agent>.record.jsonl` stream where its writer owns the event; have the supervisor write harness events to `<agent>/harness.jsonl`. Do not parse human-readable `session.log` or model prose to determine failure categories. Every diagnostic event has:

```json
{
  "schema": 1,
  "seq": 1517,
  "t": 1793300000000,
  "agent": "a30",
  "session": "session-attempt-id",
  "task": "main",
  "call": "c1516",
  "parent_call": "c1510",
  "kind": "action_finished",
  "op": "act.go_to",
  "outcome": "failed",
  "code": "unreachable",
  "duration_ms": 1240,
  "details": {"destination": {"map": 0, "x": -9843, "y": 127, "z": 5}, "gap_yards": 18}
}
```

`session` changes on reconnect; `(session, seq)` identifies a session event. Each action invocation has a session-unique `call` ID, and nested helpers and raw actions name their `parent_call`. A raw action event also includes its session-unique `request` ID; helper calls need no raw request ID and `ActResult` does not gain an `id` field for tracing. A supervisor event has its own monotonically increasing `seq` in `harness.jsonl` and may omit `session`, `task`, `call`, `parent_call`, and `request`. Timestamps are UTC Unix milliseconds. `details` is typed per event kind, bounded in size, and contains no model prompt, credentials, or full chat text. Add these kinds:

| Kind | Required details |
|---|---|
| `action_finished` | operation, outcome, error code on failure, duration, call ID, raw request ID when applicable, any bounded correction attempts |
| `movement_owner` | owner task, priority, acquire/release/preempt reason, displaced request ID |
| `route_finished` | source (`azworld` or session), start and destination, result code, duration, path length, endpoint gap, floor validity, risk summary |
| `recovery_transition` | state, attempt, chosen method, reason, result |
| `gear_state` | broken equipped item count and whether the main hand is broken; emit on change |
| `tool_call` | runtime, tool name, duration, exit or timeout class, command family (not full arguments) |
| `runtime_state` | start, stop, restart, pause, resume, usage-limit exit, and judge state |
| `task_status` | task, agent-supplied status text, when `task.status(text)` changes |

Emit a bounded `task_status` event when the existing `task.status(text)` changes. The status is observational: it does not validate a quest, coordinate a party, or change the judge. Diagnostic tools may correlate a status change with preceding action results but must identify it as an agent claim, not verified game state. Include a source file and line when available. Status text is optional; no report category may depend on agents setting it.

Use one coherent error code set across the new API; keeping an old code is a choice, not a compatibility requirement. Control failures include `movement_busy`, `preempted`, `replaced_by_owner`, `route_timeout`, `route_unreachable`, `danger_ahead`, `unsafe_recovery`, `needs_repair`, and `retry_exhausted`. Every failure result contains `code`, a plain-language `message`, and `next` when there is one concrete corrective action. Do not encode a successful cast as an error.

### Report contract

`azbench report` writes `diagnostics` to each agent's `report.json` entry and a compact `diagnostics` summary to `team.json`. Version these fields with `diagnostics.schema = 1`. Judge scores keep their meanings; other report fields may change in the clean schema revision. Include:

- Counts and total time by operation and error code, plus the first and last event IDs for each repeated failure.
- Repeated-failure episodes: at least three identical `(task, op, code, target or destination)` failures within 30 seconds with no intervening success. Show episode duration, not only count.
- Movement preemptions, time waiting for movement ownership, route timeouts, recovery attempts, dead time, broken-weapon time, and shell tool timeouts or denials.
- Data coverage: record files found, session attempts, earliest and latest event, missing intervals, and model cost status (`known`, `estimated`, or `unavailable`). An unavailable cost is `null`, not zero.
- Run state (`running`, `paused`, `completed`, or `failed`) and per-agent judge state. A paused run shows provisional board values and no final goal result until its judge finishes.
- A separate evidence view for agent choices: action result, any subsequent task status, observed quest or group state, and later action. Do not automatically label a failed quest accept or turn-in as completed, or a repeated pull as a tooling fault. A status change can suggest that the script ignored a failed result; only inspected code or an explicit agent assertion can establish that it did. Show the sequence as evidence without inferring intent from the action trace alone.

`azbench report` works on a live or paused run and never mutates the judge's final result. `azbench status` shows the three most consequential active failure episodes, selected by time lost, with links or event IDs for investigation. Reports remain reproducible from saved run artifacts after the server is stopped.

## Runtime shell contract

Generate the agent-facing command rules from each runtime's actual permission configuration. A command shown as allowed must run in a smoke test for Claude Code, Codex, and OpenCode. Prefer one command per tool call. Where pipes or separators cannot be supported consistently, remove them from the shared instructions and provide `wow`/`azworld` flags for common filtering and limiting instead.

`wow wait` reports the runtime's safe maximum wait in `wow help`; examples stay below that maximum. If a runtime shell returns a session handle before a wait completes, document and test the polling path. A shell timeout is a tool failure in diagnostics, not a game timeout. Unknown or blocked commands return a concise reason with an allowed alternative where one exists. Keep the workspace write boundary and game rules intact.

## Scenario calibration

Add an optional `azbench scenario check <scenario.yaml>` preflight. It verifies that the goal exists, the starting position and floor are reachable, required quest givers and objects are present, and each character can take the goal at its configured level. For combat goals, it reports the target's level, elite status, health, expected hit penalty from level difference, starting gear and money, and whether the scenario supplies enough time to train or prepare. It labels these as estimates, not a prediction of victory. A deliberately difficult scenario may still run; the preflight reports its conditions instead of silently changing them.

The preflight output is saved with the run's scenario snapshot so later reviews can distinguish a tooling regression from an intentional challenge. It is for scenario authors and reviewers, not injected into agent prompts or run workspaces. Agents can inspect encounter evidence available through ordinary game observations and decide whether to prepare, try, or abandon the goal. Tests cover an early quest chain, a cave exploration goal, and the level-5 Hogger trio setup.

## Reference and contract validation

The generated API reference remains the agent's source for every callable function, type, event, and result. `apigen` continues to read the live function registry and `types`, but generation alone does not prove behavior. Add an API contract test suite that:

1. Enumerates `api.docs` and asserts that each registered function has a unique name, a parseable signature, correct required and optional argument counts, a return type, and a documented error or `nil` condition.
2. Checks the generated reference against the current registry and model. `Request`, `ActionResult`, `ActResult`, lookup failures, and every composite helper's `data` type appear in the reference; generated result fields match actual values.
3. Runs Lua examples from the reference and skeleton against mocked session actions. Test entry versus GUID validation, unexpected option keys, cached and uncached lookups, and all success/partial/failure branches.
4. Exercises the critical paths on the GM test session: a wrong-floor destination, failed vendor purchase after a sale, failed trainer spell after a learned spell, full bags, object use without quest progress, rest timeout, and a failed reclaim or spirit-healer action.

Update `azeroth-sdk/docs/sdk.md` to introduce raw requests and waiting helper results together. Use distinct notation in the reference: `→ Request<T>`, `→ ActionResult<T>` for `await`, and `→ ActResult<T>` for `act`. Mark nullable queries with `?` only when `nil` has one precise meaning. Regenerate `azeroth-sdk/docs/api-reference.md` via `azbench apidoc`; do not edit the generated file by hand.

## Verification and rollout

Implementation started on 2026-10-30. The stack now contains:

| Change | Implemented and unit tested | Remaining boundary |
|---|---|---|
| `tzyvkpts` — result contracts | Separate `Request`, `ActionResult`, and `ActResult`; immediate raw actions return handles; task-only `await` distinguishes wait timeout from eventual completion; waiting helper envelopes preserve partial data and omit raw IDs; buff no-op returns success explicitly | Mechanical success criteria for every raw action and complete contract-registry validation are still pending |
| `pznosnry` — NPC errands | Explicit entry/GUID helper selectors; raw NPC GUIDs cannot fall back to entries; errand option validation; vendor/trainer failure propagation and partial work; partial raw sales/repairs fail; awaitable vendor lists | Other server-backed lookups, validation outside these helpers, and profession queries remain pending |
| `uqxkokkt` — item/quest postconditions | Confirmed consume/destroy; gear-aware disposal; opt-in bag clearing; checked loot steps; rest thresholds/deadline; bounded object use with verified quest progress; recovery propagates a failed rest | Movement ownership, full recovery supervision, and cancellation of underlying requests remain pending |
| `xlnzxnrw` — SDK diagnostics | Session/sequence and nested call/request correlation; unsuppressed raw and helper outcomes; task identity across coroutine waits; bounded status claims; helper termination on task stop/session close; allowlisted trace fields persisted beside gameplay records | Movement, route, recovery and gear events await their controllers; observed quest/group state is not yet included in the evidence view |
| `mkvwrunk` — saved-artifact reports | Operation/error counts and durations; repeated-failure episodes; bounded evidence with source references; coverage and null cost; provisional scores and explicit judge/run state; shared diagnostic reader | Metrics without instrumentation remain explicitly unavailable; diagnostics do not infer strategy failures |
| `yyusqurt` — lifecycle persistence | Supervisor, agent and judge lifecycle events across restart; drained transcripts before usage snapshots; saved usage independent of runtime home logs; pause boundaries excluded from sampled attribution; active episodes in status | Cross-runtime permission smoke tests and live pause/restart acceptance remain pending |
| `ysrlopwt` — runtime tool outcomes | Claude tool-use/results, Codex command executions and OpenCode tool parts; duration/outcome evidence; explicit timeout/denial codes; unknown interrupted outcomes; bounded command-family metadata; timestamp preservation and transcript I/O coverage gaps | Runtime-specific missing failure causes remain unknown; usage-limit observations are recorded without inferring an exit cause; additional runtime event variants need fixtures |
| `pruqooqp` — incomplete/moved runs | Agent cards survive a missing recorder; moved runs prefer their saved local files over stale absolute ready-file paths | Missing files are reported as missing evidence, not reconstructed |

Verification includes mocked Lua action sequences with real scheduler waits, recorder persistence across sessions, exactly-once request observation, episode boundaries, partial/torn artifacts, paused reports, saved usage after moving runtime homes, tool normalization for all three runtimes, transcript draining and moved/incomplete run directories. Race tests cover SDK diagnostic/request observers and the focused harness/tool paths. The API reference is regenerated from the GM session via `azbench apidoc`; the earlier GM smoke test also verifies an immediate spell refusal, a missing quest, and rejection of the old ambiguous NPC selector. This does not complete the live gameplay acceptance matrix.

`go test ./... ./azeroth-sdk/...` passes after the diagnostics stack. Reports regenerated from temporary copies of `20261029-220217-models-frost` and `20261030-100559-hogger-trio` preserve all 13 per-agent score objects. The former reports completed, the latter paused; both correctly report structured diagnostics as unavailable. Original run artifacts were not modified.

The next boundaries remain: full registry/signature validation and server-backed lookup/skills coverage; observed quest/group evidence; movement ownership and recovery supervision; bounded 3D routes; casting readiness/corrections; gear/dead-time diagnostics; cross-runtime permission smoke tests; scenario preflight; and matched fresh-start benchmark runs. Unknown run state is permitted when saved artifacts cannot establish any of the specified lifecycle states. Tool timeout/denial totals describe observed structured codes only. The rollout below remains the target order for the outstanding work.

Implement in this order so later work has trustworthy contracts and evidence:

1. **API contract and diagnostics.** Define requests, results, identifiers, error codes, and event schemas together. Add contract tests, task status events, report coverage, paused-run handling, cost `null`, and cross-runtime command smoke tests. Use saved recent reports as the baseline for judge scores; missing diagnostics in old artifacts remain explicitly unavailable.
2. **Helper correctness and query coverage.** Normalize every action helper to `ActResult`, propagate nested failures, enforce postconditions, make server-backed lookups awaitable, and add skills queries. Exercise failed vendor, trainer, loot, object-use, and rest steps. Update the skeleton and examples and regenerate the reference as each contract changes.
3. **Movement controller and recovery.** Test ownership, queueing, preemption, task stop, reconnect, corpse reclaim, camped graveyard, and broken weapon. Run a short live scenario with a combat loop, an errand, and a death handler active together.
4. **Routes.** Test a road, bridge, cave entrance, upper/lower cave floor, unreachable destination, dangerous but reachable destination, and a deliberately expensive query. Verify the 15-second deadline and cancellation under concurrent agents.
5. **Casting.** Test moving, facing, moving target, line of sight, cooldown, lost target, and a heal across different floors. Verify the correction cap, deadline, and repeated-failure diagnostics from the structured trace.
6. **Benchmark confirmation.** Repeat matched fresh-start runs with the same scenario and runtime settings. Compare time in repeated failure episodes, dead time, route timeouts, and score. Report the sample count and world contention; a single run is a regression signal, not a model ranking.

Keep `wow`, `azworld`, `standings`, and raw action APIs where they serve distinct purposes. Deploy with fresh workspaces. Document every new function and error code in the generated API reference through `azbench apidoc`.

## Completion criteria

- No `act` helper returns success while a required subaction failed or its documented postcondition is false. Every action helper returns `ActResult`, including no-op branches, and exposes partial work without requiring logs.
- The same identifier label has the same meaning in every signature. Entry IDs and GUIDs cannot be silently interchanged.
- Every waiting helper has a deadline or a required completion condition plus explicit cancellation. Failed resurrection or object use cannot cause an unbounded internal loop.
- No route query exceeds 15 seconds. No route marked `verified` has an unreachable leg or silently changes floors in the test fixtures. Movement to the right `x,y` on the wrong floor fails.
- Concurrent normal movement does not oscillate. Every preempted movement request finishes with a named owner and reason.
- Recovery ends alive with its requested postconditions, in `attention`, or with a typed failure within its configured attempt and time limits; repeated deaths do not create an unbounded automatic loop.
- Each cast-helper call performs at most the documented corrections and never returns a false success. Repeated failed calls appear in diagnostics without being silently suppressed.
- Server-backed lookups distinguish a valid absence from loading, refusal, and timeout. `spells.check` explains readiness without requiring a failed cast.
- A missing quest cannot make `act.use_object` succeed. A failed quest action followed by an agent-authored task status change is shown with its observed quest state; the report does not claim the quest succeeded or assign a planning diagnosis. No SDK function chooses a party phase, readiness threshold, or wipe budget.
- An agent can read its current profession skill value for any skill goal offered by `azbench`.
- A completed, paused, or restarted run can be reported from its saved artifacts. Judge scores are unchanged, missing cost is `null`, and every diagnosed failure links to source events.
- Every command in the shared agent instructions passes the permission smoke test on each supported runtime.
- The API reference, runtime `help()`, examples, and contract tests agree on signatures, return shapes, and error meanings.
