#!/bin/sh
# ./bench.sh <agent-name> <Character> <race> <class> <goal> [timeout-min]
#   e.g. ./bench.sh claude1 Humanwar 1 1 level:5 60
# Fresh account + character, a persistent session, the judge, and a headless Claude Code agent.
# The agent runs inside agents/<name>/ (its persistent library, the only place it can read or write besides
# the client/server source); tools come from bin/ on PATH so it can't edit them.
# Results land in runs/<id>/: judge.json (score), samples.jsonl (progress every 15s), agent.jsonl
# (transcript), session.log, prompt.md. $WOW_PORT (default 8090) for parallel runs, $MODEL to pick a model.
# Starting conditions (applied in the DB before the agent's session): $LEVEL, $MONEY (copper),
# $START="x y z map zone" (e.g. Goldshire: "-9460 70 56.6 0 12"). $CHANNEL: zone channel auto-joined
# (default "General - Elwynn Forest"; real clients join their zone's General automatically).
# $HINT: extra line appended to the task prompt. $GO: file to wait for before the judge/agent start
# (team.sh uses it to start every clock together; touches $GO.<name>.ready once the session is up).
set -e
{ # parsed whole before running, so editing this file can't break runs in progress
name=$1 char=$2 race=$3 class=$4 goal=$5 mins=${6:-60}
[ -n "$goal" ] || { sed -n 2,7p "$0"; exit 1; }
export WOW_PORT=${WOW_PORT:-8090}
id="$(date +%Y%m%d-%H%M%S)-$name"; dir="runs/$id"
root=$(pwd); ws="$root/agents/$name"
mkdir -p "$dir" "$ws"
cp AGENTS.md agents/example.lua "$ws/" # refreshed each run
acct=$(printf %s "$name" | tr -cd 'a-zA-Z0-9' | cut -c1-16)
n=${goal#*:}
case "$goal" in
  level:*) goal_text="reach character level $n" ;;
  quests:*) goal_text="complete $n quests (accepted, objectives done, and turned in to the quest ender)" ;;
  money:*) goal_text="hold $n copper" ;;
  item:*) goal_text="own item $n (item_template entry)" ;;
  quality:*) goal_text="own any item of quality $n or better (3 = rare)" ;;
  score:*) goal_text="make as much overall progress with your character as you can" ;;
  achpoints:*) goal_text="earn as many achievement points as you can" ;;
  quest:*) goal_text="complete the quest \"$(bin/worlddb "SELECT LogTitle FROM quest_template WHERE ID = $n" | tail -1)\" (quest $n): accept it, do its objectives, and turn it in" ;;
esac
score_text="You are scored on wall-clock time until an external judge sees the goal met; if time
runs out ($mins minutes), your progress toward it is the score."
stop_text="Do not stop until the goal is met: confirm it yourself with wow before finishing."
case "$goal" in achpoints:*)
  score_text="Your score is the total points of the achievements you earn during the next $mins minutes
(ones already earned don't count). There is no finish line: keep earning until time runs out. Achievement
names, points and criteria are in worlddb (see AGENTS.md); bot.achievements() shows your progress."
  stop_text="Do not stop early: keep earning points until the time runs out." ;;
score:*)
  score_text="Your score is a weighted sum of how much these grow during the next $mins minutes (weight per unit):
$(jq -r '.weights | to_entries[] | "  \(.key): \(.value)"' "$root/score.json")
Fractional level counts (10.5 = halfway to 11); avg_item_level is over your 17 gear slots; wealth_copper is money
plus the vendor value of what you own; deaths subtract. There is no finish line."
  stop_text="Do not stop early: keep improving your score until the time runs out." ;;
esac

bun account.ts "$acct" "$acct" # non-GM
docker exec ac-database mysql -uroot -ppassword -e "CREATE USER IF NOT EXISTS 'worldreader'@'%' IDENTIFIED BY 'worldreader';
  GRANT SELECT ON acore_world.* TO 'worldreader'@'%';" 2>/dev/null # for worlddb
