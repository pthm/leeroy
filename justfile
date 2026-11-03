# LEEROY's tasks. `just` lists them; tools come from mise.toml (`mise install`). Recipes run from the repo root.

set shell := ["bash", "-euo", "pipefail", "-c"]

compose := "docker compose -f server/azerothcore/docker-compose.yml -f server/override.yml"

# the newest run directory, for the recipes that take one
latest := `ls -d var/runs/*/ 2>/dev/null | tail -1 | sed 's|/$||' || true`

[doc("List the recipes")]
default:
    @just --list --unsorted

# ---------- build ----------

[doc("Build every tool into bin/ (azsdk, wow, clientdata, azbench, azview, azworld, ...)")]
[group('build')]
build:
    go build -o bin/ ./azsdk/cmd/... ./cmd/...

[doc("Install every tool into $GOBIN (or ~/go/bin)")]
[group('build')]
install:
    go install ./azsdk/cmd/... ./cmd/...

[doc("Remove built binaries and the built site")]
[group('build')]
clean:
    rm -rf bin site/dist

# ---------- test ----------

[doc("Run both modules' tests (some need the game data in data/ and the server up)")]
[group('test')]
test:
    go test ./...
    cd azsdk && go test ./...

[doc("go vet both modules")]
[group('test')]
vet:
    go vet ./...
    cd azsdk && go vet ./...

[doc("What CI checks: both modules build as outside users see them (no go.work), and vet clean")]
[group('test')]
ci:
    GOWORK=off go build ./...
    cd azsdk && GOWORK=off go build ./...
    just vet

[doc("Everything before a push: ci, the tests, the site with its links")]
[group('test')]
check: ci test site-check

# ---------- server ----------

[doc("Build the server images (~10-20 min; again after changing the server)")]
[group('server')]
server-build:
    {{ compose }} build

[doc("Start the server (the first boot downloads client data and imports the databases)")]
[group('server')]
up:
    {{ compose }} up -d

[doc("Stop the server")]
[group('server')]
down:
    {{ compose }} down

[doc("Restart the worldserver (after a rebuild, or to clear its caches)")]
[group('server')]
restart:
    {{ compose }} up -d --force-recreate ac-worldserver

[doc("Follow the worldserver's console")]
[group('server')]
logs:
    docker logs -f --tail 100 ac-worldserver

# ---------- data (once) ----------

[doc("Set up data/ after the server's first boot: its maps and DBCs, the wowbench schema, the SDK's navmesh")]
[group('data')]
setup: data-copy data-schema data-navmesh

[doc("Copy the maps, vmaps, mmaps and the DBCs the tools read out of the server's volume (~3 GB)")]
[group('data')]
data-copy:
    mkdir -p data/dbc
    docker run --rm -v server_ac-client-data:/src:ro -v "$PWD/data:/dst" alpine sh -c \
      'cp -r /src/maps /src/mmaps /src/vmaps /dst/ && cd /src/dbc && cp Spell.dbc Achievement.dbc Achievement_Criteria.dbc \
       Achievement_Category.dbc AreaTable.dbc WorldMapOverlay.dbc Faction.dbc FactionTemplate.dbc SpellCastTimes.dbc \
       SpellRange.dbc SpellDuration.dbc SpellRadius.dbc SpellDescriptionVariables.dbc AreaTrigger.dbc Talent.dbc TalentTab.dbc \
       TaxiNodes.dbc TaxiPath.dbc TaxiPathNode.dbc SkillLine.dbc SkillLineAbility.dbc Lock.dbc /dst/dbc/'

[doc("(Re)build the wowbench schema: achievements, areas, spawn zones, faction hostility, spells")]
[group('data')]
data-schema: build
    bin/azworld setup

