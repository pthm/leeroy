#!/usr/bin/env bash
# The run's live viewer (run.sh starts it as the leeroy-view unit): azview on this machine alone (127.0.0.1:8800),
# reached through an SSH tunnel (just remote-view), once azbench has made the run's directory.
set -uo pipefail
. /opt/leeroy/deploy/hetzner/server/lib.sh
exec >>/var/log/leeroy-view.log 2>&1
run=""
for _ in $(seq 1 360); do # a fresh server has one run: the newest directory is this one
	run=$(ls -d var/runs/*/ 2>/dev/null | tail -1)
	[ -n "$run" ] && [ -f "$run/run.json" ] && break
	sleep 5
done
[ -n "$run" ] || { log "no run directory after 30 minutes: no viewer"; exit 1; }
exec bin/azview live --run "${run%/}" --host 127.0.0.1 --port 8800
