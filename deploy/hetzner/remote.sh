#!/usr/bin/env bash
# Remote runs on Hetzner Cloud, from this checkout (the justfile's remote-* recipes call this):
#
#   image                 build the server snapshot: the server images, this checkout's data/ and tools, ready to run
#   run <task> [args]     a server from the snapshot runs `azbench run suite/tasks/<task>.yaml [args]` on this checkout's
#                         code as it is now, uploads the run to R2 and deletes itself
#   ls                    the run servers
#   status [server]       its run's status (default: the newest run server)
#   ssh <server> [cmd]    a shell (or a command) on it
#   view [server] [port]  its live viewer, which it runs from the start, through an SSH tunnel, in your browser
#                         (default: the newest run server; the first free port from 8801)
#   stop <server>         end its run early: the run pauses, is uploaded, and the server goes
#   kill <server>         delete the server now, run and all
#   runs                  the runs in R2
#   fetch [run...]        download runs from R2 into var/runs (default: every one not here yet)
#
# Settings and keys are in .env (see .env.example). Servers only accept SSH (the leeroy firewall); the game server,
# MySQL and the viewer are reachable through SSH alone.
set -euo pipefail
cd "$(dirname "$0")/../.."
. deploy/lib.sh

: "${LEEROY_LOCATION:=hil}"        # Hillsboro, Oregon (US West)
: "${LEEROY_TYPE:=ccx33}"          # run servers
: "${LEEROY_IMAGE_TYPE:=ccx23}"    # the snapshot's builder: its disk (160 GB) is the smallest a run server can have
: "${LEEROY_MAX_HOURS:=26}"        # a run server deletes itself after this long, whatever state it's in
: "${LEEROY_SSH_KEY:=$HOME/.ssh/id_ed25519}"
: "${R2_RUNS_BUCKET:=leeroy-runs}"
KNOWN=var/remote/known_hosts
mkdir -p var/remote
SSH=(ssh -i "$LEEROY_SSH_KEY" -o UserKnownHostsFile="$KNOWN" -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 -o ServerAliveInterval=30)

ip() { hcloud server ip "$1"; }
wait_ssh() { # until the server takes SSH logins
	local addr=$1
	ssh-keygen -R "$addr" -f "$KNOWN" >/dev/null 2>&1 || true # an address an old server had
	for _ in $(seq 1 60); do
		"${SSH[@]}" "root@$addr" true 2>/dev/null && return 0
		sleep 5
	done
	die "no SSH on $addr after 5 minutes"
}
setup_project() { # the SSH key and the firewall every server gets, once per project
	need HCLOUD_TOKEN
	[ -f "$LEEROY_SSH_KEY.pub" ] || die "no $LEEROY_SSH_KEY.pub (LEEROY_SSH_KEY)"
	hcloud ssh-key describe leeroy >/dev/null 2>&1 || hcloud ssh-key create --name leeroy --public-key-from-file "$LEEROY_SSH_KEY.pub" >/dev/null
	if ! hcloud firewall describe leeroy >/dev/null 2>&1; then
		hcloud firewall create --name leeroy >/dev/null
		hcloud firewall add-rule leeroy --direction in --protocol tcp --port 22 --source-ips 0.0.0.0/0 --source-ips ::/0 \
			--description ssh >/dev/null
	fi
}
snapshot() { # the newest leeroy snapshot's id
	hcloud image list --type snapshot --selector leeroy=image -o noheader -o columns=id,created --sort created:desc | head -1 | awk '{print $1}'
}
newest_server() { # the run server made last
	hcloud server list --selector leeroy=run -o noheader -o columns=name --sort created:desc | head -1
}
newest_run() { echo 'ls -d /opt/leeroy/var/runs/*/ 2>/dev/null | tail -1 | sed "s|/$||"'; }
# code to the server: this checkout as it is (git history included, for the SDK revision), not its data, output or server
sync_code() {
	rsync -az --delete -e "${SSH[*]}" --exclude /var/ --exclude /data/ --exclude /bin/ --exclude /.jj/ --exclude /.env \
		--exclude /server/azerothcore/ --exclude node_modules/ --exclude /site/dist/ --exclude /.playwright-mcp/ --exclude .DS_Store ./ "root@$1:/opt/leeroy/"
}

