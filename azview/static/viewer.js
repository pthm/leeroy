// The viewer's client: the map (a canvas), the seek bar and playback. The server holds the run; this asks it for the
// samples around the playhead (/samples), a selected player's route (/route) and new data as it arrives (/live), and
// tells the htmx panels (roster, feed, stats) when to refresh. UI state is an Alpine store, $store.v.
"use strict";
const B = JSON.parse(document.getElementById("boot").textContent); // start, players, maps, labels, marks, ticks, ...
const $ = (id) => document.getElementById(id);
const fmt = (ms) => { const s = Math.max(0, Math.floor(ms / 1000)), h = Math.floor(s / 3600), m = Math.floor(s / 60) % 60, ss = String(s % 60).padStart(2, "0");
  return h ? `${h}:${String(m).padStart(2, "0")}:${ss}` : `${m}:${ss}`; };
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
const COLORS = {}; // CSS variables, read once (getComputedStyle per draw call was most of a frame)
const css = (v) => (COLORS[v] ??= getComputedStyle(document.documentElement).getPropertyValue(v).trim());
const CLASS_COLOR = { Warrior: "#C79C6E", Paladin: "#F58CBA", Hunter: "#ABD473", Rogue: "#FFF569", Priest: "#FFFFFF",
  "Death Knight": "#C41F3B", Shaman: "#0070DE", Mage: "#69CCF0", Warlock: "#9482C9", Druid: "#FF7D0A" };

// ---------- UI state ----------
// what the panels are asked for (viewerQuery) and what persists in localStorage
const saved = (() => { try { return JSON.parse(localStorage.getItem("azview") || "{}"); } catch { return {}; } })();
const V = {
  t: 0, sel: null, grp: null, tab: "feed", sort: "score", bygroup: true,
  cats: Object.fromEntries(B.cats.map((c) => [c.key, c.on])),
  metric: "dmg", range: "all", chart: "total", per: "0", heat: false, heatKind: "time", roads: false, nav: false,
  trails: true, transports: true, foliage: 1, groups: true, targets: true, marks: true, units: true, unitNames: "targets", aggro: false, combat: true,
  ...saved,
};
V.sel = V.grp = null; V.t = 0;
const PERSIST = ["tab", "sort", "bygroup", "cats", "metric", "range", "chart", "per", "heat", "heatKind", "roads", "nav", "trails", "transports", "foliage", "groups", "targets", "marks", "units", "unitNames", "aggro", "combat"];
document.addEventListener("alpine:init", () => {
  Alpine.store("v", V);
  Alpine.effect(() => { // any change to what the panels show: save it and refresh them
    const s = Alpine.store("v"), keep = Object.fromEntries(PERSIST.map((k) => [k, JSON.parse(JSON.stringify(s[k]))]));
    try { localStorage.setItem("azview", JSON.stringify(keep)); } catch {}
    s.sel; s.grp; for (const k of PERSIST) s[k]; panelsNow(); dirty = true;
  });
});
const store = () => window.Alpine?.store("v") ?? V;
window.viewerQuery = () => { const s = store();
  return { t: Math.round(B.start + t), sel: s.sel ?? "", grp: s.grp ?? "", tab: s.tab, sort: s.sort, bygroup: s.bygroup ? 1 : 0,
    cats: Object.keys(s.cats).filter((k) => s.cats[k]).join(","), metric: s.metric, range: s.range, chart: s.chart, per: s.per,
    w: Math.round(document.getElementById("pane")?.clientWidth ?? 0) }; };

// panels refresh at most 8 times a second (Stats 4), when the playhead moved, data arrived or what they show changed
// (each its own trigger, so the roster keeps its pace on the Stats tab)
let panelsDue = true, rosterDue = true, lastPanels = 0, lastRoster = 0;
const panelsNow = () => { panelsDue = rosterDue = true; lastPanels = lastRoster = 0; };
function panels(now) {
  if (!window.htmx || holding) return;
  if (rosterDue && now - lastRoster >= 125) { rosterDue = false; lastRoster = now; htmx.trigger(document.body, "roster"); }
  // Stats recomputes its window on the server each time (~15 ms for a whole long run): four times a second
  if (panelsDue && now - lastPanels >= (store().tab === "stats" ? 250 : 125)) { panelsDue = false; lastPanels = now; htmx.trigger(document.body, "pane"); }
}
let holding = false; // a button is down on a list: don't swap it under the pointer (a click needs press and release on one element)
addEventListener("pointerdown", (e) => { if (e.target.closest?.("aside, #rlist")) holding = true; });
addEventListener("pointerup", () => { holding = false; }); addEventListener("pointercancel", () => { holding = false; });
document.addEventListener("htmx:beforeSwap", (e) => { // the feed sticks to its bottom
  const p = e.detail.target; if (p.id === "pane") p.dataset.bottom = p.scrollHeight - p.scrollTop - p.clientHeight < 40 ? "1" : "";
});
document.addEventListener("htmx:afterSwap", (e) => { const p = e.detail.target; if (p.id === "pane" && p.dataset.bottom && store().tab === "feed") p.scrollTop = p.scrollHeight; });

// ---------- players and their samples ----------
// P: name -> {name, cls, color, nth, dash}. S: name -> samples in time order [{t, x, y, m, a, g, c, grp, ld, tg}],
// times relative to B.start; U: name -> units samples; R: name -> a selected player's whole route [[t, x, y, m]].
// TR: transport id -> {n, s: [{t, m, x, y, o}]}, boats and zeppelins.
const P = new Map(), S = {}, U = {}, R = {}, TR = {};
const trSample = ([tt, m, x, y, o]) => ({ t: tt - B.start, m, x, y, o });
function player(name, cls) {
  let p = P.get(name);
  if (!p) { p = { name, cls: "" }; P.set(name, p); }
  if (cls && !p.cls) p.cls = cls;
  styleByClass(); return p;
}
function styleByClass() { // class colours; a second player of a class gets #2 and a dashed line
  const seen = {};
  for (const p of P.values()) { p.color = CLASS_COLOR[p.cls] ?? "#888888"; p.nth = (seen[p.cls] = (seen[p.cls] ?? 0) + 1); p.dash = p.nth > 1 ? [6, 4] : []; }
}
for (const p of B.players) player(p.name, p.cls);
const rel = (s) => ({ ...s, t: s.t - B.start });
const sampleT = (s) => s.t;
// the index of the first element after t in a list sorted by time
const upto = (a, t, at = sampleT) => { let lo = 0, hi = a.length; while (lo < hi) { const m = (lo + hi) >> 1; if (at(a[m]) <= t) lo = m + 1; else hi = m; } return lo; };
const ins = (a, v) => { let i = a.length; while (i > 0 && a[i - 1].t > v.t) i--; a.splice(i, 0, v); };
// a jump between two samples: a gap of more than 5 s (logged out), another map, or faster than 40 yd/s (a teleport:
// the hearthstone, released to a graveyard, a summon, a flight's end)
const TELEPORT = 0.04; // yd per ms
const jump = (t0, x0, y0, m0, t1, x1, y1, m1) => t1 - t0 > 5000 || m0 !== m1 || Math.hypot(x1 - x0, y1 - y0) > Math.max(30, (t1 - t0) * TELEPORT);
// where a player is at t: the last sample, moved towards the next (held over a jump)
function at(name, t) {
  const a = S[name]; if (!a) return null;
  const i = upto(a, t) - 1; if (i < 0) return null;
  const s = a[i], n = a[i + 1];
  if (!n || jump(s.t, s.x, s.y, s.m, n.t, n.x, n.y, n.m)) return { s, x: s.x, y: s.y, pet: s.pet };
  const f = (t - s.t) / (n.t - s.t), pet = s.pet && n.pet ? [s.pet[0] + (n.pet[0] - s.pet[0]) * f, s.pet[1] + (n.pet[1] - s.pet[1]) * f, s.pet[2], s.pet[3]] : s.pet;
  return { s, x: s.x + (n.x - s.x) * f, y: s.y + (n.y - s.y) * f, pet };
}

// The samples loaded cover [have.from, have.to] (relative ms). Playback and seeking ask for a window around the
// playhead; live data extends it while it reaches the newest data.
let have = { from: 0, to: -1 }, loading = null;
const TRAIL = 15000, BEHIND = 300000; // a trail is 15 s; a selected route fades over 5 minutes
async function need(t) {
  const ahead = Math.max(60000, speed * 20000);
  if (loading || (t - BEHIND >= have.from && t + Math.min(ahead / 2, 30000) <= have.to) || (t > have.to && t <= dataEnd && liveFollow && have.to >= dataEnd - 5000)) return;
  const from = Math.max(0, t - BEHIND - 30000), to = t + ahead;
  loading = fetch(`samples?from=${Math.round(B.start + from)}&to=${Math.round(B.start + to)}`).then((r) => r.json()).then((d) => {
    const overlap = from <= have.to && to >= have.from;
    for (const [n, a] of Object.entries(d.s)) { player(n, d.cls?.[n]); const got = a.map(rel); S[n] = overlap ? merge(S[n] ?? [], got) : got; }
    for (const [n, a] of Object.entries(d.u)) { const got = a.map(rel); U[n] = overlap ? merge(U[n] ?? [], got) : got; }
    if (!overlap) HITS.length = 0;
    for (const h of d.hits ?? []) addHit(rel(h));
    for (const [id, v] of Object.entries(d.tr ?? {})) { const got = v.s.map(trSample); TR[id] = { n: v.n, e: v.e, s: overlap ? merge(TR[id]?.s ?? [], got) : got }; }
    if (!overlap) for (const n of Object.keys(S)) if (!(n in d.s)) S[n] = [];
    have = overlap ? { from: Math.min(from, have.from), to: Math.max(to, have.to) } : { from, to };
    trim(); dirty = true; updateMaps();
  }).catch(() => {}).finally(() => { loading = null; });
}
const merge = (a, b) => { const m = new Map(a.map((s) => [s.t, s])); for (const s of b) m.set(s.t, s); return [...m.values()].sort((x, y) => x.t - y.t); };
function trim() { // keep memory bounded: samples more than 15 minutes from the playhead go
  const lo = t - 900000, hi = t + Math.max(900000, speed * 60000);
  for (const a of [...Object.values(S), ...Object.values(U), ...Object.values(TR).map((v) => v.s)]) { const i = upto(a, lo); if (i > 0) a.splice(0, i); const j = upto(a, hi); if (j < a.length) a.splice(j); }
  have.from = Math.max(have.from, lo); have.to = Math.min(have.to, hi);
}
async function loadRoute(n) {
  if (R[n]) return;
  R[n] = [];
  const a = await fetch(`route?n=${encodeURIComponent(n)}`).then((r) => r.json()).catch(() => []);
  R[n] = a.map((s) => [s[0] - B.start, s[1], s[2], s[3]]); dirty = true;
}

