#!/bin/sh
# 10 human agents from level 1 in Northshire, 120 minutes, scored on progress (score = (progress + pace) / 2),
# with "Wanted: Hogger" as a soft target. Watch live: bun server/recorder/live.ts bench/runs/<team>/*.record.jsonl
cd "$(dirname "$0")/.." || exit 1
HINT="Suggested target: complete \"Wanted: Hogger\" (quest 176) in Elwynn Forest. It's a stretch from level 1, and \
everything on the way (levels, quests, gear) scores. Other players are in the area." \
exec ./team.sh score:100000 "${MINUTES:-120}" \
  a01:Alaric:1:1 a02:Branwen:1:1 a03:Corwin:1:2 a04:Delphine:1:2 a05:Emrys:1:4 \
  a06:Faelan:1:4 a07:Gisela:1:5 a08:Halden:1:5 a09:Iseult:1:8 a10:Joren:1:8
