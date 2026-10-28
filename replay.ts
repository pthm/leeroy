// bun replay.ts <run-dir | team-dir> [--out replay.html] [--log server/env/dist/logs/recorder.jsonl]
//                [--chars A,B] [--from <ms|ISO>] [--to <ms|ISO>]
// Builds a self-contained HTML replay of a run from the mod-recorder log: a terrain map rendered from the
// server's own map files (data/maps: heights, water, zone borders) with every player's route, a seek bar,
// and chat / combat / event panels synced to the playhead. Characters and the time window come from the
// run's judge.json (or team.json), or from --chars/--from/--to.
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { deflateSync } from "node:zlib";
import { execFileSync } from "node:child_process";
import { join } from "node:path";
import { sample } from "./terrain.ts";

const [dirArg, ...rest] = process.argv.slice(2);
const flag = (k: string) => { const i = rest.indexOf(`--${k}`); return i >= 0 ? rest[i + 1] : undefined; };
const time = (s?: string) => (s ? (/^\d+$/.test(s) ? Number(s) : Date.parse(s)) : undefined);

// ---------- which characters, which window ----------
let chars = flag("chars")?.split(",") ?? [];
let from = time(flag("from")), to = time(flag("to"));
let title = "WoW run";
if (dirArg && existsSync(dirArg)) {
  title = dirArg.replace(/\/$/, "").split("/").pop()!;
  const judges = existsSync(join(dirArg, "team.json")) ? JSON.parse(readFileSync(join(dirArg, "team.json"), "utf8")).agents
    : existsSync(join(dirArg, "judge.json")) ? [JSON.parse(readFileSync(join(dirArg, "judge.json"), "utf8"))] : [];
  if (!chars.length) chars = judges.map((j: { char: string }) => j.char);
  const ends = judges.map((j: { at: string }) => Date.parse(j.at));
  to ??= ends.length ? Math.max(...ends) + 5000 : undefined;
  from ??= existsSync(join(dirArg, "go")) ? Number(readFileSync(join(dirArg, "go"), "utf8")) * 1000
    : judges.length ? Math.min(...judges.map((j: { at: string; elapsed_s: number }) => Date.parse(j.at) - j.elapsed_s * 1000)) : undefined;
}
const logFile = flag("log") ?? "server/env/dist/logs/recorder.jsonl";
type Ev = { t: number; ev: string; n?: string; [k: string]: unknown };
const all: Ev[] = readFileSync(logFile, "utf8").split("\n").filter(Boolean).map((l) => { try { return JSON.parse(l); } catch { return null; } })
  .filter((e: Ev | null): e is Ev => !!e);
const evs = all.filter((e) => (!chars.length || chars.includes(e.n ?? "")) && (!from || e.t >= from) && (!to || e.t <= to));
if (!evs.length) throw new Error(`no recorder events for ${chars.join(",") || "anyone"} in that window (${logFile})`);
if (!chars.length) chars = [...new Set(evs.map((e) => e.n!).filter(Boolean))];
const t0 = from ?? evs[0].t, t1 = to ?? evs[evs.length - 1].t;

// ---------- tracks ----------
const CLASS = ["", "Warrior", "Paladin", "Hunter", "Rogue", "Priest", "Death Knight", "Shaman", "Mage", "Warlock", "", "Druid"];
const COLOR = ["#888", "#C79C6E", "#F58CBA", "#ABD473", "#FFF569", "#FFFFFF", "#C41F3B", "#0070DE", "#69CCF0", "#9482C9", "#888", "#FF7D0A"];
const pos = evs.filter((e) => e.ev === "pos");
const mapCount = new Map<number, number>();
for (const p of pos) mapCount.set(p.m as number, (mapCount.get(p.m as number) ?? 0) + 1);
const mapId = [...mapCount].sort((a, b) => b[1] - a[1])[0]?.[0] ?? 0;
const players = chars.map((n) => {
  const login = all.findLast((e) => e.n === n && e.ev === "login" && e.t <= t1); // may predate the window
  const cls = Number(login?.cls ?? 0);
  return {
    name: n, cls: CLASS[cls] ?? "", color: COLOR[cls] ?? "#888",
    // [t, x, y, hp, hpmax, pw, pwmax, pt, lvl, xp, money, alive, ghost, combat, zone]
    track: pos.filter((p) => p.n === n).map((p) => [p.t - t0, p.x, p.y, p.hp, p.hpmax, p.pw, p.pwmax, p.pt, p.lvl, p.xp, p.money,
      p.alive, p.ghost, p.combat, p.zone, p.m]),
  };
});
const events = evs.filter((e) => e.ev !== "pos").map((e) => ({ ...e, t: e.t - t0 }));

