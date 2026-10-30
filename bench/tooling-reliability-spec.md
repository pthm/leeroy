# Reliable agent tooling for World of Warcraft runs

## Purpose and scope

Agents can complete early quests and keep Lua scripts running, but a failed route, competing movement tasks, or an unsafe death recovery can consume much of a run. The tools must make these failures observable and bounded, then handle routine movement and casting corrections without choosing the agent's strategy.

This spec covers `azbench` diagnostics and runtime configuration, `azworld` routes, and the session's Lua and `wow` interfaces. It applies to Claude Code, Codex, and OpenCode runs. The judge continues to score game outcomes from server state; tool diagnostics do not affect scores. `wow look`, danger summaries, fight logs, `units.in_sight`, `units.reachable`, and the existing action helpers are inputs to this work, not replacements for it.

An agent still chooses its goal, targets, spells, party behavior, risk tolerance, and whether to abandon an objective. The SDK may stop an unsafe or impossible operation and explain why; it must not silently choose a new quest, target, route destination, or resurrection method with a material cost.

## Behavioral requirements

1. A tool call must either make progress or return a typed failure within a stated deadline. No route query, movement request, recovery attempt, or cast retry may wait indefinitely.
2. At most one task controls character movement at a time. The current owner and queued or preempted tasks must be visible through `wow tasks` and `me.movement()`.
3. A higher-priority recovery operation may preempt travel. Preemption must be explicit in the displaced action's result and trace; ordinary tasks of equal priority must not repeatedly supersede each other.
4. Helpers may correct routine mechanics, such as stopping before a cast or facing a target. Corrections have fixed attempt and time limits and appear in the action result and trace.
5. Reports must distinguish a game loss, an agent script mistake, an SDK failure, and a harness failure as far as the evidence allows. Unknown causes remain `unknown`, with the relevant event IDs; the report must not guess.
6. Pause, resume, and agent restart must preserve diagnostics and avoid treating the paused interval as active play or a completed judge result.

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
  "request": 806,
  "kind": "action_finished",
  "op": "act.go_to",
  "outcome": "failed",
  "code": "no_path",
  "duration_ms": 1240,
  "details": {"destination": {"map": 0, "x": -9843, "y": 127, "z": 5}, "gap_yards": 18}
}
```

`session` changes on reconnect; `(session, seq)` identifies a session event, and `request` is unique within that session. A supervisor event has its own monotonically increasing `seq` in `harness.jsonl` and may omit `session`, `task`, and `request`. Timestamps are UTC Unix milliseconds. `details` is typed per event kind, bounded in size, and contains no model prompt, credentials, or full chat text. Add these kinds:

| Kind | Required details |
|---|---|
| `action_finished` | operation, outcome, error code, duration, request ID, any bounded correction attempts |
| `movement_owner` | owner task, priority, acquire/release/preempt reason, displaced request ID |
| `route_finished` | source (`azworld` or session), start and destination, result code, duration, path length, endpoint gap, floor validity, risk summary |
| `recovery_transition` | state, attempt, chosen method, reason, result |
| `gear_state` | broken equipped item count and whether the main hand is broken; emit on change |
| `tool_call` | runtime, tool name, duration, exit or timeout class, command family (not full arguments) |
| `runtime_state` | start, stop, restart, pause, resume, usage-limit exit, and judge state |

The SDK retains the existing result codes. New control failures use `movement_busy`, `preempted`, `replaced_by_owner`, `route_timeout`, `route_unreachable`, `danger_ahead`, `unsafe_recovery`, `needs_repair`, and `retry_exhausted`. Every failure result contains `code`, a plain-language `message`, and `next` when there is one concrete corrective action. Do not encode a successful cast as an error.

### Report contract

`azbench report` adds `diagnostics` to each agent's `report.json` entry and a compact `diagnostics` summary to `team.json`. Version these fields with `diagnostics.schema = 1`; existing report fields and judge scores keep their meanings. Include:

- Counts and total time by operation and error code, plus the first and last event IDs for each repeated failure.
- Repeated-failure episodes: at least three identical `(task, op, code, target or destination)` failures within 30 seconds with no intervening success. Show episode duration, not only count.
- Movement preemptions, time waiting for movement ownership, route timeouts, recovery attempts, dead time, broken-weapon time, and shell tool timeouts or denials.
- Data coverage: record files found, session attempts, earliest and latest event, missing intervals, and model cost status (`known`, `estimated`, or `unavailable`). An unavailable cost is `null`, not zero.
- Run state (`running`, `paused`, `completed`, or `failed`) and per-agent judge state. A paused run shows provisional board values and no final goal result until its judge finishes.

`azbench report` works on a live or paused run and never mutates the judge's final result. `azbench status` shows the three most consequential active failure episodes, selected by time lost, with links or event IDs for investigation. Reports remain reproducible from saved run artifacts after the server is stopped.

## Bounded three-dimensional routes

`azworld route` supplies a strategic route: roads, static spawn danger, and broad waypoints. The session's embedded navmesh supplies the authoritative walkability and floor check for each leg. A route returned as executable must connect the requested start and destination on the requested map and floor. A two-dimensional terrain path alone cannot certify a cave, bridge, building, or upstairs destination.

Add an explicit destination form that accepts `x y z map`; keep the current two-dimensional syntax for outdoor planning and label its result `unverified`. An agent can submit a proposed route to `world.route(points, opts)` in the session, or use `act.travel{points=...}` with validation enabled by default. The validator returns the first invalid leg and its endpoint gap before movement starts. The existing `world.path` remains available for one leg. Do not silently replace a requested cave floor with outdoor terrain height.

The route result has `status` (`verified`, `unverified`, `partial`, or `failed`), `reason`, `map`, 3D waypoints, total walk distance, estimated hostile exposure, validation source, and per-leg endpoint gaps. `partial` includes the reachable prefix and the blocked leg; callers must opt in before walking a partial route. `route_unreachable` names the first disconnected leg. A direct-line fallback is labeled `unverified` and is never handed to `act.travel` as a verified path.

Enforce a 15-second wall-clock deadline per route query and a documented maximum query area. If the deadline is reached, return `route_timeout` with a suggested maximum leg length and any completed prefix. Cap simultaneous route computations and cache identical navmesh leg checks for the session. A cancellation must stop the underlying computation rather than only return early to the caller. Keep road and threat costs separate from walkability: a dangerous but walkable route is not `unreachable`.

Static mob positions are an estimate. The live session recomputes nearby hostile exposure from observed units, their floor, line of sight, and aggro range. `act.travel` checks this exposure before each leg and when new hostiles appear. It returns `danger_ahead` with the threatening units and a safe stopping point unless the caller provided an `on_attacked` handler or explicitly chose `risk = "accept"`. It does not choose a different strategic destination.

## One movement owner

All movement-producing operations (`move.to`, `act.go_to`, `act.travel`, `act.approach`, follow, corpse recovery, and helper calls to them) use one session movement controller. The controller sits below the Lua helpers so direct `move.to` cannot bypass ownership. It tracks `(task ID, request ID, priority, destination, phase)` and exposes the state through `me.movement()` and `wow tasks`.

Priorities are `recovery` > `urgent` > `normal`. `act.recover` acquires `recovery`; an agent may request `urgent` for a bounded escape or defensive move. All other movement defaults to `normal`. A task retains ownership across waypoints and while its `on_attacked` callback handles an attacker; the callback may move under the same owner. Ownership releases when the operation succeeds, fails, is cancelled, or the task ends.

At equal priority, a new request waits in a FIFO queue for up to its `queue_timeout_s` (default 5 seconds), then returns `movement_busy` with the owner's task and operation. The same task may replace its own destination; the old request returns `replaced_by_owner`. A higher-priority request preempts immediately; the old request returns `preempted` with the new owner and does not resume automatically. When a task is paused, stopped, or fails, the controller cancels its active movement and clears its queued requests. A login reset clears ownership and emits a `runtime_state` event.

Expose `move.owner()` for inspection and `move.cancel(request_id)` for explicit cancellation. `act` helpers manage acquisition automatically. Scripts that need a multi-step critical section may use `move.with_control{priority, queue_timeout_s, fn}`; it cannot hold ownership while waiting indefinitely and has a maximum lease duration. The controller must never pause or stop an unrelated Lua task on its own.

## Bounded cast correction

Keep `spells.cast` as the raw request. Extend `act.cast(id, guid[, options])` as the mechanical helper. Its default behavior is:

1. Check spell knowledge, power, aura condition, cooldown, target validity, range, and current casting state. `not_ready` includes the specific known reason and remaining cooldown when available.
2. If the character is moving under the calling task's control, stop and wait for movement to cease. If another task owns movement, return `movement_busy` rather than stopping that task.
3. Face the target and wait for the turn to complete; cast once. Retry at most once when the target moved across the facing arc.
4. Return the raw server result plus `attempts` and `corrections`. Never report success from a timeout alone; reconcile it with a matching cast event before returning success.

The helper does not chase a target by default. `options.approach = true` permits one bounded approach only when the target is reachable and line of sight or range is the failure, with `max_move_yards` and `max_time_s` required or defaulted conservatively. Healing a party member never moves the healer unless the caller requests it. Repeated identical failures for the same target and spell use a short per-task backoff and return `retry_exhausted`; a new target, movement, or changed spell state clears that backoff. The script chooses another action.

## Supervised recovery

`act.recover` becomes a bounded state machine with states `dead`, `releasing`, `ghost_travel`, `reclaim_wait`, `reclaiming`, `spirit_healer`, `resting`, `needs_repair`, `done`, and `attention`. It acquires recovery movement priority before moving and emits each transition. Duplicate death handlers join the current recovery attempt instead of starting another. Other movement receives `preempted` or `movement_busy`; a script's combat loop is not silently restarted while dead.

Default policy favors corpse reclaim when a validated path exists and a reclaim point lies outside observed hostile aggro. It checks the corpse floor, live hostiles, line of sight, and the server's reclaim timer. It does not assume that a timer rounded to zero guarantees reclaim is ready. Spirit healer resurrection requires `spirit = true` or `allow_spirit_fallback = true`; when corpse travel fails without that option, return `attention` with the available healer's durability cost so the agent can choose. Even with permission, refuse a healer area observed to be camped. If both sites are unsafe or two attempts fail, stop at `attention` with `unsafe_recovery` or `retry_exhausted`. Do not loop through repeated deaths automatically.

After resurrection, check health, power, broken equipment, and money. Rest when safe. If the main-hand weapon is broken, emit a high-priority `gear_broken` attention event and return `needs_repair` with known nearby repair options; do not resume a farming loop as if combat capability were intact. The agent chooses whether to travel to a vendor or continue. A reconnect resumes from observed game state, not a stale Lua state name.

## Runtime shell contract

Generate the agent-facing command rules from each runtime's actual permission configuration. A command shown as allowed must run in a smoke test for Claude Code, Codex, and OpenCode. Prefer one command per tool call. Where pipes or separators cannot be supported consistently, remove them from the shared instructions and provide `wow`/`azworld` flags for common filtering and limiting instead.

`wow wait` reports the runtime's safe maximum wait in `wow help`; examples stay below that maximum. If a runtime shell returns a session handle before a wait completes, document and test the polling path. A shell timeout is a tool failure in diagnostics, not a game timeout. Unknown or blocked commands return a concise reason with an allowed alternative where one exists. Keep the workspace write boundary and game rules intact.

## Scenario calibration

Add an optional `azbench scenario check <scenario.yaml>` preflight. It verifies that the goal exists, the starting position and floor are reachable, required quest givers and objects are present, and each character can take the goal at its configured level. For combat goals, it reports the target's level, elite status, health, expected hit penalty from level difference, starting gear and money, and whether the scenario supplies enough time to train or prepare. It labels these as estimates, not a prediction of victory. A deliberately difficult scenario may still run; the preflight reports its conditions instead of silently changing them.

The preflight output is saved with the run's scenario snapshot so later reviews can distinguish a tooling regression from an intentional challenge. Tests cover an early quest chain, a cave exploration goal, and the level-5 Hogger trio setup.

## Verification and rollout

Implement in this order so later work has trustworthy evidence:

1. **Diagnostics and shell contract.** Add event schemas, report coverage, paused-run handling, cost `null`, and cross-runtime command smoke tests. Compare a rebuilt report against saved recent runs without changing their judge scores.
2. **Movement controller and recovery.** Test ownership, queueing, preemption, task stop, reconnect, corpse reclaim, camped graveyard, and broken weapon. Run a short live scenario with a combat loop, an errand, and a death handler active together.
3. **Routes.** Test a road, bridge, cave entrance, upper/lower cave floor, unreachable destination, dangerous but reachable destination, and a deliberately expensive query. Verify the 15-second deadline and cancellation under concurrent agents.
4. **Casting.** Test moving, facing, moving target, line of sight, cooldown, lost target, and a heal across different floors. Verify the correction cap and failure backoff from the structured trace.
5. **Benchmark confirmation.** Repeat matched fresh-start runs with the same scenario and runtime settings. Compare time in repeated failure episodes, dead time, route timeouts, and score. Report the sample count and world contention; a single run is a regression signal, not a model ranking.

Acceptance gates for the durable solution:

- No route query exceeds 15 seconds, and no route marked `verified` has an unreachable leg or silently changes floors in the test fixtures.
- Concurrent normal movement does not oscillate. Every preempted movement request finishes with a named owner and reason.
- Recovery ends alive, in `attention`, or with a typed failure within its configured attempt and time limits; repeated deaths do not create an unbounded automatic loop.
- The cast helper performs at most the documented corrections and never returns a false success or repeats an unchanged failure indefinitely.
- A completed, paused, or restarted run can be reported from its saved artifacts. Judge scores are unchanged, missing cost is `null`, and every diagnosed failure links to source events.
- Every command in the shared agent instructions passes the permission smoke test on each supported runtime.

The existing `wow`, `azworld`, `standings`, raw action APIs, run files, and score schema remain available. New result fields are additive; new error codes are documented in `api-reference.md`. Refresh generated API documentation through `azbench apidoc` after the Lua interface changes.
