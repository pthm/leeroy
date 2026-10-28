# wowbench

AI agents play WoW 3.3.5a on a local AzerothCore server, writing their own Lua scripts, and get scored on what they
achieve.

| Folder | What's in it |
|---|---|
| [`server/`](server) | The AzerothCore server: upstream checkout (`azerothcore/`, gitignored), config overrides (`override.yml`), account creation (`account.ts`), and `recorder/`: the `mod-recorder` module plus the tools that read its log (`replay.ts` run replays, `movecheck.ts` movement audit) |
| [`bench/`](bench/README.md) | The benchmark: launchers (`bench.sh`, `team.sh`), scoring (`judge/`), what agents get (`agent/`: task docs and the `wow`, `worlddb`, `wowdb` tools), the session runner (`client/play.sh`, one `azsdk` per agent), agent `workspaces/` and `runs/` |
| [`azeroth-sdk/`](azeroth-sdk/README.md) | The Go headless client, `azsdk` daemon and TypeScript SDK, plus the Lua API docs, world data API (`worlddata/`: `wowdb`, `dbsetup`) and example scripts (a root `go.work` includes it: `go build ./azeroth-sdk/...`) |
| `data/` | Client data copied out of the server (maps, navmesh, DBC files), shared by all of the above (gitignored) |

All commands below run from the repo root.

## Setup (once)

```sh
# server
git clone --depth 1 https://github.com/azerothcore/azerothcore-wotlk.git server/azerothcore
cp -r server/recorder/mod-recorder server/azerothcore/modules/   # replays (see below)
docker compose -f server/azerothcore/docker-compose.yml -f server/override.yml build   # ~10-20 min
bun run server                                       # first boot downloads client data + imports DBs

# client data, copied out of the server's volume (~3GB)
mkdir -p data/dbc && docker run --rm -v server_ac-client-data:/src:ro -v $PWD/data:/dst alpine sh -c \
  'cp -r /src/maps /src/mmaps /src/vmaps /dst/ && cd /src/dbc && cp Spell.dbc Achievement.dbc Achievement_Criteria.dbc \
   Achievement_Category.dbc AreaTable.dbc WorldMapOverlay.dbc Faction.dbc FactionTemplate.dbc SpellCastTimes.dbc \
   SpellRange.dbc /dst/dbc/'
bun azeroth-sdk/worlddata/dbsetup.ts   # wowbench schema: achievements, areas, spawn zones, faction hostility, spells

# the client (bench/client/play.sh also builds it on first use)
go build -o azeroth-sdk/azsdk ./azeroth-sdk/cmd/azsdk
```

The compose project is pinned to `server` (`server/override.yml`), so the volumes are `server_ac-database` and
`server_ac-client-data`. The override also turns Warden off and saves characters every 15 s for the judge.

## Run a benchmark

```sh
bench/bench.sh claude1 Humanwar 1 1 level:5 60          # agent name, character, race, class, goal, minutes
LEVEL=10 START="-9460 70 56.6 0 12" bench/team.sh score:100000 60 a1:Garrick:1:1 a2:Paloma:1:2   # several agents, one clock
```

Each agent gets a fresh non-GM account and runs as headless Claude Code inside `bench/workspaces/<name>/` (its
persistent script library). It can read and write only there, read the client and server source, and run `wow`, `worlddb`,
`wowdb` and read-only shell commands. Results go to `bench/runs/<id>/`:
- `judge.json`: the score;
- `samples.jsonl`: progress every 15 s;
- `agent.jsonl`: the transcript;
- `session.log`;
- `replay.html`.

Goals: `level:<n>`, `item:<entry>`, `quality:<n>` (3 = rare), `money:<copper>`, `quests:<n>`, `quest:<id>`,
`achpoints:<n>`, and `score:<n>`: a weighted composite of every progress signal (`bun bench/judge/signals.ts <char>`
lists them; weights in `bench/judge/score.json`). For "as much as possible" goals pass a large n and read `value` at timeout.

## Replays

`mod-recorder` logs every player's position (1/s), chat, damage, heals, casts, kills, deaths, levels, quests,
achievements and loot to `server/azerothcore/env/dist/logs/recorder.jsonl`. Runs then get a `replay.html`: a
self-contained page with a terrain map rendered from `data/maps`, every player's route, a seek bar with key events
marked, and chat, combat and event logs synced to the playhead. By hand: `bun server/recorder/replay.ts bench/runs/<id>` (or
`--chars A,B --from <ISO> --to <ISO>`). `bun server/recorder/movecheck.ts` audits the same log for speed, slopes and
swimming.

## Run a task by hand

```sh
bun server/account.ts agent1 agent1                               # non-GM account
WOW_PORT=8090 ACCOUNT=agent1 bench/client/play.sh Agentone > bench/runs/agent1.log 2>&1 &
bun bench/judge/judge.ts Agentone level:10 --timeout 4h           # prints JSON + elapsed_s when met
# ...give the agent the docs (see bench/README.md) + the goal; it drives the session with bench/agent/bin/wow
```
