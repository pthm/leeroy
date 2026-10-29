#!/bin/sh
# The same ten agents on their characters (levels 8-11, gear and libraries kept, fresh conversations), each with a
# spec and a role (specs.tsv): two groups' worth (two tanks, two healers, six DPS). Talents reset, everyone revived,
# healed, repaired and moved to Stormwind's Trade District. 60 minutes, judge v6 (XP per hour) plus each player's
# damage dealt / taken / healing done on the board (reported, not scored).
cd "$(dirname "$0")/.." || exit 1
chars="'Alaric','Brannoc','Corwin','Delphine','Emrys','Faelan','Gisela','Halden','Iseult','Joren'"
sql() { docker exec ac-database mysql -uroot -ppassword -N acore_characters -e "$1" 2>/dev/null; }
# a clean start while they're offline: alive (health > 0, else the login makes a ghost), no ghost flag, aura or corpse,
# every item at full durability, and no talents (they pick their spec's)
sql "DELETE FROM corpse WHERE guid IN (SELECT guid FROM characters WHERE name IN ($chars));
  DELETE FROM character_aura WHERE spell = 8326 AND guid IN (SELECT guid FROM characters WHERE name IN ($chars));
  DELETE FROM character_talent WHERE guid IN (SELECT guid FROM characters WHERE name IN ($chars));
  UPDATE characters SET playerFlags = playerFlags & ~16, health = 100000 WHERE name IN ($chars);
  UPDATE item_instance ii JOIN acore_world.item_template it ON it.entry = ii.itemEntry SET ii.durability = it.MaxDurability
    WHERE it.MaxDurability > 0 AND ii.owner_guid IN (SELECT guid FROM characters WHERE name IN ($chars))"
START="-8833.38 628.63 94.01 0 1519" CHANNEL="General - Stormwind City" NOTICE="$PWD/scenarios/stormwind-roles.notice.md" \
exec ./team.sh score:100000 "${MINUTES:-60}" \
  a01:Alaric:1:1:fury a02:Brannoc:1:1:protwarrior a03:Corwin:1:2:ret a04:Delphine:1:2:protpaladin \
  a05:Emrys:1:4:combat a06:Faelan:1:4:assassination a07:Gisela:1:5:holy a08:Halden:1:5:discipline \
  a09:Iseult:1:8:fire a10:Joren:1:8:frost
