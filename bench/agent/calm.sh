#!/bin/sh
# bench/agent/calm.sh: before a session on $WOW_PORT is killed (pause, restart, end of run): stop its scripts and
# wait up to 90 s for it to leave combat. A connection dropped in combat leaves the character in the world, where it
# can die with nobody logged in. Run it after the agent is stopped, so nothing starts a new fight.
wow=$(dirname "$0")/bin/wow
$wow 'for _, t in ipairs(task.list()) do task.stop(t.id) end; on_tick = nil; move.stop()' >/dev/null 2>&1 || exit 0
i=0
while [ $i -lt 45 ] && [ "$($wow 'return me.in_combat()' 2>/dev/null | jq -r .result)" = true ]; do sleep 2; i=$((i + 1)); done
