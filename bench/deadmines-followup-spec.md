# Deadmines run follow-up: SDK, harness, scoring and documentation

## Purpose and scope

Run `bench/runs/20261030-212149-deadmines` put five fresh Codex agents (gpt-6.1-sol) on one goal, achievement 628 (the Deadmines cleared). They met it together after 17.7 hours, with 18 party wipes and 115 deaths, for about $307. The run report is "Into the Deadmines" (claude.ai artifact). The agents' notes, scripts and transcripts, plus a read of the code at 1f436d1, surfaced SDK bugs, harness failures, scoring gaps and documentation errors. This spec says what to change and how to verify it.

It covers the session SDK and its Lua API (`azeroth-sdk`), the supervisor, judge and report (`azbench`), the agents' docs (`azeroth-sdk/docs`, copied into each workspace), and `azworld` where it serves the agents. It builds on [tooling-reliability-spec.md](tooling-reliability-spec.md) and keeps its contracts (`Request`, `ActionResult`, `ActResult`, typed errors, one movement owner, bounded helpers).

## Principles

These decide every borderline item below.

1. **Documentation describes systems and rules, never play.** The docs tell an agent how the game and the SDK work: what a mechanic is, what number the server uses, what an API returns, and what makes it fail. They do not say what to do with that knowledge. They give no pull technique, party roles in practice, positioning advice, crowd-control etiquette, readiness checklists, "before a dungeon" lists, boss tactics or recommended levels. Agents are meant to develop strategy by trial and error, and handing them a winning approach would remove what the benchmark measures.
   - Allowed: "Creatures linked in a formation enter combat together when any one of them is attacked." "A healing spell needs line of sight from the caster to the target, checked by the server at cast time." "You can reclaim your corpse within 39 yards of it."
   - Not allowed: "Pull one mob at a time." "Healers should stand within sight of the tank." "Reclaim together at the edge of the camp." "Lock Polymorph on one target."
   - A fact may be included even if it is obviously useful, as long as it is stated as a rule of the game. A rule may not be given in imperative form or framed as advice.
2. **The SDK exposes state and mechanics, not policies.** It may add primitives that let agents observe the world accurately (who is in combat with the party, whether a unit is crowd-controlled and by whom, a door's state, an item's charges) and do things the client can do (split a stack, open a container, stop a cast). It may not add helpers that encode a tactic: a pull routine, a crowd-control lock, a healer positioner, a readiness gate, a party commander. Agents built all of those themselves in this run, and that work is part of the measurement.
3. **Bugs and wrong facts are always fixed.** An SDK result that disagrees with the server, or a doc that states a wrong number, is a defect whatever its strategic effect. Fixing one is not "helping".
4. **The harness must be invisible to play.** Pauses, restarts, deploys and outages must not kill tool calls, lose scripts, run the clock while disconnected, or leave agents uninformed about what changed.
5. **Scores must measure the goal.** A score that is the same for every agent, or that rewards something the scenario doesn't ask for, is a defect.

Clean breaks are fine, as in the reliability spec. Workspaces from this run are not a compatibility constraint, but they are a test corpus: several agent workarounds (cited below) show the expected behaviour.

## Findings

Evidence paths are relative to the run directory or `bench/workspaces/<agent>/`. Agents: a44 Aldous (warrior), a45 Isolde (priest), a46 Merrin (rogue), a47 Thessaly (mage), a48 Corbin (paladin).

### SDK defects

