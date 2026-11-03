# tasks

A task is the benchmark's unit, one YAML file: a goal, a clock, starting conditions and a party, played by each model
in its list. The models are in [`models.yaml`](../models.yaml), so a task is defined once however many models play it,
and adding a model to a task is one word.

```sh
bin/azbench run suite/tasks/hardcore.yaml                      # every model, one after another
bin/azbench run suite/tasks/hardcore.yaml --model sonnet,opus  # just these
bin/azbench run suite/tasks/travel.yaml --dry-run              # the players and the first prompt, nothing started
bin/azbench run suite/tasks/hogger.yaml --minutes 60
bin/azbench run suite/checks/smoke.yaml                        # a check (below)
bin/azbench run suite/tasks/travel.yaml --reset                # again from scratch (below)
```

Each model's party plays its own run, one after another so none share the world; with `together: true` every model's
party plays one run in one world (everyone starts at the same moment once all sessions are up, so times are
comparable). Each character gets a name from [`names.txt`](../../internal/bench/config/names.txt), picked by the task, the
model and the party slot: neutral (chat doesn't say who is which model), the same each time, and no two characters of
any task share one. The agent and its workspace, `var/workspaces/<name>/`, take it in lowercase. Names of
characters already on the server are left out of the list. A task with
`require_fresh` therefore runs again with `--reset`: its characters are deleted (soft, as the game does), its agents'
workspaces moved to `var/workspaces/.old/<agent>-<time>`, and the worldserver restarted and waited for (it caches
character names). It refuses if any of the characters is online.

These are the published set: each is played by every model in [`models.yaml`](../models.yaml) (Claude Code, Codex, and
opencode through OpenRouter), so a model's results line up across them.

| Task | Minutes | Models | What it sets up |
|---|---|---|---|
| `travel.yaml` | 90 | together | a new level-30 Human warrior in Northshire per model, goal: reach Darnassus (`zone:1657`) on the other continent |
| `hardcore.yaml` | 240 | apart | a new level-1 Human Arms warrior in Northshire, levelling with `hardcore`: the first death ends the run |
| `hogger.yaml` | 180 | apart | three new level-1 Humans in Northshire (Protection Paladin, Holy Priest, Frost Mage), goal: "Wanted: Hogger" (quest 176) |
| `deadmines-geared.yaml` | 120 | apart | a dungeon party of five at level 20 with [kits](#kits) (green gear, bags, the class spells trained), goal: clear the Deadmines (`achievement:628`), each boss a checkpoint |
| `ubrs.yaml` | 120 | apart | ten level-60s, every Alliance class but the death knight (two tanks, three healers), with pre-raid dungeon gear (kits: blues up to item level 63, none from Blackrock Spire), from Morgan's Vigil, goal: clear Upper Blackrock Spire (`achievement:1307`), each boss a checkpoint |

All set `require_fresh`.

## Checks

[`suite/checks/`](../checks) holds tasks that test the tooling, not the models; they're never published. `just run
<name>` and `just remote-run <name>` find them by name too, and their characters' names are kept apart from the
tasks'.

| Check | Minutes | What it checks |
|---|---|---|
| `smoke.yaml` | 2 | two characters with no agent: the harness end to end without spending model time (`just smoke`) |
| `smoke-openrouter.yaml` | 5 | Claude Code and opencode on cheap models through OpenRouter: both paths for a few cents |
| `kill10.yaml` | 30 | one agent logs in and kills 10 creatures: a whole agent run end to end, e.g. on a remote server |

## Fields

Unknown fields are errors, so a typo doesn't silently change a run.

| Field | |
|---|---|
| `name` | default the file name; the website groups runs by it. A run directory is `runs/<time>-<task>-<model>/` (together: `runs/<time>-<task>/`) |
| `description` | for people |
| `goal` | a judge goal: `score:100000` (XP per hour; "as much as possible"), `level:10`, `quest:176`, `achievement:628`, `quests:5`, `skill:164:75`, `money:<copper>`, `item:<entry>`, `quality:<0-5>`, `achpoints:<n>`, `zone:<id>` (in that zone, an AreaTable id) ([judge](../../internal/bench/judge/README.md)) |
| `minutes` | the run's length; default 60 |
| `wrap_up_minutes` | how long an agent keeps going after its judge records the goal, to look back and write its notes: a `goal_met` event tells it its score is final and its run is over, its scripts stop and game actions fail `run_over` (reads still answer); the other players play on. Default 5 |
| `start` | where every character starts: a name from [`places.yaml`](../places.yaml), `"x y z map zone"`, or `{x, y, z, map, zone}`. Omitted: where they are (new characters: their race's start) |
| `spread` | with `start`: the characters stand evenly on a circle this many yards across around it, each facing its centre (two players: that far apart, face to face); default 0, all on the start |
| `level`, `money` | set before the run (money in copper) |
| `setup` | for characters from earlier runs: `revive` (no ghost, corpse or resurrection sickness), `repair` (every item), `reset_talents` |
| `channel` | the zone channel joined at login; default `General - Elwynn Forest` |
| `hint` | appended to the goal in the prompt |
| `hardcore` | a character's first death ends its run; the score is then the total value earned before it ends (binary goals: unmet, 0), and the prompt states the rule |
| `checkpoints` | progress on the way to the goal: `{name, kill}`, reached when the character or its party kills that creature (an entry), e.g. a dungeon's bosses. The judge records when each was first reached (`checkpoints` in the samples, the result and the report, `at_s` null until then), so a run that doesn't finish shows how far it got. An unmet goal then scores the share reached times the score of meeting it as the clock ends (3600 / the clock in seconds), so any finish outranks any unfinished run. Not in the prompt or the standings |
| `notice` | added to the prompt: text, or a `.md` file next to the task (e.g. what changed since the agents' last run) |
| `require_fresh` | refuse to start if any agent's workspace exists |
| `party` | the characters each model plays: `class`, `spec` (a key from [`specs.yaml`](../specs.yaml): the prompt states class, spec, role, what to optimise and key stats, lists the other players' specs, and the judge puts the role on the board), `race` (default human), `kit` (a starting kit from [`kits/`](../kits), below; none: a new character as the task's other conditions leave it) |
| `models` | who plays: keys of [`models.yaml`](../models.yaml) |
| `together` | `false` (default): a run per model, one after another, so none share the world. `true`: one run with every model's party in one world |

Starting conditions (`start`, `spread`, `level`, `money`, `setup`) are applied in the database while the character is
offline, with full health and mana;
a character that doesn't exist yet is created first with a short login.

## Kits

A kit (`suite/kits/<name>.yaml`) starts a character beyond a new one: its level, money, spells, gear, bags and items.
The harness applies it while the character is offline, after the task's other starting conditions; the run's
`scenario.yaml` keeps the kit as applied, and the agent's prompt says the character starts at that level with gear
equipped and its class spells trained (the facts, not how to use them).

```sh
bin/azbench kit gen warrior 20 --spec protwarrior > suite/kits/protwarrior-20.yaml   # race: --race (default human)
bin/azbench kit gen priest 60 --spec holy --quality rare --max-ilvl 63 --not-from 229 > suite/kits/holy-60.yaml
bin/azbench kit save <Char> > suite/kits/<name>.yaml                                  # a character as it is now
bin/azbench kit apply <Char> suite/kits/<name>.yaml                                   # try one on an offline character
```

`kit gen` gives each slot the item that scores best for the spec's stats (a slot with nothing for them stays as it
is), among items a character of that class, race and level could get by playing:
- **how good:** up to `--quality` (`uncommon`, the default: greens; `rare`: blues too; `epic`) and `--max-ilvl`
  (default the level + 10), from 10 item levels below the character;
- **where from:** dropped by a creature or chest that is spawned in the world, a reward of a quest it can take (given
  by someone not hostile to its side, at most 2 levels above it), or made by a profession the trainers teach;
- **what:** armour of the best type the class wears at that level, weapons the class can learn (the kit includes the
  proficiency, as a weapon master would teach it);
- **left out:** raids (their loot and the quests filed under them), the death knights' starting zone, test,
  deprecated and seasonal items, random-enchantment items ("of the Bear"), items that need a profession, reputation
  or rank, and with `--not-from <map>` a dungeon's own loot and quests (the dungeon the kit is for).

Bags (and a hunter's ammunition) come from vendors. What a class's spells need comes too: a shaman's totems (its class
quests' rewards), a warlock's soul shards, and a hunter's pet (the highest-level beast in the open world it could
tame, a cat if there is one). Each item is named in a comment, with where it comes from; edit
the file freely.

| Field | |
|---|---|
| `level` | 1 to 80 |
| `money` | copper |
| `trained` | every spell the class trainers teach and the class quests reward (a warrior's stances) up to the level, each once its prerequisites are, with what those spells teach in turn (a paladin's Judgement: Judgement of Light); and riding up to Journeyman (40), so class mounts work |
| `spells` | more spells by id: weapon proficiencies, or every spell, from `kit save` |
| `gear` | slot to item entry: `head` `neck` `shoulders` `shirt` `chest` `waist` `legs` `feet` `wrists` `hands` `finger1` `finger2` `trinket1` `trinket2` `back` `main_hand` `off_hand` `ranged` `tabard`. A slot not listed keeps what the character wears |
| `bags` | up to 4 bag entries |
| `items` | `{entry, count}` stacks, in the backpack and then the bags. Arrows or bullets go in the ammo slot |
| `talents` | a fixed build: `{id, rank}` (Talent.dbc ids; `kit save` writes them with names), replacing the character's talents; at most a point a level from 10. A talent that is itself a spell (Ice Barrier) is learned with it |
| `pet` | `{entry, name}`: a hunter's tamed beast (a creature it can tame), its current pet at the kit's level; the server gives it its abilities |

The skills that rise with level (weapons, Defense, the class's own) start at their cap for it, as playing there would
leave them.
Talents stay unspent unless the kit has a `talents` build (a duel kit fixes one, so duels measure fighting rather
than talent choices): the server gives the points the level has left over. A kit can't go with an `item:` or `quality:` goal:
the judge counts every item owned, the kit's too.

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
`azworld`, `standings`, and the command inventory generated into each workspace from the runtime configuration.
`checks/smoke-openrouter.yaml` checks both OpenRouter paths for a few cents.

## Preflight

`bin/azbench check <task.yaml> [--model m,...]` prints bounded, read-only checks of goals, starting floors,
quest prerequisites/spawns, combat levels/estimated health and hit penalties, and starting resources.
Unknown evidence stays unknown. Every new run saves this as `preflight.json`; resume preserves the original.
The assessment is not injected into prompts or workspaces and never chooses an agent's strategy.
