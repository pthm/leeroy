// bun server/recorder/live.ts [log.jsonl ...] [--port 8800] [--chars A,B] [--since 60m]
// Live run viewer: tails recorder-format logs (default: the server's mod-recorder log; also azsdk -record
// files, several at once) and serves the replay viewer in live mode at http://localhost:<port>/ with the map
// as terrain tiles rendered on demand. Events stream to the browser as server-sent events; the playhead
// follows the newest data until you seek back.
import { existsSync, openSync, readSync, statSync, closeSync, readFileSync } from "node:fs";
import { S, areas, itemNames, labels, nameUnits, parseLines, renderMap, type Ev } from "./map.ts";

const args = process.argv.slice(2);
const flag = (k: string) => { const i = args.indexOf(`--${k}`); return i >= 0 ? args[i + 1] : undefined; };
const files = args.filter((a, i) => !a.startsWith("--") && !args[i - 1]?.startsWith("--"));
if (!files.length) files.push(new URL("../azerothcore/env/dist/logs/recorder.jsonl", import.meta.url).pathname);
const port = Number(flag("port") ?? 8800), chars = flag("chars")?.split(",") ?? [];
const sinceMin = Number((flag("since") ?? "60m").replace(/m$/, ""));
const start = Date.now() - sinceMin * 60_000;

// ---------- tail the logs ----------
const events: Ev[] = []; // everything since `start`, in arrival order
const clients = new Set<(e: Ev) => void>();
const items = new Map<number, { name: string; q: number }>();
const offsets = new Map<string, number>(), partial = new Map<string, string>();
function poll() {
  const fresh: Ev[] = [];
  for (const f of files) {
    if (!existsSync(f)) continue;
    const size = statSync(f).size, from = offsets.get(f) ?? 0;
    if (size < from) offsets.set(f, 0); // truncated or rotated: start over
    if (size <= from) continue;
    const fd = openSync(f, "r"), buf = Buffer.alloc(size - from);
    readSync(fd, buf, 0, buf.length, from);
    closeSync(fd);
    offsets.set(f, size);
    const text = (partial.get(f) ?? "") + buf.toString("utf8"), cut = text.lastIndexOf("\n") + 1;
    partial.set(f, text.slice(cut));
    fresh.push(...parseLines(text.slice(0, cut)).filter((e) => e.t >= start && (!chars.length || chars.includes(e.n ?? ""))));
  }
  fresh.sort((a, b) => a.t - b.t);
  nameUnits(fresh);
  const loot = fresh.filter((e) => e.ev === "loot" && !items.has(Number(e.item))).map((e) => Number(e.item));
  for (const [id, info] of Object.entries(itemNames([...new Set(loot)]))) items.set(Number(id), info);
  for (const e of fresh) {
    if (e.ev === "loot") e.itemInfo = items.get(Number(e.item));
    events.push(e);
    for (const send of clients) send(e);
  }
}
poll();
setInterval(poll, 500);

// ---------- viewer data ----------
const zones: Record<number, string> = {};
for (const m of [0, 1]) for (const [id, name] of areas(m).names) zones[id] = name;
const allLabels = [...labels(0), ...labels(1)];
const page = () => readFileSync(new URL("replay.html", import.meta.url).pathname, "utf8")
  .replace("__TITLE__", "live")
  .replace("__MAP__", "")
  .replace("__DATA__", JSON.stringify({
    // the clock starts at the oldest event we have (not --since), so the timeline isn't mostly empty
    live: true, title: "live", start: events[0]?.t ?? Date.now(), duration: Date.now() - (events[0]?.t ?? Date.now()), chars, players: [], events: [], zones, items: {},
    labels: allLabels, map: { id: 0, xMax: S * 32, yMax: S * 32, res: 2 }, // map origin at the grid corner, 2 yd/px
  }).replace(/</g, "\\u003c"));

const tiles = new Map<string, Buffer>();
function tile(map: number, gx: number, gy: number) {
  const key = `${map}/${gx}/${gy}`;
  if (!tiles.has(key)) {
    const xMax = S * (32 - gx), yMax = S * (32 - gy);
    tiles.set(key, renderMap(map, xMax - S, xMax, yMax - S, yMax, 2, [-60, 420]).png); // fixed colour scale so tiles match
  }
  return tiles.get(key)!;
}

Bun.serve({
  port,
  idleTimeout: 0, // event streams stay open
  fetch(req) {
    const url = new URL(req.url);
    if (url.pathname === "/") return new Response(page(), { headers: { "content-type": "text/html; charset=utf-8" } });
    const t = url.pathname.match(/^\/tile\/(\d+)\/(\d+)\/(\d+)\.png$/);
    if (t) return new Response(tile(+t[1], +t[2], +t[3]), { headers: { "content-type": "image/png", "cache-control": "max-age=86400" } });
    if (url.pathname === "/stream") {
      const since = Number(url.searchParams.get("since") ?? start);
      let send: (e: Ev) => void;
      return new Response(new ReadableStream({
        start(ctl) {
          const enc = new TextEncoder();
          send = (e) => { try { ctl.enqueue(enc.encode(`data: ${JSON.stringify(e)}\n\n`)); } catch { clients.delete(send); } };
          for (const e of events) if (e.t >= since) send(e);
          clients.add(send);
        },
        cancel() { clients.delete(send); },
      }), { headers: { "content-type": "text/event-stream", "cache-control": "no-cache" } });
    }
    return new Response("not found", { status: 404 });
  },
});
console.log(`live viewer on http://localhost:${port}/ (${files.join(", ")}; ${events.length} events from the last ${sinceMin} min)`);
