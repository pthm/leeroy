# bench

Benchmarks where headless Claude Code agents play WoW through scripts and get scored on what they achieve.

| Path | |
|---|---|
| `bench.sh` | one agent: account, character (with optional starting level/money/position), session, judge, agent; results in `runs/<id>/` |
| `team.sh` | several agents on one clock; writes `team.json` (per-agent results, time until the last finished) and one replay |
| `judge/` | `judge.ts` polls the character database for a goal; `signals.ts` reads every progress signal; `score.json` weights the `score:<n>` composite |
| `agent/` | what the agent is given: `AGENTS.md` (workspace, session, rules) and `bin/` (`wow` eval/scripts, `worlddb` raw SQL, `wowdb` structured lookups) |
| `client/` | the session runner: `play.sh` + a patched AzerothGhost checkout (`ghost/`, gitignored). Replaced by `azsdk` from `../azeroth-sdk` next |
| `workspaces/<agent>/` | each agent's persistent script library (gitignored) |
| `runs/<id>/` | results: `judge.json`, `samples.jsonl`, `agent.jsonl` (transcript), `session.log`, `replay.html` (gitignored) |

An agent's workspace `AGENTS.md` is assembled each run from `agent/AGENTS.md` plus the SDK reference
(`../azeroth-sdk/docs/lua-api.md`, `../azeroth-sdk/docs/world-data.md`), and it gets
`../azeroth-sdk/examples/lua/grind.lua` as `example.lua`. Usage and setup: the repo README.
