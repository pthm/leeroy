---
title: Setup
description: One-time setup of the server, game data, navmesh and tools.
order: 2
---

You need Docker, Go and about 10 GB of disk space. A World of Warcraft 3.3.5a client is optional. With it, the viewer
and route planning can use the game's minimap, road textures and models. Run every command from the repository root.

```sh
git clone https://github.com/pthm/leeroy && cd leeroy
```


```sh
# server
git clone --depth 1 https://github.com/azerothcore/azerothcore-wotlk.git server/azerothcore
cp -r server/recorder/mod-recorder server/azerothcore/modules/   # server-side recording (optional)
docker compose -f server/azerothcore/docker-compose.yml -f server/override.yml build   # ~10-20 min
docker compose -f server/azerothcore/docker-compose.yml -f server/override.yml up -d   # first boot downloads client data + imports DBs

# client data, copied out of the server's volume (~3GB)
mkdir -p data/dbc && docker run --rm -v server_ac-client-data:/src:ro -v $PWD/data:/dst alpine sh -c \
  'cp -r /src/maps /src/mmaps /src/vmaps /dst/ && cd /src/dbc && cp Spell.dbc Achievement.dbc Achievement_Criteria.dbc \
   Achievement_Category.dbc AreaTable.dbc WorldMapOverlay.dbc Faction.dbc FactionTemplate.dbc SpellCastTimes.dbc \
   SpellRange.dbc SpellDuration.dbc SpellRadius.dbc SpellDescriptionVariables.dbc AreaTrigger.dbc Talent.dbc TalentTab.dbc TaxiNodes.dbc TaxiPath.dbc TaxiPathNode.dbc SkillLine.dbc SkillLineAbility.dbc Lock.dbc /dst/dbc/'
./build.sh && bin/azworld setup   # wowbench schema: achievements, areas, spawn zones, faction hostility, spells

# the SDK's navmesh: the server's is built for creatures (60 degree slopes); players walk 50 degrees at most, so the
# SDK gets its own (about 15 minutes on 12 cores). Leave the server's alone: it's how creatures path
docker compose -f server/azerothcore/docker-compose.yml --profile tools build ac-tools
mkdir -p data/mmaps50 && sed 's/^    walkableSlopeAngle: 60$/    walkableSlopeAngle: 50/' \
  server/azerothcore/src/tools/mmaps_generator/mmaps-config.yaml > data/mmaps50/mmaps-config.yaml
docker run --rm -v $PWD/data/maps:/work/maps:ro -v $PWD/data/vmaps:/work/vmaps:ro -v $PWD/data/mmaps50:/work/mmaps \
  -w /work --entrypoint /azerothcore/env/dist/bin/mmaps_generator acore/ac-wotlk-tools:master \
  --config /work/mmaps/mmaps-config.yaml --threads 10 --silent
rm -r data/mmaps && mv data/mmaps50 data/mmaps   # never under a running session: they load tiles as they go

# optional, from a 3.3.5a game install, into data/ (nothing reads the install afterwards): the game's minimap (viewer
# and `azworld map`), its road textures (`azworld route`) and the terrain and model files the viewer's zoomed-in map
# draws; ~5 GB for every continent, about 30 s (-maps 0 for Eastern Kingdoms only: ~1 GB)
go run ./azeroth-sdk/cmd/clientdata -client <your 3.3.5a game dir> extract data
go run ./azeroth-sdk/cmd/clientdata roads data   # the road layers again from data/client, after the road rules change (no install needed)

# every tool, into bin/ (azbench run does this too)
./build.sh
```

The compose project is pinned to `server` (`server/override.yml`), so the volumes are `server_ac-database` and
`server_ac-client-data`. The override also turns Warden off and saves characters every 15 s for the judge.