sql() { docker exec ac-database mysql -uroot -ppassword -N acore_characters -e "$1" 2>/dev/null; }
if [ -n "$LEVEL$MONEY$START" ]; then
  # create the character with a short login, then edit it while offline
  for try in 1 2 3; do # concurrent logins can time out on the auth server
    ACCOUNT=$acct ./play.sh "$char" --race "$race" --class "$class" --duration 8s > "$ws/session.log" 2>&1 || true
    [ -n "$(sql "SELECT 1 FROM characters WHERE name = '$char'")" ] && break
    sleep $((try * 5))
  done
  [ -n "$(sql "SELECT 1 FROM characters WHERE name = '$char'")" ] || { echo "$name: character $char not created, see $ws/session.log" >&2; exit 1; }
  until [ "$(sql "SELECT online FROM characters WHERE name = '$char'")" = 0 ]; do sleep 1; done
  set -- $START
  sql "UPDATE characters SET health = 100000
    ${LEVEL:+, level = $LEVEL, xp = 0} ${MONEY:+, money = $MONEY}
    ${START:+, position_x = $1, position_y = $2, position_z = $3, map = $4, zone = $5}
    WHERE name = '$char'"
fi
trap 'kill $agent $judge $session 2>/dev/null; cp "$ws/session.log" "$dir/"' EXIT INT TERM
[ -n "$LEVEL$MONEY$START" ] || : > "$ws/session.log" # the workspace (and old log) persists
for try in 1 2 3; do # the auth server sometimes times out the logon proof
  ACCOUNT=$acct ./play.sh "$char" --race "$race" --class "$class" >> "$ws/session.log" 2>&1 &
  session=$!
  until bin/wow 'return 1' 2>/dev/null | grep -q result || ! kill -0 $session 2>/dev/null; do sleep 1; done
  kill -0 $session 2>/dev/null && break
  echo "$name: session login failed (try $try), see $ws/session.log" >&2
  [ $try = 3 ] && exit 1
  sleep $((try * 5))
done
bin/wow "LIB = '$ws/'; bot.join_channel('${CHANNEL:-General - Elwynn Forest}', 1)" >/dev/null
# ponytail: one zone channel joined at start; no auto-rejoin on zone change (agents can join_channel)

[ -z "$GO" ] || { touch "$GO.$name.ready"; until [ -e "$GO" ]; do sleep 1; done; }
bun judge.ts "$char" "$goal" --timeout "${mins}m" --samples "$dir/samples.jsonl" > "$dir/judge.json" &
judge=$!

cat > "$dir/prompt.md" <<PROMPT
You are playing World of Warcraft (3.3.5a) on a local private server set up for AI benchmarking. Your
character $char is already logged in and waiting.

GOAL: $goal_text. $score_text${HINT:+
$HINT}

Read AGENTS.md first. It documents your session (wow), the bot API, and worlddb, a read-only SQL view
of the game's world database: mob spawns and levels, quest givers, vendors, loot tables.
Reference source (read-only): game client $root/ghost/client, server $root/server/src/server/game and
$root/server/src/server/shared.

HOW TO WORK
- Start earning progress within your first few minutes, then improve. Don't plan at length before you have
  a working baseline.
- Iterate in small steps: try one action with wow, check it worked, then build it into a script.
- Measure, don't guess: wow 'return bot.get_level(), bot.get_xp()' gives level, xp, xp for next level.
  Track your XP/min and change strategy when it drops (e.g. mobs becoming too low level, frequent deaths).
- While a script runs, poll progress with wow -w <seconds> '<lua>' (waits, then queries). Don't end your
  session.
- $stop_text

YOUR LIBRARY: your working directory is yours and persists across runs. Reuse what's there, and save
anything reusable (combat, death handling, navigation, packet parsers) as you go.

RULES: Progress through legitimate gameplay only. You have no GM access; don't try to modify the server,
the database, the judge or the harness. Your session log is session.log in your working directory.
PROMPT

(cd "$ws" && PATH="$root/bin:$PATH" exec timeout "${mins}m" claude -p "$(cat "$root/$dir/prompt.md")" \
  --setting-sources project --strict-mcp-config --output-format stream-json --verbose ${MODEL:+--model "$MODEL"} \
  --add-dir "$root/ghost/client" "$root/server/src/server/game" "$root/server/src/server/shared" \
  --allowedTools "Bash(wow:*)" "Bash(worlddb:*)" "Bash(wowdb:*)" "Bash(sleep:*)" "Bash(tail:*)" "Bash(cat:*)" \
    "Bash(ls:*)" "Bash(grep:*)" "Bash(mkdir:*)" "Edit(/**)" \
  > "$root/$dir/agent.jsonl" 2>&1) &
agent=$!

wait $judge || true
cat "$dir/judge.json"
[ -n "$GO" ] || bun replay.ts "$dir" >/dev/null 2>&1 || true # team.sh builds one replay for the whole team
exit
}
