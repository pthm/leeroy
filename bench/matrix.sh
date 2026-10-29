#!/bin/sh
# bench/matrix.sh <matrix.json>        DRY=1: print the cells and the wall-clock estimate, run nothing
# Expands a matrix config into cells, task × minutes × race × class × rep, drops race/class pairs the game doesn't
# allow (playercreateinfo), and runs each cell as a solo bench.sh with a new character and an empty workspace,
# "parallel" at a time (ports 8090+). Config: see matrices/README.md. Writes runs/<ts>-matrix-<name>/: cells.jsonl,
# results.jsonl (each cell's judge result plus its cell), failed.jsonl (cells that never got a result) and
# summary.json (n, goals met, mean time to goal, value and score mean ± standard error; per cell, per task × class
# and per task).
set -e
[ -f "$1" ] || { sed -n 2,7p "$0"; exit 1; }
cfg=$(cd "$(dirname "$1")" && pwd)/$(basename "$1") # before the cd: the path may be relative to the caller
cd "$(dirname "$0")" # relative to bench/
valid=$(docker exec ac-database mysql -uroot -ppassword -N acore_world -e \
  "SELECT DISTINCT CONCAT(race, ':', class) FROM playercreateinfo" 2>/dev/null | tr '\n' ' ')
[ -n "$valid" ] || { echo "can't read playercreateinfo: is ac-database up?" >&2; exit 1; }
cells=$(jq -c --arg valid "$valid" '. as $m | ($valid | split(" ")) as $ok | .tasks[] as $t
  | ($t.minutes // $m.minutes | if type == "array" then .[] else . end) as $min
  | ($t.races // $m.races)[] as $r | ($t.classes // $m.classes)[] as $c
  | select($ok | index("\($r):\($c)")) | range($t.reps // $m.reps // 1) as $i
  | {key: $t.key, goal: $t.goal, minutes: $min, race: $r, class: $c, rep: ($i + 1),
     level: $t.level, money: $t.money, start: $t.start, hint: $t.hint, channel: $t.channel} | with_entries(select(.value != null))' "$cfg")
par=$(jq -r '.parallel // 1' "$cfg")
count=$(printf '%s\n' "$cells" | grep -c .)
mins=$(printf '%s\n' "$cells" | jq -s 'map(.minutes) | add')
echo "$count cells, $mins agent-minutes, about $((mins / par / 60 + 1)) h at $par in parallel" >&2
[ -z "$DRY" ] || { printf '%s\n' "$cells"; exit 0; }

dir="$PWD/runs/$(date +%Y%m%d-%H%M%S)-matrix-$(jq -r .name "$cfg")"; mkdir -p "$dir/ports"
cp "$cfg" "$dir/matrix.json"; printf '%s\n' "$cells" > "$dir/cells.jsonl"
# character names: consonant-vowel syllables of a number unique to the cell (letters only, at most 12)
base=$(( $(date +%s) % 100000000 * 1000 ))
name_of() {
  n=$1 s=""
  while [ "$n" -gt 0 ]; do
    s="$s$(printf bcdfghjklmnprstvwxyz | cut -c$((n % 20 + 1)))$(printf aeiou | cut -c$((n / 20 % 5 + 1)))"
    n=$((n / 100))
  done
  printf %s "$s" | awk '{ print toupper(substr($0, 1, 1)) substr($0, 2) }'
}
run_cell() { # <cell> <slot> <number>
  eval "$(printf %s "$1" | jq -r '@sh "key=\(.key) goal=\(.goal) m=\(.minutes) race=\(.race) class=\(.class)
    level=\(.level // "") money=\(.money // "") start=\(.start // "") hint=\(.hint // "") channel=\(.channel // "")"')"
  char=$(name_of $((base + $3)))
  WOW_PORT=$((8090 + $2)) LEVEL=$level MONEY=$money START=$start HINT=$hint ${channel:+CHANNEL="$channel"} \
    ./bench.sh "m$(printf %s "$char" | tr A-Z a-z)" "$char" "$race" "$class" "$goal" "$m" > "$dir/$char.out" 2>&1 < /dev/null || true
  result=$(grep '^{' "$dir/$char.out" | tail -n 1)
  if [ -n "$result" ]; then printf %s "$result" | jq -c --argjson cell "$1" '. + {cell: $cell}' >> "$dir/results.jsonl"
  else printf '%s\n' "$1" | jq -c --arg c "$char" '. + {char: $c}' >> "$dir/failed.jsonl"; fi
  rmdir "$dir/ports/$2"
}
i=0
while read -r cell; do
  slot=""
  until [ -n "$slot" ]; do
    for p in $(seq 0 $((par - 1))); do mkdir "$dir/ports/$p" 2>/dev/null && { slot=$p; break; }; done
    [ -n "$slot" ] || sleep 5
  done
  run_cell "$cell" "$slot" "$i" &
  i=$((i + 1)); sleep 10 # concurrent logins time out on the auth server
done < "$dir/cells.jsonl"
wait

[ -f "$dir/results.jsonl" ] || { echo "no results, see $dir/*.out" >&2; exit 1; }
jq -s 'def stats: length as $n | if $n == 0 then null else (add / $n) as $m
    | {mean: $m, se: (if $n > 1 then (map(. - $m | . * .) | add) / ($n - 1) / $n | sqrt else null end)} end;
  def summary(k): group_by(.cell | k) | map({group: (.[0].cell | k), goal: .[0].goal, n: length,
    met: (map(select(.met)) | length), time_to_goal_s: (map(select(.met) | .elapsed_s) | stats),
    value: (map(.value) | stats), score: (map(.score) | stats)});
  {by_task: summary({key, minutes}), by_class: summary({key, minutes, class}), by_cell: summary({key, minutes, race, class})}' \
  "$dir/results.jsonl" | tee "$dir/summary.json" | jq -c '.by_task[]'
echo "$dir"
