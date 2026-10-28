// Roads, safe routes and map images for wowdb (roads / route / map).
// Roads come from the patrol paths of friendly NPCs (guards, patrollers, travelling merchants walk the roads); the
// client's road textures aren't available (no client MPQs here), so off-patrol paths are missing.
// Routes: A* on an 8-yard grid over the server's terrain (no slopes over ~45 degrees, swimming is slow), cheaper on
// roads and more expensive near hostile mobs around or above your level. Terrain only: buildings, caves and
// bridges aren't modelled, so follow the route with move_to, which uses the real navmesh between waypoints.
import { writeFileSync } from "node:fs";
import { sample } from "../../server/recorder/terrain.ts";
import { renderMap } from "../../server/recorder/map.ts";

type Row = Record<string, string | number | null>;
export type Q = (sql: string) => Row[];
const CELL = 8;

export function roads(q: Q, map: number, x0: number, x1: number, y0: number, y1: number) {
  const rows = q(`SELECT w.id, w.point, ROUND(w.position_x) x, ROUND(w.position_y) y FROM waypoint_data w
    WHERE w.id IN (SELECT a.path_id FROM creature c JOIN creature_addon a ON a.guid = c.guid JOIN creature_template t ON t.entry = c.id
      JOIN wowbench.reaction r ON r.faction_template = t.faction
      WHERE c.map = ${map} AND a.path_id > 0 AND r.to_alliance = 'friendly' AND r.to_horde <> 'friendly'
        AND c.position_x BETWEEN ${x0 - 300} AND ${x1 + 300} AND c.position_y BETWEEN ${y0 - 300} AND ${y1 + 300})
    ORDER BY w.id, w.point`);
  const paths = new Map<number, [number, number][]>();
  for (const r of rows) {
    if (!paths.has(Number(r.id))) paths.set(Number(r.id), []);
    paths.get(Number(r.id))!.push([Number(r.x), Number(r.y)]);
  }
  // keep travelling paths (not an NPC pacing in a room), clipped loosely to the box
  return [...paths.values()].filter((p) => {
    const xs = p.map((v) => v[0]), ys = p.map((v) => v[1]);
    return Math.max(...xs) - Math.min(...xs) + Math.max(...ys) - Math.min(...ys) > 120;
  }).filter((p) => p.some(([x, y]) => x >= x0 && x <= x1 && y >= y0 && y <= y1));
}

// Hostile spawns (to an Alliance character; horde flips it) with their levels
export function threats(q: Q, map: number, x0: number, x1: number, y0: number, y1: number, horde = false) {
  return q(`SELECT s.id entry, t.name, t.minlevel lo, t.maxlevel hi, t.\`rank\` rk, ROUND(s.x) x, ROUND(s.y) y FROM wowbench.creature_spawn s
    JOIN creature_template t ON t.entry = s.id JOIN wowbench.reaction r ON r.faction_template = t.faction
    WHERE s.map = ${map} AND r.${horde ? "to_horde" : "to_alliance"} = 'hostile'
      AND t.flags_extra & 128 = 0 AND t.unit_flags & 0x2000000 = 0 -- not invisible triggers / unselectable dummies
      AND s.guid NOT IN (SELECT guid FROM game_event_creature) -- holiday spawns
      AND s.x BETWEEN ${x0} AND ${x1} AND s.y BETWEEN ${y0} AND ${y1}`)
    .map((r) => ({ entry: Number(r.entry), name: String(r.name), level: (Number(r.lo) + Number(r.hi)) / 2, elite: Number(r.rk) > 0, x: Number(r.x), y: Number(r.y) }));
}

// how far a mob of `lvl` notices a player of `pl` (roughly the server's aggro radius: 20 yd at equal level, +-1 per level)
const aggro = (lvl: number, pl: number) => Math.max(5, Math.min(40, 20 + (lvl - pl)));

