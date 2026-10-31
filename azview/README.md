# azview

The LEEROY viewer: watch a run live, build a self-contained replay of a finished one, and audit recorded movement.
It reads recordings (below) and, for maps and names, the extracted game data and the world database (via
`azworld`'s package).

| Command | What it does |
|---|---|
| `bin/azview live` | tails recorder logs and serves the viewer page |
| `bin/azview replay` | builds a self-contained `replay.html` for a finished run |
| `bin/azview movecheck` | audits recorded movement for speed, slopes and swimming |

| File | |
|---|---|
| `main.go` | the subcommands |
| `live.go`, `replay.go`, `movecheck.go` | each command |
| `view.go` | shared: recorder logs, run sources, agent transcripts, `meta.json`, chat filtering, the page |
| `replay.html` | the viewer page, shared by both (embedded; data and map are filled in by `live` or `replay`) |

The server-side writer, `mod-recorder`, is an AzerothCore module in `server/recorder/mod-recorder/`. Build
everything with `./build.sh` (binaries in `bin/`); all commands run from the repo root.

## Recording format

A recording is JSON Lines, one event per line, appended as it happens. Two writers produce the same format:

| Writer | Who it sees | Where it writes |
|---|---|---|
| `mod-recorder` (server side, optional) | Every player on the server | `$RECORDER_FILE`, default `server/azerothcore/env/dist/logs/recorder.jsonl` |
| `azsdk -record <file>` (client side, `azeroth-sdk/session/recorder.go`) | The daemon's own characters, plus agent-side events | The given file; `azbench run` uses `<agent>.record.jsonl` in the run dir |

Every line has:

| Field | Meaning |
|---|---|
| `t` | Unix time in ms |
| `ev` | Event kind (below) |
| `p` | Player guid counter |
| `n` | Player name |

Units (attacker, target, killer, victim) are objects: `{"id", "n", "pl": 1}` for a player (`id` = guid counter), or
`{"id", "n", "lvl"}` for a creature (`id` = creature entry). Client-side records may lack a creature's name; the
viewers fill it in from `acore_world.creature_template`.

| `ev` | Fields | Server | Client |
|---|---|---|---|
| `pos` | `m` map, `x` `y` `z` `o`, `hp` `hpmax`, `pt` power type, `pw` `pwmax`, `lvl`, `xp`, `money` (copper), `zone`, `area`, `alive` `ghost` `combat` (0/1), `grp` (group id, 0 = none), `tgt` (current victim, if any) | every 1 s | every 1 s |
| `login` | `race`, `cls`, `lvl` | yes | yes |
| `logout` | | yes | yes |
| `chat` | `type` (chat message type), `msg`, `to` (whisper target, `party`, `guild`, channel) | messages a player sends | messages the character receives from others, with `from` = speaker |
| `dmg` | `src`, `dst`, `amount` | any damage involving a player | melee and spell damage to or from the character, plus `spell` |
| `heal` | `src`, `dst`, `amount` | yes | no |
| `cast` | `spell`, `name`, `target` | non-passive casts | spells the character casts (no `target`) |
| `kill` | `victim` | yes | yes |
| `death` | `killer` | yes | yes (`killer` = last attacker) |
| `pvp` | `victim` | yes | no |
| `release`, `resurrect` | `resurrect`: `pct`, `sickness` | yes | no |
| `level` | `from`, `to` | yes | `to` only |
| `xp` | `amount`, `src`, `victim` | yes | `amount` only |
| `money` | `delta` (copper) | yes | no |
| `units` | `m` map, `u`: the creatures within 60 yd, each `[entry, level, x, y, health %, state]` (state 0 hostile, 1 neutral, 2 friendly, +4 dead) | no | every 2 s |
| `quest` (completed) | `id`, `title` | yes | `id`, `xp`, `money` |
| `achievement` | `id`, `title`, `points` | yes | `id` only |
| `loot` | `item` (entry), `count` | yes | yes |
| `zone` | `zone`, `area` | yes | no |
| `spell` (learned) | `id` | yes | no |
| `group` | `grp`, `leader`, `members` (names); `grp` 0 and no members = left | one per online member on any change | yes |
| `api` | `method`, `params` (raw JSON, cut at 500 chars) | no | each daemon API call (except read-only and waiting ones) |
| `script_error` | the script errors from a Lua eval | no | yes |

Two more kinds come from elsewhere:
- `score`: the judge's `--record <file>` appends each judge sample (`value`, `level`, `rate`,
  `recent_rate`, `best_window`, `pace`, `score`) to the run's record file. The viewer charts them.
- `say`: not in any log. The viewers turn each agent's transcript into `say` events (below).

Client-side differences worth knowing: `pos` has `zone` 0 and no `area` or `tgt`, and `chat` from other players
appears once per listening agent, without a sender guid.

`mod-recorder` reads two environment variables on its first write: `RECORDER_FILE` (the path, inside the container)
and `RECORDER_INTERVAL_MS` (the `pos` interval, default 1000, minimum 100). Neither is set by `override.yml`.

## Installing mod-recorder

```sh
cp -r server/recorder/mod-recorder server/azerothcore/modules/
docker compose -f server/azerothcore/docker-compose.yml -f server/override.yml build
docker compose -f server/azerothcore/docker-compose.yml -f server/override.yml up -d
```

AzerothCore picks up modules in `modules/` at build time, so the worldserver image must be rebuilt after copying (and
after changing `recorder.cpp`: copy again, rebuild). The module is optional: without it `azbench run`
still records every run client-side.

## Viewers

