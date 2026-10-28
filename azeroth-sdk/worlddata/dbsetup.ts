// bun azeroth-sdk/worlddata/dbsetup.ts
// Builds the `wowbench` schema that worlddb and wowdb read: client DBC data the world DB lacks, plus
// precomputed joins (aowow-style). Needs data/dbc/*.dbc and data/maps (see README). Tables:
//  achievement, achievement_criteria, achievement_category, criteria_type: the achievement catalogue
//  area (AreaTable), map_overlay (explore_area criteria asset -> area ids), area_location (where each area
//    is) and area_cell (area of every ~33yd cell), from the 16x16 area grid in each data/maps/MMMXXYY.map
//  creature_spawn, gameobject_spawn: every spawn with its area and zone (the world DB leaves them 0)
//  faction (names), faction_template + reaction: whether a creature faction is hostile/neutral/friendly
//    to Alliance and Horde players
//  spell: names, ranks, descriptions, cast time, range, cooldowns, cost, item created
import { readFileSync, readdirSync } from "node:fs";

const REPO = new URL("../../", import.meta.url).pathname;
import { execFileSync } from "node:child_process";

function dbc(file: string) {
  const b = readFileSync(`${REPO}data/dbc/${file}`);
  if (b.toString("latin1", 0, 4) !== "WDBC") throw new Error(`${file}: not a DBC`);
  const [n, fields, size] = [b.readUInt32LE(4), b.readUInt32LE(8), b.readUInt32LE(12)];
  const strs = 20 + n * size;
  const rows: { u: (i: number) => number; i: (i: number) => number; s: (i: number) => string; buf: Buffer; o: number }[] = [];
  for (let r = 0; r < n; r++) {
    const o = 20 + r * size;
    const u = (i: number) => b.readUInt32LE(o + i * 4);
    rows.push({
      u, i: (i: number) => b.readInt32LE(o + i * 4), buf: b, o,
      s: (i: number) => { const p = strs + u(i); return b.toString("utf8", p, b.indexOf(0, p)); },
    });
  }
  if (fields * 4 !== size) throw new Error(`${file}: unexpected record size`);
  return rows;
}

const q = (s: string) => `'${s.replace(/\\/g, "\\\\").replace(/'/g, "\\'")}'`;
const chunks = <T,>(a: T[], n = 5000) => Array.from({ length: Math.ceil(a.length / n) }, (_, i) => a.slice(i * n, (i + 1) * n));
const values = (rows: (string | number)[][]) =>
  rows.map((r) => `(${r.map((v) => (typeof v === "number" ? v : q(v))).join(",")})`).join(",\n");

// Field indices per AzerothCore DBCStructure.h / DBCfmt.h (enUS strings).
const ach = dbc("Achievement.dbc").map((r) =>
  [r.u(0), r.i(1), r.i(2), r.u(3), r.s(4), r.s(21), r.u(38), r.u(39), r.u(41), r.u(60), r.u(61)]);
const crit = dbc("Achievement_Criteria.dbc").map((r) =>
  [r.u(0), r.u(1), r.u(2), r.u(3), r.u(4), r.u(5), r.u(6), r.u(7), r.u(8), r.s(9), r.u(26), r.u(29)]);
const cat = dbc("Achievement_Category.dbc").map((r) => [r.u(0), r.i(1), r.s(2)]);
const area = dbc("AreaTable.dbc").map((r) => [r.u(0), r.u(1), r.u(2), r.i(10), r.s(11)]);
const overlay = dbc("WorldMapOverlay.dbc").map((r) => [r.u(0), r.u(2), r.u(3), r.u(4), r.u(5)]);
const factions = dbc("Faction.dbc");
const factionName = factions.map((r) => [r.u(0), r.s(23)]);
// A new character's standing with a reputation faction (FactionEntry base rep for its race), as a reaction:
// ReputationMgr ranks, hostile below -3000. ponytail: class masks and at-war flags ignored
const repFaction = new Map(factions.filter((r) => r.i(1) >= 0).map((r) => [r.u(0), r]));
const repReaction = (f: (typeof factions)[0], raceBit: number) => {
  const i = [0, 1, 2, 3].find((k) => f.u(2 + k) & raceBit);
  const v = i === undefined ? 0 : f.i(10 + i);
  return v < -3000 ? "hostile" : v < 0 ? "unfriendly" : v < 3000 ? "neutral" : "friendly";
};
const ft = dbc("FactionTemplate.dbc").map((r) => ({ id: r.u(0), faction: r.u(1), our: r.u(3), friend: r.u(4), hostile: r.u(5),
  enemies: [6, 7, 8, 9].map(r.u), friends: [10, 11, 12, 13].map(r.u) }));
