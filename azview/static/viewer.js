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
  metric: "dmg", range: "all", chart: "total", per: "0",
  ...saved,
};
V.sel = V.grp = null; V.t = 0;
const PERSIST = ["tab", "sort", "bygroup", "cats", "metric", "range", "chart", "per"];
document.addEventListener("alpine:init", () => {
  Alpine.store("v", V);
  Alpine.effect(() => { // any change to what the panels show: save it and refresh them
    const s = Alpine.store("v"), keep = Object.fromEntries(PERSIST.map((k) => [k, JSON.parse(JSON.stringify(s[k]))]));
    try { localStorage.setItem("azview", JSON.stringify(keep)); } catch {}
    s.sel; s.grp; panelsNow();
  });
});
const store = () => window.Alpine?.store("v") ?? V;
window.viewerQuery = () => { const s = store();
  return { t: Math.round(B.start + t), sel: s.sel ?? "", grp: s.grp ?? "", tab: s.tab, sort: s.sort, bygroup: s.bygroup ? 1 : 0,
    cats: Object.keys(s.cats).filter((k) => s.cats[k]).join(","), metric: s.metric, range: s.range, chart: s.chart, per: s.per }; };

// panels refresh at most 5 times a second, when the playhead moved, data arrived or what they show changed
let panelsDue = true, lastPanels = 0;
const panelsNow = () => { panelsDue = true; lastPanels = 0; };
function panels(now) {
  if (!panelsDue || now - lastPanels < 200 || !window.htmx || holding) return;
  panelsDue = false; lastPanels = now; htmx.trigger(document.body, "panels");
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
const P = new Map(), S = {}, U = {}, R = {};
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
// where a player is at t: the last sample, moved towards the next (held over a gap of more than 5 s)
function at(name, t) {
  const a = S[name]; if (!a) return null;
  const i = upto(a, t) - 1; if (i < 0) return null;
  const s = a[i], n = a[i + 1];
  if (!n || n.t - s.t > 5000) return { s, x: s.x, y: s.y };
  const f = (t - s.t) / (n.t - s.t);
  return { s, x: s.x + (n.x - s.x) * f, y: s.y + (n.y - s.y) * f };
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
    if (!overlap) for (const n of Object.keys(S)) if (!(n in d.s)) S[n] = [];
    have = overlap ? { from: Math.min(from, have.from), to: Math.max(to, have.to) } : { from, to };
    trim(); dirty = true; updateMaps();
  }).catch(() => {}).finally(() => { loading = null; });
}
const merge = (a, b) => { const m = new Map(a.map((s) => [s.t, s])); for (const s of b) m.set(s.t, s); return [...m.values()].sort((x, y) => x.t - y.t); };
function trim() { // keep memory bounded: samples more than 15 minutes from the playhead go
  const lo = t - 900000, hi = t + Math.max(900000, speed * 60000);
  for (const a of [...Object.values(S), ...Object.values(U)]) { const i = upto(a, lo); if (i > 0) a.splice(0, i); const j = upto(a, hi); if (j < a.length) a.splice(j); }
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
es.addEventListener("s", (m) => { const d = JSON.parse(m.data), s = rel(d.s); player(d.n, d.cls); live(s.t);
  if (have.to >= s.t - 10000) { ins(S[d.n] ??= [], s); have.to = Math.max(have.to, s.t); }
  if (R[d.n]?.length) R[d.n].push([s.t, s.x, s.y, s.m]); });
es.addEventListener("u", (m) => { const d = JSON.parse(m.data), u = rel(d.u); live(u.t); if (have.to >= u.t - 10000) ins(U[d.n] ??= [], u); });
es.addEventListener("mark", (m) => { const e = rel(JSON.parse(m.data)); ins(MARKS, e); live(e.t); });
es.addEventListener("tick", (m) => { const [tt, k] = JSON.parse(m.data); ins(TICKS, { t: tt - B.start, ev: k }); });
es.addEventListener("data", () => { lastLive = performance.now(); panelsDue = true; });
function live(tt) { dataEnd = Math.max(dataEnd, tt); lastLive = performance.now(); dirty = true; }

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
let autoZoom = saved.autoZoom ?? true, zoomBias = saved.zoomBias ?? 1, autoCam = true, follow = null;
const saveZoom = () => { try { localStorage.setItem("azview", JSON.stringify({ ...JSON.parse(localStorage.getItem("azview") || "{}"), autoZoom, zoomBias })); } catch {} };
const tiles = new Map();
function drawTiles(w, h) {
  const wx = (sx, sy) => [MAX - ((sy - view.oy) / view.s) * RES, MAX - ((sx - view.ox) / view.s) * RES];
  const [xa, ya] = wx(0, 0), [xb, yb] = wx(w, h), g = (v) => Math.floor(32 - v / GRID);
  for (let gx = g(xa); gx <= g(xb); gx++) for (let gy = g(ya); gy <= g(yb); gy++) {
    if (gx < 0 || gy < 0 || gx > 63 || gy > 63) continue;
    const key = `${M.id}/${gx}/${gy}`;
    if (!tiles.has(key)) { const im = new Image(); im.onload = () => (dirty = true); im.src = `tile/${key}.png?v=${B.tileVersion}`; tiles.set(key, im); }
    const im = tiles.get(key); if (!im.complete || !im.naturalWidth) continue;
    const [sx, sy] = scr(GRID * (32 - gx), GRID * (32 - gy)), size = (GRID / RES) * view.s;
    cx.drawImage(im, sx, sy, size + 0.5, size + 0.5);
  }
}
const groupMembers = () => { const s = store(); if (s.grp == null) return null; const g = Number(s.grp);
  return new Set([...P.keys()].filter((n) => at(n, t)?.s.grp === g)); };
const focusSet = () => { const s = store(); return s.sel ? new Set([s.sel]) : groupMembers(); };
let markerHits = [];
function drawMap() {
  const r = cv.getBoundingClientRect(), s = store(), sel = s.sel;
  // the camera: on the followed player; or on the middle of the players it keeps in frame (the selected group, or
  // everyone), with auto zoom easing towards a fit of them (at least 80 yd across, 45 yd of room around), times zoomBias
  let centre = null;
  const gm = s.grp != null ? groupMembers() : null;
  const frame = follow ? null : gm ? [...gm] : autoCam ? [...P.keys()] : null;
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
  // labels
  cx.textAlign = "center";
  for (const l of B.labels) {
    if (l.map !== M.id || (!l.zone && view.s < 0.9)) continue;
    const [sx, sy] = scr(l.x, l.y);
    cx.font = l.zone ? "600 15px system-ui" : "12px system-ui";
    cx.lineWidth = 3; cx.strokeStyle = "#000a"; cx.fillStyle = l.zone ? "#fff" : "#fffd";
    cx.strokeText(l.name, sx, sy); cx.fillText(l.name, sx, sy);
  }
  // creatures around the players: hostile red, neutral yellow, friendly green, dead grey; named when zoomed in
  const UNIT_COLOR = ["#e04040", "#e8c040", "#40c050"], drawn = new Set();
  cx.font = "11px system-ui"; cx.textAlign = "left";
  for (const n of P.keys()) {
    const a = U[n]; if (!a) continue;
    const u = a[upto(a, t) - 1]; if (!u || t - u.t > 5000 || u.m !== M.id) continue;
    for (const [name, lvl, x, y, hp, state] of u.u) {
      const key = `${name}/${Math.round(x)}/${Math.round(y)}`; if (drawn.has(key)) continue; drawn.add(key);
      const [sx, sy] = scr(x, y), dead = state & 4;
      cx.beginPath(); cx.arc(sx, sy, dead ? 2.5 : 3.5, 0, 7);
      cx.fillStyle = dead ? "#777a" : UNIT_COLOR[state & 3] ?? "#aaa"; cx.fill();
      cx.lineWidth = 1; cx.strokeStyle = "#000a"; cx.stroke();
      if (view.s >= 4 && !dead) { const label = `${name} ${lvl}${hp < 100 ? ` ${hp}%` : ""}`;
        cx.lineWidth = 3; cx.strokeStyle = "#000a"; cx.fillStyle = "#ddd"; cx.strokeText(label, sx + 6, sy + 4); cx.fillText(label, sx + 6, sy + 4); }
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
  // points [t, x, y, m] from..to on this map as screen points, in runs split at gaps (logged out, teleports)
  const runs = (pts, from, to, tail) => { const out = []; let cur = [], lastT = null;
    for (let k = from; k <= to; k++) { const q = pts[k]; if (q[3] !== M.id || (lastT != null && q[0] - lastT > 5000)) { if (cur.length) out.push(cur); cur = []; }
      if (q[3] === M.id) { cur.push([...scr(q[1], q[2]), q[0]]); lastT = q[0]; } }
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
  if (!sel) for (const p of P.values()) {
    const a = S[p.name], pp = at(p.name, t); if (!a || !pp || pp.s.m !== M.id) continue;
    const i = upto(a, t) - 1; let from = i; while (from > 0 && t - a[from - 1].t <= TRAIL) from--;
    const pts = runs(a.map((q) => [q.t, q.x, q.y, q.m]), from, i, pp).at(-1); if (!pts || pts.length < 2) continue;
    fadeLine(p.color, pts, TRAIL, 0, [4, 2.2]);
  }
  // targets: a thin red line from each player to what they're targeting, and a health bar over it
  const bars = new Map(), focus = focusSet();
  for (const n of P.keys()) {
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
  for (const n of P.keys()) { const pp = at(n, t); if (pp?.s.grp && pp.s.m === M.id) (groups.get(pp.s.grp) ?? groups.set(pp.s.grp, []).get(pp.s.grp)).push([n, pp]); }
  cx.setLineDash([4, 4]); cx.lineWidth = 1.5; cx.strokeStyle = "#fffc";
  for (const members of groups.values()) {
    const head = members.find(([n, pp]) => n === pp.s.ld) ?? members[0], [hx, hy] = scr(head[1].x, head[1].y);
    for (const m of members) { if (m === head) continue; const [mx, my] = scr(m[1].x, m[1].y); cx.beginPath(); cx.moveTo(hx, hy); cx.lineTo(mx, my); cx.stroke(); }
  }
  cx.setLineDash([]);
  // event markers up to now (kept, with where they were drawn, for the hover tooltip)
  markerHits = [];
  cx.font = "bold 13px system-ui"; cx.textAlign = "center"; cx.strokeStyle = "#000"; cx.lineWidth = 1;
  for (let i = 0, end = upto(MARKS, t); i < end; i++) {
    const e = MARKS[i]; if (e.m !== M.id || e.x == null || (focus && !focus.has(e.n))) continue;
    const [sx, sy] = scr(e.x, e.y); if (sx < -10 || sy < -10 || sx > r.width + 10 || sy > r.height + 10) continue;
    const [g, c] = GLYPH[e.ev]; cx.fillStyle = css(c);
    cx.strokeText(g, sx, sy + 4); cx.fillText(g, sx, sy + 4);
    markerHits.push([sx, sy, e]);
  }
  // players now; each name nudged down until it doesn't overlap one already drawn
  const names = [];
  for (const p of P.values()) {
    const pp = at(p.name, t); if (!pp || pp.s.m !== M.id) continue;
    const [sx, sy] = scr(pp.x, pp.y), dead = !pp.s.a, ghost = pp.s.g;
    cx.beginPath(); cx.arc(sx, sy, 6, 0, 7);
    cx.fillStyle = ghost ? "#fff4" : p.color; cx.fill();
    cx.lineWidth = sel === p.name ? 3 : 1.5; cx.strokeStyle = pp.s.c ? css("--death") : "#000"; cx.stroke();
    if (dead && !ghost) { cx.strokeStyle = css("--death"); cx.lineWidth = 2; cx.beginPath(); cx.moveTo(sx - 5, sy - 5); cx.lineTo(sx + 5, sy + 5); cx.moveTo(sx + 5, sy - 5); cx.lineTo(sx - 5, sy + 5); cx.stroke(); }
    cx.font = "600 12px system-ui"; cx.textAlign = "left"; cx.lineWidth = 3; cx.strokeStyle = "#000b"; cx.fillStyle = "#fff";
    const label = p.name + (ghost ? " (ghost)" : ""), w = cx.measureText(label).width;
    let lx = sx + 9, ly = sy - 7;
    for (let k = 0; k < 8; k++) { const hit = names.find(([x, y, pw]) => lx < x + pw + 4 && x < lx + w + 4 && Math.abs(y - ly) < 13); if (!hit) break; lx = hit[0]; ly = hit[1] + 14; }
    names.push([lx, ly, w]);
    cx.strokeText(label, lx, ly); cx.fillText(label, lx, ly);
  }
}
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
      return tip(evs.length ? evs.slice(0, 6).map((ev) => { const [g, c] = GLYPH[ev.ev];
        return `<div class="ev"><span class="t">${fmt(ev.t)}</span><span class="g" style="color:var(${c})">${g}</span><span>${ev.html}</span></div>`; }).join("")
        + (evs.length > 6 ? `<div class="muted">+${evs.length - 6} more</div>` : "") + `<div class="muted hintline">click to jump there</div>` : null, e);
    }
    if (Math.hypot(e.clientX - down[0], e.clientY - down[1]) > 4) { follow = null; autoCam = false; }
    view.ox += e.clientX - drag[0]; view.oy += e.clientY - drag[1]; drag = [e.clientX, e.clientY]; dirty = true;
  });
  cv.addEventListener("pointerup", (e) => {
    drag = null;
    if (!down || Math.hypot(e.clientX - down[0], e.clientY - down[1]) > 4) return;
    const r = cv.getBoundingClientRect(), mx = e.clientX - r.left, my = e.clientY - r.top;
    let best = null, bd = 14;
    for (const n of P.keys()) { const pp = at(n, t); if (!pp || pp.s.m !== M.id) continue;
      const [sx, sy] = scr(pp.x, pp.y), d = Math.hypot(sx - mx, sy - my); if (d < bd) { bd = d; best = n; } }
    const evs = markersAt(e); if (evs.length && bd >= 7) return setT(evs[0].t);
    select(best, false);
  });
  cv.addEventListener("wheel", (e) => {
    e.preventDefault(); const r = cv.getBoundingClientRect(), mx = e.clientX - r.left, my = e.clientY - r.top, k = Math.exp(-e.deltaY * 0.0015);
    if (autoZoom && !follow && (store().grp != null || autoCam)) { zoomBias = Math.max(0.1, Math.min(10, zoomBias * k)); saveZoom(); dirty = true; return; }
    view.ox = mx - (mx - view.ox) * k; view.oy = my - (my - view.oy) * k; view.s *= k; dirty = true;
  }, { passive: false });
  $("fitbtn").addEventListener("click", refit);
  $("zoombtn").addEventListener("click", toggleZoom);
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
    if (e.code === "ArrowRight") setT(t + (e.shiftKey ? 60000 : 10000));
    if (e.code === "ArrowLeft") setT(t - (e.shiftKey ? 60000 : 10000));
  });
  // the panels: clicks select, seek and switch what's shown
  document.addEventListener("click", (e) => {
    const el = e.target.closest?.("[data-clear], .tile[data-metric], [data-chart], .mrow[data-n], .pc[data-n], .ghead[data-g], .row[data-t]"); if (!el) return;
    const s = store();
    if (el.matches("[data-clear]")) { s.sel = s.grp = null; follow = null; return; }
    if (el.matches(".tile")) { s.metric = el.dataset.metric; return; }
    if (el.matches("[data-chart]")) { s.chart = el.dataset.chart; return; }
    if (el.matches(".mrow")) return select(s.sel === el.dataset.n ? null : el.dataset.n, false);
    if (el.matches(".pc")) return select(s.sel === el.dataset.n ? null : el.dataset.n);
    if (el.matches(".ghead")) { const g = Number(el.dataset.g); return selectGroup(s.grp === g ? null : g); }
    if (el.matches(".row")) setT(Number(el.dataset.t) - B.start);
  });
  document.addEventListener("change", (e) => { const k = e.target.dataset?.stat; if (k) store()[k] = e.target.value; });
  addEventListener("resize", () => { fit(); dirty = true; });
}
const refit = () => { follow = null; autoCam = true; zoomBias = 1; saveZoom(); fit(); dirty = true; };
const toggleZoom = () => { autoZoom = !autoZoom; saveZoom(); controls(); };
// one selection for everything: the roster highlights it, the map draws its route (and follows it, from a card), the
// feed and stats narrow to it
function select(n, followIt = true) {
  const s = store(); follow = followIt ? n : n === follow ? follow : null; s.sel = n; s.grp = null;
  if (n) loadRoute(n);
  if (follow) { view.s = Math.max(view.s, 2.5); mapMode = "auto"; updateMaps(); }
  dirty = true;
}
function selectGroup(g) { const s = store(); s.grp = g; s.sel = null; follow = null; if (g != null) view.s = Math.max(view.s, 2); dirty = true; }

