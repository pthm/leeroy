// Shared by replay.ts (static replays) and live.ts (live viewer): terrain map rendering from the server's map
// files, area/zone names and labels from the wowbench schema, item names, player colours.
import { minimapAt } from "./clientmap.ts";
import { deflateSync } from "node:zlib";
import { execFileSync } from "node:child_process";
import { sample } from "./terrain.ts";

export const S = 1600 / 3; // yards per map grid (one .map file)
export const CLASS = ["", "Warrior", "Paladin", "Hunter", "Rogue", "Priest", "Death Knight", "Shaman", "Mage", "Warlock", "", "Druid"];
export const COLOR = ["#888", "#C79C6E", "#F58CBA", "#ABD473", "#FFF569", "#FFFFFF", "#C41F3B", "#0070DE", "#69CCF0", "#9482C9", "#888", "#FF7D0A"];

export type Ev = { t: number; ev: string; n?: string; [k: string]: unknown };

// An agent transcript as "say" events: only the agent's visible text messages. Claude Code stream-json (assistant
// messages), or Codex --json (agent_message items, timestamped by bench.sh as they arrive).
export const transcriptEvents = (text: string, name: string): Ev[] =>
  text.split("\n").filter(Boolean).flatMap((l) => {
    let m: { type?: string; timestamp?: string; message?: { content?: { type: string; text?: string }[] }; item?: { type?: string; text?: string } };
    try { m = JSON.parse(l); } catch { return []; }
    if (!m.timestamp) return [];
    if (m.type === "item.completed" && m.item?.type === "agent_message" && m.item.text?.trim())
      return [{ t: Date.parse(m.timestamp), ev: "say", n: name, text: m.item.text.trim() }];
    if (m.type !== "assistant") return [];
    return (m.message?.content ?? []).filter((c) => c.type === "text" && c.text?.trim())
      .map((c) => ({ t: Date.parse(m.timestamp!), ev: "say", n: name, text: c.text!.trim() }));
  });

// Who plays each character (bench.sh writes meta.json in each agent's run dir): runtime, model, spec, role.
export type AgentMeta = { agent: string; char: string; runtime: string; model: string; spec?: string; role?: string };
export function agentMeta(transcripts: { path: string; name: string }[]): Record<string, AgentMeta> {
  const { existsSync, readFileSync } = require("node:fs") as typeof import("node:fs");
  const { dirname, join } = require("node:path") as typeof import("node:path");
  const out: Record<string, AgentMeta> = {};
  for (const t of transcripts) {
    const f = join(dirname(t.path), "meta.json");
    if (existsSync(f)) try { out[t.name] = JSON.parse(readFileSync(f, "utf8")); } catch { /* being written */ }
  }
  return out;
}

// Everything a bench run directory has: record files, and each agent's transcript with its character name.
// A team dir holds <agent>.record.jsonl; the agents' own run dirs (<time>-<agent>/) sit beside it.
export function runSources(dir: string) {
  const { readdirSync, existsSync, readFileSync } = require("node:fs") as typeof import("node:fs");
  const { join, dirname, basename } = require("node:path") as typeof import("node:path");
  const records = readdirSync(dir).filter((f) => /(^|\.)record\.jsonl$/.test(f)).map((f) => join(dir, f));
  const transcripts: { name: string; path: string }[] = [];
  const charOf = (runDir: string) => {
    const p = join(runDir, "prompt.md");
    const m = existsSync(p) ? readFileSync(p, "utf8").match(/Your\s+character (\w+)|You are (\w+), an? /) : null;
    return m?.[1] ?? m?.[2]; // "Your character X is..." or, with a spec, "You are X, a Human Warrior..."
  };
  if (existsSync(join(dir, "agent.jsonl"))) { // a single run
    const name = charOf(dir);
    if (name) transcripts.push({ name, path: join(dir, "agent.jsonl") });
  }
  for (const r of records) { // team: find each agent's run dir by its suffix
    const agent = basename(r).replace(/\.?record\.jsonl$/, "");
    if (!agent) continue;
    const runDir = readdirSync(dirname(dir)).filter((d) => d.endsWith(`-${agent}`)).sort().at(-1);
    const name = runDir && charOf(join(dirname(dir), runDir));
    if (name) transcripts.push({ name, path: join(dirname(dir), runDir!, "agent.jsonl") });
  }
  return { records, transcripts };
}
// Chat: every agent's client log has its own copy of each message it received (and no sender name), so player chat
// comes from the server's recorder log instead: one event per message, n = sender, to = channel/party/whisper target.
export const SERVER_LOG = new URL("../azerothcore/env/dist/logs/recorder.jsonl", import.meta.url).pathname;
export const serverChat = (text: string, chars: Set<string>): Ev[] =>
  text.split("\n").filter((l) => l.includes('"ev":"chat"')).flatMap((l) => { try { return [JSON.parse(l) as Ev]; } catch { return []; } })
    .filter((e) => chars.has(e.n ?? ""));
