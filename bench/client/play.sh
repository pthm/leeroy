#!/bin/sh
# bench/client/play.sh <character> [startup.lua] [azsdk flags, e.g. --race 2 --class 1 --duration 8s]
# Runs one character in its own azsdk daemon (until killed or --duration), serving the agent API on
# 127.0.0.1:$WOW_PORT (default 8090). Account: $ACCOUNT (default agent1, password = account name).
set -e
[ -n "$1" ] || { echo "usage: $0 <character> [startup.lua] [azsdk flags...]"; exit 1; }
char="$1"; shift
repo=$(cd "$(dirname "$0")/../.." && pwd)
script=""
case "$1" in *.lua) script="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"; shift ;; esac
bin="$repo/azeroth-sdk/azsdk"
[ -x "$bin" ] || (cd "$repo" && go build -o "$bin" ./azeroth-sdk/cmd/azsdk)
acct="${ACCOUNT:-agent1}"
exec "$bin" -char "$char" -account "$acct" -listen "127.0.0.1:${WOW_PORT:-8090}" -data "$repo/data" \
  ${script:+-script "$script"} "$@"
