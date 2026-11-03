#!/usr/bin/env bash
# The snapshot's setup (remote.sh image), on the builder with the checkout, the server source and data/ copied to
# /opt/leeroy: Docker, mise and the pinned tools, the server images built and its databases imported, the wowbench
# schema, the tools built; it ends with the server stopped, ready to snapshot.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl git jq rsync zstd >/dev/null
command -v docker >/dev/null || curl -fsSL https://get.docker.com | sh >/dev/null
[ -x /root/.local/bin/mise ] || curl -fsSL https://mise.run | sh
. /opt/leeroy/deploy/hetzner/server/lib.sh

mise trust -q mise.toml
mise install node # first: Codex installs with its npm
mise install
log "building the server images"
just server-build
just up
wait_ready
just data-schema # the wowbench schema, in the database volume the snapshot keeps
just build       # warms the Go build cache; each run builds its own checkout
just down
log "ready to snapshot"