export function route(q: Q, map: number, from: [number, number], to: [number, number], level: number, horde = false) {
  const pad = 200;
  const x0 = Math.min(from[0], to[0]) - pad, x1 = Math.max(from[0], to[0]) + pad;
  const y0 = Math.min(from[1], to[1]) - pad, y1 = Math.max(from[1], to[1]) + pad;
  const W = Math.ceil((y1 - y0) / CELL) + 1, H = Math.ceil((x1 - x0) / CELL) + 1;
  if (W * H > 400_000) throw new Error("route too long for one query (max ~5000 yd): split it into legs");
  const idx = (x: number, y: number) => Math.round((x - x0) / CELL) * W + Math.round((y - y0) / CELL);
  const cx = (i: number) => x0 + Math.floor(i / W) * CELL, cy = (i: number) => y0 + (i % W) * CELL;
  const h = new Float32Array(W * H), cost = new Float32Array(W * H).fill(1);
  for (let i = 0; i < W * H; i++) {
    const [g, wl] = sample(map, cx(i), cy(i));
    h[i] = g;
    if (!isNaN(wl) && wl > g + 1.5) cost[i] = 3; // swimming
  }
  const road = new Uint8Array(W * H);
  for (const p of roads(q, map, x0, x1, y0, y1)) for (let k = 1; k < p.length; k++) {
    const [ax, ay] = p[k - 1], [bx, by] = p[k], n = Math.ceil(Math.hypot(bx - ax, by - ay) / (CELL / 2));
    for (let s = 0; s <= n; s++) {
      const x = ax + ((bx - ax) * s) / n, y = ay + ((by - ay) * s) / n;
      if (x >= x0 && x <= x1 && y >= y0 && y <= y1) road[idx(x, y)] = 1;
    }
  }
  for (let i = 0; i < W * H; i++) if (road[i]) cost[i] *= 0.6;
  const mobs = threats(q, map, x0, x1, y0, y1, horde).filter((m) => m.level >= level - 2);
  for (const m of mobs) {
    const r = aggro(m.level, level) + (m.elite ? 10 : 0), weight = 4 + 2 * Math.max(0, m.level - level) + (m.elite ? 10 : 0);
    for (let dx = -r; dx <= r; dx += CELL) for (let dy = -r; dy <= r; dy += CELL) {
      const d = Math.hypot(dx, dy), x = m.x + dx, y = m.y + dy;
      if (d <= r && x >= x0 && x <= x1 && y >= y0 && y <= y1) cost[idx(x, y)] += weight * (1 - d / r);
    }
  }
  // A* over 8 neighbours; a step is blocked if steeper than ~45 degrees or off the map
  const start = idx(...from), goal = idx(...to), dist = new Float32Array(W * H).fill(Infinity), prev = new Int32Array(W * H).fill(-1);
  const heap: [number, number][] = [];
  const push = (f: number, i: number) => { heap.push([f, i]); let k = heap.length - 1; while (k > 0) { const p = (k - 1) >> 1; if (heap[p][0] <= heap[k][0]) break; [heap[p], heap[k]] = [heap[k], heap[p]]; k = p; } };
  const pop = () => { const top = heap[0], last = heap.pop()!; if (heap.length) { heap[0] = last; let k = 0; for (;;) { const l = 2 * k + 1, r = l + 1; let m = k; if (l < heap.length && heap[l][0] < heap[m][0]) m = l; if (r < heap.length && heap[r][0] < heap[m][0]) m = r; if (m === k) break; [heap[m], heap[k]] = [heap[k], heap[m]]; k = m; } } return top; };
  const hEst = (i: number) => Math.hypot(cx(i) - to[0], cy(i) - to[1]) * 0.6;
  dist[start] = 0; push(hEst(start), start);
  while (heap.length) {
    const [, i] = pop();
    if (i === goal) break;
    const r = Math.floor(i / W), c = i % W;
    for (const [dr, dc] of [[1, 0], [-1, 0], [0, 1], [0, -1], [1, 1], [1, -1], [-1, 1], [-1, -1]]) {
      const nr = r + dr, nc = c + dc;
      if (nr < 0 || nr >= H || nc < 0 || nc >= W) continue;
      const j = nr * W + nc, step = Math.hypot(dr, dc) * CELL;
      if (isNaN(h[j]) || Math.abs(h[j] - h[i]) > step) continue;
      const d = dist[i] + step * (cost[i] + cost[j]) / 2;
      if (d < dist[j]) { dist[j] = d; prev[j] = i; push(d + hEst(j), j); }
    }
  }
  if (prev[goal] < 0 && goal !== start) return { error: "no walkable route on the terrain (water, cliffs, or the target is indoors/underground): try a nearer point" };
  const cells: number[] = [];
  for (let i = goal; i >= 0; i = prev[i]) cells.unshift(i);
  // waypoints: a point every ~40 yd or where the direction turns
  const pts = cells.map((i) => [cx(i), cy(i), Math.round(h[i] * 10) / 10]);
  const way: number[][] = [pts[0]];
  for (let k = 1; k < pts.length - 1; k++) {
    const a = way.at(-1)!, b = pts[k], c = pts[k + 1];
    const turn = Math.abs(Math.atan2(c[1] - b[1], c[0] - b[0]) - Math.atan2(b[1] - a[1], b[0] - a[0]));
    if (Math.hypot(b[0] - a[0], b[1] - a[1]) >= 40 || (turn > 0.5 && turn < 2 * Math.PI - 0.5)) way.push(b);
  }
  way.push([to[0], to[1], pts.at(-1)![2]]);
  let length = 0, onRoad = 0;
  for (let k = 1; k < cells.length; k++) { const s = Math.hypot(cx(cells[k]) - cx(cells[k - 1]), cy(cells[k]) - cy(cells[k - 1])); length += s; if (road[cells[k]]) onRoad += s; }
  const near = new Map<number, { entry: number; name: string; level: number; elite: boolean; closest: number; n: number }>();
  for (const m of mobs) {
    let d = Infinity;
    for (const i of cells) d = Math.min(d, Math.hypot(cx(i) - m.x, cy(i) - m.y));
    if (d > aggro(m.level, level) + 5) continue;
    const e = near.get(m.entry) ?? { entry: m.entry, name: m.name, level: m.level, elite: m.elite, closest: d, n: 0 };
    e.n++; e.closest = Math.round(Math.min(e.closest, d)); near.set(m.entry, e);
  }
  return { length: Math.round(length), on_road: Math.round((onRoad / Math.max(1, length)) * 100) + "%", waypoints: way,
    passes_near: [...near.values()].sort((a, b) => b.level - a.level),
    note: "walk it with move_to(x, y, ground_z(x, y)) leg by leg; terrain only, so buildings and caves aren't modelled" };
}

