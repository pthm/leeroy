#!/bin/sh
# bench/resume.sh <team-dir> <total-minutes> <message-file>
# Resumes a paused team run (stop team.sh, then every bench.sh): each agent's character logs back in with the
# current SDK, its judge continues from its samples (same baseline and curve; the pause doesn't count), and its
# Claude Code conversation is resumed with the message (use $LEFT in it for the minutes left). Same ports
# (8090 + agent order), records, transcripts and run dirs as before; writes team.json at the end like team.sh.
set -e
team=$(cd "$1" && pwd) mins=$2 msg=$(cd "$(dirname "$3")" && pwd)/$(basename "$3") # absolute: agents run in their workspace
cd "$(dirname "$0")" # relative to bench/
[ -f "$msg" ] || { sed -n 2,6p "$0"; exit 1; }
root=$(pwd); repo=$(dirname "$root")
sql() { docker exec ac-database mysql -uroot -ppassword -N acore_characters -e "$1" 2>/dev/null; }
cat agent/AGENTS.md ../azeroth-sdk/docs/playing.md ../azeroth-sdk/docs/guide.md ../azeroth-sdk/docs/runtime.md ../azeroth-sdk/docs/lua-api.md ../azeroth-sdk/docs/api-reference.md ../azeroth-sdk/docs/world-data.md > "$team/AGENTS.md"

resume_one() { # <agent> <port>
  name=$1 port=$2
  dir=$(ls -d "$root"/runs/*-"$name" | tail -1) ws="$root/workspaces/$name" record="$team/$name.record.jsonl"
  char=$(sed -n 's/.*Your character \([A-Za-z]*\).*/\1/p; s/^character \([A-Za-z]*\) is already.*/\1/p; s/^You are \([A-Za-z]*\), an* .*/\1/p' "$dir/prompt.md" | head -1)
  sid=$(head -1 "$dir/agent.jsonl" | jq -r .session_id)
  acct=$(printf %s "$name" | tr -cd 'a-zA-Z0-9' | cut -c1-16)
  left=$(( mins - $(tail -1 "$dir/samples.jsonl" | jq .elapsed_s) / 60 ))
  export WOW_PORT=$port
  cp "$team/AGENTS.md" "$ws/AGENTS.md"
  [ -f "$ws/NOTES.md" ] || printf '# Notes\n\nYour own notes: what worked, what killed you, where things are. Kept between runs.\n' > "$ws/NOTES.md"
  until [ "$(sql "SELECT online FROM characters WHERE name = '$char'")" = 0 ]; do sleep 2; done
  for try in 1 2 3; do
    ACCOUNT=$acct WORKSPACE="$ws" RECORD="$record" EVENTS="$team" client/play.sh "$char" >> "$ws/session.log" 2>&1 &
    session=$!
    until agent/bin/wow 'return 1' 2>/dev/null | grep -q result || ! kill -0 $session 2>/dev/null; do sleep 1; done
    kill -0 $session 2>/dev/null && break
    [ $try = 3 ] && { echo "$name: session login failed" >&2; return 1; }
    sleep $((try * 5))
  done
  agent/bin/wow "LIB = '$ws/'; chat.join('${CHANNEL:-General - Elwynn Forest}', 1)" >/dev/null
  trap 'kill $agent $judge $session 2>/dev/null; cp "$ws/session.log" "$dir/"' EXIT INT TERM
  bun judge/judge.ts "$char" score:100000 --timeout "${mins}m" --resume "$dir/samples.jsonl" --samples "$dir/samples.jsonl" \
    --record "$record" --board "$team/board" > "$dir/judge.json" &
  judge=$!
  if [ "$(jq -r .runtime "$dir/meta.json" 2>/dev/null)" = codex ]; then
    (PATH="$root/agent/bin:$PATH" WOWBENCH_BOARD="$team/board" WOWBENCH_CHAR="$char" exec "$root/agent/codex-resume.sh" "$dir" "$ws" "$left" \
      "$(LEFT=$left envsubst '$LEFT' < "$msg")") &
  else
  (cd "$ws" && PATH="$root/agent/bin:$PATH" CLAUDE_CODE_DISABLE_AUTO_MEMORY=1 WOWBENCH_BOARD="$team/board" WOWBENCH_CHAR="$char" exec timeout "${left}m" claude \
      -p "$(LEFT=$left envsubst '$LEFT' < "$msg")" --resume "$sid" \
      --setting-sources project --strict-mcp-config --output-format stream-json --verbose ${MODEL:+--model "$MODEL"} \
      --add-dir "$repo/azeroth-sdk/client" "$repo/azeroth-sdk/session" "$repo/azeroth-sdk/script" "$repo/server/azerothcore/src/server/game" "$repo/server/azerothcore/src/server/shared" \
      --allowedTools "Bash(wow:*)" "Bash(worlddb:*)" "Bash(wowdb:*)" "Bash(sleep:*)" "Bash(tail:*)" "Bash(cat:*)" \
        "Bash(ls:*)" "Bash(grep:*)" "Bash(mkdir:*)" "Bash(standings:*)" "Bash(party:*)" "Edit(./**)" --disallowedTools "Edit(~/.claude/**)" "Read(~/.claude/**)" \
      >> "$dir/agent.jsonl" 2>&1) &
  fi
  agent=$!
  wait $judge || true
  cat "$dir/judge.json"
}

port=8090
for r in "$team"/*.record.jsonl; do
  name=$(basename "$r" .record.jsonl)
  (resume_one "$name" $port >> "$team/$name.out" 2>&1) &
  port=$((port + 1)); sleep 5 # concurrent logins time out on the auth server
done
wait
n=$(ls "$team"/*.record.jsonl | wc -l)
for f in "$team"/*.out; do grep '^{' "$f" | tail -n 1; done | jq -s --argjson n $n '{agents: sort_by(-(.score // .value)), faction_score: (map(.score // 0) | add), team_s: (if length == $n and all(.met) then map(.elapsed_s) | max else null end)}' | tee "$team/team.json"
bun ../server/recorder/replay.ts "$team" || true