// marks (deaths, achievements, levels, quests) with where they happened, and the seek bar's ticks
const MARKS = B.marks.map(rel), TICKS = B.ticks.map(([tt, k]) => ({ t: tt - B.start, ev: k }));

// ---------- live ----------
// samples and marks as they arrive; the newest data's time (dataEnd) and when anything last came (lastLive)
let dataEnd = B.last - B.start, lastLive = -Infinity, dirty = true;
const es = new EventSource("live");
es.addEventListener("s", (m) => { const d = JSON.parse(m.data), s = rel(d.s); player(d.n, d.cls); live(s.t, false);
  if (have.to >= s.t - 10000) { ins(S[d.n] ??= [], s); have.to = Math.max(have.to, s.t); }
  if (R[d.n]?.length) R[d.n].push([s.t, s.x, s.y, s.m]); });
es.addEventListener("u", (m) => { const d = JSON.parse(m.data), u = rel(d.u); live(u.t, false); if (have.to >= u.t - 10000) ins(U[d.n] ??= [], u); });
es.addEventListener("tr", (m) => { const d = JSON.parse(m.data), s = trSample(d.s), v = (TR[d.id] ??= { n: d.n, e: d.e, s: [] });
  if (have.to >= s.t - 10000) { ins(v.s, s); dirty = true; } }); // not live(): the server's, not the run's
es.addEventListener("hit", (m) => { addHit(rel(JSON.parse(m.data))); dirty = true; });
es.addEventListener("mark", (m) => { const e = rel(JSON.parse(m.data)); ins(MARKS, e); live(e.t); });
es.addEventListener("tick", (m) => { const [tt, k] = JSON.parse(m.data); ins(TICKS, { t: tt - B.start, ev: k }); });
es.addEventListener("data", (m) => { lastLive = performance.now(); shows(JSON.parse(m.data).t - B.start); });
function live(tt, all = true) { dataEnd = Math.max(dataEnd, tt); lastLive = performance.now(); dirty = true; shows(tt, all); }
// Watching live, the panels refresh when something new comes into view (the playhead, LIVE_LAG behind the clock,
// reaches a sample or event that arrived), not every frame: SHOWS holds those times, oldest first.
// Each [time, all]: a sample changes the roster (and Stats), an event (all) the feed too.
const SHOWS = [];
const shows = (tt, all = true) => { let i = SHOWS.length; while (i > 0 && SHOWS[i - 1][0] > tt) i--; SHOWS.splice(i, 0, [tt, all]); if (SHOWS.length > 5000) SHOWS.splice(0, 1000); };

