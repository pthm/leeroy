#!/bin/sh
# ./team.sh <goal> <minutes> <name:Character:race:class>...
#   e.g. LEVEL=10 MONEY=2000 START="-9460 70 56.6 0 12" HINT="..." \
#        ./team.sh quest:176 60 hogger1:Garrick:1:1 hogger2:Paloma:1:2
# One bench.sh per agent (WOW_PORT 8090, 8091, ...), logins staggered. Every judge and agent starts at
# the same moment once all sessions are up, so times are comparable. Writes runs/<id>/team.json:
# per-agent judge results plus team_s = time until the last agent met the goal (null if any didn't).
set -e
goal=$1 mins=$2; shift 2
[ $# -gt 0 ] || { sed -n 2,7p "$0"; exit 1; }
id="$(date +%Y%m%d-%H%M%S)-team"; dir="$PWD/runs/$id"; mkdir -p "$dir"
port=8090
for spec; do
  IFS=: read -r name char race class <<EOF
$spec
EOF
  WOW_PORT=$port GO="$dir/go" ./bench.sh "$name" "$char" "$race" "$class" "$goal" "$mins" > "$dir/$name.out" 2>&1 &
  port=$((port + 1)); sleep 10 # concurrent logins time out on the auth server
done
# ponytail: a bench that dies before its session is up just delays the start by up to 5 min
i=0; until [ "$(ls "$dir"/go.*.ready 2>/dev/null | wc -l)" -ge $# ] || [ $i -ge 300 ]; do sleep 1; i=$((i + 1)); done
date +%s > "$dir/go"; echo "go: $(ls "$dir"/go.*.ready | wc -l)/$# sessions up"
wait
tail -qn1 "$dir"/*.out | grep '^{' | jq -s --argjson n $# '{agents: ., team_s: (if length == $n and all(.met) then map(.elapsed_s) | max else null end)}' | tee "$dir/team.json"