// PNG around (x, y): terrain, roads (light), hostile mobs (red; brighter = higher level than you, big = elite), your
// position (white), optional route (yellow). North (+x) is up, west (+y) is left.
export function mapImage(q: Q, map: number, x: number, y: number, radius: number, level: number, out: string, path?: number[][]) {
  const res = Math.max(1, Math.round((radius * 2) / 600));
  const x0 = x - radius, x1 = x + radius, y0 = y - radius, y1 = y + radius;
  const rd = roads(q, map, x0, x1, y0, y1), mobs = threats(q, map, x0, x1, y0, y1);
  const img = renderMap(map, x0, x1, y0, y1, res, undefined, (px) => {
    const dot = (wx: number, wy: number, r: number, col: number[]) => {
      for (let dx = -r; dx <= r; dx++) for (let dy = -r; dy <= r; dy++) if (dx * dx + dy * dy <= r * r) px(wx + dx * res, wy + dy * res, col);
    };
    const line = (pts: number[][], col: number[], w: number) => {
      for (let k = 1; k < pts.length; k++) {
        const [ax, ay] = pts[k - 1], [bx, by] = pts[k], n = Math.ceil(Math.hypot(bx - ax, by - ay) / res);
        for (let s = 0; s <= n; s++) dot(ax + ((bx - ax) * s) / n, ay + ((by - ay) * s) / n, w, col);
      }
    };
    for (const p of rd) line(p, [235, 220, 170], 1);
    for (const m of mobs) dot(m.x, m.y, m.elite ? 4 : 2, m.level >= level + 2 ? [255, 30, 30] : m.level >= level - 1 ? [230, 120, 40] : [150, 110, 90]);
    if (path) line(path, [255, 230, 0], 1);
    dot(x, y, 4, [255, 255, 255]);
  });
  writeFileSync(out, img.png);
  return { file: out, width: img.w, height: img.h, yards_per_pixel: res, center: [x, y],
    legend: "north up; light lines = roads (guard patrols); red = hostile 2+ levels above you, orange = around your level, brown = lower; big dot = elite; white = (x, y); yellow = route",
    roads: rd.length, hostile_spawns: mobs.length };
}
