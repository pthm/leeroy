# Shared by the server-side scripts (sourced): mise's tools on the PATH, the server's readiness, R2 through rclone.
export HOME=${HOME:-/root} # systemd units start without it: git's and mise's settings are found through it
export PATH="/root/.local/share/mise/shims:/root/.local/bin:$PATH"
cd /opt/leeroy || exit 1
# the checkout arrives owned by the laptop's user, the scripts run as root: git (and Go's VCS stamping) refuse that
git config --global --get-all safe.directory | grep -qx /opt/leeroy || git config --global --add safe.directory /opt/leeroy

log() { echo "$(date -u +%FT%TZ) $*" | tee -a /var/log/leeroy-run.log; }

# wait_ready: until the worldserver is up (its first boot downloads the client data and imports the databases)
wait_ready() {
	for _ in $(seq 1 360); do
		docker logs ac-worldserver 2>&1 | grep -q 'ready\.\.\.' && return 0
		sleep 10
	done
	log "the worldserver isn't ready after an hour"
	return 1
}

r2() {
	RCLONE_CONFIG=/dev/null RCLONE_CONFIG_R2_TYPE=s3 RCLONE_CONFIG_R2_PROVIDER=Cloudflare RCLONE_CONFIG_R2_ACCESS_KEY_ID="$R2_ACCESS_KEY_ID" \
		RCLONE_CONFIG_R2_SECRET_ACCESS_KEY="$R2_SECRET_ACCESS_KEY" RCLONE_CONFIG_R2_ENDPOINT="https://$R2_ACCOUNT_ID.r2.cloudflarestorage.com" \
		RCLONE_CONFIG_R2_NO_CHECK_BUCKET=true rclone "$@"
}
