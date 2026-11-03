# deploy

Running tasks off your laptop, and publishing what run pages link to. Everything starts from your laptop; nothing here
runs on a schedule.

| | |
|---|---|
| [`hetzner/remote.sh`](hetzner/remote.sh) | Remote runs on Hetzner Cloud (`just remote-*`): one server per run, made from a snapshot, deleted when the run is uploaded |
| [`hetzner/server/`](hetzner/server) | What runs on those servers: the snapshot's setup, a run, its end (upload, delete) |
| [`upload.sh`](upload.sh) | `just upload <run>`: a published run's trajectories and game logs to the public assets bucket |
| `lib.sh` | `.env` and R2 (through rclone), for both |

## Once

1. **Hetzner Cloud.** Make a project for LEEROY and an API token (read and write) in it.
2. **Cloudflare R2.** Make two buckets: `leeroy-runs` (private: whole runs) and `leeroy-assets` (public: what run pages
   link to: give it the custom domain `assets.leeroy.dev`, which `site/.env.production` points run pages at). Make an
   R2 API token with Object Read & Write on both, and note its S3 access key, secret and the account id.
3. **Keys.** `cp .env.example .env` and fill in the Hetzner and R2 keys. For Claude Code, run `just claude-token`: it
   runs `claude setup-token` (sign in in the browser), writes the long-lived token it prints into `.env` and checks
   Claude Code takes it. (This machine's own login can't be copied: on macOS it's in the Keychain, and it refreshes as
   it's used.) Codex's login (`~/.codex/auth.json`) is copied to each server as it is. `just remote-run` stops before
   making a server if the task's runtimes need a key that's missing.
4. **Tools.** `mise install` (hcloud, rclone and jq are in `mise.toml`).
5. **The snapshot.** `just remote-image`: a builder server in Hillsboro, Oregon (`hil`, US West; `LEEROY_LOCATION`
   for another) gets this checkout, the AzerothCore source and your `data/`
   (about 8 GB, uploaded from here), installs Docker and the pinned tools, builds the server images, imports the
   databases and the `wowbench` schema, and is snapshotted, then deleted. About an hour. Again whenever the server,
   `data/` or `mise.toml` change; the newest two snapshots are kept.

## A run

```sh
just remote-run ubrs --model terra          # LEEROY_TYPE=ccx43 just remote-run ... for a bigger server
just remote-ls                              # the run servers
just remote-status leeroy-ubrs-1102-1530    # its run
just remote-view leeroy-ubrs-1102-1530      # the viewer on http://localhost:8801/, through SSH
just remote-stop leeroy-ubrs-1102-1530      # end it early: it pauses, uploads, and the server goes
just remote-runs                            # finished runs in R2
just remote-fetch                           # download them into var/runs
just publish var/runs/<run> && just upload var/runs/<run>   # then as for any run
```

`remote-run` makes a server from the newest snapshot, copies this checkout as it is (uncommitted changes and git
history included, so the SDK revision is the same as here; not `data/`, `var/` or the server source, which the snapshot
has), copies the keys, and starts `azbench run suite/tasks/<task>.yaml --monitor 10s` as a systemd unit. When the run
ends the server uploads it to `leeroy-runs/runs/<run>.tar.zst` (its log to `logs/<server>.log`) and deletes itself.
Several runs at once are several servers: each has its own game server, so ports and characters never collide.

**Measuring the server.** Each remote run records the machine every 10 s (`server/resources.jsonl` in the run: CPU,
memory, load, disk, network, and each kind of process: `azsdk`, the agents' runtimes, the worldserver, MySQL), and
`run.json`'s `host` names its type, location and hourly price. The report card's `host` sums it up (mean, 95th
percentile and peak CPU, peak memory, per-process figures, and what the server cost), to choose a server type by.

**Safety.**
- Servers accept SSH alone (the `leeroy` firewall); the game server, MySQL and the viewer are only reachable through it.
- The agents' keys are in `/etc/leeroy/agents.env`; the Hetzner and R2 keys, which the server needs to upload and delete
  itself, are in `/etc/leeroy/ops.env`, outside the agents' environment. Both go with the server.
- A server deletes itself after `LEEROY_MAX_HOURS` (default 26) whatever state it's in, pausing and uploading a run
  still going first. If that upload fails it powers off instead (`remote-ls` shows it; a stopped server still costs its
  disk), so a run is never lost.
- A Codex login file holds a refresh token that changes when it's used: two runs at once on one login can log each
  other out. Give Codex an API key for concurrent runs, or run one Codex run at a time.