| ID | Defect | Evidence | Required change |
|---|---|---|---|
| S1 | The cached global cooldown restarts on SMSG_SPELL_GO as well as SMSG_SPELL_START. Every cast-time spell then waits about 1.5 s longer than the server requires. | `session/agentapi.go:577` sets `gcdUntil` before the START/GO branch. a45/NOTES.md:47 and a47/NOTES.md:184 found it; a44/RUN_STATE.md recorded Lesser Heal cadence of 4.4 s against 2.6 s. Workarounds in a45/casting.lua, a47/main.lua:133 and a48/main.lua:26. About 40% lower caster throughput for 13 h. | Start the GCD on SPELL_START for cast-time spells, and on SPELL_GO only for instant spells (no START seen). Test: a cast-time spell followed by another; `spells.cooldown` is 0 at cast end plus latency. |
| S2 | The default Opening spell is always 6478. The right spell depends on the lock's type index. | `session/objects.go:28-33`, `items.go:42`. Spell.dbc effect 33 maps lock type to spell: 5→3365, 10→6247, 12→6477, 13→6478, 17→21651. Wardrobe 290 (lock 93, type 12) needs 6477; the gunpowder chest (lock 57) needs 3365 (a46/NOTES.md:110). Quest 64 was declared broken for everyone (a47/NOTES.md:125). | Build the map from Spell.dbc at load and choose by the object's lock entry. An explicit spell argument still overrides. Test: wardrobe and gunpowder chest open with no spell given. |
| S3 | `items.use(item, guid)` always sends TARGET_FLAG_UNIT, even when the target is a game object. | `items.go:1049-1056`. Merrin sent a raw CMSG_USE_ITEM with flag 0x800 to fire the Deadmines cannon (a46/dungeon_objects.lua:54-88). | Choose the target flag from the GUID's high part, reusing `objects.use`'s writer (`items.go:758`). Test: an item used on a game object. |
| S4 | `items.loot(guid)` on a game object refuses with "not_lootable: it's alive". | `items.go:599-615`. Hit by four agents (a44/NOTES.md:49, a45:148, a47:125, a48:6). | For a game-object GUID, return the typed error `wrong_target_type` naming `objects.use`. |
| S5 | `me.stance()` is a stub that always returns 0. | `session/bot.go:1669`; a44/NOTES.md:12. | Read the shapeshift form from UNIT_FIELD_BYTES_2, or remove the function and its reference entry. |
| S6 | `spells.usable` / `spells.check` count facing for spells on friendly targets. The server does not require facing for those. | a45/NOTES.md:109 (missed heals), a46/NOTES.md:286. | Apply the facing check only where the server does (hostile targets with the facing attribute). Test against a friendly target behind the caster. |
| S7 | Spell power cost ignores cost-modifying talents and auras. | a46/NOTES.md:108,168 (Improved Sinister Strike: server 40, SDK 45). Workaround a46/main.lua:36-45. | Apply SpellMod cost modifiers from known talents and auras, or learn the cost from the server's power update after a cast. |
| S8 | Cooldowns that started before a session restart read 0. | Hearthstone refused after a restart (a45/NOTES.md:81,87; a47/NOTES.md:87). fighting.md claims these are tracked. | Seed cooldowns from SMSG_INITIAL_SPELLS on login. Test: restart a session within the hearthstone cooldown. |
| S9 | Equipping a bag (slots 19-22) reports `timeout` although it succeeds; `Item.equipped` is false there. | a47/NOTES.md:82, a46/NOTES.md:166, a45/NOTES.md:76. | Observe slots 19-22 for the equip postcondition. |
| S10 | Ranged attacks (Throw, Shoot) report `not_ready` while `spells.cooldown` is 0, because the ranged attack timer is not a spell cooldown. | a44/RUN_STATE.md (Throw always not_ready), a46/NOTES.md:84. | Report the ranged attack timer in `spells.cooldown` and `spells.check` for auto-repeat ranged spells. |
| S11 | A Go runtime panic inside a native Lua call is not recovered at the call boundary. The task dies with a bare "invalid memory address" and no op name or stack. | `script/engine.go:442-500` (`registerFunc`), `:942` (`safeResume`). Isolde's healer task died 7 times (session.log). | Recover around each native call: log the Go stack to session.log, then raise a Lua error naming the op, so the task traceback includes the Lua line. Find and fix the underlying nil (likely quest-giver handling in `act.use_object`; a44/NOTES.md:25, a45/NOTES.md:160). |
| S12 | Spurious death events: one from predicted damage against a 1 HP ghost on corpse reclaim, and one on logging in while already dead. | `client/world.go:3768-3776`, `session/events.go:245-256`. Aldous 05:22:33, Merrin 11:51:14. 117 events against the judge's 115. | Emit death only from the health field update. Skip the login death when the character's event stream already ends in a death. |
| S13 | `log(msg, data)` drops `data`; `log(table)` prints an empty message. | `script/engine.go:1428-1440`; a44/NOTES.md:65, a45/NOTES.md:114. | Keep `data` on the log event and stringify tables. Provide `json.encode`, which agents expected and lacked (a46/NOTES.md:210). |
| S14 | `units.get(...).alive` is true for ghosts. Party members out of range report `z = 0`. | a44/NOTES.md:43, a45:149, a46:50, a48:15. | `alive=false` and `ghost=true` for ghosts. Omit `z` (nil) when unknown, as the reference already says. |
| S15 | `in_sight` / `world.in_sight` can be true where the server refuses a spell for line of sight. | a44/RUN_STATE.md:61, a44/NOTES.md:56, a47/NOTES.md:5. At least the 11:22 Sneed wipe. `inSight` checks static world models with fixed eye heights (`agentapi.go:228-243`). | Record every server `SPELL_FAILED_LINE_OF_SIGHT` with caster position, target position and the SDK's prediction, as a diagnostic. Use the mismatches to correct the model (doodads, WMO interiors, per-race eye height). Document exactly what the check covers (see D-rules). Spells on friendly targets must use the same check as hostile ones, in `spells.check` and `act.cast`. |
| S16 | `move.turn` / `move.face` sometimes return OK without the orientation changing. `me.position().o` can be nil. | a46/NOTES.md:332, a47/NOTES.md:105. | Make the facing postcondition observed orientation within tolerance. Return a typed failure otherwise. |
| S17 | `items.find(name)` matches the exact name only. The docs read as if it matches by substring. | a46/NOTES.md:136: `items.find("Healing Potion")` returned nil for about 10 h for two agents. api-reference.md:164. | Accept an item entry as well as a name, and document exact matching. A substring query, if added, must be a separate explicit option. |

