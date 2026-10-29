# azworld

World queries for agents: where an NPC stands and what it sells or teaches, what a quest needs and where its targets
are, where an item drops, gear upgrades, safe walking routes and map images, all as JSON. It reads the server's
databases (`acore_world` and the derived `wowbench` schema) and the extracted game data in `data/` (maps, DBC files,
and the client's minimap and road tiles when extracted). Agents run it as `azworld` (`bench/agent/bin/azworld`), and `azworld sql` for raw SQL;
the agent-facing docs are in [`azeroth-sdk/docs/world-data.md`](../azeroth-sdk/docs/world-data.md).

```sh
./build.sh
bin/azworld help                     # every query command
bin/azworld quest 33                 # e.g. Wolves Across the Border
bin/azworld setup [-schema name]     # (re)build the wowbench schema; another name builds a copy to compare against
```

The database is on `$LEEROY_DB_ADDR` (default `127.0.0.1:3306`): queries run as the read-only `worldreader` user,
`setup` as root. The data directory is `$LEEROY_DATA`, else the nearest `data/` with `maps/` above the working
directory or the binary.

## Package `world`

`azworld/world` is the shared side of every tool that reads the server or the game data: `azview` (maps, names),
the judge (the characters database) and `bin/account` (the auth database) use it too. The headless
client (`azeroth-sdk`) doesn't: it needs no database.

| File | |
|---|---|
| `db.go` | pooled MySQL connections (`DB`, `World`, `Bench`) and string rows |
| `data.go` | finding `data/` |
| `terrain.go` | ground height, water level and area at a point, from the server's map files (via `azeroth-sdk/pathfinding`) |
| `minimap.go` | the client's minimap and road tiles |
| `render.go` | map images: shaded relief, water, minimap, zone borders, overlays |
| `names.go` | area names, zone labels, creature and item names |
| `query.go` | the query commands (`Run`) |
| `gear.go`, `nav.go` | `upgrades`; `roads`, `route` and `map` |
| `rows.go` | JSON output helpers |
| `dbsetup.go` | `setup`: the `wowbench` schema and a small DBC reader |