// ---------- map image from data/maps ----------
const S = 1600 / 3;
const inMap = pos.filter((p) => p.m === mapId);
let xMin = Math.min(...inMap.map((p) => p.x as number)), xMax = Math.max(...inMap.map((p) => p.x as number));
let yMin = Math.min(...inMap.map((p) => p.y as number)), yMax = Math.max(...inMap.map((p) => p.y as number));
const pad = Math.max(250, 0.15 * Math.max(xMax - xMin, yMax - yMin));
xMin -= pad; xMax += pad; yMin -= pad; yMax += pad;
const res = Math.max(1.5, Math.ceil((Math.max(xMax - xMin, yMax - yMin) / 1800) * 2) / 2); // yards per pixel
const W = Math.ceil((yMax - yMin) / res), H = Math.ceil((xMax - xMin) / res);

const zoneOf = new Map<number, number>(), names = new Map<number, string>();
const sql = (q: string) => execFileSync("docker", ["exec", "ac-database", "mysql", "-uworldreader", "-pworldreader", "-N", "-B", "wowbench", "-e", q],
  { stdio: ["ignore", "pipe", "ignore"] }).toString().trim().split("\n").filter(Boolean).map((l) => l.split("\t"));
for (const [id, zone, name] of sql(`SELECT id, zone, name FROM area WHERE map = ${mapId}`)) {
  zoneOf.set(+id, +zone || +id);
  names.set(+id, name);
}
const heights = new Float32Array(W * H), water = new Float32Array(W * H), zone = new Uint16Array(W * H);
for (let r = 0; r < H; r++) for (let c = 0; c < W; c++) {
  const [h, wl, a] = sample(mapId, xMax - r * res, yMax - c * res);
  heights[r * W + c] = h; water[r * W + c] = wl; zone[r * W + c] = zoneOf.get(a) ?? 0;
}
let hLo = Infinity, hHi = -Infinity;
for (const h of heights) if (!isNaN(h)) { hLo = Math.min(hLo, h); hHi = Math.max(hHi, h); }
const rgb = Buffer.alloc((W * 3 + 1) * H);
const ramp = [[0, [86, 125, 70]], [0.35, [120, 150, 84]], [0.6, [158, 150, 104]], [0.8, [140, 120, 96]], [1, [235, 232, 225]]] as const;
const tint = (u: number) => {
  for (let k = 1; k < ramp.length; k++) if (u <= ramp[k][0]) {
    const [u0, a] = ramp[k - 1], [u1, b] = ramp[k], f = (u - u0) / (u1 - u0);
    return a.map((v, i) => v + (b[i] - v) * f);
  }
  return ramp[ramp.length - 1][1] as unknown as number[];
};
for (let r = 0; r < H; r++) {
  rgb[r * (W * 3 + 1)] = 0;
  for (let c = 0; c < W; c++) {
    const k = r * W + c, h = heights[k], o = r * (W * 3 + 1) + 1 + c * 3;
    if (isNaN(h)) { rgb[o] = rgb[o + 1] = rgb[o + 2] = 40; continue; }
    // hillshade: light from the north-west, slopes exaggerated x2
    const hx = (heights[Math.min(H - 1, r + 1) * W + c] - heights[Math.max(0, r - 1) * W + c]) / (2 * res) * 2;
    const hy = (heights[r * W + Math.min(W - 1, c + 1)] - heights[r * W + Math.max(0, c - 1)] ) / (2 * res) * 2;
    const shade = Math.max(0.35, Math.min(1.25, 0.85 + (hx * 0.7 + hy * 0.7) * -0.6));
    let col = tint((h - hLo) / Math.max(1, hHi - hLo)).map((v) => v * (isNaN(shade) ? 1 : shade));
    if (water[k] > h) {
      const depth = Math.min(1, (water[k] - h) / 20);
      col = [70 - depth * 30, 120 - depth * 30, 170 - depth * 20];
    }
    const z = zone[k];
    if ((c + 1 < W && zone[k + 1] !== z) || (r + 1 < H && zone[k + W] !== z)) col = col.map((v) => v * 0.45);
    rgb[o] = col[0]; rgb[o + 1] = col[1]; rgb[o + 2] = col[2];
  }
}
const crcTable = Array.from({ length: 256 }, (_, n) => { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; return c >>> 0; });
const crc = (b: Buffer) => { let c = 0xffffffff; for (const x of b) c = crcTable[(c ^ x) & 255] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0; };
const chunk = (type: string, data: Buffer) => {
  const t = Buffer.concat([Buffer.from(type), data]), len = Buffer.alloc(4), c = Buffer.alloc(4);
  len.writeUInt32BE(data.length); c.writeUInt32BE(crc(t));
  return Buffer.concat([len, t, c]);
};
const ihdr = Buffer.alloc(13);
ihdr.writeUInt32BE(W, 0); ihdr.writeUInt32BE(H, 4); ihdr[8] = 8; ihdr[9] = 2;
const png = Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk("IHDR", ihdr), chunk("IDAT", deflateSync(rgb, { level: 9 })), chunk("IEND", Buffer.alloc(0))]);

