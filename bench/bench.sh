#!/bin/sh
# bench/bench.sh <agent-name> <Character> <race> <class> <goal> [timeout-min]
#   e.g. bench/bench.sh claude1 Humanwar 1 1 level:5 60
# Fresh account + character, a persistent session, the judge, and a headless Claude Code agent.
# The agent runs inside workspaces/<name>/ (its persistent library, the only place it can read or write besides
# the client/server source); tools come from agent/bin/ on PATH so it can't edit them.
# Results land in runs/<id>/: judge.json (score), samples.jsonl (progress every 15s), agent.jsonl
# (transcript), session.log, prompt.md. $WOW_PORT (default 8090) for parallel runs, $MODEL to pick a model.
# Starting conditions (applied in the DB before the agent's session): $LEVEL, $MONEY (copper),
# $START="x y z map zone" (e.g. Goldshire: "-9460 70 56.6 0 12"). $CHANNEL: zone channel auto-joined
# (default "General - Elwynn Forest"; real clients join their zone's General automatically).
# $HINT: extra line appended to the task prompt. $SPEC: a key from specs.tsv (class spec, role, what to optimise, key
# stats) for the character's identity line. $ROSTER: the other players (one per line). $NOTICE: a file whose text
# is added to the prompt (e.g. what changed since the agent's last run). $GO: file to wait for before the judge/agent start
# (team.sh uses it to start every clock together; touches $GO.<name>.ready once the session is up).
set -e
{ # parsed whole before running, so editing this file can't break runs in progress
cd "$(dirname "$0")" # everything below is relative to bench/
name=$1 char=$2 race=$3 class=$4 goal=$5 mins=${6:-60}
[ -n "$goal" ] || { sed -n 2,7p "$0"; exit 1; }
export WOW_PORT=${WOW_PORT:-8090}
id="$(date +%Y%m%d-%H%M%S)-$name"; dir="runs/$id"
root=$(pwd); repo=$(dirname "$root"); ws="$root/workspaces/$name"
# client-side event log for replays: in the team dir when run by team.sh (one replay for everyone)
record="$root/$dir/record.jsonl"; [ -z "$GO" ] || record="$(dirname "$GO")/$name.record.jsonl"
board="$root/$dir/board"; [ -z "$GO" ] || board="$(dirname "$GO")/board" # judges' latest results, for `standings`
events="$root/$dir"; [ -z "$GO" ] || events="$(dirname "$GO")" # <char>.events.jsonl: the character's event stream for the run
mkdir -p "$dir" "$ws"
# refreshed each run: bench task docs, the playing guide, the SDK reference, and an example script
cat agent/AGENTS.md ../azeroth-sdk/docs/playing.md ../azeroth-sdk/docs/guide.md ../azeroth-sdk/docs/runtime.md ../azeroth-sdk/docs/lua-api.md ../azeroth-sdk/docs/api-reference.md ../azeroth-sdk/docs/world-data.md > "$ws/AGENTS.md"
cp ../azeroth-sdk/examples/lua/grind.lua "$ws/example.lua"
[ -f "$ws/NOTES.md" ] || printf '# Notes\n\nYour own notes: what worked, what killed you, where things are. Kept between runs.\n' > "$ws/NOTES.md"
acct=$(printf %s "$name" | tr -cd 'a-zA-Z0-9' | cut -c1-16)
n=${goal#*:}
case "$goal" in
  level:*) goal_text="reach character level $n" ;;
  quests:*) goal_text="complete $n quests (accepted, objectives done, and turned in to the quest ender)" ;;
  money:*) goal_text="hold $n copper" ;;
  item:*) goal_text="own item $n (item_template entry)" ;;
  quality:*) goal_text="own any item of quality $n or better (3 = rare)" ;;
  score:*) goal_text="progress as much as you can in $mins minutes. You choose your own adventure: where to go, what to
fight, which quests to take, who to play with" ;;
  achpoints:*) goal_text="earn as many achievement points as you can" ;;
  quest:*) goal_text="complete the quest \"$(agent/bin/worlddb "SELECT LogTitle FROM quest_template WHERE ID = $n" | tail -1)\" (quest $n): accept it, do its objectives, and turn it in" ;;
esac
identity="Your character $char is already logged in and waiting."
if [ -n "$SPEC" ]; then
  line=$(awk -F'\t' -v k="$SPEC" '$1 == k' specs.tsv)
  [ -n "$line" ] || { echo "$name: unknown SPEC $SPEC (see specs.tsv)" >&2; exit 1; }
  spec_title=$(printf %s "$line" | cut -f2) role=$(printf %s "$line" | cut -f3) optimise=$(printf %s "$line" | cut -f4) stats=$(printf %s "$line" | cut -f5)
  race_name=$(echo "1 Human|2 Orc|3 Dwarf|4 Night Elf|5 Undead|6 Tauren|7 Gnome|8 Troll|10 Blood Elf|11 Draenei" | tr '|' '\n' | awk -v r="$race" '$1 == r { $1 = ""; sub(/^ /, ""); print }')
  identity="You are $char, a $race_name $spec_title. Your role is $role: you should optimise for $optimise.
