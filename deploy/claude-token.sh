#!/usr/bin/env bash
# claude-token.sh (just claude-token): Claude Code's long-lived token for the run servers, into .env. Runs `claude
# setup-token` (you sign in with your Claude subscription in the browser; the token lasts about a year), takes the token
# from what it prints, writes CLAUDE_CODE_OAUTH_TOKEN in .env, and checks Claude Code takes it. The servers can't use
# this machine's login: on macOS it's in the Keychain, and it refreshes as it's used, so sharing it would sign this
# machine out.
set -euo pipefail
cd "$(dirname "$0")/.."
. deploy/lib.sh
command -v claude >/dev/null || die "no claude on the PATH (mise install)"
[ -f .env ] || die "no .env: cp .env.example .env first"

out=$(mktemp)
trap 'rm -f "$out"' EXIT
# recorded through a terminal (it draws a terminal UI), made wide so the token prints on one line
if [ "$(uname)" = Darwin ]; then
	script -q "$out" sh -c 'stty cols 400 2>/dev/null; claude setup-token'
else
	script -q -c 'stty cols 400 2>/dev/null; claude setup-token' "$out"
fi
token=$(sed 's/\x1b\[[0-9;?]*[A-Za-z]//g' "$out" | grep -aoE 'sk-ant-oat[0-9]+-[A-Za-z0-9_-]+' | tail -1 || true)
[ -n "$token" ] || die "no token in claude setup-token's output: copy it into .env as CLAUDE_CODE_OAUTH_TOKEN by hand"

# .env with the new token in place of any old one, readable by you alone
umask 077
grep -v '^CLAUDE_CODE_OAUTH_TOKEN=' .env >"$out.env" || true
echo "CLAUDE_CODE_OAUTH_TOKEN=$token" >>"$out.env"
mv "$out.env" .env
echo "CLAUDE_CODE_OAUTH_TOKEN written to .env (${#token} characters)"

# Claude Code with that token alone (no Keychain login: a fresh config directory), as a server will run it
check=$(mktemp -d)
reply=$(CLAUDE_CONFIG_DIR="$check" CLAUDE_CODE_OAUTH_TOKEN="$token" claude -p 'Reply with the single word: ok' --max-turns 1 2>&1 || true)
rm -r "${check:?}"
case "$reply" in
*ok*) echo "checked: Claude Code answers with it" ;;
*) die "Claude Code didn't take the token: $reply" ;;
esac
