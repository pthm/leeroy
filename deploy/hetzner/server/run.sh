#!/usr/bin/env bash
# One remote run (remote.sh run starts it as the leeroy-run unit): the server up, the checkout built, the task run with
# the machine monitored, then finish.sh uploads it and deletes the server.
set -uo pipefail
task=$1
shift
. /opt/leeroy/deploy/hetzner/server/lib.sh
set -a
. /etc/leeroy/agents.env # the agents' keys and LEEROY_HOST_*; the ops keys stay out of their environment
set +a
log "run $task $*"
if just up && wait_ready && just build; then
	bin/azbench run "suite/tasks/$task.yaml" --no-build --monitor 10s "$@" >>/var/log/leeroy-run.log 2>&1
	log "azbench exited $?"
else
	log "the server or the build failed"
fi
exec bash /opt/leeroy/deploy/hetzner/server/finish.sh "done"
