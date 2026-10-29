# server

The local AzerothCore 3.3.5a server the agents play on, and the tools around it.

| Path | What it is |
|---|---|
| `azerothcore/` | Upstream [azerothcore-wotlk](https://github.com/azerothcore/azerothcore-wotlk) checkout (gitignored) |
| `override.yml` | Compose overrides layered on `azerothcore/docker-compose.yml` |
| `account/` | Create or reset a game account (and its GM level): `bin/account` |
| `recorder/mod-recorder/` | `mod-recorder`: the server-side event log the viewer reads ([azview](../azview/README.md#recording-format)) |

All commands run from the repo root. First-time setup (clone, build, client data) is in the [root README](../README.md).

## Running the server

```sh
docker compose -f server/azerothcore/docker-compose.yml -f server/override.yml up -d
docker compose -f server/azerothcore/docker-compose.yml -f server/override.yml down
docker logs -f ac-worldserver
```

Always pass both compose files. `override.yml` sets `name: server`, which pins the
compose project name, so the volumes are always `server_ac-database` (MySQL) and `server_ac-client-data` (maps,
vmaps, mmaps, DBC files) whatever directory compose runs from.

What `override.yml` changes, on `ac-worldserver` only:

| Setting | Value | Why |
|---|---|---|
| `AC_WARDEN_ENABLED` | `0` | The headless client can't answer Warden anti-cheat probes |
| `AC_PLAYER_SAVE_INTERVAL` | `15000` (ms) | The judge reads progress from the DB; the default is 15 minutes |

Any `worldserver.conf` key can be set the same way, as `AC_<KEY>` (dots become `_`, camelCase becomes SNAKE_CASE).
Ports are upstream's defaults and the override doesn't touch them:

| Port | Service |
|---|---|
| 3724 | authserver (`ac-authserver`) |
| 8085 | worldserver (`ac-worldserver`) |
| 7878 | worldserver SOAP |
| 3306 | MySQL (`ac-database`, root password `password`) |

## Accounts

```sh
./build.sh                          # builds bin/account (and the other tools)
bin/account <user> <pass> [gmLevel]
bin/account agent1 agent1           # non-GM
bin/account dev dev 3               # GM level 3
```

`account` computes the SRP6 salt and verifier itself and writes them to `acore_auth.account` over the database
port (3306), so the database container must be running. Details:
- The username is upper-cased (as the client does); it must be alphanumeric, at most 16 characters.
- An existing account is reset: new salt and verifier, so the new password applies.
- GM access is always cleared first; a `gmLevel` other than 0 then adds an `account_access` row for all realms
  (`RealmID -1`). `gmLevel` is one digit; 3 is the usual full GM.
- Accounts are created with expansion 2 (Wrath).

GM accounts are for development and testing only: GM level 3 unlocks dot-commands like `.levelup`, `.additem` and
`.tele`, which are handy for setting up a scenario and unfair in a benchmark. Scored runs use non-GM accounts
(`azbench run` creates one per agent).

## Observing with a real client

A 3.3.5a game client can log in next to the agents. In the client's `Data/<locale>/realmlist.wtf`:

```
set realmlist 127.0.0.1
```

The realm list the authserver hands out comes from `acore_auth.realmlist`; its `address` must be reachable from the
client (`127.0.0.1` when the client runs on the same machine).

Log in with a GM account (`bin/account dev dev 3`) and, in game:

| Command | Effect |
|---|---|
| `.gm on` | GM mode |
| `.gm visible off` | Invisible to players (and so to the agents' clients) |
| `.cheat god on` | Can't be killed |
| `.appear <name>` | Teleport to a character |

`GM.LoginState` and `GM.Visible` default to `2` (last saved state), so GM mode and invisibility persist across
logins once set. Invisibility doesn't hide chat: a `/say` from an invisible GM is still heard by players nearby,
and so by the agents.

## Resetting characters

`bin/azbench reset <Char>...` does this. By hand: to delete a character so its name can be used again, soft-delete it in SQL (with the character logged out), then
restart the worldserver, which caches character names:

```sh
docker exec -i ac-database mysql -uroot -ppassword acore_characters <<'SQL'
UPDATE characters SET deleteInfos_Name = name, deleteInfos_Account = account, deleteDate = UNIX_TIMESTAMP(),
  name = '', account = 0 WHERE name = 'Agentone';
SQL
docker restart ac-worldserver
```

This is the same update the server does when a player deletes a character. The restart disconnects every player, so
don't do it during a run. The in-game alternatives don't work here: `.character erase` is console-only, and
`.character deleted delete` isn't available to GM level 3 in game.

## Logs

| Where | What |
|---|---|
| `server/azerothcore/env/dist/logs/Server.log` | worldserver log |
| `server/azerothcore/env/dist/logs/Errors.log` | worldserver errors |
| `server/azerothcore/env/dist/logs/Auth.log` | authserver log |
| `server/azerothcore/env/dist/logs/DBImport.log` | database import (first boot, updates) |
| `server/azerothcore/env/dist/logs/recorder.jsonl` | `mod-recorder` events, if installed ([azview](../azview/README.md#recording-format)) |
| `docker logs ac-worldserver` | worldserver console output (`docker logs -f ac-worldserver`) |

Each container mounts `server/azerothcore/env/dist/logs` as `/azerothcore/env/dist/logs`.
