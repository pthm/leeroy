# bench

The benchmark: headless agents (Claude Code or Codex CLI) each play one character through an `azsdk` session, write
their own Lua scripts, and are scored by a judge that reads the character database. All commands below run from
the repo root; server and client setup is in the [repo README](../README.md).

| Path | |
|---|---|
| `bench.sh` | one agent: account, character, session, judge, agent; results in `runs/<ts>-<agent>/` |
| `matrix.sh` | a grid of solo runs (task × race × class × minutes × reps) from a config in `matrices/`; writes `summary.json` |
| `report.ts` | the report card: how each agent spent a run (duty cycle, idle gaps, progress by what the agent was doing, wakes and what triggered them, restarts, deaths, tokens, cost); `report.json` in the run or team dir, written at the end of every run |
| `team.sh` | several `bench.sh` on one clock; writes `team.json` and one replay to `runs/<ts>-team/` |
| `resume.sh` | resumes a paused team run |
| `restart-agent.sh` | mid-run, brings back one agent whose session or conversation ended |
| `scenarios/` | ready-to-launch team runs ([scenarios/README.md](scenarios/README.md)) |
| `specs.tsv` | class specs: key, class and spec, role, what to optimise, key stats |
| `judge/` | the judge ([judge/README.md](judge/README.md)) |
| `agent/` | what the agent is given: `AGENTS.md` and the tools in `bin/` |
| `client/play.sh` | runs one character in its own `azsdk` daemon |
| `dev/` | a GM test character and API doc generation, for SDK work |
| `workspaces/<agent>/` | each agent's persistent script library (gitignored) |
| `runs/` | results (gitignored) |

## One agent: bench.sh

```sh
bench/bench.sh <agent-name> <Character> <race> <class> <goal> [minutes, default 60]
bench/bench.sh claude1 Humanwar 1 1 level:5 60
LEVEL=10 MONEY=2000 START="-9460 70 56.6 0 12" SPEC=arms bench/bench.sh a1 Garrick 1 1 score:100000 60
```

