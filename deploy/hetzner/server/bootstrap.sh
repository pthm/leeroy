#!/usr/bin/env bash
# The snapshot's setup (remote.sh image), on the builder with the checkout, the server source and data/ copied to
# /opt/leeroy: Docker, mise and the pinned tools, the server images built and its databases imported, the wowbench
# schema, the tools built; it ends with the server stopped, ready to snapshot.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
# the system up to date when the snapshot is taken (run servers then take no updates: below); a new kernel is the one
# each run server boots from the snapshot. needrestart may restart services here, before anything runs
NEEDRESTART_MODE=a apt-get full-upgrade -y -qq -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold >/dev/null
apt-get autoremove -y -qq >/dev/null
apt-get install -y -qq ca-certificates curl git jq rsync zstd >/dev/null
# no unattended upgrades on the run servers (needrestart would restart a run's unit mid-run; run.sh does it too)
systemctl disable --now -q apt-daily.timer apt-daily-upgrade.timer unattended-upgrades.service 2>/dev/null || true
mkdir -p /etc/needrestart/conf.d && echo "\$nrconf{restart} = 'l';" >/etc/needrestart/conf.d/leeroy.conf
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
rm -f /var/log/leeroy-run.log # each run's log starts empty
rm -rf /var/lib/leeroy # and its once-a-server mark (run.sh) isn't set