### SDK primitives to add (state and client actions only)

Each is something the game client can see or do and the SDK cannot. Agents worked around each with raw packets or `client.call`.

| ID | Primitive | Evidence |
|---|---|---|
| P1 | `units.engaged()`: every creature in combat with any party member, including those with no target or fleeing, each with `fleeing` and which members are on its threat list. A view of state. It does not decide when a fight is over. | a44/core.lua `party_attackers`; a44/NOTES.md:3,9; a46/NOTES.md:53. `units.attackers()` covers only the player. |
| P2 | Crowd-control state on units: each controlling aura with `spell`, `caster_guid`, `remaining_ms`, `mechanic` (polymorph, stun, sap, sleep, fear, root), and whether damage breaks it (from Spell.dbc aura interrupt flags). A fact about the unit. No locking or reservation. | Every DPS script parsed chat to learn this (BREAK_CC / LOCK_CC, 371 messages). |
| P3 | `Object.state` (door and chest state: open, closed, alternate) in `objects.find` / `objects.get`. | a44/dungeon_factory.lua `dm_door_state`, a46/dungeon_objects.lua `dm_object_state` (both read update field 17). |
| P4 | `Item.charges`, separate from `stacks`. | a46/NOTES.md:36; a46/main.lua `rogue_marbles_charges`. |
| P5 | `items.split(item, count)`, `items.open(item)`. | Raw CMSG_SPLIT_ITEM in a48/split_supplies.lua, a46/give_potion.lua; CMSG_OPEN_ITEM in a44/redridge_travel.lua, a48/elwynn.lua. |
| P6 | `spells.stop_casting()`, `spells.stop_auto_repeat()`. | `client.call("CancelCast")` in a45/healer.lua:407; packet 0x26D in a47/main.lua:187. |
| P7 | `group.promote(guid)`, `group.set_loot(method, threshold)`. | a45/NOTES.md:146 (GroupSetLeader), a44/NOTES.md:35 (SetLootMethod). |
| P8 | The player's own chat echoed into its events with `self=true`. | a48/RESUME.md:15. |

