#!/bin/sh
# Ten brand-new agents (empty workspaces, new level-1 Human characters in Northshire), split between two runtimes:
# five on Claude Code (claude-opus-5-5), five on Codex (its default model). Each runtime gets one of each class and
# the same role mix (a tank, a healer, three DPS) with mirrored specs. 120 minutes, judge v6 (XP per hour) plus
# role stats on the board; team.json ranks players and sums each runtime.
cd "$(dirname "$0")/.." || exit 1
for a in a11 a12 a13 a14 a15 a16 a17 a18 a19 a20; do
  [ -e "workspaces/$a" ] && { echo "workspaces/$a exists: this scenario is for fresh agents" >&2; exit 1; }
done
MODEL="${MODEL:-claude-opus-5-5}" \
exec ./team.sh score:100000 "${MINUTES:-120}" \
  a11:Harlan:1:1:arms:claude a12:Mirabel:1:2:protpaladin:claude a13:Tobin:1:4:combat:claude \
  a14:Elowen:1:5:holy:claude a15:Quillan:1:8:fire:claude \
  a16:Brandt:1:1:protwarrior:codex a17:Seraphine:1:2:ret:codex a18:Vexley:1:4:assassination:codex \
  a19:Liora:1:5:discipline:codex a20:Orrin:1:8:frost:codex