// ---------- map view ----------
// World (x north, y west) -> map pixels at RES yards/px, origin at the corner of the 64x64 grid; tiles are 533-yard grid squares.
const cv = $("map"), cx = cv.getContext("2d"), GRID = 1600 / 3, RES = 2, MAX = GRID * 32;
const M = { id: 0 };
const toPx = (x, y) => [(MAX - y) / RES, (MAX - x) / RES];
let view = { s: 1, ox: 0, oy: 0 };
const scr = (x, y) => { const [px, py] = toPx(x, y); return [view.ox + px * view.s, view.oy + py * view.s]; };
function sizeCanvas() { const r = cv.getBoundingClientRect(); cv.width = r.width * devicePixelRatio; cv.height = r.height * devicePixelRatio; return r; }
function fitBox(xMin, xMax, yMin, yMax) {
  const r = sizeCanvas(), [x0, y0] = toPx(xMax, yMax), [x1, y1] = toPx(xMin, yMin);
  view.s = Math.min(r.width / Math.max(1, x1 - x0), r.height / Math.max(1, y1 - y0));
  view.ox = (r.width - (x1 + x0) * view.s) / 2; view.oy = (r.height - (y1 + y0) * view.s) / 2;
}
const placed = () => [...P.keys()].map((n) => at(n, t)).filter((pp) => pp && pp.s.m === M.id);
function fit() {
  const pts = placed(); if (!pts.length) return sizeCanvas();
  const xs = pts.map((q) => q.x), ys = pts.map((q) => q.y), pad = 120;
  fitBox(Math.min(...xs) - pad, Math.max(...xs) + pad, Math.min(...ys) - pad, Math.max(...ys) + pad);
}
// Auto zoom: while the camera keeps a set of players in frame (a selected group, or everyone with nobody followed),
// it also zooms to fit them; scrolling then adjusts zoomBias, how much closer or further than a snug fit
// Auto follow (autoCam): the camera keeps the selected player (follow), the selected group, or everyone in view; off,
// the map stays where it's put. Dragging turns it off, the button, A or Fit turn it back on.
let autoZoom = saved.autoZoom ?? true, zoomBias = saved.zoomBias ?? 1, autoCam = saved.autoCam ?? true;
// the player the camera follows: the selected one, while auto follow is on (however they were selected)
const followed = () => (autoCam ? store().sel : null);
const saveZoom = () => { try { localStorage.setItem("azview", JSON.stringify({ ...JSON.parse(localStorage.getItem("azview") || "{}"), autoZoom, zoomBias, autoCam })); } catch {} };
// Map, road and navmesh tiles and patrol queries go through one queue: a few at a time, the newest first (what's in
// view now), and the oldest dropped past QUEUE_MAX (forgotten, so they're asked for again if they come back into
// view): zoomed out over a continent, a square each was thousands of requests at once (ERR_INSUFFICIENT_RESOURCES).
const LOADS = 8, QUEUE_MAX = 300, loadQueue = [];
let loadsInFlight = 0;
function pumpLoads() {
  while (loadsInFlight < LOADS && loadQueue.length) {
    const job = loadQueue.pop(); loadsInFlight++;
    job.start(() => { loadsInFlight--; dirty = true; pumpLoads(); });
  }
}
function queueLoad(start, dropped) {
  loadQueue.push({ start, dropped });
  if (loadQueue.length > QUEUE_MAX) loadQueue.shift().dropped();
  pumpLoads();
}
// queuedImage: an Image in cache under key, loaded through the queue
function queuedImage(cache, key, src) {
  const im = new Image(); cache.set(key, im);
  queueLoad((done) => { im.onload = im.onerror = done; im.src = src; }, () => { if (cache.get(key) === im) cache.delete(key); });
  return im;
}
const tiles = new Map();
function drawTiles(w, h) {
  const wx = (sx, sy) => [MAX - ((sy - view.oy) / view.s) * RES, MAX - ((sx - view.ox) / view.s) * RES];
  const [xa, ya] = wx(0, 0), [xb, yb] = wx(w, h), g = (v) => Math.floor(32 - v / GRID);
  for (let gx = g(xa); gx <= g(xb); gx++) for (let gy = g(ya); gy <= g(yb); gy++) {
    if (gx < 0 || gy < 0 || gx > 63 || gy > 63) continue;
    const key = `${M.id}/${gx}/${gy}`;
    const im = tiles.get(key) ?? queuedImage(tiles, key, `tile/${key}.png?v=${B.tileVersion}`);
    if (!im.complete || !im.naturalWidth) continue;
    const [sx, sy] = scr(GRID * (32 - gx), GRID * (32 - gy)), size = (GRID / RES) * view.s;
    cx.drawImage(im, sx, sy, size + 0.5, size + 0.5);
  }
  drawDetail(xa, ya, xb, yb);
}
// Zoomed in past the minimap's own resolution (a pixel per ~2 yd), detail tiles: a square cut into 2^level x 2^level,
// each 256 px (/detail, world.DetailTile). Between two levels the finer one fades in as you zoom (by how far between
// them the zoom is), over the coarser; a tile that arrives fades in over FADE_MS rather than popping. A sub-square
// whose tile hasn't come yet shows its part of a coarser one already here.
const detail = new Map(), DETAIL_LOADS = 12, FADE_MS = 250; // at most this many tiles on their way (the browser runs six at once)
function drawDetail(xa, ya, xb, yb) {
  if (!B.detail?.includes(M.id)) return;
  const zoom = Math.log2(((GRID / RES) * view.s) / 256 * 0.9); // 0: a minimap pixel per screen pixel; each level is 1
  const base = Math.max(0, Math.min(B.maxDetail, Math.floor(zoom))), fine = base < B.maxDetail ? base + 1 : 0;
  const mix = fine ? smooth(zoom - base) : 0; // how far the finer level has faded in
  const want = new Set(), missing = [], now = performance.now();
  let fading = false;
  const r = cv.getBoundingClientRect();
  cx.imageSmoothingEnabled = true;
  // one level's tiles over the screen at opacity alpha; the base level fills gaps from coarser tiles already here.
  // bare: the tiles without foliage (detail?leaves=0), keyed with a "~" after the place
  const layer = (L, alpha, fillGaps, bare) => {
    const n = 1 << L, sub = GRID / n, size = (sub / RES) * view.s, idx = (v) => Math.floor((MAX - v) / sub);
    const cr = idx(MAX - ((r.height / 2 - view.oy) / view.s) * RES), cc = idx(MAX - ((r.width / 2 - view.ox) / view.s) * RES);
    for (let R = Math.max(0, idx(xa)); R <= Math.min(64 * n - 1, idx(xb)); R++) for (let C = Math.max(0, idx(ya)); C <= Math.min(64 * n - 1, idx(yb)); C++) {
      const [sx, sy] = scr(MAX - R * sub, MAX - C * sub);
      for (let l = L; l >= (fillGaps ? 1 : L); l--) { // this level, or the part of a coarser one already here
        const f = 1 << (L - l), rr = Math.floor(R / f), c = Math.floor(C / f), m = 1 << l;
        const key = `${M.id}/${l}/${Math.floor(rr / m)}/${Math.floor(c / m)}/${rr % m}/${c % m}` + (bare ? "~" : "");
        if (l === L) want.add(key);
        const im = detail.get(key);
        if (!im?.complete || !im.naturalWidth) { if (l === L && !im) missing.push([Math.hypot(R - cr, C - cc) + (fillGaps ? 0 : 1e4), key]); continue; }
        const born = Math.min(1, (now - (im.loaded ?? 0)) / FADE_MS); // fading in since it arrived
        if (born < 1) fading = true;
        const part = 256 / f;
        cx.globalAlpha = alpha * (l === L ? born : 1);
        cx.drawImage(im, (C % f) * part, (R % f) * part, part, part, sx, sy, size + 0.5, size + 0.5);
        break;
      }
    }
    cx.globalAlpha = 1;
  };
  // the foliage slider: the bare tiles, then the full ones over them at its opacity, which is the same as foliage that
  // see-through (only one of them at either end)
  const leaves = +store().foliage, levels = (L, alpha, fill) => {
    if (leaves < 0.99) layer(L, alpha, fill, true);
    if (leaves > 0.01) layer(L, leaves < 0.99 ? alpha * leaves : alpha, fill, false);
  };
  if (base) levels(base, 1, true);
  if (fine && mix > 0.01) levels(fine, mix, false);
  // tiles on their way that aren't wanted any more (zoomed or panned past) are dropped, and the nearest to the
  // middle of the screen are asked for first, the coarser level before the finer
  let loading = 0;
  for (const [key, im] of detail) if (!im.complete) { if (want.has(key)) loading++; else { im.src = ""; detail.delete(key); } }
  missing.sort((a, b) => a[0] - b[0]);
  for (const [, key] of missing.slice(0, Math.max(0, DETAIL_LOADS - loading))) {
    const im = new Image(); im.onload = () => { im.loaded = performance.now(); dirty = true; }; im.onerror = () => (dirty = true);
    im.src = `detail/${key.replace("~", "")}.jpg?v=${B.detailVersion}${key.endsWith("~") ? "&leaves=0" : ""}`; detail.set(key, im);
    if (detail.size > 1500) for (const [k, old] of detail) { if (old.complete && !want.has(k)) { detail.delete(k); break; } } // the oldest drawn one
  }
  if (missing.length || fading) dirty = true; // more to ask for, or a tile still fading in: draw again next frame
}
const smooth = (x) => { x = Math.max(0, Math.min(1, x)); return x * x * (3 - 2 * x); };
// ---------- stats plots ----------
// The Stats tab's chart and activity strip, drawn from the data the server sends with the panel (#plots: the metric's
// cells and a line per player, the strip's cells and each player's seconds in each state). They share one time axis
// and margins (PL, PR), so a moment is at the same x in each; hovering either shows a crosshair in both (hoverT, a
// time), and hovering a player (a row in the list, a strip row) brings their line forward (hoverN).
const PL = 66, PR = 66;
let hoverT = null, hoverN = null, plots = null, chartGeom = null, stripGeom = null;
const cellEnd = (g, k) => Math.min(g.g0 + (k + 1) * g.step, g.t1);
function drawStats() {
  const el = document.getElementById("plots");
  plots = el ? JSON.parse(el.textContent) : null;
  if (!plots) return;
  drawChart(); drawStrip();
}
function canvasFor(id, h) {
  const c = document.getElementById(id); if (!c) return null;
  if (h) c.style.height = h + "px";
  const r = c.getBoundingClientRect(), g = c.getContext("2d");
  c.width = r.width * devicePixelRatio; c.height = r.height * devicePixelRatio; g.setTransform(devicePixelRatio, 0, 0, devicePixelRatio, 0, 0);
  return { c, r, g };
}
function drawChart() {
  const d = plots.chart, cv2 = canvasFor("chart", 170); if (!cv2 || !d) return;
  const { r, g } = cv2, W = r.width - PL - PR, H = r.height - 18 - 8, a0 = d.g0, a1 = d.g0 + d.nb * d.step;
  const X = (k) => PL + ((cellEnd(d, k) - a0) / (a1 - a0)) * W, Y = (v) => 6 + H - ((v - d.lo) / (d.hi - d.lo || 1)) * H;
  chartGeom = { W, a0, a1 };
  g.font = "11px system-ui"; g.fillStyle = css("--quiet"); g.strokeStyle = css("--line"); g.lineWidth = 1;
  for (let k = 0; k <= 3; k++) {
    const y = Math.round(Y(d.lo + ((d.hi - d.lo) * k) / 3)) + 0.5;
    g.setLineDash(k ? [2, 4] : []); g.beginPath(); g.moveTo(PL, y); g.lineTo(PL + W, y); g.stroke();
    g.textAlign = "right"; g.fillText(d.yticks[k], PL - 6, y + 4);
  }
  g.setLineDash([]);
  for (let k = 0; k <= 3; k++) { g.textAlign = ["left", "center", "center", "right"][k]; g.fillText(d.xticks[k], PL + (W * k) / 3, r.height - 3); }
  const focus = hoverN ?? store().sel;
  const series = [...d.series].sort((a, b) => (a.n === focus) - (b.n === focus)); // the focused line on top
  for (const s of series) {
    g.beginPath(); let pen = false;
    s.v.forEach((v, k) => { if (v == null) { pen = false; return; } pen ? g.lineTo(X(k), Y(v)) : g.moveTo(X(k), Y(v)); pen = true; });
    g.globalAlpha = focus && s.n !== focus ? 0.3 : 1;
    if (s.n === focus) { // a soft fill under the focused line
      const pts = s.v.map((v, k) => (v == null ? null : [X(k), Y(v)])).filter(Boolean);
      if (pts.length) { const f = new Path2D(); f.moveTo(pts[0][0], Y(d.lo)); pts.forEach(([x, y]) => f.lineTo(x, y)); f.lineTo(pts.at(-1)[0], Y(d.lo)); f.closePath();
        const grad = g.createLinearGradient(0, 6, 0, 6 + H); grad.addColorStop(0, s.color + "55"); grad.addColorStop(1, s.color + "00"); g.fillStyle = grad; g.fill(f); }
    }
    g.setLineDash(s.dashed ? [6, 4] : []); g.strokeStyle = s.color; g.lineWidth = s.n === focus ? 3 : 1.6; g.stroke(); g.setLineDash([]); g.globalAlpha = 1;
  }
  // the latest values in the right margin, nudged apart so they don't overlap
  g.font = "600 11px system-ui"; g.textAlign = "left";
  const ends = d.series.map((s) => { let k = s.v.length - 1; while (k >= 0 && s.v[k] == null) k--; return k >= 0 ? [s, Y(s.v[k]), s.s[k]] : null; }).filter(Boolean).sort((a, b) => a[1] - b[1]);
  let lastY = -1e9;
  for (const [s, y0, txt] of ends) { const y = Math.max(y0 + 4, lastY + 12); lastY = y;
    g.globalAlpha = focus && s.n !== focus ? 0.4 : 1; g.fillStyle = s.color; g.fillText(txt, PL + W + 6, y, PR - 8); g.globalAlpha = 1; }
  if (hoverT != null) {
    const k = cellAt(d, hoverT), x = X(k);
    crosshair(g, x, 6, 6 + H);
    for (const s of d.series) { const v = s.v[k]; if (v == null) continue;
      g.beginPath(); g.arc(x, Y(v), 3.5, 0, 7); g.fillStyle = s.color; g.fill(); g.strokeStyle = "#000"; g.lineWidth = 1; g.stroke(); }
  }
}
const cellAt = (d, tm) => Math.max(0, Math.min(d.nb - 1, Math.round((tm - d.g0) / d.step) - 1)); // the nearest point
const crosshair = (g, x, y0, y1) => { g.strokeStyle = css("--ink"); g.globalAlpha = 0.55; g.setLineDash([3, 3]); g.lineWidth = 1;
  g.beginPath(); g.moveTo(Math.round(x) + 0.5, y0); g.lineTo(Math.round(x) + 0.5, y1); g.stroke(); g.setLineDash([]); g.globalAlpha = 1; };
