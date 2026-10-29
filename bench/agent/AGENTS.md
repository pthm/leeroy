# Playing WoW as an agent

You control one World of Warcraft (3.3.5a) character on a local AzerothCore server. The character stays logged in
for the whole run in a **session**: a game client that runs **Lua scripts**. Scripts play the character; you
write them, start them, watch what happens and change them. With no script running, the character does nothing.

## Your files

Your working directory is your workspace. It's the only place you can write, and it's kept between runs.

| File | |
|---|---|
| `AGENTS.md` | this file: how your session and tools work |
| `game.md` | the rules of the game: stats, gear, classes, XP, combat, death, quests, groups, chat |
| `sdk.md` | the SDK: tasks, actions, events, the Lua API and how it behaves, the world data tool |
| `api-reference.md` | every function, type and event |
| `example.lua` | the shape of a script |
| `NOTES.md` | your notebook (below) |
| `session.log` | your session's log, timestamped: `log()` lines, script errors, slow ticks, kills, deaths, level-ups |

The SDK's source is readable, not writable, at the paths in your task prompt.

## Your tools

- **`wow`**: the game. It runs Lua in your session and prints the result as JSON (`{"error": ...}` on failure).
- **`azworld`**: the world's static data: where things are, what drops what, who gives which quest (sdk.md).
- **`standings`**: the score: every player's score, rates and level in this run, and the minutes left.

```sh
wow 'return me.level(), me.health()'             # query -> {"result":[3,{"current":72,"max":72}], "events": {...}}
wow status                                       # what you'd see on screen: you, your fight, what's around you
wow -f main.lua                                  # load a script file (it starts its tasks)
wow tasks                                        # your tasks: state, status, what each waits for
wow 'task.stop("main")'                          # stop one
wow subscribe                                    # what wakes you; +types adds, -types removes, a list replaces
wow wait 300                                     # block until something you subscribe to happens (or 300 s)
wow events --types chat,death --last 20          # the event log for the whole run
wow events --types log --last 20                 # your scripts' log() lines, with time and task
wow answer 812 sell                              # answer a script's ask()
azworld quest 176                                # a quest's giver, objectives and where its targets are
standings                                        # the score, and the minutes left
```

- A `wow` call runs between your scripts' steps, on the same Lua state: it sees what they see, and what it sets
  they see. It can't wait: anything that takes time goes in a task.
- Every `wow` reply carries `"events"`: what happened since your last call, counted, with the important ones in
  full (sdk.md, "Events"), and `"script_errors"` if a script failed. Script errors, slow ticks and chat you
  subscribe to also go to stderr.
- An error names the file and line (`main.lua:22:`) with a traceback; `wow tasks` keeps a failed task's error and
  shows each task's `busy_ms`. sdk.md, "Seeing what your scripts do", has the rest: logging your own data, cost.
- `wow wait` and `wow -w` take whole seconds, at most 600. If your shell tool returns before a long wait ends (some
  stop waiting after 30 s), use shorter waits in a loop.
- The shell runs only these commands: `wow`, `azworld`, `standings`, `sleep`, `cat`, `ls`, `grep`, `head`, `tail`, `wc`,
  `sort`, `uniq`, `cut`, `mkdir`. Pipes and `;` work when every command in them is one of these (`azworld quest 7 |
  head -20`); anything else (`echo`, `sed`, `for` loops, `python`) is refused. Create and edit files with your
  file-editing tools, not shell redirects or heredocs.

## How it fits together

1. **Scripts run as tasks.** A task is a Lua function that can wait without blocking the game (`wait`, `await`,
   `events.wait`). `task.keep(name, fn)` runs one and runs it again whenever it ends or fails; `task.spawn` runs it
   once. Actions return requests that resolve to `{ok, data}` or `{ok = false, error = {code, message}}`.
2. **Scripts react to events.** `events.on(type, fn)` runs `fn` for each matching event (a death, a chat message,
   a finished fight) while you're doing something else.
3. **You choose what wakes you.** `wow wait` blocks until an event in your subscription (`wow subscribe`): by
   default deaths, stalls (no XP for a while: `events.watch`), script errors, tasks ending, your scripts'
   questions, invites, trades, broken gear, and chat from players to you. `wow events` shows what led up to it.
4. **You change scripts while they run.** Globals persist until you replace them. A loop that reads a settings
   table, or calls functions you redefine with `wow -f`, picks up the change on its next pass (sdk.md,
   "Reloading and changing a running script"). Keep state you want to inspect in globals (`wow 'return state'`).
5. **Scripts can ask you.** `ask(question, options)` in a task waits for `wow answer`.

Lua in the session can read files in your workspace: `LIB` is its path, so `dofile(LIB .. "lib/combat.lua")`
loads a module of yours.

## Other players

The other characters on the server are other agents. They reach you only through chat, and you see chat only in a
reply's `"events"`, in `wow wait`, and in `wow events --types chat`: replies filtered down to `.result`, a
subscription without the chat kinds players use (`chat:whisper`, `chat:party`), or long stretches without a `wow`
call all hide it. A script can handle messages itself with `events.on("chat", fn)`.

## NOTES.md

Your notebook, kept with your workspace. Write in it as you go: what you learn about the game and the tools, what
you tried and how it went, and anything about the SDK or the tools that got in your way or that you'd want
changed.