// client chat minus player chat (server-side above); NPC speech once per (speaker, text, second), not once per listener
const PLAYER_CHAT = new Set([1, 2, 3, 4, 6, 7, 9, 10, 17]);
export function clientChatFilter() {
  const seen = new Set<string>();
  return (e: Ev) => {
    if (e.ev !== "chat") return true;
    if (PLAYER_CHAT.has(Number(e.type))) return false; // say, party, raid, guild, yell, whisper (+ echo), emote, channel
    const k = `${e.from}|${e.msg}|${Math.floor(e.t / 2000)}`;
    if (seen.has(k)) return false;
    seen.add(k);
    return true;
  };
}
export const parseLines = (text: string): Ev[] =>
  text.split("\n").filter(Boolean).map((l) => { try { return JSON.parse(l); } catch { return null; } }).filter((e): e is Ev => !!e);

// One position sample as the viewer stores it:
// [t, x, y, hp, hpmax, pw, pwmax, pt, lvl, xp, money, alive, ghost, combat, zone, map, group id (0 = none)]
export const trackRow = (p: Ev, t0: number) => [p.t - t0, p.x, p.y, p.hp, p.hpmax, p.pw, p.pwmax, p.pt, p.lvl, p.xp, p.money,
  p.alive, p.ghost, p.combat, p.zone, p.m, p.grp ?? 0];

export const sql = (db: string, q: string) =>
  execFileSync("docker", ["exec", "ac-database", "mysql", "-uworldreader", "-pworldreader", "-N", "-B", db, "-e", q],
    { stdio: ["ignore", "pipe", "ignore"] }).toString().trim().split("\n").filter(Boolean).map((l) => l.split("\t"));

const areaCache = new Map<number, { zoneOf: Map<number, number>; names: Map<number, string> }>();
export function areas(mapId: number) {
  if (!areaCache.has(mapId)) {
    const zoneOf = new Map<number, number>(), names = new Map<number, string>();
    for (const [id, zone, name] of sql("wowbench", `SELECT id, zone, name FROM area WHERE map = ${mapId}`)) {
      zoneOf.set(+id, +zone || +id);
      names.set(+id, name);
    }
    areaCache.set(mapId, { zoneOf, names });
  }
  return areaCache.get(mapId)!;
}

// Zone and sub-area labels (only areas big enough to name), optionally inside a box
export const labels = (mapId: number, box?: [number, number, number, number]) =>
  sql("wowbench", `SELECT a.id, a.name, a.zone = 0, l.x, l.y FROM area_location l JOIN area a ON a.id = l.area AND a.map = l.map
    WHERE l.map = ${mapId} AND l.cells >= 8${box ? ` AND l.x BETWEEN ${box[0]} AND ${box[1]} AND l.y BETWEEN ${box[2]} AND ${box[3]}` : ""}`)
    .map(([id, name, z, x, y]) => ({ id: +id, name, zone: z === "1", x: +x, y: +y }));

// Fill in creature names the client couldn't resolve (SDK recordings carry only entry ids).
const creatureCache = new Map<number, string>();
export function nameUnits(events: Ev[]) {
  const refs = events.flatMap((e) => ["src", "dst", "victim", "killer", "target", "tgt"].map((k) => e[k] as { id: number; n?: string; pl?: number } | undefined))
    .filter((u): u is { id: number; n?: string; pl?: number } => !!u && !u.pl && !u.n && !!u.id);
  const missing = [...new Set(refs.map((u) => u.id).filter((id) => !creatureCache.has(id)))];
  if (missing.length) for (const [id, name] of sql("acore_world", `SELECT entry, name FROM creature_template WHERE entry IN (${missing.join(",")})`)) creatureCache.set(+id, name);
  for (const u of refs) u.n = creatureCache.get(u.id) ?? `#${u.id}`;
}

// item id -> {name, q (quality: 0 poor ... 5 legendary)}
export const itemNames = (ids: number[]) => (ids.length
  ? Object.fromEntries(sql("acore_world", `SELECT entry, name, Quality FROM item_template WHERE entry IN (${ids.join(",")})`)
    .map(([id, name, q]) => [id, { name, q: +q }])) : {}) as Record<string, { name: string; q: number }>;