[doc("Build the SDK's navmesh: 50° slopes, as players walk (~15 min on 12 cores; never under a running session)")]
[group('data')]
data-navmesh:
    docker compose -f server/azerothcore/docker-compose.yml --profile tools build ac-tools
    mkdir -p data/mmaps50
    sed 's/^    walkableSlopeAngle: 60$/    walkableSlopeAngle: 50/' \
      server/azerothcore/src/tools/mmaps_generator/mmaps-config.yaml > data/mmaps50/mmaps-config.yaml
    docker run --rm -v "$PWD/data/maps:/work/maps:ro" -v "$PWD/data/vmaps:/work/vmaps:ro" -v "$PWD/data/mmaps50:/work/mmaps" \
      -w /work --entrypoint /azerothcore/env/dist/bin/mmaps_generator acore/ac-wotlk-tools:master \
      --config /work/mmaps/mmaps-config.yaml --threads 10 --silent
    rm -rf data/mmaps && mv data/mmaps50 data/mmaps

[doc("Extract the minimap, road textures, terrain and models from a 3.3.5a game install (~5 GB; optional)")]
[group('data')]
data-client game_dir *args:
    go run ./azsdk/cmd/clientdata -client "{{ game_dir }}" {{ args }} extract data

[doc("Extract spell and item icons from a game install (for the site; optional)")]
[group('data')]
data-icons game_dir:
    go run ./azsdk/cmd/clientdata -client "{{ game_dir }}" icons data

[doc("Rebuild the road layers from data/client (after the road rules change)")]
[group('data')]
data-roads:
    go run ./azsdk/cmd/clientdata roads data

# ---------- the benchmark ----------

[doc("Run a task from suite/tasks (e.g. just run ubrs --model terra)")]
[group('bench')]
run task *args: build
    bin/azbench run "suite/tasks/{{ task }}.yaml" --no-build {{ args }}

[doc("The smoke task: two characters, no agents, 2 minutes: the harness end to end")]
[group('bench')]
smoke: (run "smoke")

[doc("Check a task's plan and the first prompt without starting anything")]
[group('bench')]
plan task *args: build
    bin/azbench run "suite/tasks/{{ task }}.yaml" --no-build --dry-run {{ args }}

[doc("A running run's status: its players, clocks and standings (default: the newest run)")]
[group('bench')]
status run=latest:
    bin/azbench status "{{ run }}"

[doc("Watch a run in the viewer, live or finished (default: the newest) on http://localhost:8800/")]
[group('bench')]
view run=latest *args: build
    bin/azview live --run "{{ run }}" {{ args }}

[doc("Write a run's report card again (default: the newest)")]
[group('bench')]
report run=latest:
    bin/azbench report "{{ run }}"

[doc("Publish runs to the site (site/src/data/runs)")]
[group('bench')]
publish +runs: build
    bin/azbench publish {{ runs }}

[doc("Any azbench command (just azbench pause <run>, just azbench kit gen mage 60, ...)")]
[group('bench')]
azbench *args: build
    bin/azbench {{ args }}

# ---------- site ----------

[doc("Install the site's dependencies")]
[group('site')]
site-install:
    cd site && npm ci

[doc("The site with live reload on http://localhost:4321/leeroy/")]
[group('site')]
site-dev:
    cd site && npm run dev

[doc("Build the site into site/dist")]
[group('site')]
site-build:
    cd site && npm run build

[doc("Build the site and check every internal link and anchor")]
[group('site')]
site-check: site-build
    cd site && npm run check:links

# ---------- development ----------

[doc("Log in a GM character for testing (warrior: Devgm on 8112; another class: Dev<class> on 8113)")]
[group('dev')]
gm class="warrior" *where: build
    bin/azbench gm --class {{ class }} {{ where }}

[doc("Regenerate azsdk/docs/api-reference.md from a live session")]
[group('dev')]
apidoc: build
    bin/azbench apidoc

# ---------- remote runs (Hetzner Cloud; settings and keys in .env, see .env.example) ----------