// FactionTemplateEntry::IsHostileTo / IsFriendlyTo against the player templates (1 Human, 2 Orc)
const ftById = new Map(ft.map((t) => [t.id, t]));
const reactionTo = (c: (typeof ft)[0], p: (typeof ft)[0]) => {
  if (c.id === p.id) return "friendly";
  if (c.enemies.includes(p.faction)) return "hostile";
  if (c.friends.includes(p.faction)) return "friendly";
  if (c.hostile & p.our) return "hostile";
  if ((c.friend & p.our) || (c.our & p.friend)) return "friendly";
  return "neutral";
};
// Unit::GetFactionReactionTo: reputation decides for reputation factions, templates otherwise
const reaction = ft.map((t) => {
  const f = repFaction.get(t.faction);
  return [t.id, t.faction, f ? repReaction(f, 1) : reactionTo(t, ftById.get(1)!), f ? repReaction(f, 2) : reactionTo(t, ftById.get(2)!)];
});
const castTime = new Map(dbc("SpellCastTimes.dbc").map((r) => [r.u(0), r.i(1)]));
const range = new Map(dbc("SpellRange.dbc").map((r) => [r.u(0), r.buf.readFloatLE(r.o + 4 * 3)])); // max hostile range
const spell = dbc("Spell.dbc").map((r) => {
  const created = [0, 1, 2].find((e) => r.u(71 + e) === 24); // SPELL_EFFECT_CREATE_ITEM
  return [r.u(0), r.s(136), r.s(153), r.s(170).slice(0, 1000), r.u(39), castTime.get(r.u(28)) ?? 0,
    Math.round(range.get(r.u(46)) ?? 0), r.u(29), r.u(30), r.i(41), r.u(42), created === undefined ? 0 : r.u(107 + created)];
});

// Cell (lx, ly) of grid file (gx, gy) is centred at 533.33 * (32 - g - (l + 0.5) / 16), the inverse of
// GridTerrainData::getArea. ponytail: maps 0 and 1 only (Eastern Kingdoms, Kalimdor).
const S = 1600 / 3;
const cells = new Map<string, { map: number; id: number; pts: [number, number][] }>();
const grid: number[][] = []; // map, ix, iy, area: ix = floor(16 * (32 - x / S)), as GridTerrainData::getArea
for (const f of readdirSync(`${REPO}data/maps`)) {
  const m = f.match(/^(\d{3})(\d{2})(\d{2})\.map$/);
  if (!m || +m[1] > 1) continue;
  const b = readFileSync(`${REPO}data/maps/${f}`);
  const off = b.readUInt32LE(12), flags = b.readUInt16LE(off + 4), whole = b.readUInt16LE(off + 6);
  for (let lx = 0; lx < 16; lx++) for (let ly = 0; ly < 16; ly++) {
    const id = flags & 1 ? whole : b.readUInt16LE(off + 8 + (lx * 16 + ly) * 2);
    if (!id) continue;
    grid.push([+m[1], +m[2] * 16 + lx, +m[3] * 16 + ly, id]);
    const k = `${m[1]}:${id}`;
    if (!cells.has(k)) cells.set(k, { map: +m[1], id, pts: [] });
    cells.get(k)!.pts.push([S * (32 - +m[2] - (lx + 0.5) / 16), S * (32 - +m[3] - (ly + 0.5) / 16)]);
  }
}
const loc = [...cells.values()].map(({ map, id, pts }) => {
  const cx = pts.reduce((a, p) => a + p[0], 0) / pts.length, cy = pts.reduce((a, p) => a + p[1], 0) / pts.length;
  // a cell inside the area nearest its centroid (the centroid itself can fall outside, e.g. a ring)
  const [x, y] = pts.reduce((best, p) => (Math.hypot(p[0] - cx, p[1] - cy) < Math.hypot(best[0] - cx, best[1] - cy) ? p : best));
  const xs = pts.map((p) => p[0]), ys = pts.map((p) => p[1]);
  return [id, map, Math.round(x), Math.round(y), Math.round(Math.min(...xs)), Math.round(Math.max(...xs)),
    Math.round(Math.min(...ys)), Math.round(Math.max(...ys)), pts.length];
});

const enumSrc = readFileSync(`${REPO}server/azerothcore/src/server/shared/DataStores/DBCEnums.h`, "utf8");
const body = enumSrc.slice(enumSrc.indexOf("enum AchievementCriteriaTypes"));
const types = [...body.slice(0, body.indexOf("};")).matchAll(/ACHIEVEMENT_CRITERIA_TYPE_(\w+)\s*=\s*(\d+)/g)]
  .map((m) => [+m[2], m[1].toLowerCase()]);