cmd=${1:-}; shift || true
case "$cmd" in
image)
	setup_project
	name="leeroy-image-$(date +%Y%m%d-%H%M)"
	echo "creating $name ($LEEROY_IMAGE_TYPE, $LEEROY_LOCATION)"
	hcloud server create --name "$name" --type "$LEEROY_IMAGE_TYPE" --location "$LEEROY_LOCATION" --image ubuntu-24.04 \
		--ssh-key leeroy --firewall leeroy --label leeroy=image >/dev/null
	addr=$(ip "$name")
	trap 'echo "remote: the builder $name is still up: just remote-kill $name" >&2' ERR
	wait_ssh "$addr"
	echo "copying the checkout and the server source"
	sync_code "$addr"
	rsync -az -e "${SSH[*]}" --exclude build/ server/azerothcore "root@$addr:/opt/leeroy/server/"
	echo "copying data/ ($(du -sh --exclude mmaps-server60 data 2>/dev/null | cut -f1 || du -sh data | cut -f1))"
	rsync -az -e "${SSH[*]}" --exclude /mmaps-server60/ data/ "root@$addr:/opt/leeroy/data/"
	"${SSH[@]}" "root@$addr" bash /opt/leeroy/deploy/hetzner/server/bootstrap.sh
	echo "snapshotting"
	hcloud server shutdown "$name" >/dev/null
	hcloud server create-image --type snapshot --label leeroy=image --description "$name" "$name" >/dev/null
	hcloud server delete "$name" >/dev/null
	trap - ERR
	# keep the newest two
	hcloud image list --type snapshot --selector leeroy=image -o noheader -o columns=id --sort created:desc | tail -n +3 |
		while read -r id; do hcloud image delete "$id" >/dev/null; done
	echo "snapshot $(snapshot) ready"
	;;
run)
	task=${1:?usage: run <task> [azbench run args]}; shift
	[ -f "suite/tasks/$task.yaml" ] || die "no suite/tasks/$task.yaml"
	# the keys its agents' runtimes need, before paying for a server (from the task's plan)
	plan=$(go run ./cmd/azbench run "suite/tasks/$task.yaml" --dry-run --no-build "$@" 2>&1) || die "the task's plan failed: $plan"
	if grep -qw claude <<<"$plan" && [ -z "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]; then
		die "this task runs Claude Code: no CLAUDE_CODE_OAUTH_TOKEN in .env (just claude-token)"
	fi
	if grep -qw codex <<<"$plan" && [ ! -f "$HOME/.codex/auth.json" ]; then
		die "this task runs Codex: no ~/.codex/auth.json (codex login)"
	fi
	setup_project
	need R2_ACCOUNT_ID; need R2_ACCESS_KEY_ID; need R2_SECRET_ACCESS_KEY
	img=$(snapshot); [ -n "$img" ] || die "no snapshot yet: just remote-image"
	name="leeroy-$task-$(date +%m%d-%H%M%S)"
	price=$(hcloud server-type describe "$LEEROY_TYPE" -o json |
		jq -r --arg l "$LEEROY_LOCATION" '.prices[] | select(.location == $l) | .price_hourly.gross | tonumber')
	echo "creating $name ($LEEROY_TYPE in $LEEROY_LOCATION, €$price an hour) from snapshot $img"
	hcloud server create --name "$name" --type "$LEEROY_TYPE" --location "$LEEROY_LOCATION" --image "$img" \
		--ssh-key leeroy --firewall leeroy --label leeroy=run --label task="$task" >/dev/null
	addr=$(ip "$name")
	wait_ssh "$addr"
	echo "copying the checkout and the keys"
	sync_code "$addr"
	# the agents' keys, readable by root alone; the ops keys (R2, Hetzner) stay out of the agents' environment
	{
		echo "CLAUDE_CODE_OAUTH_TOKEN=${CLAUDE_CODE_OAUTH_TOKEN:-}"
		echo "OPENROUTER_API_KEY=${OPENROUTER_API_KEY:-}"
		echo "OPENAI_API_KEY=${OPENAI_API_KEY:-}"
		echo "LEEROY_HOST_PROVIDER=hetzner"
		echo "LEEROY_HOST_TYPE=$LEEROY_TYPE"
		echo "LEEROY_HOST_LOCATION=$LEEROY_LOCATION"
		echo "LEEROY_HOST_PRICE_HOUR=$price"
	} | "${SSH[@]}" "root@$addr" 'umask 077; mkdir -p /etc/leeroy && cat > /etc/leeroy/agents.env'
	{
		echo "HCLOUD_TOKEN=$HCLOUD_TOKEN"
		echo "R2_ACCOUNT_ID=$R2_ACCOUNT_ID"
		echo "R2_ACCESS_KEY_ID=$R2_ACCESS_KEY_ID"
		echo "R2_SECRET_ACCESS_KEY=$R2_SECRET_ACCESS_KEY"
		echo "R2_RUNS_BUCKET=$R2_RUNS_BUCKET"
	} | "${SSH[@]}" "root@$addr" 'umask 077; cat > /etc/leeroy/ops.env'
	for f in auth.json config.toml; do # Codex's login and settings
		[ -f "$HOME/.codex/$f" ] && "${SSH[@]}" "root@$addr" "umask 077; mkdir -p /root/.codex; cat > /root/.codex/$f" < "$HOME/.codex/$f"
	done
	args=$(printf '%q ' "$@")
	"${SSH[@]}" "root@$addr" "systemd-run --unit leeroy-run --description 'leeroy $task' --property=KillMode=mixed \
		bash /opt/leeroy/deploy/hetzner/server/run.sh $task $args >/dev/null &&
		systemd-run --unit leeroy-deadline --on-active=${LEEROY_MAX_HOURS}h bash /opt/leeroy/deploy/hetzner/server/finish.sh deadline >/dev/null"
	echo "started: just remote-status $name · just remote-view $name · deletes itself after the run (at most ${LEEROY_MAX_HOURS} h)"
	;;
ls)
	hcloud server list --selector leeroy -o columns=name,status,server_type,datacenter,ipv4,created,labels
	;;
