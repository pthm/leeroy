#!/bin/sh
# bench/team.sh <goal> <minutes> <name:Character:race:class[:spec[:runtime]]>...   runtime: claude (default) or codex
#   e.g. LEVEL=10 MONEY=2000 START="-9460 70 56.6 0 12" HINT="..." \
#        bench/team.sh quest:176 60 hogger1:Garrick:1:1 hogger2:Paloma:1:2
# One bench.sh per agent (WOW_PORT 8090, 8091, ...), logins staggered. Every judge and agent starts at
# the same moment once all sessions are up, so times are comparable. Writes runs/<id>/team.json:
# per-agent judge results ranked by score, plus team_s = time until the last agent met the goal (null if any didn't),
# and report.json (report.ts).
# With a spec (a key from specs.tsv), each agent's prompt says its class, spec, role and key stats, and lists the
# other players with theirs.
set -e
cd "$(dirname "$0")" # relative to bench/
goal=$1 mins=$2; shift 2
[ $# -gt 0 ] || { sed -n 2,7p "$0"; exit 1; }
../build.sh # the judge, viewer and agent tools in bin/, once for every bench.sh
id="$(date +%Y%m%d-%H%M%S)-team"; dir="$PWD/runs/$id"; mkdir -p "$dir"
# the roster: "- Char: Warrior - Protection (Tank)" per agent with a spec
roster=""
for a; do
  IFS=: read -r _ char _ _ sp _ <<EOF
$a
EOF
  [ -n "$sp" ] && roster="$roster- $char: $(awk -F'\t' -v k="$sp" '$1 == k { print $2 " (" $3 ")" }' specs.tsv)
"
done
port=8090
for a; do
  IFS=: read -r name char race class sp rt <<EOF
$a
EOF
  others=$(printf %s "$roster" | grep -v "^- $char:")
  WOW_PORT=$port GO="$dir/go" SPEC="$sp" RUNTIME="${rt:-claude}" ROSTER="$others" ./bench.sh "$name" "$char" "$race" "$class" "$goal" "$mins" > "$dir/$name.out" 2>&1 &
  port=$((port + 1)); sleep 10 # concurrent logins time out on the auth server
done
# ponytail: a bench that dies before its session is up just delays the start by up to 5 min
i=0; until [ "$(ls "$dir"/go.*.ready 2>/dev/null | wc -l)" -ge $# ] || [ $i -ge 300 ]; do sleep 1; i=$((i + 1)); done
date +%s > "$dir/go"; echo "go: $(ls "$dir"/go.*.ready | wc -l)/$# sessions up"
wait
for f in "$dir"/*.out; do grep '^{' "$f" | tail -n 1; done | jq -s --argjson n $# '{agents: sort_by(-(.score // .value)), faction_score: (map(.score // 0) | add),
  by_runtime: (group_by(.runtime // "claude") | map({runtime: (.[0].runtime // "claude"), players: length, score: (map(.score // 0) | add), best: (map(.score // 0) | max)})),
  team_s: (if length == $n and all(.met) then map(.elapsed_s) | max else null end)}' | tee "$dir/team.json"
bun report.ts "$dir" || true # report.json: how each agent spent the run (duty cycle, wakes, restarts...)
../bin/azview replay "$dir" || echo "no replay (is mod-recorder installed? see README)"