// labels: zones and sub-areas whose point falls on the map
const labels = sql(`SELECT a.id, a.name, a.zone = 0 is_zone, l.x, l.y FROM area_location l JOIN area a ON a.id = l.area AND a.map = l.map
  WHERE l.map = ${mapId} AND l.x BETWEEN ${xMin} AND ${xMax} AND l.y BETWEEN ${yMin} AND ${yMax} AND l.cells >= 8`)
  .map(([id, name, z, x, y]) => ({ id: +id, name, zone: z === "1", x: +x, y: +y }));
const itemIds = [...new Set(events.filter((e) => e.ev === "loot").map((e) => Number(e.item)))];
const itemNames = itemIds.length ? Object.fromEntries(execFileSync("docker", ["exec", "ac-database", "mysql", "-uworldreader", "-pworldreader", "-N", "-B",
  "acore_world", "-e", `SELECT entry, name FROM item_template WHERE entry IN (${itemIds.join(",")})`], { stdio: ["ignore", "pipe", "ignore"] })
  .toString().trim().split("\n").filter(Boolean).map((l) => l.split("\t"))) : {};

const data = { title, start: t0, duration: t1 - t0, map: { id: mapId, xMax, yMax, res, w: W, h: H }, players, events, labels, zones: Object.fromEntries(names), items: itemNames };
const html = readFileSync(new URL("replay.html", import.meta.url).pathname, "utf8")
  .replace("__TITLE__", title)
  .replace("__MAP__", `data:image/png;base64,${png.toString("base64")}`)
  .replace("__DATA__", JSON.stringify(data).replace(/</g, "\\u003c"));
const out = flag("out") ?? join(dirArg && existsSync(dirArg) ? dirArg : ".", "replay.html");
writeFileSync(out, html);
console.log(`${out}: ${players.length} players, ${pos.length} positions, ${events.length} events, map ${W}x${H} @ ${res} yd/px, ${(html.length / 1e6).toFixed(1)} MB`);