function drawStrip() {
  const d = plots.strip; if (!d) return;
  const RH = 12, GAP = 7, cv2 = canvasFor("strip", Math.max(1, d.rows.length) * (RH + GAP) - GAP + 2); if (!cv2) return;
  const { r, g } = cv2, W = Math.max(10, r.width - PL - PR), SW = PR - 10, a0 = chartGeom?.a0 ?? d.g0, a1 = chartGeom?.a1 ?? d.g0 + d.nb * d.step;
  const xOf = (tm) => PL + ((tm - a0) / (a1 - a0)) * W;
  stripGeom = { W, SW, RH, GAP, a0, a1 };
  const focus = hoverN ?? store().sel, colors = d.states.map((s) => s[1]), order = [2, 1, 0, 3, 4]; // a cell's colour: combat over moving over idle...
  g.font = "600 11px system-ui"; g.textBaseline = "middle"; g.textAlign = "left";
  const rr = (x, y, w, h) => { g.beginPath(); g.roundRect(x, y, w, h, 2); g.fill(); };
  d.rows.forEach((row, i) => {
    const y = i * (RH + GAP) + 1;
    g.globalAlpha = focus && row.n !== focus ? 0.4 : 1;
    g.fillStyle = row.color; g.fillText(row.n, 0, y + RH / 2, PL - 8);
    row.cells.forEach((v, k) => {
      const c0 = d.g0 + k * d.step, c1 = cellEnd(d, k), x0 = Math.max(PL, xOf(c0)), x1 = Math.min(PL + W, xOf(c1)); if (x1 - x0 < 1) return;
      let best = -1; for (const st of order) if (v[st] > 0 && (best < 0 || v[st] > v[best])) best = st;
      const covered = v.reduce((a, b) => a + b, 0);
      if (best < 0 || covered < ((c1 - c0) / 1000) * 0.3) { g.fillStyle = css("--line"); rr(x0 + 0.5, y + RH / 2 - 1, x1 - x0 - 1, 2); return; } // logged out
      g.fillStyle = colors[best]; rr(x0 + 0.5, y, Math.max(1, x1 - x0 - 1), RH);
    });
    let x = L0(W); const tot = row.all.reduce((a, b) => a + b, 0) || 1; // the window's split
    for (const st of order) { const f = row.all[st] / tot; if (f <= 0) continue; g.fillStyle = colors[st]; g.fillRect(x, y, f * SW, RH); x += f * SW; }
    g.globalAlpha = 1;
  });
  if (hoverT != null && plots.chart) crosshair(g, xOf(cellEnd(plots.chart, cellAt(plots.chart, hoverT))), 0, r.height); // where the chart's is
}
const L0 = (W) => PL + W + 10;
const pctText = (f) => (f > 0 && f < 0.005 ? "<1%" : Math.round(100 * f) + "%");
function hoverStats(e) {
  const t0 = e.target;
  if (t0.id === "chart" && plots?.chart && chartGeom) {
    const d = plots.chart, x = e.clientX - t0.getBoundingClientRect().left, { W, a0, a1 } = chartGeom;
    const k = cellAt(d, a0 + ((x - PL) / W) * (a1 - a0)), at = cellEnd(d, k); // snapped to a point on the lines
    if (at !== hoverT) { hoverT = at; drawChart(); drawStrip(); }
    const rows = d.series.filter((s) => s.v[k] != null).sort((a, b) => b.v[k] - a.v[k]);
    return tip(`<div class="t">${fmt(at)} · ${esc(d.label)}</div>` + (rows.map((s) => `<div class="tr"><span class="who" style="color:${s.color}">${esc(s.n)}</span><b>${esc(s.s[k])}</b></div>`).join("") || `<div class="muted">nothing</div>`), e);
  }
  if (t0.id === "strip" && plots?.strip && stripGeom) {
    const d = plots.strip, rc = t0.getBoundingClientRect(), { W, RH, GAP, a0, a1 } = stripGeom, x = e.clientX - rc.left - PL, i = Math.floor((e.clientY - rc.top) / (RH + GAP));
    const row = d.rows[i], onAxis = x >= 0 && x <= W ? a0 + (x / W) * (a1 - a0) : null;
    if (hoverN !== (row?.n ?? null) || hoverT !== onAxis) { hoverN = row?.n ?? null; hoverT = onAxis; drawChart(); drawStrip(); }
    if (!row || x < 0) return tip(null);
    const whole = x > W, k = Math.max(0, Math.min(d.nb - 1, Math.floor(((onAxis ?? d.t1) - d.g0) / d.step))), v = whole ? row.all : row.cells[k];
    const when = whole ? `${fmt(d.t0)}–${fmt(d.t1)}` : `${fmt(d.g0 + k * d.step)}–${fmt(cellEnd(d, k))}`, tot = v.reduce((a, b) => a + b, 0);
    return tip(`<div class="t"><span class="who" style="color:${row.color}">${esc(row.n)}</span> · ${when}</div>` + (tot ? [2, 1, 0, 3, 4].filter((st) => v[st] > 0).map((st) =>
      `<div class="tr"><span><span class="sw" style="background:${d.states[st][1]}"></span>${d.states[st][0]}</span><b>${pctText(v[st] / tot)}</b></div>`).join("") : `<div class="muted">logged out</div>`), e);
  }
  const m = t0.closest?.(".mrow[data-n]"), n = m?.dataset.n ?? null; // a player's row in the list
  if (n !== hoverN || hoverT != null) { hoverN = n; hoverT = null; drawChart(); drawStrip(); }
  tip(null);
}
// ---------- combat and aggro ----------
// HITS: damage and heals involving a player, {t, a (amount), k (d dealt, t taken, h healed), n (the player)}, in time
// order: from /samples and /live. Drawn as combat text rising over the player (dealt: over their target) for 1.5 s.
const HITS = [], HIT_MS = 1500;
const addHit = (h) => { ins(HITS, h); if (HITS.length > 20000) HITS.splice(0, HITS.length - 15000); };
function drawCombatText() {
  const by = {};
  cx.font = "bold 12px system-ui"; cx.textAlign = "center"; cx.lineWidth = 3; cx.strokeStyle = "#000c";
  for (let i = upto(HITS, t) - 1; i >= 0 && t - HITS[i].t < HIT_MS; i--) {
    const h = HITS[i], pp = at(h.n, t); if (!pp || pp.s.m !== M.id) continue;
    const k = (by[h.n + h.k] = (by[h.n + h.k] ?? 0) + 1); if (k > 4) continue; // the latest four of each, per player
    let x = pp.x, y = pp.y;
    if (h.k === "d" && pp.s.tg && pp.s.tg[2] === M.id) [x, y] = pp.s.tg; // dealt: over what they hit
    const [sx, sy] = scr(x, y), age = (t - h.t) / HIT_MS, jitter = ((Math.round(h.t / 97) % 5) - 2) * 7;
    cx.globalAlpha = Math.max(0, 1 - age * age);
    cx.fillStyle = h.k === "t" ? "#ff5050" : h.k === "h" ? "#50ff70" : "#ffe040";
    const label = (h.k === "t" ? "-" : h.k === "h" ? "+" : "") + Math.round(h.a);
    cx.strokeText(label, sx + jitter, sy - 14 - 26 * age); cx.fillText(label, sx + jitter, sy - 14 - 26 * age);
  }
  cx.globalAlpha = 1;
  if (HITS.length && t - HITS[upto(HITS, t) - 1]?.t < HIT_MS) dirty = true; // still rising
}
// Aggro radii: around each hostile creature alive, how close a player can come before it attacks, as the server
// works it out (Creature::GetAggroRange): its detection range, a yard less for each level the nearest player is
// above it, at least 5 yd and at most 45.
function drawAggro() {
  const placed = [...P.keys()].map((n) => at(n, t)).filter((pp) => pp && pp.s.m === M.id && pp.s.a);
  if (!placed.length) return;
  const seen = new Set();
  for (const n of P.keys()) {
    const a = U[n]; if (!a) continue;
    const u = a[upto(a, t) - 1]; if (!u || t - u.t > 5000 || u.m !== M.id) continue;
    for (const [name, lvl, x, y, , state, , detect] of u.u) {
      if ((state & 3) !== 0 || state & 4 || !detect) continue; // hostile and alive only
      const key = `${name}/${Math.round(x)}/${Math.round(y)}`; if (seen.has(key)) continue; seen.add(key);
      let near = placed[0], nd = Infinity;
      for (const pp of placed) { const d = Math.hypot(pp.x - x, pp.y - y); if (d < nd) { nd = d; near = pp; } }
      const radius = Math.max(5, Math.min(45, detect - ((near.s.l ?? lvl) - lvl)));
      const [sx, sy] = scr(x, y), rp = radius * yd(), close = nd <= radius;
      cx.beginPath(); cx.arc(sx, sy, rp, 0, 7);
      cx.fillStyle = close ? "#ff30301f" : "#ff303010"; cx.fill();
      cx.lineWidth = close ? 1.5 : 1; cx.strokeStyle = close ? "#ff4040b0" : "#ff404055"; cx.stroke();
    }
  }
}
// ---------- overlays ----------
// The heatmap (/heat: one image of the map's visited area, where the players spent their time up to the playhead) is
// fetched again when the map or the selection changes, or the playhead has moved 20 s (live: every 5 s).
let heat = null, heatKey = "", heatT = -Infinity, heatAt = 0, heatLoading = false;
function drawHeat() {
  const s = store(), key = `${M.id}|${s.sel ?? ""}|${s.grp ?? ""}|${s.heatKind}`, now = performance.now();
  if (!heatLoading && (key !== heatKey || Math.abs(t - heatT) > 20000 || (liveFollow && now - heatAt > 5000))) {
    heatLoading = true; const tt = t;
    fetch(`heat?m=${M.id}&kind=${s.heatKind}&t=${Math.round(B.start + tt)}&sel=${encodeURIComponent(s.sel ?? "")}&grp=${s.grp ?? ""}`).then((r) => r.json()).then((h) => {
      if (!h.png) { heat = null; return; }
      const im = new Image(); im.onload = () => { heat = { ...h, im }; dirty = true; }; im.src = h.png;
    }).catch(() => {}).finally(() => { heatLoading = false; heatKey = key; heatT = tt; heatAt = performance.now(); });
  }
  if (!heat?.im) return;
  const r = cv.getBoundingClientRect();
  cx.fillStyle = "#00000059"; cx.fillRect(0, 0, r.width, r.height); // the map dimmed a little, so the heat reads over it
  const [sx, sy] = scr(heat.x0, heat.y0), k = (heat.res / RES) * view.s;
  cx.imageSmoothingEnabled = true; cx.globalAlpha = 0.85;
  cx.drawImage(heat.im, sx, sy, heat.w * k, heat.h * k);
  cx.globalAlpha = 1;
}
// The navmesh (/nav, world.NavTile): a continent's walkable floors at the level matching the zoom, like the detail
// tiles (level 0: a tile a square); a sub-square whose tile hasn't come shows its part of a coarser one.
const navTiles = new Map();
function drawNav(w, h) {
  const wx = (sx, sy) => [MAX - ((sy - view.oy) / view.s) * RES, MAX - ((sx - view.ox) / view.s) * RES];
  const [xa, ya] = wx(0, 0), [xb, yb] = wx(w, h);
  const L = Math.max(0, Math.min(B.maxDetail, Math.ceil(Math.log2(((GRID / RES) * view.s) / 256 * 0.9))));
  const n = 1 << L, sub = GRID / n, size = (sub / RES) * view.s, idx = (v) => Math.floor((MAX - v) / sub);
  cx.imageSmoothingEnabled = view.s < 4; cx.globalAlpha = 0.9;
  for (let R = Math.max(0, idx(xa)); R <= Math.min(64 * n - 1, idx(xb)); R++) for (let C = Math.max(0, idx(ya)); C <= Math.min(64 * n - 1, idx(yb)); C++) {
    const [sx, sy] = scr(MAX - R * sub, MAX - C * sub);
    for (let l = L; l >= 0; l--) {
      const f = 1 << (L - l), rr = Math.floor(R / f), c = Math.floor(C / f), m = 1 << l;
      const key = `${M.id}/${l}/${Math.floor(rr / m)}/${Math.floor(c / m)}/${rr % m}/${c % m}`;
      let im = navTiles.get(key);
      if (!im && l === L) { im = queuedImage(navTiles, key, `nav/${key}.png?v=${B.navVersion}`);
        if (navTiles.size > 800) navTiles.delete(navTiles.keys().next().value); }
      if (!im?.complete || !im.naturalWidth) continue;
      const part = 256 / f;
      cx.drawImage(im, (C % f) * part, (R % f) * part, part, part, sx, sy, size + 0.5, size + 0.5);
      break;
    }
  }
  cx.globalAlpha = 1;
}
// Roads: each grid square's painted roads (/roads, amber, from the client's ground textures) and the friendly NPCs'
// patrol paths crossing it (/patrols, dashed): the network the SDK's routes prefer.
const roadTiles = new Map(), patrolPaths = new Map(), ROAD_SQUARES = new Set(B.roads ?? []); // squares with roads: "map/gx/gy"
function drawRoads(w, h) {
  const wx = (sx, sy) => [MAX - ((sy - view.oy) / view.s) * RES, MAX - ((sx - view.ox) / view.s) * RES];
  const [xa, ya] = wx(0, 0), [xb, yb] = wx(w, h), g = (v) => Math.floor(32 - v / GRID), size = (GRID / RES) * view.s;
  cx.imageSmoothingEnabled = true;
  for (let gx = Math.max(0, g(xa)); gx <= Math.min(63, g(xb)); gx++) for (let gy = Math.max(0, g(ya)); gy <= Math.min(63, g(yb)); gy++) {
    const key = `${M.id}/${gx}/${gy}`;
    if (ROAD_SQUARES.has(key)) { // v: the road rasters' version (2: more road textures, bridges)
      const im = roadTiles.get(key) ?? queuedImage(roadTiles, key, `roads/${key}?v=2`);
      if (im.complete && im.naturalWidth) { const [sx, sy] = scr(GRID * (32 - gx), GRID * (32 - gy)); cx.globalAlpha = 0.8; cx.drawImage(im, sx, sy, size + 0.5, size + 0.5); cx.globalAlpha = 1; }
    }
    if (size >= 40 && !patrolPaths.has(key)) { // patrol paths only near enough to tell them apart
      patrolPaths.set(key, []);
      queueLoad((done) => fetch(`patrols?m=${M.id}&gx=${gx}&gy=${gy}`).then((r) => r.json()).then((p) => patrolPaths.set(key, p)).catch(() => {}).finally(done),
        () => patrolPaths.delete(key));
    }
  }
  const drawn = new Set();
  cx.lineWidth = 2; cx.setLineDash([6, 5]); cx.strokeStyle = "#ffe6a8cc"; cx.lineJoin = "round";
  for (const [key, paths] of patrolPaths) {
    if (!key.startsWith(`${M.id}/`)) continue;
    for (const p of paths) {
      const id = p[0].join(",") + p.length; if (drawn.has(id) || p.length < 2) continue; drawn.add(id); // a path crossing several squares, once
      cx.beginPath(); p.forEach(([x, y], i) => { const [sx, sy] = scr(x, y); i ? cx.lineTo(sx, sy) : cx.moveTo(sx, sy); }); cx.stroke();
    }
  }
  cx.setLineDash([]);
}
const groupMembers = () => { const s = store(); if (s.grp == null) return null; const g = Number(s.grp);
  return new Set([...P.keys()].filter((n) => at(n, t)?.s.grp === g)); };
