// bun achievements.ts
// Loads the achievement catalogue from the client DBCs (data/dbc/Achievement*.dbc) into a `wowbench`
// schema that worlddb can read (the world DB only has a few server overrides; names, points and criteria
// live in the DBCs). Tables: achievement, achievement_criteria, achievement_category, criteria_type,
// area (AreaTable), map_overlay (explore_area criteria asset -> area ids), and area_location: where each
// area is, from the 16x16 area grid in every server terrain file (data/maps/MMMXXYY.map, ~33yd cells).
import { readFileSync, readdirSync } from "node:fs";
import { execFileSync } from "node:child_process";

function dbc(file: string) {
  const b = readFileSync(`data/dbc/${file}`);
  if (b.toString("latin1", 0, 4) !== "WDBC") throw new Error(`${file}: not a DBC`);
  const [n, fields, size] = [b.readUInt32LE(4), b.readUInt32LE(8), b.readUInt32LE(12)];
  const strs = 20 + n * size;
  const rows: { u: (i: number) => number; i: (i: number) => number; s: (i: number) => string }[] = [];
  for (let r = 0; r < n; r++) {
    const o = 20 + r * size;
    const u = (i: number) => b.readUInt32LE(o + i * 4);
    rows.push({
      u, i: (i: number) => b.readInt32LE(o + i * 4),
      s: (i: number) => { const p = strs + u(i); return b.toString("utf8", p, b.indexOf(0, p)); },
    });
  }
  if (fields * 4 !== size) throw new Error(`${file}: unexpected record size`);
  return rows;
}

const q = (s: string) => `'${s.replace(/\\/g, "\\\\").replace(/'/g, "\\'")}'`;
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

// Cell (lx, ly) of grid file (gx, gy) is centred at 533.33 * (32 - g - (l + 0.5) / 16), the inverse of
// GridTerrainData::getArea. ponytail: maps 0 and 1 only (Eastern Kingdoms, Kalimdor).
const S = 1600 / 3;
const cells = new Map<string, { map: number; id: number; pts: [number, number][] }>();
for (const f of readdirSync("data/maps")) {
  const m = f.match(/^(\d{3})(\d{2})(\d{2})\.map$/);
  if (!m || +m[1] > 1) continue;
  const b = readFileSync(`data/maps/${f}`);
  const off = b.readUInt32LE(12), flags = b.readUInt16LE(off + 4), whole = b.readUInt16LE(off + 6);
  for (let lx = 0; lx < 16; lx++) for (let ly = 0; ly < 16; ly++) {
    const id = flags & 1 ? whole : b.readUInt16LE(off + 8 + (lx * 16 + ly) * 2);
    if (!id) continue;
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

const enumSrc = readFileSync("server/src/server/shared/DataStores/DBCEnums.h", "utf8");
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
CREATE USER IF NOT EXISTS 'worldreader'@'%' IDENTIFIED BY 'worldreader';
GRANT SELECT ON wowbench.* TO 'worldreader'@'%';
`;
execFileSync("docker", ["exec", "-i", "ac-database", "mysql", "-uroot", "-ppassword"], { input: sql, stdio: ["pipe", "inherit", "ignore"] });
console.log(`loaded ${ach.length} achievements, ${crit.length} criteria, ${cat.length} categories, ` +
  `${types.length} criteria types, ${area.length} areas, ${overlay.length} overlays, ${loc.length} area locations`);
