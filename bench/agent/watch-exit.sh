#!/bin/sh
# bench/agent/watch-exit.sh <pid> <agent.jsonl> <board json> <label> <alerts file>: wait for an agent process to
# exit and, if its run still has 2+ minutes left, report it on stderr and in the alerts file with the runtime's last
# error (a Codex usage limit, say). Nothing else notices: the bench only waits on the judge.
pid=$1 log=$2 board=$3 label=$4 alerts=$5
while kill -0 "$pid" 2>/dev/null; do sleep 10; done
left=$(jq -r '.minutes_left // 0' "$board" 2>/dev/null)
[ "${left:-0}" -ge 2 ] || exit 0
why=$(jq -r 'select(.type == "turn.failed" or .type == "error" or (.type == "result" and .is_error)) |
  .error.message // .message // .result' "$log" 2>/dev/null | tail -1)
msg="$(date +%H:%M:%S) $label: agent exited with $left min left${why:+: $why}"
echo "$msg" >&2
echo "$msg" >> "$alerts"
