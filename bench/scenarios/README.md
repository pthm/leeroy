# scenarios

Ready-to-launch team runs. Each calls `../team.sh` with a `score:100000` goal (XP per hour, judge v6); `MINUTES`
overrides the length. Agents a01-a10 are Humans: two each of Warrior, Paladin, Rogue, Priest and Mage.

```sh
bench/scenarios/fresh-split.sh
MINUTES=60 bench/scenarios/goldshire-rerun.sh
```

| Script | Minutes | What it sets up |
|---|---|---|
| `hogger-from-1.sh` | 120 | a01-a10 from level 1 in Northshire, with "Wanted: Hogger" (quest 176) as a suggested target |
| `goldshire-rerun.sh` | 120 | the same characters again (gear and libraries kept, fresh conversations), moved to Goldshire. Before launching it clears corpses and ghosts, sets full health and repairs every item |
| `stormwind-roles.sh` | 60 | the same characters, each with a spec from `specs.tsv` (two tanks, two healers, six DPS), talents reset, moved to Stormwind's Trade District, `CHANNEL=General - Stormwind City`, and `stormwind-roles.notice.md` as the `NOTICE` (the API rebuild, specs, the Stormwind start) |
| `fresh-split.sh` | 120 | ten new agents a11-a20 from level 1: five on Claude Code (`MODEL`, default `claude-opus-5-5`), five on Codex (its default model), each side one of each class with mirrored specs (a tank, a healer, three DPS). Refuses to start if any of their workspaces exists |

`goldshire-rerun.sh` and `stormwind-roles.sh` edit the characters in the database, so run them while the characters
are offline.
