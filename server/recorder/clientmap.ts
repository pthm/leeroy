// What the game client's own data adds to the map (extracted by azeroth-sdk/cmd/clientdata into data/):
// the minimap images (roads, buildings, water, as players see them) and the road layers of the terrain textures.
// minimap/<map>/<gx>_<gy>.png: one 256x256 image per 533-yard grid square, north up, west left.
// roads/<map>/<gx>_<gy>.bin: 128x128 bytes per square, the share of each cell covered by road textures (0-255).
import { existsSync, readFileSync } from "node:fs";
import { inflateSync } from "node:zlib";
import { DATA } from "./terrain.ts";

const S = 1600 / 3; // yards per grid square
const grid = (v: number) => Math.floor(32 - v / S);

// decodePNG: 8-bit RGB or RGBA, non-interlaced (what Go's image/png writes for these tiles) -> RGB bytes
export function decodePNG(buf: Buffer): { w: number; h: number; rgb: Uint8Array } {
  let w = 0, h = 0, type = 0;
  const idat: Buffer[] = [];
  for (let p = 8; p < buf.length;) {
    const len = buf.readUInt32BE(p), tag = buf.toString("latin1", p + 4, p + 8), body = buf.subarray(p + 8, p + 8 + len);
    if (tag === "IHDR") { w = body.readUInt32BE(0); h = body.readUInt32BE(4); type = body[9]; }
    if (tag === "IDAT") idat.push(body);
    p += 12 + len;
  }
  const bpp = type === 6 ? 4 : 3, stride = w * bpp, raw = inflateSync(Buffer.concat(idat));
  const px = new Uint8Array(stride * h), rgb = new Uint8Array(w * h * 3);
  for (let y = 0; y < h; y++) {
    const f = raw[y * (stride + 1)], src = y * (stride + 1) + 1, o = y * stride;
    for (let x = 0; x < stride; x++) {
      const a = x >= bpp ? px[o + x - bpp] : 0, b = y > 0 ? px[o - stride + x] : 0, c = x >= bpp && y > 0 ? px[o - stride + x - bpp] : 0;
      let v = raw[src + x];
      if (f === 1) v += a;
      else if (f === 2) v += b;
      else if (f === 3) v += (a + b) >> 1;
      else if (f === 4) { const pa = Math.abs(b - c), pb = Math.abs(a - c), pc = Math.abs(a + b - 2 * c); v += pa <= pb && pa <= pc ? a : pb <= pc ? b : c; }
      px[o + x] = v & 255;
    }
  }
  for (let i = 0; i < w * h; i++) for (let k = 0; k < 3; k++) rgb[i * 3 + k] = px[i * bpp + k];
  return { w, h, rgb };
}

const file = (kind: string, map: number, gx: number, gy: number, ext: string) => `${DATA}/${kind}/${map}/${gx}_${gy}.${ext}`;

/** The minimap image of a grid square (PNG), or null when there's none (run cmd/clientdata extract). */
export function minimapTile(map: number, gx: number, gy: number): Buffer | null {
  const f = file("minimap", map, gx, gy, "png");
  return existsSync(f) ? readFileSync(f) : null;
}

const decoded = new Map<string, { w: number; h: number; rgb: Uint8Array } | null>();
/** The minimap colour at a world point, or null. */
export function minimapAt(map: number, x: number, y: number): [number, number, number] | null {
  const gx = grid(x), gy = grid(y), key = `${map}/${gx}/${gy}`;
  if (!decoded.has(key)) { const t = minimapTile(map, gx, gy); decoded.set(key, t ? decodePNG(t) : null); }
  const t = decoded.get(key);
  if (!t) return null;
  const r = Math.min(t.h - 1, Math.floor(((S * (32 - gx) - x) / S) * t.h)), c = Math.min(t.w - 1, Math.floor(((S * (32 - gy) - y) / S) * t.w));
  const o = (r * t.w + c) * 3;
  return [t.rgb[o], t.rgb[o + 1], t.rgb[o + 2]];
}

const roadTiles = new Map<string, Uint8Array | null>();
/** How much of the ground at a world point is road (0-255; 0 off the road layers or without client data). */
export function roadAt(map: number, x: number, y: number): number {
  const gx = grid(x), gy = grid(y), key = `${map}/${gx}/${gy}`;
  if (!roadTiles.has(key)) { const f = file("roads", map, gx, gy, "bin"); roadTiles.set(key, existsSync(f) ? new Uint8Array(readFileSync(f)) : null); }
  const t = roadTiles.get(key);
  if (!t) return 0;
  const n = 128, r = Math.min(n - 1, Math.floor(((S * (32 - gx) - x) / S) * n)), c = Math.min(n - 1, Math.floor(((S * (32 - gy) - y) / S) * n));
  return t[r * n + c];
}

/** Whether client data was extracted (for messages and legends). */
export const hasClientData = () => existsSync(`${DATA}/minimap`);
