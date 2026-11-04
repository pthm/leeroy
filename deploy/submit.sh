#!/usr/bin/env bash
# submit.sh <run> (just submit): a contributor's run, packed for a pull request: var/submissions/<run>.tar.gz with the
# run's published JSON, what its page loads from the assets bucket (assets/: its maps, trajectories and game logs, and
# the icons), and the evidence a maintainer reviews it by (run/: run.json, scenario, team, the report card, the judge's
# samples, diagnostics; not the agents' raw transcripts, which run to hundreds of MB). Publish the run first
# (just publish). A maintainer unpacks it with just intake and uploads the assets; contributors can't. Every home
# directory in it (/Users/<name>, /home/<name>) is written ~, as publish writes them, so it doesn't name your account.
set -euo pipefail
cd "$(dirname "$0")/.."
. deploy/lib.sh
r=${1:-}
[ -n "$r" ] || die "usage: just submit <run dir or id>"
id=$(basename "${r%/}")
run=var/runs/$id
[ -d "$run" ] || die "no run $run"
[ -f "site/src/data/runs/$id.json" ] && [ -d "var/assets/runs/$id" ] || die "publish it first: just publish $run"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/$id/run" "$tmp/$id/assets/runs"
cp "site/src/data/runs/$id.json" "$tmp/$id/"
cp -R "var/assets/runs/$id" "$tmp/$id/assets/runs/"
[ ! -d var/assets/icons ] || cp -R var/assets/icons "$tmp/$id/assets/"
for f in run.json scenario.yaml team.json report.json preflight.json supervisor.log judge diagnostics board; do
	[ ! -e "$run/$f" ] || cp -R "$run/$f" "$tmp/$id/run/"
done
# Home directories as ~, in the text files and the gzipped game logs (publish does the same; runs published before it
# did keep theirs).
home='s#(^|[\s"\x27(\[=:,;|>`]|\\[ntr"]|\\u00[0-9a-f]{2})/(?:Users|home)/[^/"\x27\\\s]+#$1~#g'
grep -rIl '' "$tmp/$id" | while IFS= read -r f; do perl -pi -e "$home" "$f"; done
find "$tmp/$id" -name '*.gz' | while IFS= read -r f; do
	gzip -dc "$f" | perl -pe "$home" | gzip -9 >"$f.tmp" && mv "$f.tmp" "$f"
done
if grep -rqE '/(Users|home)/' "$tmp/$id" || find "$tmp/$id" -name '*.gz' -exec gzip -dc {} + | grep -qE '/(Users|home)/[^/"]+/'; then
	echo "note: some /Users/ or /home/ text is left (not a path's start); look before you share"
fi
# Secrets: anything shaped like an API key or a private key, and the value of every key, token, secret or password in
# the environment and .env (an agent's runtime has its provider's key in its environment, and an agent can cat). Only
# names print; a hit stops the packing.
plain=$(mktemp)
{ grep -rIh '' "$tmp/$id" || true; find "$tmp/$id" -name '*.gz' -exec gzip -dc {} + || true; } >"$plain"
leak=$(grep -oE 'sk-ant-[A-Za-z0-9_-]{16,}|sk-(or-v1-|proj-)?[A-Za-z0-9_-]{32,}|AIza[0-9A-Za-z_-]{30,}|gh[pousr]_[A-Za-z0-9]{30,}|AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----' "$plain" | cut -c1-6 | sort -u | tr '\n' ' ' || true)
for k in $(compgen -e | grep -iE 'KEY|TOKEN|SECRET|PASS|AUTH' || true); do
	v=${!k}
	[ ${#v} -lt 8 ] || ! grep -qF -- "$v" "$plain" || leak+="\$$k "
done
rm -f "$plain"
[ -z "$leak" ] || die "it holds what looks like a secret ($leak): find it in var/runs/$id or var/assets/runs/$id and take it out"
mkdir -p var/submissions
out=var/submissions/$id.tar.gz
COPYFILE_DISABLE=1 tar -C "$tmp" -czf "$out" "$id"

size=$(wc -c <"$out" | tr -d ' ')
echo "packed $out ($((size / 1024 / 1024)) MB)"
if [ "$size" -gt $((25 * 1024 * 1024)) ]; then
	echo "too big to attach to a pull request (25 MB): upload it somewhere public and link it in the pull request instead"
else
	echo "attach it to your pull request, which commits site/src/data/runs/$id.json (see CONTRIBUTING.md)"
fi