// ---------- map picker ----------
// Auto shows the map the followed player is on, else the one most players are on; picking a map holds it.
let mapMode = "auto", mapOptions = "";
const SEEN_MAPS = new Set();
const mapName = (m) => B.maps?.[m] ?? `map ${m}`;
function showMap(m) { if (m === undefined || m === M.id) return; M.id = m; fit(); dirty = true; }
function updateMaps() {
  const n = new Map();
  for (const name of P.keys()) { const pp = at(name, t); if (pp) { n.set(pp.s.m, (n.get(pp.s.m) ?? 0) + 1); SEEN_MAPS.add(pp.s.m); } }
  if (mapMode === "auto") {
    const f = follow && at(follow, t);
    let best = f ? f.s.m : undefined;
    if (best === undefined) for (const [m, c] of n) if (best === undefined || c > n.get(best) || (c === n.get(best) && m === M.id)) best = m;
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
  $("live").setAttribute("aria-pressed", liveFollow); $("zoombtn").setAttribute("aria-pressed", autoZoom); $("zoombtn").disabled = !!follow;
}
function frame(now) {
  if (liveFollow) t = running() ? Date.now() - B.start - LIVE_LAG : dataEnd;
  else if (playing) { t += (now - last) * speed; if (t >= duration()) { t = duration(); playing = false; liveFollow = running(); controls(); } }
  last = now;
  if (t !== lastT) { lastT = t; dirty = true; panelsDue = true; need(t); }
  if (dirty) { dirty = false; updateMaps(); drawMap(); drawTimeline(); $("clock").textContent = `${fmt(t)} / ${fmt(duration())}`; }
  panels(now);
  requestAnimationFrame(frame);
}
addEventListener("DOMContentLoaded", () => {
  liveFollow = running();
  t = liveFollow ? Date.now() - B.start - LIVE_LAG : dataEnd;
  wire(); controls(); sizeCanvas();
  need(t).then(() => { fit(); dirty = true; });
  requestAnimationFrame(frame);
});