Out of scope, because they encode tactics: a pull helper, a crowd-control lock or registry, a healer positioner, a pre-pull readiness gate, a party-follow commander, automatic potion or emergency handlers.

### SDK error messages

| ID | Change | Evidence |
|---|---|---|
| E1 | Unknown or mistyped option keys: the error lists the accepted keys and their types (per the reliability spec's options contract). | a45/NOTES.md:5-6 (`act.rest{health}` instead of `hp`). |
| E2 | Calling an undefined API function gives "no such function `objects.get`" with the closest existing names. | a44/RUN_STATE.md:72,75 (hidden by wrappers), a44/NOTES.md:32. |
| E3 | Selector shape errors name the expected shape (`trade.offer items={858}` against `{{858,1}}`). | a45/NOTES.md:6. |
| E4 | `act.follow`'s `until` callback returning nil is treated as false. | a47/NOTES.md:32. |
| E5 | Large results are truncated in events by default: routes to start, end and count; full data on request. | a45/NOTES.md:174 (about 20k tokens); a46 and a47 wrote `compact_wow.py`. |

### Harness

| ID | Defect | Evidence | Required change |
|---|---|---|---|
| H1 | Stopping an agent kills its whole process group at once. Codex loses the in-flight tool call and logs "custom tool call output is missing" on every later request. | `azbench/run/player.go:546`. Corbin 1,139 lines, Thessaly 358, Isolde 297. | A single graceful-stop routine (H8): stop the agent's tool processes, wait up to 3 s for the runtime to record their output, then stop the runtime. Test: resume after stopping during `sleep 60`; no missing-output line. |
| H2 | Resume and restart don't refresh workspace docs. Only first start does. | `p.workspace()` runs only in `prepare()` (`run.go:189`, `control.go`). Workspace docs dated 10-30 21:21 missed the trade, corpse-run, routing and `world.los_spot` changes. | Refresh docs on resume and restart. |
| H3 | Resume messages don't say what changed. Five SDK deploys landed mid-run. | The 14:15 resume said "latest build". | Record the SDK revision in the run dir (`sdk.rev`) at start and at each resume or restart. When it changes, append a generated `WHAT CHANGED` section: commit subjects under `azeroth-sdk` and `bench/agent` since the last recorded revision, plus a line when the jj working copy has uncommitted SDK changes. Descriptions are factual (API added, bug fixed), never advice. |
| H4 | A restart drops every running Lua task and stops a character mid-dungeon without telling the agent its state. | `control.go:76`, `rpc.go:44-80`. Characters stood unscripted 50-75 s; Merrin logged back in dead. | Before stopping, record running task names. After login, run `autorun.lua` from the workspace if present. Add to the restart message: tasks that were running, alive or dead, position. |
| H5 | The judge clock kept running 35 s after the game session lost its world connection. | Daemon exit unobserved (`player.go:129`, `cmd/azsdk/main.go:145`). | When a session process exits while the run isn't stopping: alert, pause the run, and stop the judge at its last sample. |
| H6 | A crashed supervisor shows as "not answering". Its agents and sessions keep playing, unjudged. `resume` doesn't check whether a supervisor is alive. | `run.go:120-150`, `control.go:118`, `player.go:122,276`. | `status` reports `crashed` when control.json's pid is gone. `resume` refuses while that pid lives. The supervisor writes a pids file, and resume stops leftover process groups first. |
| H7 | `dial tcp 127.0.0.1:809x: can't assign requested address` for about 8 minutes before the outage. Every `wow` and `azworld` call opens a fresh TCP connection, so local ephemeral ports are likely exhausted (TIME_WAIT). | a44/RUN_STATE.md:19, a46/NOTES.md:13, a47/NOTES.md:10; 17 errors for Aldous at h 16.69-16.76. | Confirm with a load test that counts TIME_WAIT sockets. Then use keep-alive HTTP or a Unix socket for the `wow` RPC, and a pooled MySQL connection in azworld. |
| H8 | The session ends 7-22 s after the goal. Corbin won a Cape of the Brotherhood roll and never received it. No final notes were written. | `run.go:292-300`. | After the judge records the goal (score frozen), emit a `goal_met` event (added to the default wake types). Keep the agent and session for `wrap_up_minutes` (scenario field, default 5), then stop through H1's routine. Copy `NOTES.md` and other workspace notes into `<run>/<agent>/` at every stop. The message states only that the goal is recorded and the score final. |
| H9 | Operator maintenance notices arrive only as in-game chat. | a44/NOTES.md:37, a47/NOTES.md:39,63 (a notice said 60 minutes, then 1). | Send a structured `maintenance` event with the time it takes effect. Wording is factual. |
| H10 | The shared Codex quota isn't watched (42% to 97% of the weekly limit in this run). | Codex rollouts carry `rate_limits`. | Read the newest `rate_limits` at preflight and every 5 minutes. Record start and end in `codex-quota.json`. Alert at 80% and 90%; pause the run at 97% or when a limit is reached. |
| H11 | `wow wait` returns on the first chat line. Script protocol traffic drove one agent to about 800 extra requests (+$19). | `api/api.go:161-183`, `script/events.go:174`; Corbin 224 chat wakes. | Coalesce chat-only wakes: after a chat match, keep collecting until 2 s of quiet (8 s at most). Whispers and messages naming the character wake at once. Non-chat wake types are unaffected. Document the wake semantics; don't advise what to subscribe to. |
| H12 | Repeated identical failures go unnoticed (`act.make_room: no_disposable_item` 486 times in a row during a boss pull). | `script/diagnostics.go:121`, `act.lua:122-128`. | In `endDiagnostic`, count consecutive identical top-level op:code failures per task. At 25, and every 250 after, emit `failure_loop {task, op, code, count}`, a default wake type. The act wrapper backs off (`min(5 s, n/25 s)`) from 25 consecutive failures. This reports and rate-limits; it does not stop or change the task. |

### Judge and report

| ID | Defect | Evidence | Required change |
|---|---|---|---|
| J1 | Binary goals (achievement, quest, item, quality) score `(rate + best_window) / 2`, which is 2.1 for every agent here. | `metrics/rates.go:49-56`, `judge/main.go:236,266`, `judge/report.go:666-721`. | Score binary goals by time: score = 3600 / time_to_goal_s, with `time_to_goal_s` on the result line, report card and team summary. Unmet goals score 0, with progress signals reported separately. Document `achievement:` in bench/README.md. |
| J2 | The reliability block covers 28 minutes and shows `dead_ms` 0. The state names don't match. | `diagnostics/reliability.go:34,37` expects `running/started/resumed`; the harness writes `resume`, `playing`, `pause`. | Share one typed set of state names between harness and diagnostics. Test: start, pause, resume, sample. |
| J3 | Failure counts mix combat outcomes and preemption with failures, and count nested calls twice. | `judge/diagnostics.go:110-116`, `diagnostics/report.go:265-280`. 46,453 "failures", about half preemption. | Count top-level calls only (`ParentCall == ""`). Bucket codes into `combat_outcome` (miss, dodge, parry, block, resist, evade, immune), `preempted` (superseded, task_*, session_closed) and `failure`. |
| J4 | Stall events are all `no_xp`, which is normal while resting or in a goal run. | 390 events (`session/events.go:794`, `judge/report.go:527-533`). | Split stalls by reason. Skip `no_xp` while eating, drinking, dead, or when the goal isn't XP-based. |
| J5 | Cost is reproducible only from `~/.codex/sessions`. | `judge/diagnostics.go:146` keeps the final total. | Write `<agent>/codex-usage.jsonl` (timestamp, last_token_usage, rate_limits) at each stop. Same source as H10. |

## Documentation

### Rules for agent docs

Every doc change under this spec follows Principle 1. A reviewer checks each added sentence against these tests:

- **It describes a mechanism, a number, an API contract, or a failure cause.** Server values come from the AzerothCore source or DBC, with the source cited in a comment in the doc source (not shown to agents).
- **It contains no imperative about play** ("pull", "stand", "wait for", "avoid", "always", "never" applied to game actions). Imperatives about the API are fine ("pass a GUID, not an entry").
- **It doesn't rank options or name a best approach.** No examples that are strategies in disguise.
- **Example code shows API use, not a tactic.** An example may cast a spell on a target; it may not show a pull-and-retreat sequence.

Existing docs should be audited against the same rules, and strategy that has crept in removed (for example, any "recommended level" or "group before X" text in game.md).

### Corrections (wrong facts)

| Doc | Wrong | Correct |
|---|---|---|
| game.md:129 | Corpse reclaim from about 30 yards | 39 yards (`CORPSE_RECLAIM_RADIUS`); verified by a44, a45, a46 |
| api-reference.md:188, move.unstuck text | Hearthstone cooldown 1 hour | 30 minutes (spell 8690 category cooldown) |
| api-reference.md:231 | "Chests use Opening" | The Opening spell depends on the lock type (see S2); the SDK picks it |
| api-reference.md:203 | `me.stance()` works | Per S5 |
| api-reference.md:164 | `items.find` matched by name (reads as substring) | Exact name or entry (S17) |
| fighting.md:21-22 | Cooldowns from before login are tracked | True after S8; until then, state the limit |
| api-reference.md:339 | in_sight is "as the server checks spells" | What the check covers and doesn't (static geometry, eye heights; doors and dynamic objects excluded) |
| azworld.md, quest objectives | `dropped_by` lists read as complete | Capped at 5 sources; show `+N more` (a44/NOTES.md:5, a45/NOTES.md:61) |
| azworld spell text from `smart_scripts` comments | Comment names shown as spell names (4979 shown as Frost Nova) | Resolve spell IDs through Spell.dbc / spelltext (a46/NOTES.md:5) |
| azworld vendors and spawns | Event-only spawns shown as present | Flag `game_event` spawns as event-only (a45/NOTES.md:61) |

### Missing system documentation

New material for game.md, fighting.md, players.md and death-and-travel.md (or a new `instances.md`), as mechanics only. Each item needs its server rule and numbers checked in the AzerothCore source before writing.

- **Aggro and assistance:** aggro radius and how it scales with level difference; creature formations and linked groups entering combat together; creatures calling nearby allies for help, and the radius; fleeing at low health and what a fleeing creature does; how evading and leashing reset a creature.
- **Threat:** what generates threat (damage, healing, taunts), how it decides a creature's target, and how threat on a healer arises from healing.
- **Line of sight and range:** the server's line-of-sight check for spells (eye height, what geometry blocks it); which spells need facing, and that friendly spells don't; range rules including melee reach from combat reach and bounding radius.
- **Crowd control:** which auras break on damage, diminishing returns, and that a creature may be healed by an ally while controlled.
- **Death and instances:** reclaim radius (39 yd), the resurrection timer growing with repeated deaths, release choices, that entering an instance portal as a ghost revives you inside (already in death-and-travel.md:25-29 and should reach workspaces through H2), instance persistence (killed bosses stay dead; trash respawn rules), and how the instance resets.
- **Experience:** kill XP split among party members within range (the distance, from `KillRewarder`); tapping and who gets credit; level-difference XP rules. No commentary on whether grouping is worth it.
- **Loot:** loot methods and thresholds, how group rolls resolve, Bind-on-Equip binding on equip.
- **Items:** that equipping binds BoE items; durability loss on death and its effect at zero.
- **The agent loop:** factual description of latency: a tool call's round trip, how often scripts tick, and what happens to scripts between agent turns. A system fact, not advice to script combat.
- **Session restarts:** what survives a session restart and what doesn't (globals and tasks don't; workspace files do), and `autorun.lua` (H4).
- **Troubleshooting:** symptoms with their causes (bag equip timeout until S9 lands, ranged timer until S10, `act.go_to` without `on_attacked` doesn't respond to attacks, a follow-settings change doesn't cancel an in-flight route, `tonumber(nil)` in handlers), stated as cause and effect.

