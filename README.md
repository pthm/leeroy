# wowbench

Harness where AI agents write Lua scripts to play WoW 3.3.5a on a local AzerothCore server.
Agent-facing docs: [AGENTS.md](AGENTS.md).

## Setup (once)

```sh
git clone --depth 1 https://github.com/azerothcore/azerothcore-wotlk.git server
docker compose -f server/docker-compose.yml build   # ~10 min
bun run server                                       # first boot downloads client data + imports DBs

git clone https://github.com/azerothcore/AzerothGhost.git ghost && git -C ghost checkout be53c70  # tested commit
git -C ghost apply ../patches/azerothghost-control.patch   # live eval endpoint, chat, raw packets, bot.call
rm -rf ghost/scripts     # bundled Lua AI: agents write their own
# (regen the patch with: cd ghost && git add -N . && git diff -- . ':!scripts' > ../patches/azerothghost-control.patch)
(cd ghost && go build -o azghost ./cmd/azghost)      # Go 1.26+

# pathfinding data + Spell.dbc (cooldowns, spell_info) for the client, copied out of the server's volume (~3GB)
mkdir -p data/dbc && docker run --rm -v server_ac-client-data:/src:ro -v $PWD/data:/dst alpine \
  sh -c 'cp -r /src/maps /src/mmaps /src/vmaps /dst/ && cp /src/dbc/Spell.dbc /dst/dbc/'
```

For achievement goals and `wowdb`, also copy `Achievement.dbc Achievement_Criteria.dbc Achievement_Category.dbc
AreaTable.dbc WorldMapOverlay.dbc Faction.dbc FactionTemplate.dbc SpellCastTimes.dbc SpellRange.dbc` into `data/dbc/` the same way and run `bun dbsetup.ts` (builds the `wowbench`
schema: achievements, areas, spawn zones, faction hostility, spells; read by `worlddb` and `wowdb`).

### Replays (optional)

`modules/mod-recorder` is an AzerothCore module that logs every player's position (1/s), chat, damage, heals,
casts, kills, deaths, levels, quests, achievements and loot to `server/env/dist/logs/recorder.jsonl`. Install it
once and rebuild the worldserver:

```sh
cp -r modules/mod-recorder server/modules/
docker compose -f server/docker-compose.yml -f server.override.yml build ac-worldserver && bun run server
```

`bench.sh`/`team.sh` then write `replay.html` into the run directory: a self-contained page with a terrain map
rendered from `data/maps`, every player's route, a seek bar with deaths/levels/quests/achievements marked, and
chat, combat and event logs synced to the playhead. By hand: `bun replay.ts runs/<id>` (or `--chars A,B --from
<ISO> --to <ISO>`).

## Run a benchmark

```sh
./bench.sh claude1 Humanwar 1 1 level:5 60   # agent name, character, race, class, goal, minutes
```

Creates a fresh non-GM account, starts the session and judge, and runs headless Claude Code against it.
The agent runs inside `agents/<name>/` (its persistent script library): it can only read and write there,
plus read the client/server source, and can only run `wow`,
`worlddb` (from `bin/`) and read-only shell commands. Results go to `runs/<id>/`: `judge.json` (score),
`samples.jsonl` (level/xp/money every 15s), `agent.jsonl` (transcript), `session.log`.

## Run a task by hand

```sh
bun account.ts agent1 agent1                                   # non-GM account per agent
WOW_PORT=8090 ACCOUNT=agent1 ./play.sh Agentone > sessions/agent1.log 2>&1 &
bun judge.ts Agentone level:10 --timeout 4h                    # in parallel; prints JSON + elapsed_s when met
# ...give the agent AGENTS.md + the goal; it drives the session with bin/wow
```

Goals: `level:<n>`, `item:<entry>`, `quality:<n>` (3 = rare), `money:<copper>`, `quests:<n>`, `quest:<id>`,
`achpoints:<n>`, and `score:<n>`: a weighted composite of every progress signal (`bun signals.ts <char>` lists
them; weights in `score.json`). For "as much as possible" goals pass a large n and read `value` at timeout. Multiple agents = one
account, character and `WOW_PORT` each.

Server config overrides live in `server.override.yml` (Warden off, 15s character saves for the judge).
