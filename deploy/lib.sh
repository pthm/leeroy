# Shared by the laptop-side deploy scripts (sourced from the repo root): .env's settings, and R2 through rclone.
[ -f .env ] && { set -a; . ./.env; set +a; }

die() { echo "${0##*/}: $*" >&2; exit 1; }
need() { [ -n "${!1:-}" ] || die "$1 isn't set (.env, see .env.example)"; }

# r2: rclone against R2 (the remote is named r2), configured from the environment
r2() {
	need R2_ACCOUNT_ID; need R2_ACCESS_KEY_ID; need R2_SECRET_ACCESS_KEY
	RCLONE_CONFIG=/dev/null RCLONE_CONFIG_R2_TYPE=s3 RCLONE_CONFIG_R2_PROVIDER=Cloudflare RCLONE_CONFIG_R2_ACCESS_KEY_ID="$R2_ACCESS_KEY_ID" \
		RCLONE_CONFIG_R2_SECRET_ACCESS_KEY="$R2_SECRET_ACCESS_KEY" RCLONE_CONFIG_R2_ENDPOINT="https://$R2_ACCOUNT_ID.r2.cloudflarestorage.com" \
		RCLONE_CONFIG_R2_NO_CHECK_BUCKET=true rclone "$@"
}
