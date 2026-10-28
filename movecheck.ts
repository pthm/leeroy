// bun movecheck.ts [recorder.jsonl] [--chars A,B] [--from ms]
// Checks recorded movement against the terrain: speed (median per 1 s sample; sustained over 5 s), the steepest slope
// crossed on each step, and whether the player was under water (a real client swims at the surface).
import { readFileSync } from "node:fs";
import { sample } from "./terrain.ts";

const args = process.argv.slice(2);
const flag = (k: string) => { const i = args.indexOf(`--${k}`); return i >= 0 ? args[i + 1] : undefined; };
const file = args[0] && !args[0].startsWith("--") ? args[0] : "server/env/dist/logs/recorder.jsonl";
const chars = flag("chars")?.split(","), from = Number(flag("from") ?? 0);
const MAX_SLOPE = Math.tan((50 * Math.PI) / 180); // AzerothCore navmesh walkable slope (MmapTileRecastConfig)

const pos = readFileSync(file, "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l))
  .filter((e) => e.ev === "pos" && e.t >= from && (!chars || chars.includes(e.n)));
const by = new Map<string, typeof pos>();
for (const p of pos) by.set(p.n, [...(by.get(p.n) ?? []), p]);

for (const [name, track] of by) {
  let moving = 0, speedSum = 0, fast = 0, steep = 0, steepest = 0, under = 0, swimFast = 0, samples = 0;
  const worst: string[] = [], aliveV: number[] = [], ghostV: number[] = [];
  const med = (a: number[]) => (a.length ? +a.sort((x, y) => x - y)[a.length >> 1].toFixed(2) : null);
  for (let i = 1; i < track.length; i++) {
    const a = track[i - 1], b = track[i], dt = (b.t - a.t) / 1000;
    if (dt <= 0 || dt > 2 || a.m !== b.m) continue;
    samples++;
    const d = Math.hypot(b.x - a.x, b.y - a.y), v = d / dt;
    const [, water] = sample(b.m, b.x, b.y);
    const submerged = !isNaN(water) && b.z < water - 1.5;
    if (submerged) { under++; if (v > 5.5) swimFast++; }
    if (d < 1 || v > 40) continue; // standing, or a teleport (graveyard, spirit healer)
    moving++; speedSum += v;
    (b.ghost ? ghostV : aliveV).push(v);
    // sustained speed over ~5 s: single samples alias against the client's heartbeat cadence
    const w = track[i - 5];
    if (w && b.t - w.t <= 6000 && w.ghost === b.ghost) {
      let path = 0;
      for (let k = i - 4; k <= i; k++) path += Math.hypot(track[k].x - track[k - 1].x, track[k].y - track[k - 1].y);
      if (path / ((b.t - w.t) / 1000) > (b.ghost ? 11.5 : 7.7)) fast++; // run 7, ghost 10.5, +10%
    }
    // steepest terrain slope along the step, sampled every 2 yards
    let maxSlope = 0;
    const n = Math.max(1, Math.ceil(d / 2));
    let prev = sample(a.m, a.x, a.y)[0];
    for (let k = 1; k <= n; k++) {
      const x = a.x + ((b.x - a.x) * k) / n, y = a.y + ((b.y - a.y) * k) / n, h = sample(a.m, x, y)[0];
      if (!isNaN(h) && !isNaN(prev)) maxSlope = Math.max(maxSlope, (h - prev) / (d / n)); // uphill only
      prev = h;
    }
    steepest = Math.max(steepest, maxSlope);
    if (maxSlope > MAX_SLOPE) {
      steep++;
      if (worst.length < 3) worst.push(`(${a.x.toFixed(0)},${a.y.toFixed(0)}) ${(Math.atan(maxSlope) * 180 / Math.PI).toFixed(0)}deg`);
    }
  }
  console.log(JSON.stringify({
    name, samples, moving_s: moving, median_speed_alive: med(aliveV), median_speed_ghost: med(ghostV), faster_than_run_5s: fast,
    uphill_steeper_than_50deg: steep, steepest_deg: +((Math.atan(steepest) * 180) / Math.PI).toFixed(0), examples: worst,
    underwater_s: under, underwater_faster_than_swim: swimFast,
  }));
}
