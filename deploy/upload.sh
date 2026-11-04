#!/usr/bin/env bash
# upload.sh [run ...] (just upload): what published runs' pages load besides their JSON, from var/assets (where azbench
# publish puts it, laid out as the bucket keeps it) into the PUBLIC assets bucket (R2_ASSETS_BUCKET, behind
# assets.leeroy.dev): the icons every run shares (icons/, only the new ones), and each run's map images, trajectories
# and gzipped game logs (runs/<run>/). Never the private runs bucket (R2_RUNS_BUCKET): that holds whole runs, staging
# between the run servers and this machine. A run is its directory or its id; default: the newest run; "media" alone:
# only the icons and media (the blog's screenshots and videos), no run. Paths don't change, so everything is cached
# for good: a changed image gets a new name.
set -euo pipefail
cd "$(dirname "$0")/.."
. deploy/lib.sh
: "${R2_ASSETS_BUCKET:=leeroy-assets}"
[ "$R2_ASSETS_BUCKET" != "${R2_RUNS_BUCKET:-leeroy-runs}" ] || die "R2_ASSETS_BUCKET is the private runs bucket ($R2_RUNS_BUCKET): refusing to publish into it"
assets=var/assets
cache=(--header-upload "Cache-Control: public, max-age=31536000, immutable")
[ $# -gt 0 ] || set -- "$(ls -d var/runs/*/ 2>/dev/null | tail -1)"
[ "$*" != media ] || set --
ids=()
for r in "$@"; do
	id=$(basename "${r%/}")
	[ -d "$assets/runs/$id" ] || die "nothing to upload for $id: publish it first (just publish $r)"
	ids+=("$id")
done
if [ -d "$assets/icons" ]; then
	r2 copy "${cache[@]}" --ignore-existing --stats-one-line "$assets/icons" "r2:$R2_ASSETS_BUCKET/icons"
fi
# media the site's pages load that isn't a run's (the blog's screenshots, art and videos): like icons, a path is never
# reused, so only new files go
if [ -d "$assets/media" ]; then
	r2 copy "${cache[@]}" --ignore-existing --stats-one-line "$assets/media" "r2:$R2_ASSETS_BUCKET/media"
fi
for id in ${ids[@]+"${ids[@]}"}; do # empty with "media" (bash 3 calls an empty array unbound)
	r2 copy "${cache[@]}" --stats-one-line "$assets/runs/$id" "r2:$R2_ASSETS_BUCKET/runs/$id"
	echo "${RCLONE_DRY_RUN:+(dry run) }uploaded $id to $R2_ASSETS_BUCKET/runs/$id"
done