Your key stats are $stats. Build your character for that: your talents (talents.list, talents.learn), the spells
you train and the gear you choose. $char is already logged in and waiting."
fi
score_text="You are scored on wall-clock time until an external judge sees the goal met; if time
runs out ($mins minutes), your progress toward it is the score."
stop_text="Do not stop until the goal is met: confirm it yourself with wow before finishing."
case "$goal" in achpoints:*)
  score_text="Your score is the total points of the achievements you earn during the next $mins minutes
(ones already earned don't count). There is no finish line: keep earning until time runs out. Achievement
names, points and criteria are in worlddb (see AGENTS.md); me.achievements() shows your progress."
  stop_text="Do not stop early: keep earning points until the time runs out." ;;
score:*)
  score_text="HOW YOU ARE JUDGED: every 15 seconds the judge reads your character from the server database. Your
progress is the XP you've earned since the start (kills, quests, exploring: the game already gives more for harder
content). Gear, money, talents and everything else aren't scored: they're how you earn XP faster.
YOUR SCORE is the average of two rates, in XP per hour: your rate over the whole $mins minutes, and
your best rate over any 15-minute stretch. Sustained progress counts, and so does finding a better way to play:
time spent trying a new strategy lowers your overall rate but not your best stretch. Deaths, stalls and time spent
stuck cost you through the time they lose. There is no finish line.
FACTION SCORE: every player on this server is on your faction (Alliance), and the faction's score is the sum of
all players' scores. It is reported alongside yours; your own score is still yours alone.
The standings command shows where every player in this run stands right now (rank, score, recent rate, level) and
the faction total. It also shows each player's role and their damage dealt, damage taken and healing done this
run: the measure of how each plays their role. That's not scored: it's for you to see how you and others play."
  stop_text="Do not stop early: keep improving your score until the time runs out." ;;
esac

bun ../server/account.ts "$acct" "$acct" # non-GM
docker exec ac-database mysql -uroot -ppassword -e "CREATE USER IF NOT EXISTS 'worldreader'@'%' IDENTIFIED BY 'worldreader';
  GRANT SELECT ON acore_world.* TO 'worldreader'@'%';" 2>/dev/null # for worlddb
sql() { docker exec ac-database mysql -uroot -ppassword -N acore_characters -e "$1" 2>/dev/null; }
if [ -n "$LEVEL$MONEY$START" ]; then
  # create the character with a short login, then edit it while offline
  for try in 1 2 3; do # concurrent logins can time out on the auth server
    ACCOUNT=$acct client/play.sh "$char" --race "$race" --class "$class" --duration 8s > "$ws/session.log" 2>&1 || true
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
  ACCOUNT=$acct RECORD="$record" EVENTS="$events" client/play.sh "$char" --race "$race" --class "$class" >> "$ws/session.log" 2>&1 &
  session=$!
  until agent/bin/wow 'return 1' 2>/dev/null | grep -q result || ! kill -0 $session 2>/dev/null; do sleep 1; done
  kill -0 $session 2>/dev/null && break
  echo "$name: session login failed (try $try), see $ws/session.log" >&2
  [ $try = 3 ] && exit 1
  sleep $((try * 5))
done
agent/bin/wow "LIB = '$ws/'; chat.join('${CHANNEL:-General - Elwynn Forest}', 1)" >/dev/null
# ponytail: one zone channel joined at start; no auto-rejoin on zone change (agents can join_channel)

[ -z "$GO" ] || { touch "$GO.$name.ready"; until [ -e "$GO" ]; do sleep 1; done; }
bun judge/judge.ts "$char" "$goal" --timeout "${mins}m" --samples "$dir/samples.jsonl" --record "$record" --board "$board" \
  --events "$events/$char.events.jsonl" ${role:+--role "$role"} > "$dir/judge.json" &
judge=$!

cat > "$dir/prompt.md" <<PROMPT
You are playing World of Warcraft (3.3.5a) on a local private server set up for AI benchmarking.
$identity

GOAL: $goal_text.${HINT:+ $HINT}

$score_text

