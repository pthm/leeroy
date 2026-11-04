#!/usr/bin/env bash
# intake.sh <archive or URL> (just intake): a contributor's submitted run (from just submit), unpacked for review and
# upload. The archive is untrusted: every entry must be a plain file or directory under <run id>/, with no "..", and
# the run must not be published already (here or in the assets bucket), so an upload can't replace another run's
# files. It goes to var/submissions/<run>/ (the evidence stays there), its assets into var/assets (icons only where
# there's none of that name), and its JSON is compared with the pull request's. Then: look at it on just site-dev,
# just upload <run>, merge.
set -euo pipefail
cd "$(dirname "$0")/.."
. deploy/lib.sh
src=${1:-}
[ -n "$src" ] || die "usage: just intake <archive or URL>"
mkdir -p var/submissions
case "$src" in
http://* | https://*)
	f=var/submissions/download.tar.gz
	curl -fL --max-filesize $((200 * 1024 * 1024)) -o "$f" "$src" || die "couldn't download $src"
	src=$f
	;;
esac
[ -f "$src" ] || die "no file $src"

# The listing: one top directory, a run id, and only plain files and directories beneath it.
listing=$(tar -tzf "$src") || die "$src isn't a .tar.gz"
id=$(printf '%s\n' "$listing" | head -1 | cut -d/ -f1)
[[ "$id" =~ ^[0-9]{8}-[0-9]{6}-[a-z0-9-]+$ ]] || die "not a run id: $id"
while IFS= read -r e; do
	case "$e" in
	"$id" | "$id"/*) ;;
	*) die "entry outside $id/: $e" ;;
	esac
	case "/$e/" in */../*) die "entry with ..: $e" ;; esac
done <<<"$listing"
if tar -tvzf "$src" | grep -qv '^[-d]'; then die "links or special files in the archive"; fi

[ ! -d "var/assets/runs/$id" ] || die "$id is already in var/assets: published before?"
if [ -n "${R2_ACCOUNT_ID:-}" ]; then
	[ -z "$(r2 lsf "r2:${R2_ASSETS_BUCKET:-leeroy-assets}/runs/$id/" 2>/dev/null | head -1)" ] ||
		die "$id is already in the assets bucket: a run id is never reused"
fi

dir=var/submissions/$id
rm -rf "$dir"
tar -xzf "$src" -C var/submissions
[ -f "$dir/$id.json" ] && [ -d "$dir/assets/runs/$id" ] && [ -f "$dir/run/run.json" ] ||
	die "$dir doesn't look like just submit's: $id.json, assets/runs/$id/ and run/run.json"

mkdir -p var/assets/runs var/assets/icons
cp -R "$dir/assets/runs/$id" var/assets/runs/
[ ! -d "$dir/assets/icons" ] || cp -Rn "$dir/assets/icons/." var/assets/icons/

pr=site/src/data/runs/$id.json
if [ ! -f "$pr" ]; then
	echo "warning: $pr isn't here: check out the pull request first"
elif ! cmp -s "$pr" "$dir/$id.json"; then
	echo "warning: $pr differs from the archive's: the pull request's JSON isn't what was submitted"
fi
echo "unpacked $id: evidence in $dir/run, assets in var/assets/runs/$id"
echo "next: just site-dev to look at its page, then just upload $id, then merge"
