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
# a second start (the unit started again: by what, the journal in the log says) resumes the run it interrupted: the
# characters, workspaces and conversations are all still here; the clock didn't run while it was down. At most 3 times.
if [ -e /var/lib/leeroy/started ]; then
	run=$(ls -d var/runs/*/ 2>/dev/null | tail -1 | sed 's|/$||')
	n=$(cat /var/lib/leeroy/resumes 2>/dev/null || echo 0)
	if [ -z "$run" ] || [ "$n" -ge 3 ] || ! { just up && wait_ready; }; then
		log "started again (first at $(cat /var/lib/leeroy/started)), resumed $n times: finishing"
		exec bash /opt/leeroy/deploy/hetzner/server/finish.sh "done"
	fi
	echo $((n + 1)) >/var/lib/leeroy/resumes
	systemctl stop leeroy-keep.timer 2>/dev/null # a kept server (finish.sh): resumed, it's a run again
	log "started again (first at $(cat /var/lib/leeroy/started)): resuming $run ($((n + 1)) of 3)"
	printf '%s\n' "The run was interrupted (the benchmark's harness restarted, nothing you did) and is resuming: \$LEFT minutes are left," \
		"the time it was down didn't count. Your character is logging back in where it was. Carry on." >/var/lib/leeroy/resume.txt
	bin/azbench resume "$run" --message /var/lib/leeroy/resume.txt --no-build --monitor 10s
	log "azbench exited $?"
	exec bash /opt/leeroy/deploy/hetzner/server/finish.sh "done"
fi
mkdir -p /var/lib/leeroy && date -u +%FT%TZ >/var/lib/leeroy/started && echo "$task" >/var/lib/leeroy/task
# no unattended upgrades during a run: after one, needrestart restarts the services using old libraries, this unit
# among them (a stop and a start mid-run)
systemctl disable --now -q apt-daily.timer apt-daily-upgrade.timer unattended-upgrades.service 2>/dev/null
mkdir -p /etc/needrestart/conf.d && echo "\$nrconf{restart} = 'l';" >/etc/needrestart/conf.d/leeroy.conf
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
