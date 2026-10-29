#!/bin/sh
# The hogger-from-1 agents again, on their existing characters (levels 6-8, gear and libraries kept, fresh
# conversations), all moved to Goldshire, revived and repaired. 120 minutes, judge v6 (XP per hour).
cd "$(dirname "$0")/.." || exit 1
chars="'Alaric','Brannoc','Corwin','Delphine','Emrys','Faelan','Gisela','Halden','Iseult','Joren'"
sql() { docker exec ac-database mysql -uroot -ppassword -N acore_characters -e "$1" 2>/dev/null; }
# a clean start while they're offline: alive (health > 0, else the login makes a ghost), no ghost flag, aura or corpse,
# and every item at full durability (deaths in the last run left gear broken)
sql "DELETE FROM corpse WHERE guid IN (SELECT guid FROM characters WHERE name IN ($chars));
  DELETE FROM character_aura WHERE spell = 8326 AND guid IN (SELECT guid FROM characters WHERE name IN ($chars));
  UPDATE characters SET playerFlags = playerFlags & ~16, health = 100000 WHERE name IN ($chars);
  UPDATE item_instance ii JOIN acore_world.item_template it ON it.entry = ii.itemEntry SET ii.durability = it.MaxDurability
    WHERE it.MaxDurability > 0 AND ii.owner_guid IN (SELECT guid FROM characters WHERE name IN ($chars))"
START="-9460 70 56.6 0 12" \
HINT="Suggested target: complete \"Wanted: Hogger\" (quest 176) in Elwynn Forest. Other players are in the area." \
exec ./team.sh score:100000 "${MINUTES:-120}" \
  a01:Alaric:1:1 a02:Brannoc:1:1 a03:Corwin:1:2 a04:Delphine:1:2 a05:Emrys:1:4 \
  a06:Faelan:1:4 a07:Gisela:1:5 a08:Halden:1:5 a09:Iseult:1:8 a10:Joren:1:8