const ramp = [[0, [86, 125, 70]], [0.35, [120, 150, 84]], [0.6, [158, 150, 104]], [0.8, [140, 120, 96]], [1, [235, 232, 225]]] as const;
const tint = (u: number) => {
  u = Math.max(0, Math.min(1, u));
  for (let k = 1; k < ramp.length; k++) if (u <= ramp[k][0]) {
    const [u0, a] = ramp[k - 1], [u1, b] = ramp[k], f = (u - u0) / (u1 - u0);
    return a.map((v, i) => v + (b[i] - v) * f);
  }
  return [...ramp[ramp.length - 1][1]];
};

// Shaded-relief PNG of the box x in [xMin, xMax], y in [yMin, yMax] at `res` yards per pixel; north (+x) up, west
// (+y) left. heightRange fixes the colour scale (tiles must agree); by default it fits the box.
export function renderMap(mapId: number, xMin: number, xMax: number, yMin: number, yMax: number, res: number,
  heightRange?: [number, number], overlay?: (px: (wx: number, wy: number, rgb: number[]) => void) => void) {
  const W = Math.ceil((yMax - yMin) / res), H = Math.ceil((xMax - xMin) / res);
  const { zoneOf } = areas(mapId);
  const heights = new Float32Array(W * H), water = new Float32Array(W * H), zone = new Uint16Array(W * H);
  for (let r = 0; r < H; r++) for (let c = 0; c < W; c++) {
    const [h, wl, a] = sample(mapId, xMax - r * res, yMax - c * res);
    heights[r * W + c] = h; water[r * W + c] = wl; zone[r * W + c] = zoneOf.get(a) ?? 0;
  }
  let [hLo, hHi] = heightRange ?? [Infinity, -Infinity];
  if (!heightRange) for (const h of heights) if (!isNaN(h)) { hLo = Math.min(hLo, h); hHi = Math.max(hHi, h); }
  const rgb = Buffer.alloc((W * 3 + 1) * H);
  for (let r = 0; r < H; r++) {
    for (let c = 0; c < W; c++) {
      const k = r * W + c, h = heights[k], o = r * (W * 3 + 1) + 1 + c * 3;
      if (isNaN(h)) { rgb[o] = rgb[o + 1] = rgb[o + 2] = 40; continue; }
      // hillshade: light from the north-west, slopes exaggerated x2
      const hx = (heights[Math.min(H - 1, r + 1) * W + c] - heights[Math.max(0, r - 1) * W + c]) / res;
      const hy = (heights[r * W + Math.min(W - 1, c + 1)] - heights[r * W + Math.max(0, c - 1)]) / res;
      const shade = Math.max(0.35, Math.min(1.25, 0.85 - (hx + hy) * 0.42));
      let col = tint((h - hLo) / Math.max(1, hHi - hLo)).map((v) => v * (isNaN(shade) ? 1 : shade));
      if (water[k] > h) {
        const depth = Math.min(1, (water[k] - h) / 20);
        col = [70 - depth * 30, 120 - depth * 30, 170 - depth * 20];
      }
      const mm = minimapAt(mapId, xMax - r * res, yMax - c * res); // the game's own minimap where we have it
      if (mm) col = mm;
      const z = zone[k];
      if ((c + 1 < W && zone[k + 1] !== z) || (r + 1 < H && zone[k + W] !== z)) col = col.map((v) => v * 0.45);
      // clamp: shaded snow goes past 255, which would wrap around in the byte buffer
      rgb[o] = Math.min(255, col[0]); rgb[o + 1] = Math.min(255, col[1]); rgb[o + 2] = Math.min(255, col[2]);
    }
  }
  overlay?.((wx, wy, col) => { // draw at a world position (wowdb map)
    const r = Math.round((xMax - wx) / res), c = Math.round((yMax - wy) / res);
    if (r < 0 || r >= H || c < 0 || c >= W) return;
    const o = r * (W * 3 + 1) + 1 + c * 3;
    rgb[o] = col[0]; rgb[o + 1] = col[1]; rgb[o + 2] = col[2];
  });
  return { png: png(W, H, rgb), w: W, h: H };
}

const crcTable = Array.from({ length: 256 }, (_, n) => { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; return c >>> 0; });
const crc = (b: Buffer) => { let c = 0xffffffff; for (const x of b) c = crcTable[(c ^ x) & 255] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0; };
const chunk = (type: string, data: Buffer) => {
  const t = Buffer.concat([Buffer.from(type), data]), len = Buffer.alloc(4), c = Buffer.alloc(4);
  len.writeUInt32BE(data.length); c.writeUInt32BE(crc(t));
  return Buffer.concat([len, t, c]);
};
function png(w: number, h: number, rows: Buffer) { // rows: filter byte 0 + RGB per row
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0); ihdr.writeUInt32BE(h, 4); ihdr[8] = 8; ihdr[9] = 2;
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk("IHDR", ihdr), chunk("IDAT", deflateSync(rows, { level: 6 })), chunk("IEND", Buffer.alloc(0))]);
}