[doc("Build the run servers' snapshot from this checkout's data/, server and tools (~1 h; again after those change)")]
[group('remote')]
remote-image:
    deploy/hetzner/remote.sh image

[doc("Run a task on a new server (LEEROY_TYPE=ccx43 for another size): uploads the run to R2, then deletes itself")]
[group('remote')]
remote-run task *args:
    deploy/hetzner/remote.sh run {{ task }} {{ args }}

[doc("Claude Code's long-lived token for the run servers into .env (claude setup-token: sign in in the browser)")]
[group('remote')]
claude-token:
    deploy/claude-token.sh

[doc("The run servers")]
[group('remote')]
remote-ls:
    deploy/hetzner/remote.sh ls

[doc("A run server's run: the unit, its last lines, azbench status")]
[group('remote')]
remote-status server:
    deploy/hetzner/remote.sh status {{ server }}

[doc("Watch a run server's run in the viewer, through SSH (http://localhost:8801/)")]
[group('remote')]
remote-view server port="8801":
    deploy/hetzner/remote.sh view {{ server }} {{ port }}

[doc("A shell (or a command) on a run server")]
[group('remote')]
remote-ssh server *cmd:
    deploy/hetzner/remote.sh ssh {{ server }} {{ cmd }}

[doc("End a run server's run early: it pauses, uploads and the server goes")]
[group('remote')]
remote-stop server:
    deploy/hetzner/remote.sh stop {{ server }}

[doc("Delete a server now, without uploading")]
[group('remote')]
remote-kill server:
    deploy/hetzner/remote.sh kill {{ server }}

[doc("The runs in R2")]
[group('remote')]
remote-runs:
    deploy/hetzner/remote.sh runs

[doc("Download runs from R2 into var/runs (default: every one not here yet)")]
[group('remote')]
remote-fetch *runs:
    deploy/hetzner/remote.sh fetch {{ runs }}

[doc("Upload what a published run's page links to (trajectories, game logs) to the assets bucket (default: the newest run)")]
[group('bench')]
upload run=latest:
    deploy/upload.sh "{{ run }}"

# ---------- release ----------

[doc("Release both modules at a version: tag azsdk, require it in the root, tag the root, push (main must be pushed)")]
[group('release')]
release version:
    #!/usr/bin/env bash
    set -euo pipefail
    v="v{{ trim_start_match(version, 'v') }}"
    for tag in "azsdk/$v" "$v"; do
        if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then echo "tag $tag exists"; exit 1; fi
    done
    if [ -n "$(jj diff --summary)" ]; then echo "the working copy has changes: commit them first"; exit 1; fi
    jj git fetch
    if [ "$(jj log -r main --no-graph -T commit_id)" != "$(jj log -r main@origin --no-graph -T commit_id)" ]; then
        echo "main isn't pushed (or is behind origin): push it first"; exit 1
    fi
    if [ "$(jj log -r @- --no-graph -T commit_id)" != "$(jj log -r main --no-graph -T commit_id)" ]; then
        echo "the working copy isn't on main: jj new main first"; exit 1
    fi
    just ci
    # the SDK first: its tag must be fetchable before the root can require it
    git tag -a "azsdk/$v" main -m "azsdk $v"
    git push origin "azsdk/$v"
    # straight from GitHub, not the proxy: asking the proxy seconds after the push can cache a miss for half an hour
    export GOWORK=off GOPROXY=direct GONOSUMDB=github.com/pthm/leeroy
    go mod edit -require="github.com/pthm/leeroy/azsdk@$v"
    go mod tidy
    go build ./...
    jj commit go.mod go.sum -m "release $v: require azsdk $v"
    jj bookmark set main -r @-
    jj git push --bookmark main
    git tag -a "$v" main -m "$v"
    git push origin "$v"
    echo "released azsdk/$v and $v: go install github.com/pthm/leeroy/cmd/azbench@$v"
