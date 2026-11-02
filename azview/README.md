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
| `view.go` | run sources, agent transcripts, `meta.json`, chat filtering |
| `movecheck.go` | the movement audit |
| `templates/` | the page and the panels (`html/template`) |
| `static/` | the client (`viewer.js`: the map, seek bar and playback), styles, and vendored htmx 2.0.11, idiomorph 0.7.4 (htmx's morph swap) and Alpine 3.17.5 |

Build everything with `./build.sh` (binaries in `bin/`); all commands run from the repo root.

## Game log

Each character's session writes one game log, JSON Lines, appended as it happens: `azsdk -events <file>`, which
`azbench run` points at `game/<char>/events.jsonl` in the run dir. Every line has `t` (unix ms), `type` and `data`.
Two kinds of line share the file, in one vocabulary (units are always the SDK's `UnitRef`: `guid`, `name`, `entry`
for a creature, `level`, `player`):

- **Events** (with `seq`): what the agent saw, the same stream its `events` API reads: `damage`, `heal`, `kill`,
  `death`, `xp`, `level_up`, `item_received`, `money`, `quest_turned_in`, `achievement`, `cast_done`,
  `area_changed`, `resurrected`, `chat`, `script_error`... (`azeroth-sdk/types/events.go`).
- **Records** (no `seq`; the agent never sees them; `azeroth-sdk/types/gamelog.go`):

| `type` | `data` | When |
|---|---|---|
| `state` | `State`: `map`, `x` `y` `z` `o`, `level`, `xp`, `health` and `power` (`current`, `max`, power `type`), `money`, `group` (the server's group id), `alive`, `ghost`, `in_combat`, `target`, `pet` (a `UnitRef` with `x` `y` `z`, `health`, `alive`) | every 1 s |
| `units` | `Nearby`: `map`, `units`: the creatures within 60 yd, each `[entry, level, x, y, health %, state]` (state 0 hostile, 1 neutral, 2 friendly, +4 dead); pets are in their master's `state`, totems are here | every 2 s |
| `transports` | a list of `TransportState`: each boat and zeppelin on the map, `guid`, `entry`, `name`, `map`, `x` `y` `o` | every 4 s |
| `login` | `Login`: `race`, `class`, `level` | at login |
| `logout` | | at logout |
| `group` | `GroupState`: `group`, `leader`, `members` (names); no `group` = left | on a change |
| `api` | `APICall`: `method`, `params` (raw JSON, cut at 500 chars) | each daemon API call (except read-only and waiting ones) |

`azbench/gamelog` reads a line as it is: its data's fields beside `t`, `type` and `char` (the character); a list
(transports) under `data`. The viewer adds two kinds of its own:
- `score`: the judge's samples (`judge/<char>/samples.jsonl`, each with `t`). The viewer charts them.
- `say`: the viewer turns each agent's transcript into `say` events (below).

A chat message is in the speaker's log (`self`) and in each listener's (`from`: the speaker): the viewer keeps one
event, with `speaker` set and `heard` listing everyone who logged it. The viewer fills in creature names, quest
titles and achievement names from the world database and `data/dbc/Achievement.dbc`.

## The viewer

```sh
bin/azview live [events.jsonl ...] [--run <run or team dir>] [--port 8800] [--chars A,B] [--since 60m]
```

| Flag | Default | Meaning |
|---|---|---|
| `events.jsonl ...` | | Game logs to read |
| `--run <dir>` | | A bench run dir or team dir, running or finished: its game logs, the judge's samples and each agent's transcript, from the run's start |
| `--port` | 8800 | HTTP port |
| `--chars A,B` | everyone | Only these characters |
| `--since` | `60m` | Without `--run`: how far back to read, in minutes (`m` suffix optional) |
| `--data <dir>` | `$LEEROY_DATA`, else the nearest `data/` | The extracted game data (maps, minimap, roads, `client/`) |

It reads area names, labels, item names and creature names from the database (127.0.0.1:3306 or `$LEEROY_DB_ADDR`,
user `worldreader`, the `wowbench` schema from `bin/azworld setup`), so the server must be up.

The run lives on the server (`store.go`): every position sample, "units" sample and event, by player and time. It
reads the files every 50 ms (starting over if one shrinks; an idle check is a dozen file sizes). The page asks for what it shows at the playhead:

| Endpoint | |
|---|---|
| `/` | the page, with the players, map names, labels, marks and seek bar ticks |
| `/samples?from=&to=` | every player's samples and units samples in a window (unix ms), as the map draws them: position, level, state, group and leader, and the target with where it is; each creature with its size and detection range; and the damage and heals involving a player (combat text) |
| `/route?n=` | a player's whole route, `[t, x, y, map]` per sample |
| `/live` | new data as server-sent events: `s` (a sample), `u` (units), `hit` (combat text), `mark`, `tick`, `data` (anything else) |
| `/panel/roster`, `/panel/pane` | the roster, and the feed or stats, as HTML for a moment and the UI state (`viewerQuery` in `viewer.js`) |
| `/tile/<map>/<gx>/<gy>.png` | map tiles |
| `/heat?m=&t=&kind=&sel=&grp=` | the heatmap: where the players (or the selected one or group) spent their time on a map up to t, or where they died, killed, gained XP, took or dealt damage (`kind`: time, deaths, kills, xp, taken, dealt), as one image of the visited area (3 yd a pixel) and where it lies |
| `/nav/<map>/<level>/<gx>/<gy>/<sx>/<sy>.png` | a continent's navmesh (`world.NavTile`): the walkable floors of a square (level 0) or a sub-square, for the navmesh overlay |
| `/roads/<map>/<gx>/<gy>`, `/patrols?m=&gx=&gy=` | the road network the SDK's routes prefer (`azworld roads`, `route`): a square's painted roads (from `data/roads`) as an amber image, and the friendly NPCs' patrol paths crossing it |

The map is a canvas drawn by `viewer.js` every frame from the samples around the playhead (a window from
`/samples`, extended by `/live`). Live, the playhead trails the clock by 2.5 s so players are drawn between two samples
(about 1 s apart) and glide. The panels are htmx fragments, morphed in place (idiomorph), refreshed when the playhead
moves (playing back or seeking), when something new comes into view (watching live: the playhead reaches a sample, which refreshes the roster and Stats, or an event, which refreshes the feed too) or the UI state changes: at most 8 times a second, Stats 4. UI state (the selection, tab,
filters, metric, sort) is an Alpine store, kept in `localStorage`. Updates ease in: bars slide to their new width, card states fade, new feed rows slide in (rows and cards keep stable ids, so a refresh inserts the new ones rather than rewriting the old); off with reduced motion. `AZVIEW_DEV=<path to azview/>` serves the templates
and static files from disk on every request instead of the built-in copies: edit, then reload the browser.

Quest titles and achievement names and points, which game logs lack, are filled in from the world database and
`data/dbc/Achievement.dbc`. With `--run`, the run's players, their game logs (`game/<char>/events.jsonl`), the judge's
samples (`judge/<char>/samples.jsonl`), transcripts (`agents/<agent>/transcript.jsonl`) and the clock's start come
from its `run.json`.

Map tiles (one per 533-yard grid square) are rendered on demand and kept in memory:
- With client data extracted (`data/minimap`, from `go run ./azeroth-sdk/cmd/clientdata ... extract data`): the
  game's own minimap image for the square; for an instance, its floor plan from the navmesh.
- Otherwise: a shaded relief render of `data/maps` (heights, water, zone borders) with a fixed colour scale so tiles
  match.

Zoomed in past the minimap's own resolution (a pixel per ~2 yd), the continents get detail tiles,
`/detail/<map>/<level>/<gx>/<gy>/<sx>/<sy>.jpg`: the square cut into 2^level x 2^level (levels 1-4, down to 0.13 yd a
pixel), each 256 px, rendered on demand from the game files extracted into `data/client` (`azworld/world/detail.go`). A detail tile is the minimap,
upscaled smoothly, plus the fine detail of the ground textures the game paints the terrain with (each chunk's texture
layers and alpha maps from the client's ADT files): the texture render minus itself blurred to the minimap's
resolution. The minimap keeps the colours, shading, buildings and water; the textures add the ground's grain and sharp
road and path edges; where the two disagree a lot (a roof, water) less detail is added. Between two levels the finer
one fades in as you zoom, over the coarser (by how far between them the zoom is), a tile that arrives fades in over a
quarter second, and a sub-square whose tile hasn't come yet shows its part of a coarser one already here. Over that, the
client's own models are drawn, textured, seen from above: every building, wall and bridge (WMO) and tree, fence, lamp
and crate (M2) the ADTs place, and the doodads each building places (a city's trees and statues, an inn's lanterns),
read from the extracted game files (`azeroth-sdk/clientdata`: `ParseADT`'s placements, `LoadWMO`, `LoadM2` over a `Dir`) and placed with the
server's own transform (`pathfinding.PlacementWorld`; checked against the vmaps in `azworld/world/placement_test.go`).
Each pixel is the highest surface's texture, lit by its face's slope from the north-west, outlined where it drops to
a wall, casting a short shadow (the sun 45 degrees up, at most 10 yd). Foliage (alpha-tested textures) is a layer of
its own, at most 60% opaque (`leafOpacity`), so the roads under Elwynn's canopy still show. A model is loaded the first
time a tile needs it (Stormwind's city: ~1 s), then kept. Water is a layer between: the terrain's (lakes, rivers, the
sea: its level from the server's map files) and the WMOs' (Stormwind's canals: each building group's liquid tiles),
laid over the ground and whatever models are under its surface, more opaque the deeper it is, pale where it laps the
shore; lava and slime in their own colours. Models above it (bridges, boats, the trees on the bank) are drawn over it. Detail tiles need the
game files extracted (`clientdata -client <game dir> extract data` writes `data/client`); without them the minimap is
upscaled as before.

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
- **Map**: every player's position (a teleport, faster than 40 yd/s between two samples: the hearthstone, a release to
  the graveyard, isn't slid across: the dot jumps), with a short fading trail when nobody is selected; a selected player's whole
  route (smoothed, fading with age, the part still ahead faint, broken at teleports rather than drawn across the map)
  on a dimmed map. Thin red lines from players to their
  targets, with health bars over the targets; dashed lines join group members to their leader; creatures around the
  players (hostile red, neutral yellow, friendly green, dead grey), named when zoomed in; players and creatures are
  drawn at their size in the game once zoomed in far enough (a creature's bounding radius, from its model; a player's
  0.306 yd), never smaller than a few pixels; death, achievement, level
  and quest markers (hover for what happened, click to jump there). The camera (Auto follow, A, on by default)
  follows the selected player, however they were selected (their card, their dot, a stats row); otherwise it keeps a
  selected group, or everyone, centred, and with Auto zoom (Z, on by default) zoomed to fit them: scrolling then sets
  how much closer or further than a snug fit (remembered), instead of zooming freely. Off, the map stays where you put
  it. Dragging the map turns it off; clicking a card or a group header turns it back on, and so does Fit (F), which also
  resets the zoom to fit what it follows. A picker chooses the map (Auto: the followed player's, else the one shown while anyone is on it, else where most
  players are).
- **The address** keeps the view: `#t=<seconds from the start, or live>&m=<map>&x=&y=<the centre>&z=<zoom>&f=<auto follow>`,
  `&hold=1` when a map is picked and `&sel=<player>`, rewritten as they change, so a refresh or a link opens the same
  moment and place.
- **Layers** (the map bar's Layers menu, L; remembered). Overlays: **Heatmap** (H) of where the players (or the
  selected player or group) spent their time up to the playhead, or where they died, killed, gained XP, or took or
  dealt damage (each step between position samples adds its seconds along its line; an event adds its count or amount
  at the player's position then; smoothed, in the inferno colours over a slightly dimmed map, time on a log scale and
  events on a square root); **Roads** (R): the painted roads (amber: the road textures, the ones some zones paint roads with under other names, and bridges) and the guards' and travellers' patrol paths
  (dashed); **Navmesh** (N): where players can walk on a continent (the SDK's 50° navmesh), in the instances' floor-plan style
  (each pixel the lowest walkable floor, a building's ground floor and not its roof, shaded by its height over the
  terrain: raised floors warm up to 16 yd, the open ground faint grey, and under it slate blue, indigo, violet and purple
  down to 50 yd, so a mine's levels tell apart; floors with another storey over
  them opaque, so a building's rooms show; walls and drops darkened on their upper side (so a narrow tunnel keeps its colour); the storeys raised over the
  terrain outlined). On the map, each
  toggleable: trails, **foliage** (a slider: how much the trees' leaves, and fences' and banners' see-through parts,
  cover the zoomed-in map, from as drawn down to none: the tiles with and without foliage, mixed), **transports** (every boat and zeppelin where the server has it: the server log's transport
  samples, one kept every 4 s, drawn as the outline of its collision model, rotated to its heading), group lines, target lines, event markers. Always drawn: each player's pet (a hunter's beast, a warlock's demon, a water elemental) as a smaller hollow dot in its master's colour, joined to the master by a line and named beyond it, and on the master's roster card a row with its name, level and health. Then creatures (named by choice: the players' targets, the
  default; hostile ones; everyone; or none; names skipped where they'd overlap one drawn, players' first; hover a
  creature for its name, level and health), **aggro radii** (off by default: around
  each hostile creature, how close a player can come before it attacks, as the server works it out in
  `Creature::GetAggroRange`: its `detection_range`, a yard less for each level the nearest player is above it, 5 to
  45 yd; brighter when a player is inside), and **combat** (a pulsing red ring on players in combat, and combat text:
  damage dealt in yellow over the target, taken in red, healing in green, rising and fading over 1.5 s).
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
bin/azview movecheck <events.jsonl ...> [--chars A,B] [--from <ms>]
```

Checks `state` samples against the terrain and prints one JSON line per player:

| Field | Meaning |
|---|---|
| `samples`, `moving_s` | Consecutive sample pairs (at most 2 s apart, same map), and those that moved 1 yd or more (teleports over 40 yd/s skipped) |
| `median_speed_alive`, `median_speed_ghost` | Median speed in yd/s |
| `faster_than_run_5s` | Samples whose path over ~5 s beats run speed +10% (7.7 yd/s alive, 11.5 as a ghost) |
| `uphill_steeper_than_50deg`, `steepest_deg`, `examples` | Steps crossing terrain steeper than the navmesh's 50 degree limit, with up to 3 examples |
| `underwater_s`, `underwater_faster_than_swim` | Samples more than 1.5 yd below the water surface (a real client swims at the surface), and those over 5.5 yd/s |

Slopes and water use outdoor terrain only (no buildings), so steps on building floors and city streets can show as
steep climbs; check those against the route.