const focusSet = () => { const s = store(); return s.sel ? new Set([s.sel]) : groupMembers(); };
let markerHits = [];
let combatShown = false, unitHits = []; // unitHits: each creature drawn, [x, y, radius, name, level, health %, state], for hovering
function drawMap() {
  const r = cv.getBoundingClientRect(), s = store(), sel = s.sel;
  combatShown = false;
  // the camera: on the followed player; or on the middle of the players it keeps in frame (the selected group, or
  // everyone), with auto zoom easing towards a fit of them (at least 80 yd across, 45 yd of room around), times zoomBias
  let centre = null;
  let gm = s.grp != null ? groupMembers() : null;
  if (gm && !gm.size) { s.grp = null; gm = null; } // the selected group has broken up (or isn't formed yet): back to everyone
  const follow = followed(), frame = !autoCam || follow ? null : gm ? [...gm] : [...P.keys()];
  if (follow) centre = at(follow, t);
  else if (frame) {
    const pts = frame.map((n) => at(n, t)).filter((pp) => pp && pp.s.m === M.id);
    if (pts.length) {
      const xs = pts.map((q) => q.x), ys = pts.map((q) => q.y), pad = 45, half = 40;
      const hx = Math.max(half, (Math.max(...xs) - Math.min(...xs)) / 2 + pad), hy = Math.max(half, (Math.max(...ys) - Math.min(...ys)) / 2 + pad);
      centre = { x: (Math.min(...xs) + Math.max(...xs)) / 2, y: (Math.min(...ys) + Math.max(...ys)) / 2 };
      if (autoZoom) { const want = zoomBias * Math.min(r.width / ((2 * hy) / RES), r.height / ((2 * hx) / RES)); view.s += (want - view.s) * 0.12; } // map x is screen y
    }
  }
  if (centre) { const [px, py] = toPx(centre.x, centre.y); view.ox = r.width / 2 - px * view.s; view.oy = r.height / 2 - py * view.s; }
  cx.setTransform(devicePixelRatio, 0, 0, devicePixelRatio, 0, 0);
  cx.clearRect(0, 0, r.width, r.height);
  cx.imageSmoothingEnabled = view.s < 2;
  drawTiles(r.width, r.height);
  if (sel || s.grp != null) { cx.fillStyle = "#0000004d"; cx.fillRect(0, 0, r.width, r.height); } // dim the map so the selection stands out
  if (s.nav && [0, 1, 530, 571].includes(M.id)) drawNav(r.width, r.height); // an instance is drawn from its navmesh already
  if (s.heat) drawHeat();
  if (s.roads) drawRoads(r.width, r.height);
  // labels
  cx.textAlign = "center";
  for (const l of B.labels) {
    if (l.map !== M.id || (!l.zone && view.s < 0.9)) continue;
    const [sx, sy] = scr(l.x, l.y);
    cx.font = l.zone ? "600 15px system-ui" : "12px system-ui";
    cx.lineWidth = 3; cx.strokeStyle = "#000a"; cx.fillStyle = l.zone ? "#fff" : "#fffd";
    cx.strokeText(l.name, sx, sy); cx.fillText(l.name, sx, sy);
  }
  if (s.transports) drawTransports(t);
  // creatures around the players: hostile red, neutral yellow, friendly green, dead grey; named when zoomed in
  const UNIT_COLOR = ["#e04040", "#e8c040", "#40c050"], drawn = new Set();
  if (s.aggro) drawAggro();
  cx.font = "11px system-ui"; cx.textAlign = "left";
  // their names: by the Layers menu's choice (the players' targets, hostile ones, everyone, nobody), drawn after the
  // players so a player's name always wins, and skipped where one would overlap a name already drawn
  const unitLabels = [];
  unitHits = [];
  const aims = s.unitNames === "targets" ? [...P.keys()].map((n) => at(n, t)?.s.tg).filter((g) => g && g[2] === M.id) : [];
  if (s.units) for (const n of P.keys()) {
    const a = U[n]; if (!a) continue;
    const u = a[upto(a, t) - 1]; if (!u || t - u.t > 5000 || u.m !== M.id) continue;
    for (const [name, lvl, x, y, hp, state, rad] of u.u) {
      const key = `${name}/${Math.round(x)}/${Math.round(y)}`; if (drawn.has(key)) continue; drawn.add(key);
      const [sx, sy] = scr(x, y), dead = state & 4, r = Math.max(dead ? 2.5 : 3.5, (rad || 0.5) * yd());
      cx.beginPath(); cx.arc(sx, sy, r, 0, 7);
      cx.fillStyle = dead ? "#777a" : UNIT_COLOR[state & 3] ?? "#aaa"; cx.fill();
      cx.lineWidth = 1; cx.strokeStyle = "#000a"; cx.stroke();
      unitHits.push([sx, sy, r, name, lvl, hp, state]);
      if (dead) continue;
      const aimed = aims.some((g) => Math.hypot(g[0] - x, g[1] - y) < 2);
      const named = s.unitNames === "all" ? view.s >= 3 : s.unitNames === "hostile" ? view.s >= 3 && (state & 3) === 0 : s.unitNames === "targets" ? aimed && view.s >= 1.5 : false;
      if (named) unitLabels.push([sx, sy, r, `${name} ${lvl}${hp < 100 ? ` ${hp}%` : ""}`, aimed]);
    }
  }
  // routes are smoothed a little: each corner is cut with a curve through the midpoints of its two sides
  const mid = (a, b) => [(a[0] + b[0]) / 2, (a[1] + b[1]) / 2];
  const smooth = (pts, finish = true) => { cx.moveTo(pts[0][0], pts[0][1]);
    for (let i = 1; i < pts.length - 1; i++) { const m = mid(pts[i], pts[i + 1]); cx.quadraticCurveTo(pts[i][0], pts[i][1], m[0], m[1]); }
    if (finish && pts.length > 1) cx.lineTo(pts.at(-1)[0], pts.at(-1)[1]); };
  // a route that fades with age: solid at the player, down to `floor` for points older than fadeMs
  const fadeLine = (color, pts, fadeMs, floor, [ow, w]) => {
    let k = pts.findIndex((q) => t - q[2] <= fadeMs); if (k < 0) k = pts.length - 1;
    const alpha = (q) => floor + (1 - floor) * Math.max(0, 1 - (t - q[2]) / fadeMs);
    for (const outline of [true, false]) {
      cx.strokeStyle = outline ? "#000" : color; cx.lineWidth = outline ? ow : w;
      if (k > 0 && floor > 0) { cx.beginPath(); smooth(pts.slice(0, k + 1), false); cx.globalAlpha = outline ? floor * 0.5 : floor; cx.stroke(); }
      for (let j = k; j < pts.length - 1; j++) {
        const a = j ? mid(pts[j - 1], pts[j]) : pts[0], c = pts[j], b = j < pts.length - 2 ? mid(pts[j], pts[j + 1]) : pts[j + 1];
        const al = alpha(j ? c : pts[0]) * 0.95; if (al <= 0.02) continue;
        cx.beginPath(); cx.moveTo(a[0], a[1]); j ? cx.quadraticCurveTo(c[0], c[1], b[0], b[1]) : cx.lineTo(b[0], b[1]);
        cx.globalAlpha = outline ? al * 0.5 : al; cx.stroke();
      }
    }
    cx.globalAlpha = 1;
  };
  // points [t, x, y, m] from..to on this map as screen points, in runs split at jumps (logged out, teleports: no line
  // across the map from where they died to the graveyard, or hearthed from to the inn)
  const runs = (pts, from, to, tail) => { const out = []; let cur = [], prev = null;
    for (let k = from; k <= to; k++) {
      const q = pts[k];
      if (q[3] !== M.id || (prev && jump(prev[0], prev[1], prev[2], prev[3], q[0], q[1], q[2], q[3]))) { if (cur.length) out.push(cur); cur = []; }
      if (q[3] === M.id) cur.push([...scr(q[1], q[2]), q[0]]);
      prev = q;
    }
    if (tail) cur.push([...scr(tail.x, tail.y), t]);
    if (cur.length) out.push(cur); return out; };
  cx.lineCap = cx.lineJoin = "round";
  // the selected player's whole route: ahead faint, travelled fading over the last 5 minutes to a third
  if (sel && R[sel]?.length) {
    const p = P.get(sel), route = R[sel], i = upto(route, t, (q) => q[0]) - 1, pp = at(sel, t);
    // ponytail: the whole route is redrawn each frame (~8 ms for 40k points); cache it in a Path2D per zoom if it matters
    cx.beginPath(); for (const run of runs(route, Math.max(0, i), route.length - 1)) smooth(run);
    cx.globalAlpha = 0.18; cx.lineWidth = 2.5; cx.strokeStyle = "#000"; cx.stroke();
    cx.setLineDash(p?.dash ?? []); cx.lineWidth = 1.6; cx.strokeStyle = p?.color ?? "#888"; cx.stroke(); cx.setLineDash([]); cx.globalAlpha = 1;
    if (i >= 0) for (const run of runs(route, 0, i, pp?.s.m === M.id ? pp : null)) fadeLine(p?.color ?? "#888", run, BEHIND, 0.35, [3, 1.8]);
  }
  // with nobody selected, a short trail behind everyone, fading out with age
  if (!sel && s.trails) for (const p of P.values()) {
    const a = S[p.name], pp = at(p.name, t); if (!a || !pp || pp.s.m !== M.id) continue;
    const i = upto(a, t) - 1; let from = i; while (from > 0 && t - a[from - 1].t <= TRAIL) from--;
    const pts = runs(a.map((q) => [q.t, q.x, q.y, q.m]), from, i, pp).at(-1); if (!pts || pts.length < 2) continue;
    fadeLine(p.color, pts, TRAIL, 0, [4, 2.2]);
  }
  // targets: a thin red line from each player to what they're targeting, and a health bar over it
  const bars = new Map(), focus = focusSet();
  if (s.targets) for (const n of P.keys()) {
    if (focus && !focus.has(n)) continue;
    const pp = at(n, t), tg = pp?.s.tg; if (!tg || pp.s.m !== M.id || tg[2] !== M.id || t - pp.s.t > 3000) continue;
    const [ax, ay] = scr(pp.x, pp.y), [bx, by] = scr(tg[0], tg[1]);
    cx.globalAlpha = 0.8; cx.strokeStyle = "#ff3b3b"; cx.lineWidth = 1.2; cx.beginPath(); cx.moveTo(ax, ay); cx.lineTo(bx, by); cx.stroke(); cx.globalAlpha = 1;
    bars.set(`${Math.round(bx)}/${Math.round(by)}`, [bx, by, tg[3]]);
  }
  for (const [bx, by, hp] of bars.values()) {
    if (hp < 0) continue;
    const w = 26, h = 4, x = bx - w / 2, y = by - 12;
    cx.fillStyle = "#000c"; cx.fillRect(x - 1, y - 1, w + 2, h + 2);
    cx.fillStyle = hp > 50 ? "#3ccf4e" : hp > 20 ? "#e8c040" : "#e04040"; cx.fillRect(x, y, (w * hp) / 100, h);
  }
  // groups: dashed line from each member to its leader
  const groups = new Map();
  if (s.groups) for (const n of P.keys()) { const pp = at(n, t); if (pp?.s.grp && pp.s.m === M.id) (groups.get(pp.s.grp) ?? groups.set(pp.s.grp, []).get(pp.s.grp)).push([n, pp]); }
  cx.setLineDash([4, 4]); cx.lineWidth = 1.5; cx.strokeStyle = "#fffc";
  for (const members of groups.values()) {
    const head = members.find(([n, pp]) => n === pp.s.ld) ?? members[0], [hx, hy] = scr(head[1].x, head[1].y);
    for (const m of members) { if (m === head) continue; const [mx, my] = scr(m[1].x, m[1].y); cx.beginPath(); cx.moveTo(hx, hy); cx.lineTo(mx, my); cx.stroke(); }
  }
  cx.setLineDash([]);
  // event markers up to now (kept, with where they were drawn, for the hover tooltip)
  markerHits = [];
  cx.font = "bold 13px system-ui"; cx.textAlign = "center"; cx.strokeStyle = "#000"; cx.lineWidth = 1;
  for (let i = 0, end = s.marks ? upto(MARKS, t) : 0; i < end; i++) {
    const e = MARKS[i]; if (e.m !== M.id || e.x == null || (focus && !focus.has(e.n))) continue;
    const [sx, sy] = scr(e.x, e.y); if (sx < -10 || sy < -10 || sx > r.width + 10 || sy > r.height + 10) continue;
    const [g, c] = GLYPH[e.ev]; cx.fillStyle = css(c);
    cx.strokeText(g, sx, sy + 4); cx.fillText(g, sx, sy + 4);
    markerHits.push([sx, sy, e]);
  }
  // pets (pp.pet: [x, y, health % or -1 dead, name]): a smaller, hollow dot in the master's colour, joined to the master
  // by a line (outlined, so it shows over the master's own route); named under the dot, after the players' names
  const petLabels = [], petDots = new Map(); // master -> [x, y, r] of its pet, so its name can keep clear
  for (const p of P.values()) {
    const pp = at(p.name, t), pet = pp?.pet; if (!pet || pp.s.m !== M.id || (focus && !focus.has(p.name))) continue;
    const [ax, ay] = scr(pp.x, pp.y), [px, py] = scr(pet[0], pet[1]), pr = Math.max(4.5, playerR() * 0.75), dead = pet[2] < 0;
    cx.lineCap = "round"; cx.beginPath(); cx.moveTo(ax, ay); cx.lineTo(px, py);
    cx.strokeStyle = "#000c"; cx.lineWidth = 4; cx.stroke(); cx.strokeStyle = dead ? "#888" : p.color; cx.lineWidth = 2; cx.stroke();
    cx.beginPath(); cx.arc(px, py, pr, 0, 7); cx.fillStyle = dead ? "#555" : "#000c"; cx.fill();
    cx.lineWidth = 2.5; cx.strokeStyle = dead ? "#888" : p.color; cx.stroke();
    if (dead) { cx.strokeStyle = css("--death"); cx.lineWidth = 1.5; cx.beginPath(); cx.moveTo(px - 3, py - 3); cx.lineTo(px + 3, py + 3); cx.moveTo(px + 3, py - 3); cx.lineTo(px - 3, py + 3); cx.stroke(); }
    petDots.set(p.name, [px, py, pr]);
    const dx = px - ax, dy = py - ay, len = Math.hypot(dx, dy); // the name on the far side from the master (below when on top of it)
    petLabels.push(len < 2 ? [px, py + pr + 11] : [px + (dx / len) * (pr + 14), py + (dy / len) * (pr + 14) + 4]);
    petLabels.at(-1).push(pet[3] + (dead ? " (dead)" : ""));
  }
  // players now; each name nudged down until it doesn't overlap one already drawn
  const names = [];
  for (const p of P.values()) {
    const pp = at(p.name, t); if (!pp || pp.s.m !== M.id) continue;
    const [sx, sy] = scr(pp.x, pp.y), dead = !pp.s.a, ghost = pp.s.g, r = playerR();
    if (s.combat && pp.s.c && !dead) { // in combat: a pulsing red ring
      const pulse = 0.5 + 0.5 * Math.sin(performance.now() / 160);
      cx.beginPath(); cx.arc(sx, sy, r + 4 + 3 * pulse, 0, 7); cx.lineWidth = 2; cx.strokeStyle = `rgba(255, 64, 64, ${0.35 + 0.45 * pulse})`; cx.stroke();
      combatShown = true;
    }
    cx.beginPath(); cx.arc(sx, sy, r, 0, 7);
    cx.fillStyle = ghost ? "#fff4" : p.color; cx.fill();
    cx.lineWidth = sel === p.name ? 3 : 1.5; cx.strokeStyle = pp.s.c ? css("--death") : "#000"; cx.stroke();
    if (dead && !ghost) { cx.strokeStyle = css("--death"); cx.lineWidth = 2; cx.beginPath(); cx.moveTo(sx - 5, sy - 5); cx.lineTo(sx + 5, sy + 5); cx.moveTo(sx + 5, sy - 5); cx.lineTo(sx - 5, sy + 5); cx.stroke(); }
    cx.font = "600 12px system-ui"; cx.textAlign = "left"; cx.lineWidth = 3; cx.strokeStyle = "#000b"; cx.fillStyle = "#fff";
    const label = p.name + (ghost ? " (ghost)" : ""), w = cx.measureText(label).width;
    let lx = sx + r + 3, ly = sy - r - 1;
    const pd = petDots.get(p.name); // its pet under the name: the name goes left of the dot
    if (pd && pd[0] + pd[2] > lx - 2 && pd[0] - pd[2] < lx + w + 2 && pd[1] + pd[2] > ly - 12 && pd[1] - pd[2] < ly + 3) lx = sx - r - 3 - w;
    for (let k = 0; k < 8; k++) { const hit = names.find(([x, y, pw]) => lx < x + pw + 4 && x < lx + w + 4 && Math.abs(y - ly) < 13); if (!hit) break; lx = hit[0]; ly = hit[1] + 14; }
    names.push([lx, ly, w]);
    cx.strokeText(label, lx, ly); cx.fillText(label, lx, ly);
  }
  // pets' names, centred beyond their dots, where they don't cover a name already drawn
  cx.font = "italic 11px system-ui"; cx.textAlign = "left"; cx.lineWidth = 3; cx.strokeStyle = "#000b"; cx.fillStyle = "#e8e8e8";
  for (const [px, ly, label] of petLabels) {
    const w = cx.measureText(label).width, lx = px - w / 2;
    if (names.some(([x, y, pw]) => lx < x + pw + 2 && x < lx + w + 2 && Math.abs(y - ly) < 12)) continue;
    names.push([lx, ly, w]); cx.strokeText(label, lx, ly); cx.fillText(label, lx, ly);
  }
  // creatures' names, around the players' (names: [x, y, width] of each drawn), targeted ones first
  cx.font = "11px system-ui"; cx.textAlign = "left"; cx.lineWidth = 3; cx.strokeStyle = "#000a";
  unitLabels.sort((a, b) => b[4] - a[4]);
  for (const [sx, sy, r, label, aimed] of unitLabels) {
    const w = cx.measureText(label).width, lx = sx + r + 3, ly = sy + 4;
    if (names.some(([x, y, pw]) => lx < x + pw + 2 && x < lx + w + 2 && Math.abs(y - ly) < 12)) continue; // would overlap
    names.push([lx, ly, w]);
    cx.fillStyle = aimed ? "#ffb0a0" : "#ddd"; cx.strokeText(label, lx, ly); cx.fillText(label, lx, ly);
  }
  if (s.combat) drawCombatText();
  if (combatShown) dirty = true; // the rings pulse: keep drawing
}
// sizes are true to the game's when zoomed in far enough (a creature's bounding radius, a player's 0.306 yd: a human
// male's), with a floor so they stay visible further out
const yd = () => view.s / RES; // screen pixels per yard
const playerR = () => Math.max(5, PLAYER_RADIUS * yd());
// Boats and zeppelins on this map at t, their real outlines (transport-shapes: the collision model's, in its own axes,
// rotated by its orientation), labelled when big enough.
let SHAPES = {};
fetch("transport-shapes").then((r) => r.json()).then((d) => { SHAPES = d; dirty = true; }).catch(() => {});
function drawTransports(t) {
  for (const v of Object.values(TR)) {
    const a = v.s, i = upto(a, t) - 1;
    if (i < 0) continue;
    const p = a[i], n = a[i + 1];
    if (p.m !== M.id || (!n && t - p.t > 10000)) continue; // another map, or its samples stopped
    let x = p.x, y = p.y, o = p.o;
    if (n && n.m === p.m && n.t - p.t <= 10000) {
      const f = (t - p.t) / (n.t - p.t);
      x += (n.x - p.x) * f; y += (n.y - p.y) * f;
      o += Math.atan2(Math.sin(n.o - p.o), Math.cos(n.o - p.o)) * f;
    }
    const zep = /zeppelin/i.test(v.n), c = Math.cos(o), sn = Math.sin(o);
    const outline = SHAPES[v.e] ?? [[-25, -7], [25, -7], [25, 7], [-25, 7]];
    cx.beginPath();
    outline.forEach(([u, w], k) => { const [sx, sy] = scr(x + u * c - w * sn, y + u * sn + w * c); k ? cx.lineTo(sx, sy) : cx.moveTo(sx, sy); });
    cx.closePath();
    cx.fillStyle = zep ? "#9aa0b4cc" : "#b88a4ecc"; cx.fill();
    cx.lineWidth = 1.5; cx.strokeStyle = "#1a1208"; cx.stroke();
    const [sx, sy] = scr(x, y), size = 80 * view.s;
    if (size < 8) { cx.beginPath(); cx.arc(sx, sy, 4, 0, 2 * Math.PI); cx.fillStyle = zep ? "#9aa0b4" : "#b88a4e"; cx.fill(); cx.stroke(); }
    if (size > 30 || store().unitNames === "all") {
      const name = v.n.replace(/^[^(]*\((.*)\)$/, "$1");
      cx.font = "600 11px system-ui"; cx.textAlign = "center"; cx.lineWidth = 3; cx.strokeStyle = "#000a"; cx.fillStyle = "#f2dcb0";
      cx.strokeText(name, sx, sy); cx.fillText(name, sx, sy);
    }
  }
}

const PLAYER_RADIUS = 0.306; // ponytail: every race and gender alike; the models' radii are 0.2 to 0.5 yd
const GLYPH = { death: ["✕", "--death"], achievement: ["★", "--ach"], level: ["▲", "--level"], quest: ["!", "--quest"] };

// map interaction: drag pans (and stops the camera following), scroll zooms, a click selects a player or jumps to a marker
let drag = null, down = null;
const markersAt = (e) => { const r = cv.getBoundingClientRect(), mx = e.clientX - r.left, my = e.clientY - r.top;
  return markerHits.filter(([x, y]) => Math.hypot(x - mx, y - my) < 9).map(([, , ev]) => ev).sort((a, b) => b.t - a.t); };
const tip = (html, ev) => { const el = $("tip"); if (!html) { el.hidden = true; return; } el.innerHTML = html; el.hidden = false;
  el.style.left = Math.min(innerWidth - el.offsetWidth - 8, ev.clientX + 14) + "px"; el.style.top = Math.max(8, ev.clientY - el.offsetHeight - 10) + "px"; };
function wire() {
  cv.addEventListener("pointerleave", () => tip(null));
  cv.addEventListener("pointerdown", (e) => { drag = down = [e.clientX, e.clientY]; cv.setPointerCapture(e.pointerId); });
  cv.addEventListener("pointermove", (e) => {
    if (!drag) {
      const evs = markersAt(e); cv.style.cursor = evs.length ? "pointer" : "";
      if (!evs.length) { // a creature under the pointer: its name, level and health
        const rc = cv.getBoundingClientRect(), mx = e.clientX - rc.left, my = e.clientY - rc.top;
        const u = unitHits.find(([x, y, r]) => Math.hypot(x - mx, y - my) <= Math.max(5, r + 2));
        if (u) { const [, , , name, lvl, hp, state] = u, kind = state & 4 ? "dead" : ["hostile", "neutral", "friendly"][state & 3];
          return tip(`<div><b>${esc(name)}</b> <span class="muted">level ${lvl}</span></div><div class="muted">${kind}${state & 4 ? "" : ` · ${hp}% health`}</div>`, e); }
      }
      return tip(evs.length ? evs.slice(0, 6).map((ev) => { const [g, c] = GLYPH[ev.ev];
        return `<div class="ev"><span class="t">${fmt(ev.t)}</span><span class="g" style="color:var(${c})">${g}</span><span>${ev.html}</span></div>`; }).join("")
        + (evs.length > 6 ? `<div class="muted">+${evs.length - 6} more</div>` : "") + `<div class="muted hintline">click to jump there</div>` : null, e);
    }
    if (autoCam && Math.hypot(e.clientX - down[0], e.clientY - down[1]) > 4) { autoCam = false; controls(); } // panning stops following, keeps the selection
    view.ox += e.clientX - drag[0]; view.oy += e.clientY - drag[1]; drag = [e.clientX, e.clientY]; dirty = true;
  });
  cv.addEventListener("pointerup", (e) => {
    drag = null;
    if (!down || Math.hypot(e.clientX - down[0], e.clientY - down[1]) > 4) return;
    const r = cv.getBoundingClientRect(), mx = e.clientX - r.left, my = e.clientY - r.top;
    let best = null, bd = Math.max(14, playerR() + 4);
    for (const n of P.keys()) { const pp = at(n, t); if (!pp || pp.s.m !== M.id) continue;
      const [sx, sy] = scr(pp.x, pp.y), d = Math.hypot(sx - mx, sy - my); if (d < bd) { bd = d; best = n; } }
    const evs = markersAt(e); if (evs.length && bd >= 7) return setT(evs[0].t);
    select(best, false);
  });
  cv.addEventListener("wheel", (e) => {
    e.preventDefault(); const r = cv.getBoundingClientRect(), mx = e.clientX - r.left, my = e.clientY - r.top, k = Math.exp(-e.deltaY * 0.0015);
    if (autoZoom && autoCam && !followed()) { zoomBias = Math.max(0.1, Math.min(10, zoomBias * k)); saveZoom(); dirty = true; return; }
    view.ox = mx - (mx - view.ox) * k; view.oy = my - (my - view.oy) * k; view.s *= k; dirty = true;
  }, { passive: false });
  $("fitbtn").addEventListener("click", refit);
  $("zoombtn").addEventListener("click", toggleZoom);
  $("followbtn").addEventListener("click", toggleFollow);
  $("mapsel").addEventListener("change", (e) => { mapMode = e.target.value; if (mapMode !== "auto") showMap(Number(mapMode)); updateMaps(); });
  // timeline
  tl.addEventListener("pointerdown", (e) => { seeking = true; tl.setPointerCapture(e.pointerId); seekFrom(e); });
  tl.addEventListener("pointermove", (e) => { if (seeking) seekFrom(e); hoverTimeline(e); });
  tl.addEventListener("pointerup", () => (seeking = false));
  tl.addEventListener("pointerleave", () => ($("tltip").hidden = true));
  // playback
  $("play").addEventListener("click", () => { playing = !playing; if (playing) { liveFollow = false; if (t >= duration()) t = 0; } last = performance.now(); controls(); });
  $("speed").addEventListener("change", (e) => (speed = Number(e.target.value)));
  $("live").addEventListener("click", () => { liveFollow = true; playing = false; controls(); });
  addEventListener("keydown", (e) => {
    if (e.target.tagName === "SELECT" || e.target.tagName === "INPUT") return;
    if (e.code === "Space") { e.preventDefault(); $("play").click(); }
    if (e.code === "KeyF" && !e.metaKey && !e.ctrlKey) refit();
    if (e.code === "KeyZ" && !e.metaKey && !e.ctrlKey) toggleZoom();
    if (e.code === "KeyA" && !e.metaKey && !e.ctrlKey) toggleFollow();
    if (e.code === "KeyH" && !e.metaKey && !e.ctrlKey) store().heat = !store().heat;
    if (e.code === "KeyR" && !e.metaKey && !e.ctrlKey) store().roads = !store().roads;
    if (e.code === "KeyN" && !e.metaKey && !e.ctrlKey) store().nav = !store().nav;
    if (e.code === "ArrowRight") setT(t + (e.shiftKey ? 60000 : 10000));
    if (e.code === "ArrowLeft") setT(t - (e.shiftKey ? 60000 : 10000));
  });
  // the panels: clicks select, seek and switch what's shown
  document.addEventListener("click", (e) => {
    const el = e.target.closest?.("[data-clear], .tile[data-metric], [data-chart], .mrow[data-n], .pc[data-n], .ghead[data-g], .row[data-t]"); if (!el) return;
    const s = store();
    if (el.matches("[data-clear]")) { s.sel = s.grp = null; return; }
    if (el.matches(".tile")) { s.metric = el.dataset.metric; return; }
    if (el.matches("[data-chart]")) { s.chart = el.dataset.chart; return; }
    if (el.matches(".mrow")) return select(s.sel === el.dataset.n ? null : el.dataset.n, false);
    if (el.matches(".pc")) return select(s.sel === el.dataset.n ? null : el.dataset.n);
    if (el.matches(".ghead")) { const g = Number(el.dataset.g); return selectGroup(s.grp === g ? null : g); }
    if (el.matches(".row")) setT(Number(el.dataset.t) - B.start);
  });
  document.addEventListener("change", (e) => { const k = e.target.dataset?.stat; if (k) store()[k] = e.target.value; });
  // the stats plots: one crosshair for both (hoverT), a player's line forward when they're hovered (hoverN)
  const pane = $("pane");
  pane.addEventListener("mousemove", hoverStats);
  pane.addEventListener("mouseleave", () => { hoverT = hoverN = null; tip(null); drawStats(); });
  // the canvases are kept through a swap (hx-preserve, im-preserve: replacing one blanked it until the redraw, a
  // flash every refresh) and redrawn straight after it, before the browser paints
  document.addEventListener("htmx:afterSwap", (e) => { if (e.detail.target?.id === "pane") drawStats(); });
  addEventListener("resize", () => drawStats());
  addEventListener("resize", () => { fit(); dirty = true; });
}
// Fit: auto follow back on, the zoom reset to a snug fit of what it follows (the selection, or everyone)
const refit = () => { autoCam = true; zoomBias = 1; saveZoom(); if (!store().sel) fit(); else view.s = Math.max(view.s, 2.5); controls(); dirty = true; };
const toggleFollow = () => { autoCam = !autoCam; saveZoom(); controls(); dirty = true; };
const toggleZoom = () => { autoZoom = !autoZoom; saveZoom(); controls(); };
// one selection for everything: the roster highlights it, the map draws its route (and the camera follows it while
// auto follow is on; a card turns auto follow on), the
// feed and stats narrow to it
function select(n, followIt = true) {
  const s = store(); s.sel = n; s.grp = null;
  const follow = followIt && n; // from a card: follow them (auto follow on); from the map, the camera follows them if it's on
  if (n) loadRoute(n);
  if (follow) { autoCam = true; view.s = Math.max(view.s, 2.5); mapMode = "auto"; updateMaps(); }
  controls(); dirty = true;
}
function selectGroup(g) { const s = store(); s.grp = g; s.sel = null; if (g != null) { autoCam = true; view.s = Math.max(view.s, 2); } controls(); dirty = true; }

// ---------- map picker ----------
// Auto shows the map the followed player is on, else the one it's on while anyone is there, else the one most players
// are on; picking a map holds it.
let mapMode = "auto", mapOptions = "";
const SEEN_MAPS = new Set();
const mapName = (m) => B.maps?.[m] ?? `map ${m}`;
function showMap(m) { if (m === undefined || m === M.id) return; M.id = m; fit(); dirty = true; }
function updateMaps() {
  const n = new Map();
  for (const name of P.keys()) { const pp = at(name, t); if (pp) { n.set(pp.s.m, (n.get(pp.s.m) ?? 0) + 1); SEEN_MAPS.add(pp.s.m); } }
  if (mapMode === "auto") {
    const f = followed() && at(followed(), t);
    let best = f ? f.s.m : n.get(M.id) ? M.id : undefined; // nobody followed: stay while anyone's here (a deselect doesn't jump)
    if (best === undefined) for (const [m, c] of n) if (best === undefined || c > n.get(best)) best = m;
    showMap(best);
  }
  const seen = new Set([M.id, ...SEEN_MAPS]);
  const opts = `<option value="auto"${mapMode === "auto" ? " selected" : ""}>Auto: ${esc(mapName(M.id))}</option>` +
    [...seen].sort((a, b) => a - b).map((m) => `<option value="${m}"${mapMode === String(m) ? " selected" : ""}>${esc(mapName(m))}${n.get(m) ? ` (${n.get(m)})` : ""}</option>`).join("");
  if (opts !== mapOptions && document.activeElement !== $("mapsel")) { mapOptions = opts; $("mapsel").innerHTML = opts; }
}

// ---------- seek bar ----------
const tl = $("timeline"), tx = tl.getContext("2d");
const TICK = { death: "--death", achievement: "--ach", level: "--level", quest: "--quest", chat: "--chat" };
let seeking = false;
function drawTimeline() {
  const r = tl.getBoundingClientRect(), d = duration();
  if (tl.width !== Math.round(r.width * devicePixelRatio)) { tl.width = r.width * devicePixelRatio; tl.height = r.height * devicePixelRatio; }
  tx.setTransform(devicePixelRatio, 0, 0, devicePixelRatio, 0, 0); tx.clearRect(0, 0, r.width, r.height);
  const W = r.width, X = (ms) => (ms / Math.max(1, d)) * W;
  tx.fillStyle = css("--line"); tx.fillRect(0, 26, W, 6);
  tx.fillStyle = css("--accent"); tx.fillRect(0, 26, X(t), 6);
  for (const e of TICKS) { tx.fillStyle = css(TICK[e.ev]); const h = e.ev === "chat" ? 6 : 14; tx.fillRect(Math.round(X(e.t)), 24 - h, e.ev === "chat" ? 1 : 2, h); }
  tx.fillStyle = css("--ink"); tx.beginPath(); tx.arc(X(t), 29, 6, 0, 7); tx.fill();
}
const seekFrom = (e) => { const r = tl.getBoundingClientRect(); setT(((e.clientX - r.left) / r.width) * duration()); };
function hoverTimeline(e) {
  const r = tl.getBoundingClientRect(), x = e.clientX - r.left, d = duration(), tt = (x / r.width) * d, slop = (4 / r.width) * d;
  const near = TICKS.slice(upto(TICKS, tt - slop - 1), upto(TICKS, tt + slop)), marks = near.filter((e) => e.ev in GLYPH), chats = near.length - marks.length;
  const lines = marks.slice(0, 6).map((m) => MARKS.find((k) => k.t === m.t && k.ev === m.ev)).filter(Boolean);
  const el = $("tltip");
  el.innerHTML = `<div class="t">${fmt(tt)}</div>` + lines.map((m) => { const [g, c] = GLYPH[m.ev]; return `<div><span class="g" style="color:var(${c})">${g}</span>${m.html}</div>`; }).join("")
    + (marks.length > 6 ? `<div class="muted">+${marks.length - 6} more</div>` : "") + (chats ? `<div class="muted">${chats} chat message${chats > 1 ? "s" : ""}</div>` : "");
  el.hidden = false; el.style.left = Math.max(0, Math.min(r.width - el.offsetWidth, x - el.offsetWidth / 2)) + "px";
}

// ---------- playback ----------
// Live: the playhead trails the wall clock by LIVE_LAG, so players are drawn between two samples (about 1 s apart, then
// up to 0.5 s in the server's poll) and glide instead of jumping to each new one. A finished run has no new data: the
// run ends at its last event.
const LIVE_LAG = 2500;
let t = 0, playing = false, speed = 30, liveFollow = true, last = performance.now(), lastT = -1;
const running = () => performance.now() - lastLive < 30000 || Date.now() - B.last < 30000;
const duration = () => Math.max(dataEnd, running() ? Date.now() - B.start - LIVE_LAG : 0);
function setT(v) { t = Math.max(0, Math.min(duration(), v)); liveFollow = t >= duration() - 1500 && running(); playing = false; dirty = true; controls(); need(t); }
function controls() {
  $("play").textContent = playing ? "❚❚" : "▶"; $("play").setAttribute("aria-label", playing ? "Pause" : "Play");
  $("live").setAttribute("aria-pressed", liveFollow); $("zoombtn").setAttribute("aria-pressed", autoZoom); $("zoombtn").disabled = !!followed() || !autoCam;
  $("followbtn").setAttribute("aria-pressed", autoCam);
}
function frame(now) {
  if (liveFollow) t = running() ? Date.now() - B.start - LIVE_LAG : dataEnd;
  else if (playing) { t += (now - last) * speed; if (t >= duration()) { t = duration(); playing = false; liveFollow = running(); controls(); } }
  last = now;
  if (t !== lastT) {
    lastT = t; dirty = true; need(t);
    if (!liveFollow) panelsDue = rosterDue = true; // playing back or seeking: everything at the playhead changes
    else {
      let k = 0;
      for (; k < SHOWS.length && SHOWS[k][0] <= t; k++) { rosterDue = true; if (SHOWS[k][1] || store().tab === "stats") panelsDue = true; }
      SHOWS.splice(0, k);
    }
  }
  if (dirty) { dirty = false; updateMaps(); drawMap(); drawTimeline(); $("clock").textContent = `${fmt(t)} / ${fmt(duration())}`; }
  panels(now);
  if (now - lastHash > 500) { lastHash = now; writeHash(); }
  requestAnimationFrame(frame);
}
// ---------- the address ----------
// The URL keeps the view, so a link or a refresh opens the same moment and place: #t=<seconds from the start, or
// live>&m=<map>&x=&y=<the view's centre>&z=<zoom>&f=<auto follow, 1 or 0>, &hold=1 when a map is picked, &sel=<player>.
// Rewritten (replaceState: no history entries) twice a second when it changes.
let lastHash = 0;
function writeHash() {
  const r = cv.getBoundingClientRect(), s = store();
  const x = MAX - ((r.height / 2 - view.oy) / view.s) * RES, y = MAX - ((r.width / 2 - view.ox) / view.s) * RES;
  const q = new URLSearchParams({ t: liveFollow ? "live" : (t / 1000).toFixed(1), m: M.id, x: Math.round(x), y: Math.round(y), z: +view.s.toFixed(4), f: autoCam ? 1 : 0 });
  if (mapMode !== "auto") q.set("hold", 1);
  if (s.sel) q.set("sel", s.sel);
  const h = "#" + q;
  if (h !== location.hash) history.replaceState(null, "", h);
}
function readHash() { // true if the address had a view
  const q = new URLSearchParams(location.hash.slice(1));
  if (!q.has("m") || !q.has("z")) return false;
  const m = +q.get("m"), x = +q.get("x"), y = +q.get("y"), z = +q.get("z");
  if (q.get("hold")) mapMode = String(m);
  M.id = m; autoCam = q.get("f") === "1";
  const r = sizeCanvas(), [px, py] = toPx(x, y);
  view.s = z > 0 ? z : 1; view.ox = r.width / 2 - px * view.s; view.oy = r.height / 2 - py * view.s;
  const tt = q.get("t");
  if (tt && tt !== "live" && isFinite(+tt)) { liveFollow = false; t = Math.max(0, +tt * 1000); }
  const sel = q.get("sel");
  if (sel) { store().sel = sel; loadRoute(sel); }
  return true;
}
addEventListener("DOMContentLoaded", () => {
  liveFollow = running();
  t = liveFollow ? Date.now() - B.start - LIVE_LAG : dataEnd;
  wire(); controls(); sizeCanvas();
  const linked = readHash();
  controls();
  need(t).then(() => { if (!linked) fit(); dirty = true; });
  requestAnimationFrame(frame);
});
