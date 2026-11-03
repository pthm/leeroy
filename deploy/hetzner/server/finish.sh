#!/usr/bin/env bash
# The end of a remote run, once (a lock): the newest run uploaded to R2 (runs/<run>.tar.zst) with the server's log, then
# the server deleted. `finish.sh deadline` (the leeroy-deadline timer) pauses a run still going first; if the upload
# fails there, the server powers off instead of going, so the run isn't lost (just remote-ls shows it).
set -uo pipefail
why=${1:-done}
exec 9>/run/leeroy-finish.lock
flock -n 9 || exit 0
. /opt/leeroy/deploy/hetzner/server/lib.sh
exec >>/var/log/leeroy-run.log 2>&1

run=$(ls -d var/runs/*/ 2>/dev/null | tail -1 | sed 's|/$||')
if [ "$why" = deadline ] && systemctl is-active -q leeroy-run; then
	log "the deadline: pausing ${run:-the run}"
	[ -n "$run" ] && bin/azbench pause "$run"
	for _ in $(seq 1 90); do
		systemctl is-active -q leeroy-run || break
		sleep 10
	done
fi
set -a
. /etc/leeroy/ops.env
set +a
host=$(hostname)
ok=1
if [ -n "$run" ]; then
	name=$(basename "$run")
	mkdir -p "$run/server" && cp /var/log/leeroy-run.log "$run/server/remote.log"
	if tar -C var/runs -cf - "$name" | zstd -T0 -10 -q | r2 rcat "r2:$R2_RUNS_BUCKET/runs/$name.tar.zst"; then
		log "uploaded $name"
	else
		ok=0
		log "the upload of $name failed"
	fi
fi
r2 copyto /var/log/leeroy-run.log "r2:$R2_RUNS_BUCKET/logs/$host.log" || true
if [ $ok = 0 ]; then
	if [ "$why" = deadline ]; then
		log "powering off: the run is on its disk"
		poweroff
	fi
	exit 1 # the deadline tries again
fi
id=$(curl -fsS http://169.254.169.254/hetzner/v1/metadata/instance-id)
log "deleting this server ($host, $id)"
curl -fsS -X DELETE -H "Authorization: Bearer $HCLOUD_TOKEN" "https://api.hetzner.cloud/v1/servers/$id" >/dev/null
