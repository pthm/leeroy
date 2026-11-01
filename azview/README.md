# azview

The LEEROY viewer: watch a run, live or finished, and audit recorded movement. It reads recordings (below) and, for
maps and names, the extracted game data and the world database (via `azworld`'s package).

| Command | What it does |
|---|---|
| `bin/azview live` | reads a run's recordings (following them as they grow) and serves the viewer |
| `bin/azview movecheck` | audits recorded movement for speed, slopes and swimming |

| File | |
|---|---|
| `main.go` | the subcommands |
| `live.go` | the server: the page, tiles, samples, routes, the live stream and the panels |
| `store.go` | the run in memory: samples, units and events by player and time, read from the logs as they grow |
| `format.go` | events as lines of HTML (the feed, tooltips, the roster) |
| `roster.go`, `feed.go`, `stats.go`, `panels.go` | the panels, rendered on the server |
| `view.go` | recorder logs, run sources, agent transcripts, `meta.json`, chat filtering |
| `movecheck.go` | the movement audit |
| `templates/` | the page and the panels (`html/template`) |
| `static/` | the client (`viewer.js`: the map, seek bar and playback), styles, and vendored htmx 2.0.11, idiomorph 0.7.4 (htmx's morph swap) and Alpine 3.17.5 |

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
viewer fills it in from `acore_world.creature_template`.

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
- `say`: not in any log. The viewer turns each agent's transcript into `say` events (below).

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

## The viewer

```sh
bin/azview live [log.jsonl ...] [--run <run or team dir>] [--port 8800] [--chars A,B] [--since 60m]
```

| Flag | Default | Meaning |
|---|---|---|
| `log.jsonl ...` | the server log | Recorder files to read, any mix of server and client-side |
| `--run <dir>` | | A bench run dir or team dir, running or finished: its record files plus each agent's transcript, from the run's start |
| `--port` | 8800 | HTTP port |
| `--chars A,B` | everyone | Only these characters |
| `--since` | `60m` | Without `--run`: how far back to read, in minutes (`m` suffix optional) |

It reads area names, labels, item names and creature names from the database (127.0.0.1:3306 or `$LEEROY_DB_ADDR`,
user `worldreader`, the `wowbench` schema from `bin/azworld setup`), so the server must be up.

The run lives on the server (`store.go`): every position sample, "units" sample and event, by player and time. It
reads the files every 250 ms (starting over if one shrinks). The page asks for what it shows at the playhead:

| Endpoint | |
|---|---|
| `/` | the page, with the players, map names, labels, marks and seek bar ticks |
| `/samples?from=&to=` | every player's samples and units samples in a window (unix ms), as the map draws them: position, state, group and leader, and the target with where it is |
| `/route?n=` | a player's whole route, `[t, x, y, map]` per sample |
| `/live` | new data as server-sent events: `s` (a sample), `u` (units), `mark`, `tick`, `data` (anything else) |
| `/panel/roster`, `/panel/pane` | the roster, and the feed or stats, as HTML for a moment and the UI state (`viewerQuery` in `viewer.js`) |
| `/tile/<map>/<gx>/<gy>.png` | map tiles |

The map is a canvas drawn by `viewer.js` every frame from the samples around the playhead (a window from
`/samples`, extended by `/live`). Live, the playhead trails the clock by 2.5 s so players are drawn between two samples
(about 1 s apart) and glide. The panels are htmx fragments, morphed in place (idiomorph), refreshed when the playhead
moves, data arrives or the UI state changes: at most 5 times a second, Stats once. UI state (the selection, tab,
filters, metric, sort) is an Alpine store, kept in `localStorage`. `AZVIEW_DEV=<path to azview/>` serves the templates
and static files from disk on every request instead of the built-in copies: edit, then reload the browser.

With `--run`, and when the server log exists, these come from it for the run's characters (`ServerEvents`): player
chat, once, with sender names, instead of each agent's received copy; damage and heals (in place of the client
records' damage, which has no heals); and each change of target (a `tgt` event: the `pos` samples' `tgt`, or none). NPC
chat from the client records is kept once, with who heard it. Quest titles and achievement names and points, which
client records lack, are filled in from the world database and `data/dbc/Achievement.dbc`. Each agent's transcript is
`<run dir>/<agent>/agent.jsonl` (older team runs: a `<time>-<agent>/` dir beside it), and its character name is read
from the `prompt.md` next to it.

Map tiles (one per 533-yard grid square) are rendered on demand and kept in memory:
- With client data extracted (`data/minimap`, from `go run ./azeroth-sdk/cmd/clientdata ... extract data`): the
  game's own minimap image for the square; for an instance, its floor plan from the navmesh.
- Otherwise: a shaded relief render of `data/maps` (heights, water, zone borders) with a fixed colour scale so tiles
  match.

The page requests tiles with `?v=<tileVersion>` (3 with client data, 1 without). Tiles are cached for a day, so the
version busts browser caches when the tile source changes.

### The viewer page

Three columns, all synced to the playhead: players on the left, the map, and a Feed and Stats panel on the right.
Selecting a player (their card, their dot on the map, a row in Stats) or a group (its header in the roster) applies
everywhere: the map follows it, the feed and the stats narrow to it.

- **Players**: a card per player, grouped by party (leader first, how far apart they are) or flat, sorted by score,
  level, health, name or class. Each card: level, health and power, current target with its health, the agent's latest
  message, money, score and model. The border is the player's state: red in combat, grey dead, dashed blue a ghost;
  the selected one has a gold ring. Clicking a card selects and follows the player; clicking a group header selects
  the group.
- **Map**: every player's position, with a short fading trail when nobody is selected; a selected player's whole
  route (smoothed, fading with age, the part still ahead faint) on a dimmed map. Thin red lines from players to their
  targets, with health bars over the targets; dashed lines join group members to their leader; creatures around the
  players (hostile red, neutral yellow, friendly green, dead grey), named when zoomed in; death, achievement, level
  and quest markers (hover for what happened, click to jump there). The camera follows a selected player; otherwise
  it keeps a selected group, or everyone, centred, and with Auto zoom (Z, on by default) zoomed to fit them. Auto follow (A, on by default) is that camera: off, the map stays where you put it; dragging the map turns it off, and clicking a card or a group header turns it back on; scrolling
  then sets how much closer or further than a snug fit (remembered), instead of zooming freely. Drag to pan (which
  stops the camera following); Fit (F) goes back to framing everyone and resets the auto zoom offset. A picker chooses the map (Auto: the followed player's, else where most players are).
- **Seek bar**: play/pause, speed (1x to 300x), Live (follow the newest data), ticks for deaths, achievements, levels,
  quests and chat; hover for the time and the events there. Keys: Space plays, Left/Right seek 10 s (Shift: 60 s), F fits the map, Z toggles auto zoom.
- **Feed**: one list with filter pills for events (deaths, kills, achievements, levels, quests, loot, zones, groups,
  ...), chat, the agents' messages, combat (damage, heals, casts, XP; off by default), evals (`api`, off by default)
  and script errors. Chat reads as in game: `[Party] [Aldous]: ...`, whispers from the selected player's side
  (`To [Merrin]:`, `[Thessaly] whispers:`). With a player selected, chat is what they saw: their own, whispers to or
  from them, their party's, say and emotes within 25 yd and yells within 300, channels, and NPC speech their client
  recorded. Click a row to seek to it.
- **Stats**: a metric over a time range ending at the playhead (current fight, the last minute to hour, the whole
  run). Metrics: combat (damage and healing done and taken, kills, deaths, casts, time in combat, below 35% health,
  out of mana), score (score, progress, pace, 10-minute rate), progress (XP, quests, levels, level, loot, money),
  activity (distance, time idle, moving, dead, grouped) and agent (messages, evals, script errors, chat sent).
  Party tiles; a ranked list (DPS and HPS per second of each player's own time in combat); a chart, either the
  running total or the amount per period (Auto, 10 s to an hour), with a hover crosshair; an activity strip of
  what each player was doing (combat, moving, idle, ghost, dead) with each one's split; and the selected player's
  breakdown (by target, source, spell, quest, item...). Plots cut time on a fixed grid counted from the run's start,
  so they scroll a cell at a time as the playhead moves.

Colours follow WoW: class colours (players of the same class get `#1`, `#2`; `#2` and up draw dashed), health and
power bar colours, chat-type colours, item quality colours.

**Runtime and model badges**: `azbench run` writes `meta.json` in each agent's run dir (`agent`, `char`, `runtime`,
`model`, `spec`, `role`). The viewer reads it beside each transcript and show the model as a badge coloured by
runtime, like `opus-5-5` (the `claude-` prefix dropped), on the player's card; the card shows the spec in place of
the class when set.

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