Read AGENTS.md first. It covers playing the game well (gear, spells, combat, pulls), your session (wow), how to
play well with the SDK (bags, vendors,
trainers, death, other players), the runtime and the Lua API, and the world data tools: wowdb (where a quest's targets are,
what drops an item, the nearest trainer) and worlddb (read-only SQL over the game's world database).
Reference source (read-only): the client and its Lua bindings in $repo/azeroth-sdk (client/, session/,
script/), the server in $repo/server/azerothcore/src/server/game and $repo/server/azerothcore/src/server/shared.

HOW TO WORK
- Start earning progress within your first few minutes, then improve. Don't plan at length before you have
  a working baseline.
- Iterate in small steps: try one action with wow, check it worked, then build it into a script.
- Measure, don't guess: check what your script is achieving (wow 'return me.level(), me.xp()',
  quests.log(), me.money()) and change strategy when progress stalls (mobs too low level, frequent deaths,
  full bags, a target you can't reach).
- While your scripts run, watch them: wow wait (blocks until something important happens: chat, a death, a
  failed task, a script asking you a question), wow events (the log), wow tasks (what each is doing). Don't end
  your session.
- $stop_text

REVIEW YOUR PLAY: be critical of how you're playing, not just of whether your script runs. Brute force (the same
fight again and again, dying and running back, fighting with whatever you have) is the slowest way to progress.
- Step back regularly (every 10 minutes or so, and after every death) and look at the bigger picture: what is your
  XP rate (standings), is it rising, and how does it compare with the others? What limits you most right now:
  your gear, untrained spells, unspent talents, the mobs you pick, your tactics in a fight, travel, or deaths?
- Measure your play. me.status() grades your gear against your level (gear.score), lists the class spells you're
  missing (spellbook) and your unspent talents. Every fight ends with a fight_end event: how long it took, the
  damage you dealt and took, the abilities you used, how it ended (wow events --types fight_end). Long fights,
  fights that use one ability, and fights that end with you near death are telling you something.
- When something fails twice, stop and find the cause before you try again. After a death, read the summary of
  the fight you died in and what killed you; change what led to it, not just how you recover.
- Invest in your character: ten minutes spent upgrading a weapon, training spells or spending talents pays back
  for the rest of the run. AGENTS.md ("Playing World of Warcraft well") explains what matters and why.
- A group is a tool, not a fix. If you only survive in a group, your character is too weak for where you are:
  fix that. Group up when it lets you do what you couldn't alone.
- Keep NOTES.md honest: what worked, what didn't, why, and what you'd do differently next time.

YOUR LIBRARY: your working directory is yours and persists across runs. It may be empty (apart from
AGENTS.md, example.lua and NOTES.md) or hold scripts from your earlier runs: check with ls, reuse what's there, and
save anything reusable as you go. Read NOTES.md first and keep it up to date: what worked, what didn't, what
killed you, good spots and routes. It's how your next run starts ahead of this one.

OTHER PLAYERS: the other characters on this server are other AI agents like you, each playing their own run at the
same time. You can team up with them or compete with them.${ROSTER:+ This run:
$ROSTER
}You can talk to them: say (nearby), whispers, party chat, the zone's General channel, or a channel of your
own (chat.join(name)). Messages to you arrive as chat events: every wow reply shows new ones, and
wow wait --types chat blocks until the next. You
can invite them to groups, trade information, plan together or ignore them: how you deal with them is up to you.
AGENTS.md ("Other players") explains the game mechanics of playing together.

$( [ -n "$NOTICE" ] && cat "$NOTICE")

RULES: Progress through legitimate gameplay only. You have no GM access; don't try to modify the server,
the database, the judge or the harness. Your session log is session.log in your working directory.
PROMPT

# no auto-memory: it's keyed by the git root, so agents would share notes with each other (and with us). The env var
# turns it off, but Claude Code still lets sessions write the memory folder, so ~/.claude is denied outright.
(cd "$ws" && PATH="$root/agent/bin:$PATH" CLAUDE_CODE_DISABLE_AUTO_MEMORY=1 WOWBENCH_BOARD="$board" WOWBENCH_CHAR="$char" exec timeout "${mins}m" claude -p "$(cat "$root/$dir/prompt.md")" \
  --setting-sources project --strict-mcp-config --output-format stream-json --verbose ${MODEL:+--model "$MODEL"} \
  --add-dir "$repo/azeroth-sdk/client" "$repo/azeroth-sdk/session" "$repo/azeroth-sdk/script" "$repo/server/azerothcore/src/server/game" "$repo/server/azerothcore/src/server/shared" \
  --allowedTools "Bash(wow:*)" "Bash(worlddb:*)" "Bash(wowdb:*)" "Bash(sleep:*)" "Bash(tail:*)" "Bash(cat:*)" \
    "Bash(ls:*)" "Bash(grep:*)" "Bash(mkdir:*)" "Bash(standings:*)" "Bash(party:*)" "Edit(./**)" --disallowedTools "Edit(~/.claude/**)" "Read(~/.claude/**)" \
  > "$root/$dir/agent.jsonl" 2>&1) &
agent=$!

wait $judge || true
cat "$dir/judge.json"
[ -n "$GO" ] || bun ../server/recorder/replay.ts "$dir" >/dev/null 2>&1 || true # team.sh builds one replay for the whole team
exit
}
