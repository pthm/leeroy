#!/bin/sh
# bench/restart-agent.sh <team-dir> <agent> <message-file>: mid-run, bring back one agent whose session died (or
# whose conversation ended): restart its character's session on the same port with the current SDK, then resume its
# Claude conversation with the message ($LEFT = minutes left). Its judge keeps running in bench.sh.
set -e
team=$(cd "$1" && pwd) name=$2 msg=$(cd "$(dirname "$3")" && pwd)/$(basename "$3")
cd "$(dirname "$0")"
root=$(pwd); repo=$(dirname "$root")
dir=$(ls -d "$root"/runs/*-"$name" | tail -1) ws="$root/workspaces/$name"
char=$(sed -n 's/.*Your character \([A-Za-z]*\).*/\1/p; s/^character \([A-Za-z]*\) is already.*/\1/p; s/^You are \([A-Za-z]*\), an* .*/\1/p' "$dir/prompt.md" | head -1)
sid=$(head -1 "$dir/agent.jsonl" | jq -r .session_id)
acct=$(printf %s "$name" | tr -cd 'a-zA-Z0-9' | cut -c1-16)
mins=$(sed -n 's/.*progress as much as you can in \([0-9]*\) minutes.*/\1/p' "$dir/prompt.md" | head -1)
# minutes left on its judge's clock (pauses don't count); older boards: wall time since go
left=$(jq -r '.minutes_left // empty' "$team/board/$char.json" 2>/dev/null)
left=${left:-$(( ${mins:-60} - ($(date +%s) - $(cat "$team/go")) / 60 ))}
[ "$left" -ge 1 ] || left=1 # timeout 0m would mean no timeout
port=$(jq -r '.port // empty' "$dir/meta.json")
port=${port:-$(( 8090 + $(ls "$team"/*.record.jsonl | grep -n "/$name.record.jsonl" | cut -d: -f1) - 1 ))}
export WOW_PORT=$port
sql() { docker exec ac-database mysql -uroot -ppassword -N acore_characters -e "$1" 2>/dev/null; }
agent/calm.sh
pkill -f -- "-char $char " 2>/dev/null || true
until [ "$(sql "SELECT online FROM characters WHERE name = '$char'")" = 0 ]; do sleep 2; done
ACCOUNT=$acct WORKSPACE="$ws" RECORD="$team/$name.record.jsonl" EVENTS="$team" nohup client/play.sh "$char" >> "$ws/session.log" 2>&1 &
until agent/bin/wow 'return 1' 2>/dev/null | grep -q result; do sleep 1; done
agent/bin/wow "LIB = '$ws/'; chat.join('${CHANNEL:-General - Elwynn Forest}', 1)" >/dev/null
if [ "$(jq -r .runtime "$dir/meta.json" 2>/dev/null)" = codex ]; then
  PATH="$root/agent/bin:$PATH" WOWBENCH_BOARD="$team/board" WOWBENCH_CHAR="$char" nohup "$root/agent/codex-resume.sh" "$dir" "$ws" "$left" \
    "$(LEFT=$left envsubst '$LEFT' < "$msg")" > /dev/null 2>&1 &
  nohup "$root/agent/watch-exit.sh" $! "$dir/agent.jsonl" "$team/board/$char.json" "$name ($char)" "$team/alerts.log" > /dev/null 2>&1 &
  echo "$name ($char, codex) back on port $port, $left min left"
  exit
fi
cd "$ws" && PATH="$root/agent/bin:$PATH" CLAUDE_CODE_DISABLE_AUTO_MEMORY=1 WOWBENCH_BOARD="$team/board" WOWBENCH_CHAR="$char" nohup timeout "${left}m" claude \
  -p "$(LEFT=$left envsubst '$LEFT' < "$msg")" --resume "$sid" \
  --setting-sources project --strict-mcp-config --output-format stream-json --verbose ${MODEL:+--model "$MODEL"} \
  --add-dir "$repo/azeroth-sdk/client" "$repo/azeroth-sdk/session" "$repo/azeroth-sdk/script" "$repo/server/azerothcore/src/server/game" "$repo/server/azerothcore/src/server/shared" \
  --allowedTools "Bash(wow:*)" "Bash(worlddb:*)" "Bash(azworld:*)" "Bash(sleep:*)" "Bash(tail:*)" "Bash(cat:*)" \
    "Bash(ls:*)" "Bash(grep:*)" "Bash(mkdir:*)" "Bash(standings:*)" "Bash(party:*)" "Edit(./**)" --disallowedTools "Edit(~/.claude/**)" "Read(~/.claude/**)" \
  >> "$dir/agent.jsonl" 2>&1 &
nohup "$root/agent/watch-exit.sh" $! "$dir/agent.jsonl" "$team/board/$char.json" "$name ($char)" "$team/alerts.log" > /dev/null 2>&1 &
echo "$name ($char) back on port $port, $left min left"
