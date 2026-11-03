#!/usr/bin/env bash
# upload.sh <run dir>: what a published run's page links to, into the public assets bucket (R2_ASSETS_BUCKET): each
# agent's trajectory (runs/<run>/trajectories/<agent>.json) and each character's game log, gzipped
# (runs/<run>/game/<char>.jsonl.gz), as `azbench publish` lists them. Paths never change, so they cache forever.
set -euo pipefail
cd "$(dirname "$0")/.."
. deploy/lib.sh
run=${1:?usage: upload.sh <run dir>}
run=${run%/}
[ -f "$run/run.json" ] || die "$run isn't a run (no run.json)"
: "${R2_ASSETS_BUCKET:=leeroy-assets}"
id=$(basename "$run")
dst="r2:$R2_ASSETS_BUCKET/runs/$id"
cache=(--header-upload "Cache-Control: public, max-age=31536000, immutable")
jq -r '.players[] | "\(.agent) \(.char)"' "$run/run.json" | while read -r agent char; do
	if [ -f "$run/agents/$agent/trajectory.json" ]; then
		r2 copyto "${cache[@]}" "$run/agents/$agent/trajectory.json" "$dst/trajectories/$agent.json"
		echo "$id/trajectories/$agent.json"
	fi
	if [ -f "$run/game/$char/events.jsonl" ]; then
		gzip -9c "$run/game/$char/events.jsonl" | r2 rcat "${cache[@]}" --header-upload "Content-Type: application/gzip" "$dst/game/$char.jsonl.gz"
		echo "$id/game/$char.jsonl.gz"
	fi
done
