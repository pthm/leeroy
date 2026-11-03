---
title: Setup
description: One-time setup of the server, game data, navmesh and tools.
order: 2
---

You need Docker, [mise](https://mise.jdx.dev) (it installs Go, Node, just and the agents' runtimes) and about 10 GB of disk space. A World of Warcraft 3.3.5a client is optional. With it, the viewer
and route planning can use the game's minimap, road textures and models. Run every command from the repository root.

```sh
git clone https://github.com/pthm/leeroy && cd leeroy
```


```sh
# tools: Go, Node, Python, just and the agents' runtimes, at the versions in mise.toml (https://mise.jdx.dev)
mise install

# server
git clone --depth 1 https://github.com/azerothcore/azerothcore-wotlk.git server/azerothcore
just server-build   # ~10-20 min
just up             # the first boot downloads client data and imports the databases

# data/: the server's maps and DBCs, the wowbench schema, and the SDK's own navmesh. The server's navmesh is built
# for creatures (60° slopes); players walk 50° at most, so the SDK gets its own (~15 min on 12 cores)
just setup

# optional, from a 3.3.5a game install, into data/ (nothing reads the install afterwards): the game's minimap (viewer
# and `azworld map`), its road textures (`azworld route`) and the terrain and model files the viewer's zoomed-in map
# draws; ~5 GB for every continent, about 30 s (just data-client <dir> -maps 0 for Eastern Kingdoms only: ~1 GB)
just data-client "<your 3.3.5a game dir>"

# every tool, into bin/ (azbench run does this too)
just build
```

`just` lists every recipe: the server, data, the benchmark, the site, tests and releases. Docker isn't managed by
mise: install it yourself.

The compose project is pinned to `server` (`server/override.yml`), so the volumes are `server_ac-database` and
`server_ac-client-data`. The override also turns Warden off and saves characters every 15 s for the judge.

