# scenarios

A scenario is one run: a set of agents, the characters they play, a goal and a clock, in YAML. Every agent starts at
the same moment once all sessions are up, so times are comparable. A solo run is a scenario with one agent.

```sh
bin/azbench run bench/scenarios/fresh-split.yaml               # run it
bin/azbench run bench/scenarios/fresh-split.yaml --dry-run     # the players and the first prompt, nothing started
bin/azbench run bench/scenarios/hogger-from-1.yaml --minutes 60
```

| Scenario | Minutes | What it sets up |
|---|---|---|
| `smoke.yaml` | 2 | two characters with no agent (`runtime: none`): checks the harness end to end without spending model time |
| `hogger-from-1.yaml` | 120 | a01-a10 from level 1 in Northshire, with "Wanted: Hogger" (quest 176) as a suggested target |
| `goldshire-rerun.yaml` | 120 | the same characters again (gear and libraries kept, fresh conversations), moved to Goldshire, revived and repaired |
| `stormwind-roles.yaml` | 60 | the same characters, each with a spec (two tanks, two healers, six DPS), talents reset, in Stormwind's Trade District, with `stormwind-roles.notice.md` |
| `fresh-split.yaml` | 120 | ten new agents a11-a20 from level 1: five on Claude Code, five on Codex, each side one of each class with mirrored specs. Refuses to start if any of their workspaces exists |
| `models-frost.yaml` | 60 | ten new Frost Mages a31-a40, one per model, all on opencode through OpenRouter: Claude Opus, Sonnet and Haiku, three sizes of GPT and of Gemini, and DeepSeek |

## Fields

Unknown fields are errors, so a typo doesn't silently change a run.

| Field | |
|---|---|
| `name` | the run directory is `runs/<time>-<name>/`; default the file name |
| `description` | for people |
| `goal` | a judge goal: `score:100000` (XP per hour; "as much as possible"), `level:10`, `quest:176`, `quests:5`, `skill:164:75`, `money:<copper>`, `item:<entry>`, `quality:<0-5>`, `achpoints:<n>` ([judge](../../azbench/judge/README.md)) |
| `minutes` | the run's length; default 60 |
| `start` | where every character starts: a name from [`places.yaml`](../places.yaml), `"x y z map zone"`, or `{x, y, z, map, zone}`. Omitted: where they are (new characters: their race's start) |
| `level`, `money` | set before the run (money in copper) |
| `setup` | for characters from earlier runs: `revive` (no ghost, corpse or resurrection sickness), `repair` (every item), `reset_talents` |
| `channel` | the zone channel joined at login; default `General - Elwynn Forest` |
| `hint` | appended to the goal in the prompt |
| `notice` | added to the prompt: text, or a `.md` file next to the scenario (e.g. what changed since the agents' last run) |
| `require_fresh` | refuse to start if any agent's workspace exists |
| `runtime`, `race`, `provider` | defaults for agents without their own: `claude`, `human`, the runtime's own |
| `models` | runtime to model, e.g. `{claude: claude-opus-5-5}`; default the runtime's own |
| `agents` | the players (below) |

Starting conditions (`start`, `level`, `money`, `setup`) are applied in the database while the character is offline;
a character that doesn't exist yet is created first with a short login.

Each agent:

| Field | |
|---|---|
| `name` | the agent, and its workspace `bench/workspaces/<name>/` (its library and notes, kept between runs) |
| `char` | the character it plays (created if new); 2-12 letters |
| `class` | warrior, paladin, hunter, rogue, priest, death knight, shaman, mage, warlock, druid |
| `race` | human, dwarf, night elf, gnome, orc, undead, tauren, troll, blood elf, draenei |
| `spec` | a key from [`specs.yaml`](../specs.yaml): the prompt states class, spec, role, what to optimise and key stats, lists the other players' specs, and the judge puts the role on the board |
| `runtime` | `claude` (Claude Code), `codex` (Codex CLI), `opencode` ([opencode](https://opencode.ai), any provider), or `none` (no agent: the session and judge only, for testing the harness) |
| `model` | overrides `models`. For opencode, `provider/model` as `opencode models` lists them (`anthropic/claude-opus-5-5`, `openrouter/moonshotai/kimi-k3`) |
| `provider` | `openrouter`: the model through [OpenRouter](https://openrouter.ai), with `OPENROUTER_API_KEY`, and the model is its slug (`anthropic/claude-opus-5.5`, `deepseek/deepseek-v4-flash`). For `claude` (its Anthropic-compatible endpoint) and `opencode` (`openrouter/` is added to the model) |

opencode runs with its own empty config (`<run>/<agent>/opencode/`, not `~/.config/opencode`) and the same sandbox as
Claude Code's allowed tools: it writes only in its workspace, reads the SDK and server source, and runs only `wow`,
`azworld`, `standings` and `sleep`, `tail`, `cat`, `ls`, `grep`, `mkdir`. `smoke-openrouter.yaml` checks both
OpenRouter paths for a few cents.

## Matrices

A matrix (`bench/matrices/*.yaml`) is a grid of solo runs: tasks crossed with races, classes, durations and
repetitions. See [matrices/README.md](../matrices/README.md).
