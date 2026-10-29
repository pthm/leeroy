#!/bin/sh
# bench/agent/codex-resume.sh <run dir> <workspace> <minutes> <message>: resume a Codex agent's conversation (the
# thread in its agent.jsonl) with a message, appending timestamped events to agent.jsonl. Used by resume.sh and
# restart-agent.sh; the caller sets PATH, WOW_PORT, WOWBENCH_BOARD, WOWBENCH_CHAR.
dir=$1 ws=$2 mins=$3 msg=$4
tid=$(jq -r 'select(.type == "thread.started") | .thread_id' "$dir/agent.jsonl" | head -1)
cd "$ws" && timeout "${mins}m" codex exec resume "$tid" --json --skip-git-repo-check -c 'sandbox_mode="workspace-write"' \
  -c sandbox_workspace_write.network_access=true -c 'approval_policy="never"' -c shell_environment_policy.inherit=all \
  "$msg" < /dev/null 2>&1 |
  jq --unbuffered -cR 'fromjson? // {type: "stderr", text: .} | . + {timestamp: (now | strftime("%Y-%m-%dT%H:%M:%SZ"))}' >> "$dir/agent.jsonl"