In order, it:
1. assembles `workspaces/<agent>/AGENTS.md`, copies `example.lua` and creates `NOTES.md` if missing (see
   [The agent](#the-agent));
2. creates a non-GM account named after the agent (alphanumerics, at most 16 chars; password = account name) and the
   read-only `worldreader` MySQL user that `worlddb` uses;
3. with `LEVEL`, `MONEY` or `START`: logs in for 8 s to create the character, waits for it to go offline, then sets
   level (xp 0), money, position and full health in the database;
4. starts the session (`client/play.sh`, up to 3 login attempts), sets the Lua global `LIB` to the workspace and
   joins `CHANNEL`;
5. with `GO`: touches `$GO.<agent>.ready` and waits for `$GO` to exist;
6. starts the judge and writes `prompt.md` (goal, how it's scored, how to work, other players, `NOTICE`, rules);
7. starts the agent in its workspace under `timeout <minutes>m`, and writes `meta.json` once the runtime reports its
   model;
8. waits for the judge, prints `judge.json`, and (outside a team) builds `replay.html`. On exit it kills the agent,
   judge and session and copies `session.log` into the run directory.

The script is wrapped in `{ ... }`, so editing it doesn't affect runs in progress.

| Env var | |
|---|---|
| `RUNTIME` | `claude` (default) or `codex` |
| `MODEL` | Claude model (`--model`); default: Claude Code's |
| `CODEX_MODEL` | Codex model (`-m`); default: Codex's configured model |
| `WOW_PORT` | the session's API port, default 8090 |
| `LEVEL` | starting level |
| `MONEY` | starting money, in copper |
| `START` | starting position `"x y z map zone"`, e.g. Goldshire `"-9460 70 56.6 0 12"` |
| `CHANNEL` | zone channel joined at start, default `General - Elwynn Forest` (not rejoined on zone change) |
| `HINT` | appended to the goal line of the prompt |
| `SPEC` | a key from `specs.tsv`: the prompt states class, spec, role, what to optimise and key stats, and the judge gets the role |
| `ROSTER` | the other players, one per line, listed in the prompt |
| `NOTICE` | a file whose text is added to the prompt (e.g. what changed since the agent's last run) |
| `GO` | a file to wait for before the judge and agent start (set by `team.sh`); also moves the record, board and event stream into its directory |

Races in `<race>`: 1 Human, 2 Orc, 3 Dwarf, 4 Night Elf, 5 Undead, 6 Tauren, 7 Gnome, 8 Troll, 10 Blood Elf,
11 Draenei. Classes are the game's ids (1 Warrior, 2 Paladin, 4 Rogue, 5 Priest, 8 Mage, ...).

Goals (the judge's, see [judge/README.md](judge/README.md)) and what the prompt asks for:

| Goal | Prompt |
|---|---|
| `level:<n>` | reach level n |
| `quests:<n>` | complete n quests |
| `quest:<id>` | complete that quest (title looked up in the world DB) |
| `money:<copper>` | hold n copper |
| `item:<entry>` | own that item |
| `quality:<n>` | own any item of quality n or better (3 = rare) |
| `achpoints:<n>` | earn as many achievement points as possible in the time |
| `score:<n>` | progress as much as possible; scored on XP per hour (the n is a large number nobody reaches) |

For `level`, `quests`, `quest`, `money`, `item` and `quality` the agent is told it's scored on time until the judge
sees the goal met, or its progress if time runs out. `score` and `achpoints` have no finish line.

## Several agents: team.sh

```sh
bench/team.sh <goal> <minutes> <name:Character:race:class[:spec[:runtime]]>...
LEVEL=10 MONEY=2000 START="-9460 70 56.6 0 12" bench/team.sh quest:176 60 hogger1:Garrick:1:1 hogger2:Paloma:1:2
bench/team.sh score:100000 120 a11:Harlan:1:1:arms:claude a16:Brandt:1:1:protwarrior:codex
```

One `bench.sh` per agent, in argument order, on ports 8090, 8091, ..., started 10 s apart (concurrent logins time
out on the auth server). Each gets `SPEC`, `RUNTIME` (default `claude`) and a `ROSTER` of the other players with a
spec (`- Char: Warrior - Arms (DPS)`). Other env vars (`LEVEL`, `MONEY`, `START`, `CHANNEL`, `HINT`, `NOTICE`,
`MODEL`, `CODEX_MODEL`) pass through to every agent.

Once every session is up (or after 5 minutes), it writes the start time to `go` and every judge and agent starts at
that moment. When all have finished it writes `team.json` and builds `replay.html`.

Watch a team run live:

```sh
bin/azview live --run bench/runs/<ts>-team   # http://localhost:8800/
```

## Run directories

`runs/<ts>-<agent>/`, one per agent per run:

| File | |
|---|---|
| `prompt.md` | the task prompt |
| `agent.jsonl` | the transcript: Claude's `stream-json`, or Codex's `--json` events, each stamped with a `timestamp` |
| `samples.jsonl` | the judge's sample every 15 s |
| `judge.json` | the judge's final result |
| `meta.json` | `agent`, `char`, `runtime`, `model`, `spec`, `role` |
| `session.log` | the session log, copied from the workspace when the run ends |
| `record.jsonl`, `board/`, `<Char>.events.jsonl`, `replay.html` | only for a solo `bench.sh` run; in a team they're in the team directory |

`runs/<ts>-team/`:

| File | |
|---|---|
| `go`, `go.<agent>.ready` | the start time (unix seconds) and each session's ready marker |
| `<agent>.out` | that agent's `bench.sh` output; its last JSON line is the judge's result |
| `<agent>.record.jsonl` | client-side event log for replays (positions, combat, chat, API calls) |
| `<Char>.events.jsonl` | the character's event stream; the judge reads role stats from it |
| `board/<Char>.json` | each judge's latest result (with `minutes_left`), read by `standings` and `wow` |
| `alerts.log` | agents that exited with 2+ minutes left, with the runtime's last error (e.g. a Codex usage limit) |
| `team.json` | the results, see below |
| `replay.html` | the replay |
| `AGENTS.md` | written by `resume.sh`: the docs it gave every agent |

`team.json`:

| Field | |
|---|---|
| `agents` | each judge's result, best `score` first |
| `faction_score` | sum of every player's score (everyone is Alliance) |
| `by_runtime` | per runtime: `players`, `score` (sum), `best` |
| `team_s` | time until the last agent met the goal; null unless every agent did |

## Scoring

The judge polls the character database every 15 s (the server saves characters every 15 s, see
`server/override.yml`), so the client can't fake progress. For `score:` goals, progress is XP earned since the start
(`judge/score.json`, version 6), and

```
score = (rate + best_window) / 2      in XP per hour
rate        = XP earned / hours elapsed (the first 10 minutes count as 10)
best_window = the best rate over any 15-minute window
```

Role stats (damage dealt, damage taken, healing done) are tallied from the event stream and shown on the board, but
not scored. The faction score is the sum of all players' scores. Agents see all of this with `standings`. Details:
[judge/README.md](judge/README.md).

## Pause and resume a team run

Stop `team.sh`, then every `bench.sh`. Their exit traps stop the agent, then run `agent/calm.sh` (stop the
character's scripts and wait up to 90 s for it to leave combat: a character dropped mid-fight stays in the world and
can die there), then stop the judge and the session, which logs out. Then:

```sh
bench/resume.sh bench/runs/<ts>-team <total-minutes> <message-file>
```

For each `<agent>.record.jsonl` in the team directory it takes the agent's latest `runs/*-<agent>/`, logs the
character back in on its port from `meta.json` (5 s apart), restarts the judge with `--resume` and its goal from `meta.json` (same
baseline and curve; the pause doesn't count), and resumes the agent's conversation (Claude `--resume <session_id>`,
or `agent/codex-resume.sh` when `meta.json` says codex) with the message under a `timeout` of the minutes left.
`$LEFT` in the message file is replaced with the minutes left. It also refreshes every workspace's `AGENTS.md` and
writes `team.json` at the end.

Runs from before `meta.json` had `port` and `goal` fall back to 8090 + position in that list and `$GOAL`.

## Restart one agent

```sh
bench/restart-agent.sh bench/runs/<ts>-team <agent> <message-file>
```

For one agent whose session died or whose conversation ended while the rest keep playing: runs `agent/calm.sh`, kills its
`azsdk`, waits for the character to go offline, starts a new session on its port, joins `CHANNEL` (default here:
`General - Elwynn Forest`), and resumes its conversation with the message (`$LEFT` = minutes left on its judge's
clock). The judge in the original `bench.sh` keeps running.

## Reset characters for a fresh run

Characters live on after a run. To start over with the same names, soft-delete them (they must be offline), then
restart the worldserver, which caches character names:

```sh
docker exec ac-database mysql -uroot -ppassword acore_characters -e "UPDATE characters SET deleteInfos_Name = name,
  deleteInfos_Account = account, deleteDate = UNIX_TIMESTAMP(), name = '', account = 0 WHERE name IN ('Harlan','Mirabel')"
docker restart ac-worldserver
```

This is the same update the server makes when a player deletes a character. `.character erase` would remove them
for good, but it's a worldserver console command, not available to a client. For fresh agents too, move their
`workspaces/<agent>/` aside: the library and `NOTES.md` carry over otherwise.

## The agent

Each run, `bench.sh` writes the workspace's `AGENTS.md` from `agent/AGENTS.md` (workspace, session, `wow`, tasks)
followed by `../azeroth-sdk/docs/` `playing.md`, `guide.md`, `runtime.md`, `lua-api.md`, `api-reference.md` and
`world-data.md`. It copies `../azeroth-sdk/examples/lua/grind.lua` to `example.lua`, and creates `NOTES.md` only if
missing. The agent's tools come from `agent/bin/` on its `PATH`, outside the workspace, so it can't edit them.

| Tool | |
|---|---|
| `wow` | the session on `$WOW_PORT`: `wow '<lua>'`, `wow < file`, `wow -f file.lua`, `wow status`, `wow tasks`, `wow events [--types a,b] [--last n] [--since seq] [--match s]`, `wow wait [--types ...] [--match s] [seconds]` (max 600), `wow answer <id> <choice>`, `wow -w <s> '<lua>'`. Chat from players, invites, trades and script questions also go to stderr |
| `worlddb` | read-only SQL on `acore_world` as `worldreader`; tab-separated, at most 300 rows |
| `azworld` | structured world-data lookups (`bin/azworld`, from `azeroth-sdk/cmd/azworld`; `azworld help`) |
| `standings` | every player's rank, score, rates, level, role stats, item level and money from `$WOWBENCH_BOARD`, plus a faction total |
| `party [minutes]` | the agent's current party over the last minutes (default 10): per member XP/hour, kills, deaths, time fighting, travelling, idle, dead and with the group. Reads the server's `mod-recorder` log |

`codex-resume.sh <run-dir> <workspace> <minutes> <message>` resumes a Codex thread (from `agent.jsonl`) and appends
to the transcript; `resume.sh` and `restart-agent.sh` use it.

The agent's environment: `PATH` with `agent/bin/` first, `WOW_PORT`, `WOWBENCH_BOARD` (the board directory) and
`WOWBENCH_CHAR` (its character).

### Sandbox

Claude Code runs as `claude -p` in the workspace with:
- `--setting-sources project --strict-mcp-config`: no user settings and no MCP servers;
- `--add-dir` on `azeroth-sdk/client`, `session`, `script` and `server/azerothcore/src/server/game`, `shared`: the
  source it may read;
- `--allowedTools`: `Bash` for `wow`, `worlddb`, `azworld`, `standings`, `party`, `sleep`, `tail`, `cat`, `ls`, `grep`,
  `mkdir`, and `Edit(./**)` (the workspace); `--disallowedTools` `Read` and `Edit` on `~/.claude/**`;
- `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`: auto-memory is keyed by the git root, so agents would otherwise share notes.

Codex runs as `codex exec --json` in the workspace with `-s workspace-write` (writes only in the workspace),
`sandbox_workspace_write.network_access=true` (the session API is on localhost), `approval_policy="never"`,
`shell_environment_policy.inherit=all` (it gets `PATH` and the `WOWBENCH_*` vars) and `--skip-git-repo-check`.

The prompt's rules: legitimate gameplay only, no GM access, don't modify the server, database, judge or harness.
Accounts are non-GM.

## client/play.sh

```sh
bench/client/play.sh <character> [startup.lua] [azsdk flags, e.g. --race 2 --class 1 --duration 8s]
WOW_PORT=8090 ACCOUNT=agent1 bench/client/play.sh Agentone
```

Runs one character in its own `azsdk` daemon until killed (or `--duration`), serving the agent API on
`127.0.0.1:$WOW_PORT`. Rebuilds `azeroth-sdk/azsdk` if any Go file is newer.

| Env var | |
|---|---|
| `WOW_PORT` | API port, default 8090 |
| `ACCOUNT` | account, default `agent1` (password = account name) |
| `WORKSPACE` | the script workspace, default the current directory |
| `RECORD` | append the client-side event log to this file |
| `EVENTS` | directory for `<char>.events.jsonl`, kept across restarts |

## dev/

| Script | |
|---|---|
| `dev/gm.sh [x y z [map]]` | GM test character Devgm (account `devgm`, GM level 3) on port 8112, with `.gm on` and `.cheat god on`, optionally teleported. Uses `azsdk-dev` if built. Log in `/tmp/devgm.log`. Query it with `WOW_PORT=8112 bench/agent/bin/wow '...'`. Never use a GM account in a scored run |
| `dev/apidoc.sh` | regenerates `azeroth-sdk/docs/api-reference.md` from a live session (starts `gm.sh` if 8112 isn't up). Run it after changing the Lua API or package types |
