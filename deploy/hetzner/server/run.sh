#!/usr/bin/env bash
# One remote run (remote.sh run starts it as the leeroy-run unit): the server up, the checkout built, the task run with
# the machine monitored, then finish.sh uploads it and deletes the server.
set -uo pipefail
task=$1 # its file, suite/tasks/<task>.yaml or suite/checks/
shift
. /opt/leeroy/deploy/hetzner/server/lib.sh
exec >>/var/log/leeroy-run.log 2>&1 # every step's output, in the log that's uploaded with the run
set -a
. /etc/leeroy/agents.env # the agents' keys and LEEROY_HOST_*; the ops keys stay out of their environment
set +a
log "run $task $*"
if just up && wait_ready && just build; then
	# the live viewer beside it, for just remote-view; it outlives a failed start of its own (restarts)
	systemd-run --unit leeroy-view --property=Restart=on-failure --property=RestartSec=10 \
		bash /opt/leeroy/deploy/hetzner/server/view.sh >/dev/null
	bin/azbench run "$task" --no-build --monitor 10s "$@"
	log "azbench exited $?"
else
	log "the server or the build failed"
fi
exec bash /opt/leeroy/deploy/hetzner/server/finish.sh "done"
