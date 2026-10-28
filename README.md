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
(cd ghost && go build -o azghost ./cmd/azghost)      # Go 1.26+

# pathfinding data for the client, copied out of the server's volume (~3GB)
mkdir -p data && docker run --rm -v server_ac-client-data:/src:ro -v $PWD/data:/dst alpine cp -r /src/maps /src/mmaps /src/vmaps /dst/
```

## Run a task

```sh
bun account.ts agent1 agent1                                   # non-GM account per agent
WOW_PORT=8090 ACCOUNT=agent1 ./play.sh Agentone > sessions/agent1.log 2>&1 &
bun judge.ts Agentone level:10 --timeout 4h                    # in parallel; prints JSON + elapsed_s when met
# ...give the agent AGENTS.md + the goal; it drives the session with ./wow
```

Goals: `level:<n>`, `item:<entry>`, `quality:<n>` (3 = rare), `money:<copper>`. Multiple agents = one
account, character and `WOW_PORT` each.

Server config overrides live in `server.override.yml` (Warden off, 15s character saves for the judge).
