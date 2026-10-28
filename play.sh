#!/bin/sh
# ./play.sh <character> [startup.lua] [azghost flags, e.g. --race 2 --class 1 --duration 2h]
# Starts a persistent session (runs until killed or --duration) with a control endpoint on
# localhost:$WOW_PORT (default 8090). Account: $ACCOUNT (default agent1, password = account name).
set -e
[ -n "$1" ] || { echo "usage: $0 <character> [startup.lua] [azghost flags...]"; exit 1; }
char="$1"; shift
script="agents/idle.lua"
case "$1" in *.lua) script="$1"; shift ;; esac
script="$(cd "$(dirname "$script")" && pwd)/$(basename "$script")"
acct="${ACCOUNT:-agent1}"
cd "$(dirname "$0")/ghost" # Lua dofile() paths are relative to here
AZGHOST_CONTROL="127.0.0.1:${WOW_PORT:-8090}" exec ./azghost --config ../azghost.yml cli \
  --username "$acct" --password "$acct" --char-name "$char" --bot-mode lua --lua-script "$script" "$@"
