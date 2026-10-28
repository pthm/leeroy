// Terrain from the server's map files (data/maps/MMMXXYY.map), sampled the way GridTerrainData does:
// sample(map, x, y) -> [ground height (NaN off-map), water surface (NaN if none), area id].
// ponytail: terrain only; buildings and other WMO floors live in vmaps and aren't modelled.
import { existsSync, readFileSync } from "node:fs";

const MAPS = new URL("../data/maps/", import.meta.url).pathname;

const S = 1600 / 3;
type Grid = { h: Float32Array; area: Uint16Array; liquid?: { lvl: Float32Array | null; level: number; ox: number; oy: number; w: number; hh: number; cell: Uint8Array | null; global: number } } | null;
const grids = new Map<string, Grid>();
function grid(mapId: number, gx: number, gy: number): Grid {
  const key = `${mapId},${gx},${gy}`;
  if (grids.has(key)) return grids.get(key)!;
  const f = `${MAPS}${String(mapId).padStart(3, "0")}${String(gx).padStart(2, "0")}${String(gy).padStart(2, "0")}.map`;
  let g: Grid = null;
  if (existsSync(f)) {
    const b = readFileSync(f);
    const aOff = b.readUInt32LE(12), hOff = b.readUInt32LE(20), lOff = b.readUInt32LE(28);
    const area = new Uint16Array(256);
    if (aOff) {
      const fl = b.readUInt16LE(aOff + 4);
      for (let i = 0; i < 256; i++) area[i] = fl & 1 ? b.readUInt16LE(aOff + 6) : b.readUInt16LE(aOff + 8 + i * 2);
    }
    const h = new Float32Array(129 * 129);
    if (hOff) {
      const fl = b.readUInt32LE(hOff + 4), lo = b.readFloatLE(hOff + 8), hi = b.readFloatLE(hOff + 12), d = hOff + 16;
      for (let i = 0; i < 129 * 129; i++)
        h[i] = fl & 1 ? lo : fl & 2 ? lo + b.readUInt16LE(d + i * 2) * (hi - lo) / 65535 : fl & 4 ? lo + b[d + i] * (hi - lo) / 255 : b.readFloatLE(d + i * 4);
    }
    let liquid;
    if (lOff) {
      const fl = b[lOff + 4], global = b[lOff + 5], ox = b[lOff + 8], oy = b[lOff + 9], w = b[lOff + 10], hh = b[lOff + 11], level = b.readFloatLE(lOff + 12);
      let d = lOff + 16, cell: Uint8Array | null = null, lvl: Float32Array | null = null;
      if (!(fl & 1)) { cell = new Uint8Array(b.subarray(d + 512, d + 768)); d += 768; }
      if (!(fl & 2)) { lvl = new Float32Array(w * hh); for (let i = 0; i < w * hh; i++) lvl[i] = b.readFloatLE(d + i * 4); }
      liquid = { lvl, level, ox, oy, w, hh, cell, global };
    }
    g = { h, area, liquid };
  }
  grids.set(key, g);
  return g;
}
// Sample (height, water level or NaN, area) at a world point; same cell maths as GridTerrainData
export function sample(mapId: number, x: number, y: number): [number, number, number] {
  const fx = 32 - x / S, fy = 32 - y / S, gx = Math.floor(fx), gy = Math.floor(fy);
  const g = grid(mapId, gx, gy);
  if (!g) return [NaN, NaN, 0];
  const i = (fx - gx) * 128, j = (fy - gy) * 128, i0 = Math.min(127, Math.floor(i)), j0 = Math.min(127, Math.floor(j));
  const di = i - i0, dj = j - j0, v = (a: number, c: number) => g.h[a * 129 + c];
  const h = v(i0, j0) * (1 - di) * (1 - dj) + v(i0 + 1, j0) * di * (1 - dj) + v(i0, j0 + 1) * (1 - di) * dj + v(i0 + 1, j0 + 1) * di * dj;
  let water = NaN;
  const L = g.liquid;
  if (L && (L.cell ? L.cell[(i0 >> 3) * 16 + (j0 >> 3)] : L.global)) {
    if (!L.lvl) water = L.level;
    else {
      const cx = i0 - L.oy, cy = j0 - L.ox;
      if (cx >= 0 && cx < L.hh && cy >= 0 && cy < L.w) water = L.lvl[cx * L.w + cy];
    }
  }
  return [h, water, g.area[(i0 >> 3) * 16 + (j0 >> 3)]];
}

