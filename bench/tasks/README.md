# tasks

A task is the benchmark's unit, one YAML file: a goal, a clock, starting conditions and a party, played by each model
in its list. The models are in [`models.yaml`](../models.yaml), so a task is defined once however many models play it,
and adding a model to a task is one word.

```sh
bin/azbench run bench/tasks/hardcore.yaml                      # every model, one after another
bin/azbench run bench/tasks/hardcore.yaml --model sonnet,opus  # just these
bin/azbench run bench/tasks/travel.yaml --dry-run              # the players and the first prompt, nothing started
bin/azbench run bench/tasks/hogger.yaml --minutes 60
bin/azbench run bench/tasks/travel.yaml --reset                # again from scratch (below)
```

Each model's party plays its own run, one after another so none share the world; with `together: true` every model's
party plays one run in one world (everyone starts at the same moment once all sessions are up, so times are
comparable). Agents and characters are named from the task, the model and the party slot: a neutral name (so chat
doesn't say who is which model), the same each time, with the workspace `bench/workspaces/<name>/`. A task with
`require_fresh` therefore runs again with `--reset`: its characters are deleted (soft, as the game does), its agents'
workspaces moved to `bench/workspaces/.old/<agent>-<time>`, and the worldserver restarted and waited for (it caches
character names). It refuses if any of the characters is online.

| Task | Minutes | Models | What it sets up |
|---|---|---|---|
| `smoke.yaml` | 2 | none | two characters with no agent: checks the harness end to end without spending model time |
| `smoke-openrouter.yaml` | 5 | together | Claude Code and opencode on cheap models through OpenRouter: checks both paths for a few cents |
| `levelling-party.yaml` | 60 | apart | five new level-1 Humans in Northshire, one of each class (a tank, a healer, three DPS) |
| `frost-mages.yaml` | 60 | together | a new Frost Mage per model, all on opencode through OpenRouter: Claude, GPT and Gemini in three sizes, and DeepSeek |
| `hogger.yaml` | 120 | apart | three new level-5 characters in Goldshire (Protection Paladin, Holy Priest, Frost Mage), goal: "Wanted: Hogger" (quest 176) |
| `deadmines.yaml` | 10080 | apart | five new level-1 Humans in Northshire, a dungeon party, goal: clear the Deadmines (`achievement:628`). A week on the clock; each stops when it has the achievement |
| `travel.yaml` | 90 | together | a new level-30 Human warrior in Northshire per model, goal: reach Darnassus (`zone:1657`) on the other continent |
| `hardcore.yaml` | 240 | apart | a new level-1 Human Arms warrior in Northshire, levelling with `hardcore`: the first death ends the run |

All but the smoke tests set `require_fresh`.

## Fields

Unknown fields are errors, so a typo doesn't silently change a run.

| Field | |
|---|---|
| `name` | default the file name; the website groups runs by it. A run directory is `runs/<time>-<task>-<model>/` (together: `runs/<time>-<task>/`) |
| `description` | for people |
| `goal` | a judge goal: `score:100000` (XP per hour; "as much as possible"), `level:10`, `quest:176`, `achievement:628`, `quests:5`, `skill:164:75`, `money:<copper>`, `item:<entry>`, `quality:<0-5>`, `achpoints:<n>`, `zone:<id>` (in that zone, an AreaTable id) ([judge](../../azbench/judge/README.md)) |
| `minutes` | the run's length; default 60 |
| `wrap_up_minutes` | how long an agent and its session keep going after its judge records the goal (its score is final; a `goal_met` event tells it); default 5 |
| `start` | where every character starts: a name from [`places.yaml`](../places.yaml), `"x y z map zone"`, or `{x, y, z, map, zone}`. Omitted: where they are (new characters: their race's start) |
| `level`, `money` | set before the run (money in copper) |
| `setup` | for characters from earlier runs: `revive` (no ghost, corpse or resurrection sickness), `repair` (every item), `reset_talents` |
| `channel` | the zone channel joined at login; default `General - Elwynn Forest` |
| `hint` | appended to the goal in the prompt |
| `hardcore` | a character's first death ends its run; the score is then the total value earned before it ends (binary goals: unmet, 0), and the prompt states the rule |
| `notice` | added to the prompt: text, or a `.md` file next to the task (e.g. what changed since the agents' last run) |
| `require_fresh` | refuse to start if any agent's workspace exists |
| `party` | the characters each model plays: `class`, `spec` (a key from [`specs.yaml`](../specs.yaml): the prompt states class, spec, role, what to optimise and key stats, lists the other players' specs, and the judge puts the role on the board), `race` (default human) |
| `models` | who plays: keys of [`models.yaml`](../models.yaml) |
| `together` | `false` (default): a run per model, one after another, so none share the world. `true`: one run with every model's party in one world |

Starting conditions (`start`, `level`, `money`, `setup`) are applied in the database while the character is offline;
a character that doesn't exist yet is created first with a short login.

## Models

[`models.yaml`](../models.yaml) names who plays: each key is a runtime, its model and the reasoning effort. To compare
another effort, add another key (`opus-high`).

| Field | |
|---|---|
| `runtime` | `claude` (Claude Code), `codex` (Codex CLI), `opencode` ([opencode](https://opencode.ai), any provider), or `none` (no agent: the session and judge only, for testing the harness) |
| `model` | default the runtime's own. For opencode, `provider/model` as `opencode models` lists them (`anthropic/claude-opus-5-5`, `openrouter/moonshotai/kimi-k3`) |
| `effort` | reasoning effort: Claude Code `low` `medium` `high` `xhigh` `max` (`--effort`); Codex `minimal` `low` `medium` `high` `xhigh` (`model_reasoning_effort`); opencode the provider's variant names (`--variant`). Required for Claude Code and Codex, so the run records what the model used rather than a default its own config can change unseen; optional for opencode (empty: `default`). `meta.json` and the report record it, and the website ranks each model and effort apart |
| `provider` | `openrouter`: the model through [OpenRouter](https://openrouter.ai), with `OPENROUTER_API_KEY`, and the model is its slug (`anthropic/claude-opus-5.5`, `deepseek/deepseek-v4-flash`). For `claude` (its Anthropic-compatible endpoint) and `opencode` (`openrouter/` is added to the model) |

opencode runs with its own empty config (`<run>/<agent>/opencode/`, not `~/.config/opencode`) and the same sandbox as
Claude Code's allowed tools: it writes only in its workspace, reads the SDK and server source, and runs only `wow`,
`azworld`, `standings`, and the command inventory generated into each workspace from the runtime configuration. `smoke-openrouter.yaml` checks both
OpenRouter paths for a few cents.

## Preflight

`bin/azbench check <task.yaml> [--model m,...]` prints bounded, read-only checks of goals, starting floors,
quest prerequisites/spawns, combat levels/estimated health and hit penalties, and starting resources.
Unknown evidence stays unknown. Every new run saves this as `preflight.json`; resume preserves the original.
The assessment is not injected into prompts or workspaces and never chooses an agent's strategy.
