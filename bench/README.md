# bench

The benchmark: headless agents (Claude Code or Codex CLI) each play one character through an `azsdk` session, write
their own Lua scripts, and are scored by a judge that reads the character database. Everything runs through one
binary, [`azbench`](../azbench) (built into `bin/` by `./build.sh`), from YAML configs in this directory. All
commands below run from the repo root; server and client setup is in the [repo README](../README.md).

| Path | |
|---|---|
| `scenarios/*.yaml` | runs: agents, characters, goal, clock, starting conditions ([scenarios/README.md](scenarios/README.md)) |
| `matrices/*.yaml` | grids of solo runs: task × race × class × minutes × reps ([matrices/README.md](matrices/README.md)) |
| `specs.yaml` | class specs a scenario's agents can take: class and spec, role, what to optimise, key stats |
| `places.yaml` | named starting positions (`start: goldshire`) |
| `agent/` | what the agent is given: `AGENTS.md` and its three tools in `bin/` (links to `bin/wow`, `bin/azworld`, `bin/azbench`) |
| `workspaces/<agent>/` | each agent's persistent script library and notes (gitignored) |
| `runs/` | results (gitignored) |

## Commands

| Command | |
|---|---|
| `azbench run <scenario.yaml> [--minutes n] [--dry-run]` | a run: every session, judge and agent, on one clock |
| `azbench matrix <matrix.yaml> [--dry-run]` | a grid of solo runs, `parallel` at a time; `summary.json` |
| `azbench status <run dir>` | a running run: each player's state, score and level, and the minutes left |
| `azbench pause <run dir>` | stop a running run cleanly so it can be resumed (Ctrl-C on `azbench run` does the same) |
| `azbench resume <run dir> --message <file>` | continue a paused run |
| `azbench restart <run dir> <agent> --message <file>` | mid-run, bring back one agent whose session or conversation ended |
| `azbench report <run dir>` | the report card (`report.json`) and `team.json`; written at the end of every run |
| `azbench compare <run dir>...` | the players of several runs pooled by runtime and model, as `by_model`: a table, then JSON |
| `azbench prices` | refresh the price table for `cost_usd` from [models.dev](https://models.dev) |
| `azbench judge <Char> <goal> [flags]` | score one character by hand ([judge](../azbench/judge/README.md)) |
| `azbench signals <Char>` | a character's progress signals |
| `azbench reset <Char>...` | delete characters so a fresh run can reuse their names |
| `azbench gm [x y z [map]] [--class mage]` | a GM dev character, for SDK work |
| `azbench apidoc` | regenerate the API reference from a live session |

`run`, `matrix` and `resume` rebuild `bin/` first (`--no-build` skips it).

## A run

```sh
bin/azbench run bench/scenarios/fresh-split.yaml
bin/azview live --run bench/runs/<time>-fresh-split    # watch it: http://localhost:8800/
bin/azbench status bench/runs/<time>-fresh-split
```

One supervisor process owns the run. For each agent, logins staggered 10 s apart (concurrent logins time out on
the auth server), on ports 8090, 8091, ...:
1. refreshes `workspaces/<agent>/AGENTS.md`, copies `example.lua` and creates `NOTES.md` if missing (see
   [The agent](#the-agent)), and creates a non-GM account named after the agent (password = account name);
2. with starting conditions (`start`, `level`, `money`, `setup`): creates the character with a short login if it's
   new, waits for it to go offline, and applies them in the database;
3. starts the session (`bin/azsdk`, up to 3 login attempts), sets the Lua global `LIB` to the workspace and joins the
   scenario's `channel`.

Once every session is up it writes the start time to `go`, and every judge and agent starts at that moment: the
judge (in the supervisor), the prompt (`prompt.md`: who it plays, the goal, how it's scored, the run's length, the other
players, the notice, the rules) and the agent in its workspace until the clock runs out. `meta.json` is written once the runtime
reports its model.

When a judge is done the supervisor stops that agent (its whole process group), calms the character (stops its
scripts and waits up to 90 s for it to leave combat: a character dropped mid-fight stays in the world and can die
there) and logs it out. At the end: `team.json`, `report.json` and `replay.html`.

An agent that exits with 2 or more minutes left (a Codex usage limit, a conversation that ended) is reported on
stderr and in `alerts.log`, with the runtime's last error; bring it back with `azbench restart`.

### Pause, resume and restart

`azbench pause <run dir>` (or Ctrl-C on the supervisor) stops the judges where they are, then stops each agent,
calms its character and logs it out, and marks the run `paused`. Then:

```sh
bin/azbench resume bench/runs/<time>-fresh-split --message resume.md
```

Each character logs back in, its judge continues from its samples (same baseline and curve; the pause doesn't
count) and its agent's conversation is resumed (Claude `--resume <session id>`, Codex `exec resume <thread>`) with
the message, where `$LEFT` is replaced with the minutes left. The workspaces' `AGENTS.md` are refreshed, so SDK and
doc changes reach the agents.

`azbench restart <run dir> <agent> --message <file>` does the same for one agent while the others play: its session
restarts on the current build and its conversation resumes. Its judge keeps running.

## Run directories

`runs/<time>-<scenario>/`:

| File | |
|---|---|
| `scenario.yaml` | the scenario as run (the notice inlined); `resume` reads it |
| `go`, `go.<agent>.ready` | the start time (unix seconds) and each agent's files |
| `<agent>/prompt.md` | the task prompt |
| `<agent>/agent.jsonl` | the transcript: Claude's `stream-json`, or Codex's `--json` events, each stamped with a `timestamp` |
| `<agent>/samples.jsonl` | the judge's sample every 15 s |
| `<agent>/judge.json`, `<agent>.out` | the judge's final result |
| `<agent>/meta.json` | `agent`, `char`, `runtime`, `model`, `spec`, `role`, `goal`, `port` |
| `<agent>/session.log` | the session log, copied from the workspace when the session stops |
| `<agent>.record.jsonl` | the client-side event log for the viewer (positions, combat, chat, API calls) |
| `<Char>.events.jsonl` | the character's event stream; the judge reads combat and cooperation from it |
| `board/<Char>.json` | each judge's latest result (with `minutes_left`), read by `standings` |
| `alerts.log` | agents that stopped early |
| `control.json` | while the supervisor runs: its control API |
| `paused` | the run was paused |
| `team.json`, `report.json` | the results and the report card ([judge](../azbench/judge/README.md#azbench-report)) |
| `replay.html` | the replay |

`team.json`:

| Field | |
|---|---|
| `agents` | each judge's result, best `score` first |
| `faction_score` | sum of every player's score (everyone is Alliance) |
| `by_model` | per runtime and model (the same model on two harnesses is two rows): `players`, `score` (mean ± standard error), `best`, `total`, `cost_usd`, `cost_per_agent_hour` |
| `team_s` | time until the last agent met the goal; null unless every agent did |

## Goals and scoring

Goals are the judge's (see [judge](../azbench/judge/README.md)), and the prompt asks for them:

| Goal | Prompt |
|---|---|
| `level:<n>` | reach level n |
| `quests:<n>` | complete n quests |
| `quest:<id>` | complete that quest (title looked up in the world DB) |
| `skill:<id>:<n>` | raise that skill to n |
| `money:<copper>` | hold n copper |
| `item:<entry>` | own that item |
| `quality:<n>` | own any item of quality n or better (3 = rare) |
| `achpoints:<n>` | earn as many achievement points as possible in the time |
| `score:<n>` | progress as much as possible; scored on XP per hour (the n is a large number nobody reaches) |

For `level`, `quests`, `quest`, `skill`, `money`, `item` and `quality` the agent is told it's scored on time until
the judge sees the goal met, or its progress if time runs out. `score` and `achpoints` have no finish line.

The judge polls the character database every 15 s (the server saves characters every 15 s, see
`server/override.yml`), so the client can't fake progress. For `score:` goals, progress is XP earned since the start
(`azbench/judge/score.json`, version 6), and

```
score = (rate + best_window) / 2      in XP per hour
rate        = XP earned / hours elapsed (the first 10 minutes count as 10)
best_window = the best rate over any 15-minute window
```

Combat totals (damage dealt and taken, healing done) and cooperation (`assist_share`, `contested_hits`) are tallied
from the event streams and shown on the board, but not scored. The faction score is the sum of all players' scores.
Agents see all of this with `standings`.

## Reset characters for a fresh run

Characters live on after a run. To start over with the same names:

```sh
bin/azbench reset Harlan Mirabel
```

It soft-deletes them (the update the server makes when a player deletes a character: the name and account are
freed, the row kept; they must be offline) and restarts the worldserver, which caches names. For fresh agents too,
move their `workspaces/<agent>/` aside: the library and `NOTES.md` carry over otherwise.

## The agent

The agent's context comes in layers, each its own file:

| | File | |
|---|---|---|
| task | `prompt.md` (`azbench/run/prompt.tmpl`) | who it plays, the goal, how it's scored, the run's length, the other players, the rules |
| how it works | `AGENTS.md` (`agent/AGENTS.md`) | the workspace, the three tools, how scripts, events and subscriptions fit together; points to the rest |
| the game | `game.md` (`../azeroth-sdk/docs/`) | the rules of the game, described, not coached |
| reference | `sdk.md`, `api-reference.md` (`../azeroth-sdk/docs/`) | the runtime, the Lua API and how it behaves, `azworld`; every function, type and event |

Each run refreshes those files in the workspace, copies `../azeroth-sdk/examples/lua/skeleton.lua` (the shape of a
script, no play in it) to `example.lua`, writes `CLAUDE.md` as `@AGENTS.md` (Codex loads `AGENTS.md` by itself, so
both runtimes start from the same text), and creates `NOTES.md` only if missing. `NOTES.md` is the agent's
notebook: kept with the workspace, it's where it writes what it learned and what got in its way, for review. The
agent's tools come from `agent/bin/` on its `PATH`, outside the workspace, so it can't edit them. There are three,
one per thing the agent deals with: the game, the world's data, and its score.

| Tool | Is | |
|---|---|---|
| `wow` | the game (`azeroth-sdk/cmd/wow`) | the session on `$WOW_PORT`: `wow '<lua>'`, `wow < file`, `wow -f file.lua`, `wow status`, `wow tasks`, `wow events [--types a,b] [--last n] [--since seq] [--match s]`, `wow wait [--types ...] [--match s] [seconds]` (max 600), `wow subscribe [types]`, `wow answer <id> <choice>`, `wow -w <s> '<lua>'`. Subscribed chat, invites, trades and script questions also go to stderr. Everything in the game, including how the party spends its time (`group.report()`) |
| `azworld` | the world's data (`azworld/`) | structured lookups (`azworld help`) and raw read-only SQL (`azworld sql`, tab-separated, at most 300 rows) over the world database |
| `standings` | the score (`azbench/tools`) | every player's rank, score, rates, level, role stats, cooperation, item level and money from `$WOWBENCH_BOARD`, plus a faction total and the minutes left in the run |

The agent's environment: `PATH` with `agent/bin/` first, `WOW_PORT`, `WOWBENCH_BOARD` (the board directory) and
`WOWBENCH_CHAR` (its character).

### Sandbox

Claude Code runs as `claude -p` in the workspace with:
- `--setting-sources project --strict-mcp-config`: no user settings and no MCP servers;
- `--add-dir` on `azeroth-sdk/client`, `session`, `script` and `server/azerothcore/src/server/game`, `shared`: the
  source it may read;
- `--allowedTools`: `Bash` for `wow`, `azworld`, `standings`, `sleep`, `tail`, `cat`, `ls`, `grep`,
  `mkdir`, and `Edit(./**)` (the workspace); `--disallowedTools` `Read` and `Edit` on `~/.claude/**`;
- `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`: auto-memory is keyed by the git root, so agents would otherwise share notes.

Codex runs as `codex exec --json` in the workspace with `-s workspace-write` (writes only in the workspace),
`sandbox_workspace_write.network_access=true` (the session API is on localhost), `approval_policy="never"`,
`shell_environment_policy.inherit=all` (it gets `PATH` and the `WOWBENCH_*` vars) and `--skip-git-repo-check`.

`runtime: none` starts no agent: the session and judge only, for testing the harness (`scenarios/smoke.yaml`).

The prompt's rules: legitimate gameplay only, no GM access, don't modify the server, database, judge or harness.
Accounts are non-GM.

## SDK work

`azbench gm [x y z [map]]` starts the GM test character Devgm (account `devgm`, GM level 3) on port 8112, with
`.gm on` and `.cheat god on`, optionally teleported; its log is `/tmp/devgm.log`. Query it with
`WOW_PORT=8112 bench/agent/bin/wow '...'`. Never use a GM account in a scored run. `azbench apidoc` rebuilds
everything, restarts Devgm on the new build and regenerates `azeroth-sdk/docs/api-reference.md` from its function
registry: run it after changing the Lua API or package types.

`--class mage` (any class but warrior) starts a second GM character, Dev<class> (Devmage), on port 8113, for what a
warrior can't test: spells, mana, a caster's spellbook. `bench/e2e/sdk-fixes.md` is an end-to-end checklist for the
SDK, written for an agent to run against it:

```sh
bin/azbench gm --class mage
cd bench/e2e && WOW_PORT=8113 PATH=$PWD/../agent/bin:$PATH claude -p "$(cat sdk-fixes.md)" \
  --allowedTools "Bash(wow:*)" "Bash(azworld:*)" "Bash(sleep:*)" "Bash(time:*)" "Bash(cat:*)" "Bash(grep:*)" "Read"
```
