# wowbench

AI agents play WoW 3.3.5a on a local AzerothCore server, writing their own Lua scripts, and get scored on what they
achieve. Agent-facing docs: [bench/AGENTS.md](bench/AGENTS.md).

| Folder | What's in it |
|---|---|
| [`server/`](server) | The AzerothCore server: upstream checkout (`azerothcore/`, gitignored), our config overrides (`override.yml`), the `mod-recorder` module, account creation (`account.ts`) and the `wowbench` data schema (`dbsetup.ts`) |
| [`bench/`](bench) | The benchmark: launchers (`bench.sh`, `team.sh`, `play.sh`), judge and scoring (`judge.ts`, `signals.ts`, `score.json`), agent tools (`bin/`: `wow`, `worlddb`, `wowdb`), replays (`replay.ts`), movement audit (`movecheck.ts`), agent workspaces (`agents/`) and results (`runs/`) |
| [`azeroth-sdk/`](azeroth-sdk/README.md) | The Go headless client, `azsdk` daemon and TypeScript SDK the bench is moving onto (a root `go.work` includes it: `go build ./azeroth-sdk/...`) |
| `data/` | Client data copied out of the server (maps, navmesh, DBC files), shared by all of the above (gitignored) |

Until the bench switches to `azsdk`, sessions run on a patched AzerothGhost checkout in `bench/ghost/`.
All commands below run from the repo root.

## Setup (once)

```sh
# server
git clone --depth 1 https://github.com/azerothcore/azerothcore-wotlk.git server/azerothcore
cp -r server/modules/mod-recorder server/azerothcore/modules/   # replays (see below)
docker compose -f server/azerothcore/docker-compose.yml -f server/override.yml build   # ~10-20 min
bun run server                                       # first boot downloads client data + imports DBs

# client data, copied out of the server's volume (~3GB)
mkdir -p data/dbc && docker run --rm -v server_ac-client-data:/src:ro -v $PWD/data:/dst alpine sh -c \
  'cp -r /src/maps /src/mmaps /src/vmaps /dst/ && cd /src/dbc && cp Spell.dbc Achievement.dbc Achievement_Criteria.dbc \
   Achievement_Category.dbc AreaTable.dbc WorldMapOverlay.dbc Faction.dbc FactionTemplate.dbc SpellCastTimes.dbc \
   SpellRange.dbc /dst/dbc/'
bun server/dbsetup.ts      # wowbench schema: achievements, areas, spawn zones, faction hostility, spells

# bench client (until the switch to azeroth-sdk)
git clone https://github.com/azerothcore/AzerothGhost.git bench/ghost && git -C bench/ghost checkout be53c70
git -C bench/ghost apply ../patches/azerothghost-control.patch && rm -rf bench/ghost/scripts
(cd bench/ghost && go build -o azghost ./cmd/azghost)
# regen the patch: cd bench/ghost && git add -N . && git diff -- . ':!scripts' > ../patches/azerothghost-control.patch
```

The compose project is pinned to `server` (`server/override.yml`), so the volumes are `server_ac-database` and
`server_ac-client-data`. The override also turns Warden off and saves characters every 15 s for the judge.

## Run a benchmark

```sh
bench/bench.sh claude1 Humanwar 1 1 level:5 60          # agent name, character, race, class, goal, minutes
LEVEL=10 START="-9460 70 56.6 0 12" bench/team.sh score:100000 60 a1:Garrick:1:1 a2:Paloma:1:2   # several agents, one clock
```

Each agent gets a fresh non-GM account and runs as headless Claude Code inside `bench/agents/<name>/` (its persistent
script library). It can read and write only there, read the client and server source, and run `wow`, `worlddb`,
`wowdb` and read-only shell commands. Results go to `bench/runs/<id>/`:
- `judge.json`: the score;
- `samples.jsonl`: progress every 15 s;
- `agent.jsonl`: the transcript;
- `session.log`;
- `replay.html`.

Goals: `level:<n>`, `item:<entry>`, `quality:<n>` (3 = rare), `money:<copper>`, `quests:<n>`, `quest:<id>`,
`achpoints:<n>`, and `score:<n>`: a weighted composite of every progress signal (`bun bench/signals.ts <char>` lists
them; weights in `bench/score.json`). For "as much as possible" goals pass a large n and read `value` at timeout.

## Replays

`mod-recorder` logs every player's position (1/s), chat, damage, heals, casts, kills, deaths, levels, quests,
achievements and loot to `server/azerothcore/env/dist/logs/recorder.jsonl`. Runs then get a `replay.html`: a
self-contained page with a terrain map rendered from `data/maps`, every player's route, a seek bar with key events
marked, and chat, combat and event logs synced to the playhead. By hand: `bun bench/replay.ts bench/runs/<id>` (or
`--chars A,B --from <ISO> --to <ISO>`). `bun bench/movecheck.ts` audits the same log for speed, slopes and swimming.

## Run a task by hand

```sh
bun server/account.ts agent1 agent1                               # non-GM account
WOW_PORT=8090 ACCOUNT=agent1 bench/play.sh Agentone > bench/sessions/agent1.log 2>&1 &
bun bench/judge.ts Agentone level:10 --timeout 4h                 # prints JSON + elapsed_s when met
# ...give the agent bench/AGENTS.md + the goal; it drives the session with bench/bin/wow
```