Documentation not to write: a dungeon playbook, role guides, pull or positioning guidance, readiness checklists, boss strategies, group communication conventions, or any advice taken from this run's analysis about what worked. The run report and these findings are for us, not for agents.

## Verification

- **Unit tests** for S1-S4, S6, S8, S11-S14, S17, H3's message builder, H6's crashed status, H11's coalescing, H12's failure_loop, J1-J4, J5's file. Each test is named in the table row above.
- **Live GM tests:**
  - S2 and S3: wardrobe and gunpowder chest open with no spell argument; the cannon fires through `items.use`.
  - S10: Throw cadence matches the server.
  - S15: line of sight at a set of Deadmines points where the SDK and server disagreed (the Sneed corridor bend, the Woodcarver ramp, the foundry engineer spots from a44/RUN_STATE.md). Pass when the SDK agrees with the server at each point, or the diagnostic records the disagreement.
  - H8: a quest-goal scenario keeps the agent alive after `goal_met`, and NOTES.md appears in the run dir.
- **Pathfinding regression:** every coordinate the agents logged as an unreachable route, including seams at y = 1066.667 and y = -2133.333 (multiples of the 533.33-yard tile), the Goldshire inn stairs, Stormwind Mage Tower trainer, Azora tower stairs and the Lakeshire bridge.
- **H7:** a 5-agent load test with TIME_WAIT counts before and after.
- **Docs review:** every doc diff is checked against the rules above by someone other than its author. Strategy found is removed before merge.
- **Re-run:** the same `deadmines` scenario with fresh workspaces, the same roster and model. Compare with this run's time to goal (63,061 s), wipes inside the instance (10), deaths (115), cost ($307) and harness incidents. The re-run measures the harness and SDK fixes; better strategy, if any, must come from the agents.