Both viewers serve the same page (`replay.html`) and read area names, labels, item names and creature names from
the database (127.0.0.1:3306 or `$LEEROY_DB_ADDR`, user `worldreader`, the `wowbench` schema from `bin/azworld setup`),
so the server must be up.

### azview live

```sh
bin/azview live [log.jsonl ...] [--run <run or team dir>] [--port 8800] [--chars A,B] [--since 60m]
```

| Flag | Default | Meaning |
|---|---|---|
| `log.jsonl ...` | the server log | Recorder files to tail, any mix of server and client-side |
| `--run <dir>` | | A bench run dir or team dir: its record files plus each agent's transcript |
| `--port` | 8800 | HTTP port |
| `--chars A,B` | everyone | Only these characters |
| `--since` | `60m` | How far back to load, in minutes (`m` suffix optional) |

Without files or `--run` it tails `server/azerothcore/env/dist/logs/recorder.jsonl`. It polls the files every 500 ms
(starting over if one shrinks) and streams new events to the browser as server-sent events (`/stream`). The playhead
follows the newest data until you seek back; the Live button returns to it.

With `--run`, player chat comes once from the server log, with sender names (when it exists), instead of each agent's
received copy; NPC chat from the client records is kept, once per speaker, text and 2 s. Each agent's transcript is
`<run dir>/<agent>/agent.jsonl` (older team runs: a `<time>-<agent>/` dir beside it), and its character name is read
from the `prompt.md` next to it.

The map is served as tiles (`/tile/<map>/<gx>/<gy>.png`, one per 533-yard grid square), rendered on demand and kept
in memory:
- With client data extracted (`data/minimap`, from `go run ./azeroth-sdk/cmd/clientdata ... extract data`): the
  game's own minimap image for the square.
- Otherwise: a shaded relief render of `data/maps` (heights, water, zone borders) with a fixed colour scale so tiles
  match.

The page requests tiles with `?v=<tileVersion>` (2 with client data, 1 without). Tiles are cached for a day, so the
version busts browser caches when the tile source changes.

### azview replay

```sh
bin/azview replay <run or team dir> [--out replay.html] [--log <file>] [--chars A,B] [--from <ms|ISO>] [--to <ms|ISO>]
```

| Flag | Default | Meaning |
|---|---|---|
| `--out` | `<dir>/replay.html` (`./replay.html` without a dir) | Output file |
| `--log` | the run's `record.jsonl` / `*.record.jsonl`, else the server log | One recorder file to read instead |
| `--chars` | from `team.json` or `judge.json` | Characters to include |
| `--from`, `--to` | from the run | Time window, ms or ISO date |

The window defaults to the run's `go` file (team start) or the judge's start time, through the last judge result
plus 5 s. The map is a single image of the box around every route on the most-visited map, embedded as a data URL:
the terrain render, with the game's minimap colours where client data was extracted. `azbench run` builds one at the end
of every run.

### The viewer page

- **Map**: every player's position; click a player (or their chip) to draw their route, the chip also follows
  them. Drag to pan, scroll to zoom. Zone and area labels; dashed lines join each group member to its leader.
- **Seek bar**: key events marked; play/pause, speed (1x to 300x). Keys: Space plays, Left/Right seek 10 s (Shift:
  60 s).
- **Panels**, synced to the playhead:

| Tab | Shows |
|---|---|
| Players | Health and power bars, level, XP, money, zone, state (dead, ghost, in combat), latest score |
| Events | Deaths, kills, PvP, achievements, levels, quests, loot, zones, release/resurrect, login/logout, spells learned, groups |
| Chat | Chat, coloured by type |
| Combat | Damage, heals, casts, kills, deaths, XP |
| Agent | The agent's messages (`say`), API calls (`api`, off by default) and script errors; toggles per kind |
| Scores | Chart and leaderboard of `score`, progress, pace or 10-minute rate, plus the faction total (sum of all players) |
| Groups | Current parties: members, leader, how far apart they are; solo players |

Colours follow WoW: class colours (players of the same class get `#1`, `#2`; `#2` and up draw dashed), health and
power bar colours, chat-type colours, item quality colours.

**Runtime and model badges**: `azbench run` writes `meta.json` in each agent's run dir (`agent`, `char`, `runtime`,
`model`, `spec`, `role`). The viewers read it beside each transcript and show a badge like `claude · opus-5-5` (the
`claude-` prefix dropped) on the player's chip and card; the card shows the spec in place of the class when set.

**Agent transcripts** become `say` events: for Claude Code (`stream-json`), the text blocks of `assistant` messages;
for Codex (`--json`), `item.completed` events with an `agent_message` item. Lines without a `timestamp` are
skipped.

## azview movecheck

```sh
bin/azview movecheck [recorder.jsonl] [--chars A,B] [--from <ms>]
```

Checks `pos` samples against the terrain (default file: the server log) and prints one JSON line per player:

| Field | Meaning |
|---|---|
| `samples`, `moving_s` | Consecutive sample pairs (at most 2 s apart, same map), and those that moved 1 yd or more (teleports over 40 yd/s skipped) |
| `median_speed_alive`, `median_speed_ghost` | Median speed in yd/s |
| `faster_than_run_5s` | Samples whose path over ~5 s beats run speed +10% (7.7 yd/s alive, 11.5 as a ghost) |
| `uphill_steeper_than_50deg`, `steepest_deg`, `examples` | Steps crossing terrain steeper than the navmesh's 50 degree limit, with up to 3 examples |
| `underwater_s`, `underwater_faster_than_swim` | Samples more than 1.5 yd below the water surface (a real client swims at the surface), and those over 5.5 yd/s |

Slopes and water use outdoor terrain only (no buildings), so steps on building floors and city streets can show as
steep climbs; check those against the route.