const sql = `
DROP DATABASE IF EXISTS wowbench; CREATE DATABASE wowbench; USE wowbench;
CREATE TABLE achievement (id INT PRIMARY KEY, faction INT COMMENT '-1 any, 0 horde, 1 alliance',
  map INT COMMENT '-1 any', parent INT, name TEXT, description TEXT, category INT, points INT,
  flags INT COMMENT '0x1 = statistic counter, not earnable', count INT, ref_achievement INT);
CREATE TABLE achievement_criteria (id INT PRIMARY KEY, achievement INT, type INT COMMENT 'see criteria_type',
  asset INT COMMENT 'creature/quest/area/spell/item... id, by type', quantity INT,
  extra1_type INT, extra1_value INT, extra2_type INT, extra2_value INT, description TEXT,
  completion_flags INT, time_limit INT, KEY (achievement));
CREATE TABLE achievement_category (id INT PRIMARY KEY, parent INT, name TEXT);
CREATE TABLE criteria_type (id INT PRIMARY KEY, name TEXT);
CREATE TABLE area (id INT PRIMARY KEY, map INT, zone INT COMMENT 'parent zone area id, 0 = this is a zone',
  level INT, name TEXT);
CREATE TABLE map_overlay (id INT PRIMARY KEY COMMENT '= asset of explore_area criteria', area1 INT, area2 INT,
  area3 INT, area4 INT);
CREATE TABLE area_location (area INT, map INT, x INT COMMENT 'a point inside the area near its middle', y INT,
  min_x INT, max_x INT, min_y INT, max_y INT, cells INT COMMENT '~33x33yd cells', PRIMARY KEY (area, map));
INSERT INTO achievement VALUES ${values(ach)};
INSERT INTO achievement_criteria VALUES ${values(crit)};
INSERT INTO achievement_category VALUES ${values(cat)};
INSERT INTO criteria_type VALUES ${values(types)};
INSERT INTO area VALUES ${values(area)};
INSERT INTO map_overlay VALUES ${values(overlay)};
INSERT INTO area_location VALUES ${values(loc)};
CREATE TABLE area_cell (map INT, ix INT, iy INT, area INT, PRIMARY KEY (map, ix, iy));
${chunks(grid).map((c) => `INSERT INTO area_cell VALUES ${values(c)};`).join("\n")}
CREATE TABLE faction (id INT PRIMARY KEY, name TEXT);
INSERT INTO faction VALUES ${values(factionName)};
CREATE TABLE reaction (faction_template INT PRIMARY KEY COMMENT '= creature_template.faction', faction INT,
  to_alliance VARCHAR(10) COMMENT 'to a new Human character: hostile (attacks on sight), unfriendly, neutral, friendly',
  to_horde VARCHAR(10) COMMENT 'to a new Orc character');
INSERT INTO reaction VALUES ${values(reaction)};
CREATE TABLE spell (id INT PRIMARY KEY, name VARCHAR(255), \`rank\` TEXT, description TEXT, level INT, cast_ms INT,
  range_yd INT, cooldown_ms INT, category_cooldown_ms INT, power_type INT, cost INT,
  create_item INT COMMENT 'item made by this spell (crafting), 0 = none', KEY (name), KEY (create_item));
${chunks(spell).map((c) => `INSERT INTO spell VALUES ${values(c)};`).join("\n")}
-- every spawn with its area and zone; the cell maths matches GridTerrainData::getArea
CREATE TABLE creature_spawn (guid INT PRIMARY KEY, id INT, map INT, x FLOAT, y FLOAT, z FLOAT, area INT, zone INT,
  KEY (id), KEY (map, x, y), KEY (zone)) AS
  SELECT c.guid, c.id, c.map, c.position_x x, c.position_y y, c.position_z z, COALESCE(g.area, 0) area,
    COALESCE(IF(a.zone = 0, a.id, a.zone), 0) zone
  FROM acore_world.creature c
  LEFT JOIN area_cell g ON g.map = c.map AND g.ix = FLOOR(16 * (32 - c.position_x / ${S})) AND g.iy = FLOOR(16 * (32 - c.position_y / ${S}))
  LEFT JOIN area a ON a.id = g.area;
CREATE TABLE gameobject_spawn (guid INT PRIMARY KEY, id INT, map INT, x FLOAT, y FLOAT, z FLOAT, area INT, zone INT,
  KEY (id), KEY (map, x, y), KEY (zone)) AS
  SELECT o.guid, o.id, o.map, o.position_x x, o.position_y y, o.position_z z, COALESCE(g.area, 0) area,
    COALESCE(IF(a.zone = 0, a.id, a.zone), 0) zone
  FROM acore_world.gameobject o
  LEFT JOIN area_cell g ON g.map = o.map AND g.ix = FLOOR(16 * (32 - o.position_x / ${S})) AND g.iy = FLOOR(16 * (32 - o.position_y / ${S}))
  LEFT JOIN area a ON a.id = g.area;
CREATE USER IF NOT EXISTS 'worldreader'@'%' IDENTIFIED BY 'worldreader';
GRANT SELECT ON wowbench.* TO 'worldreader'@'%';
`;
execFileSync("docker", ["exec", "-i", "ac-database", "mysql", "-uroot", "-ppassword"], { input: sql, stdio: ["pipe", "inherit", "inherit"], maxBuffer: 1 << 30 });
console.log(`loaded ${ach.length} achievements, ${crit.length} criteria, ${cat.length} categories, ` +
  `${types.length} criteria types, ${area.length} areas, ${overlay.length} overlays, ${loc.length} area locations, ` +
  `${grid.length} area cells, ${reaction.length} faction templates, ${spell.length} spells`);