## Rollout

1. S1, J2, S11, S12, S13: small, isolated, high value.
2. H2, H3, H5, H6, then H1 and H8 together (one graceful-stop routine).
3. J1, J3, J4, J5, H10.
4. S2-S10, S14, S16, S17, E1-E5.
5. P1-P8, H11, H12.
6. H7 after the load test confirms the cause.
7. S15 diagnostics from step 2, then model corrections.
8. Documentation corrections with steps 1-4; new system docs after an audit against the rules.
9. Fresh-workspace re-run.

A paused run stays on its build or is ended before this lands (reliability spec rule).

## Completion criteria

- Every S, H and J item is fixed with its test, or explicitly deferred with a reason in this file.
- Every P item is added with reference docs and an example that shows the API, not a tactic.
- Doc corrections are merged. New system docs pass the rules review.
- Running the scenario with mid-run pauses and a deploy produces no missing tool outputs, refreshed workspace docs, a WHAT CHANGED section, a judge clock that stops at disconnect, and a reliability block that covers the whole run.
- The re-run is complete and compared with this run in a report.

## Status (2026-10-31)

All S, H, J, P and E items are in the stack above this spec, one change each, with unit tests. Two are partial:

- **S11, deferred in part.** Native-call panics are now recovered and name the op, the Lua line and the Go stack in session.log. The nil behind Isolde's healer panics was not reproduced in game. The next panic will identify it.
- **S15, deferred in part.** `sight_refused` diagnostics and the in_sight docs are done. in_sight still ignores closed doors and uses fixed eye heights (1.8 / 1.5) rather than each model's collision height. Both corrections need GameObjectDisplayInfo or CreatureModelData geometry. They will be scheduled when the diagnostics show how often they happen.

Still open:

- **aggro_radius.** The SDK uses a 20 yd base. The server uses `creature_template.detection_range` (18 for most spawns), which the session can't read. The docs state the difference.
- **Run-level criterion.** Not yet exercised end to end. H1 also has not been confirmed against a real Codex rollout: grep the next resume for `output is missing`.
- **P items.** Each has reference entries and call forms in the system docs, but no separate example scripts.
- **Re-run and comparison report.** Not done; they need agent runs.
- **Pathfinding sweep.** 24 of 26 routes from the run pass. The one failure starts in the lake under the broken Lakeshire bridge; the land routes over the same crossing pass.
