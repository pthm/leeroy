#!/bin/sh
# bench/dev/gm.sh [x y z [map]]: a GM test character (Devgm, account devgm, gm level 3) on port 8112 with GM mode
# (mobs ignore you) and god mode, optionally teleported. For testing SDK changes without walking or dying; never use a
# GM account in a scored run. Query it with: WOW_PORT=8112 bench/agent/bin/wow '...'
cd "$(dirname "$0")/../.." || exit 1
./build.sh && bin/account devgm devgm 3 >/dev/null
bin=azeroth-sdk/azsdk-dev; [ -x "$bin" ] || bin=azeroth-sdk/azsdk
pkill -f -- "-char Devgm" 2>/dev/null; sleep 1
"$bin" -char Devgm -account devgm -race 1 -class 1 -listen 127.0.0.1:8112 -data "$PWD/data" -events /tmp/devgm-events > /tmp/devgm.log 2>&1 &
export WOW_PORT=8112
i=0; until bench/agent/bin/wow 'return 1' 2>/dev/null | grep -q result || [ $i -ge 60 ]; do sleep 2; i=$((i+2)); done
bench/agent/bin/wow 'chat.command(".gm on") chat.command(".cheat god on") return 1' >/dev/null
[ -n "$3" ] && bench/agent/bin/wow "chat.command('.go xyz $1 $2 $3 ${4:-0}') return 1" >/dev/null
echo "Devgm up on 8112 (log /tmp/devgm.log)"