status)
	server=${1:-$(newest_server)}
	[ -n "$server" ] || die "no run servers (just remote-ls)"
	"${SSH[@]}" "root@$(ip "$server")" "systemctl is-active leeroy-run; tail -n 8 /var/log/leeroy-run.log; \
		cd /opt/leeroy && r=\$($(newest_run)) && [ -n \"\$r\" ] && /root/.local/bin/mise exec -- bin/azbench status \"\$r\" || true"
	;;
ssh)
	server=${1:?usage: ssh <server> [command]}; shift
	exec "${SSH[@]}" -t "root@$(ip "$server")" "$@"
	;;
view)
	# the run server's live viewer, which it runs from the start (127.0.0.1:8800 there), through an SSH tunnel; the
	# newest run server unless one is named, the first free local port from 8801, the browser opened on it
	server=${1:-$(newest_server)}
	[ -n "$server" ] || die "no run servers (just remote-ls)"
	port=${2:-8801}
	while lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; do port=$((port + 1)); done
	addr=$(ip "$server")
	echo "$server's viewer on http://localhost:$port/ (ctrl-c closes the tunnel)"
	opener=xdg-open
	command -v open >/dev/null && opener=open
	(sleep 2 && "$opener" "http://localhost:$port/" >/dev/null 2>&1) &
	exec "${SSH[@]}" -N -L "$port:127.0.0.1:8800" "root@$addr"
	;;
stop)
	server=${1:?usage: stop <server>}
	"${SSH[@]}" "root@$(ip "$server")" "cd /opt/leeroy && /root/.local/bin/mise exec -- bin/azbench pause \$($(newest_run))"
	echo "pausing: the run is uploaded and $server deletes itself when it has stopped"
	;;
kill)
	server=${1:?usage: kill <server>}
	hcloud server delete "$server"
	;;
runs)
	r2 lsf "r2:$R2_RUNS_BUCKET/runs/" | sed 's/\.tar\.zst$//'
	;;
fetch)
	# shellcheck disable=SC2046 # run names have no spaces
	[ $# -gt 0 ] || set -- $(r2 lsf "r2:$R2_RUNS_BUCKET/runs/" | sed 's/\.tar\.zst$//')
	mkdir -p var/runs
	for run in "$@"; do
		[ -d "var/runs/$run" ] && { echo "$run: here already"; continue; }
		echo "$run"
		r2 cat "r2:$R2_RUNS_BUCKET/runs/$run.tar.zst" | zstd -dc | tar -x -C var/runs
	done
	;;
*)
	sed -n '2,17p' "$0" | cut -c3-
	exit 2
	;;
esac
