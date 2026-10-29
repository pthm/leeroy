// party [minutes = 10]: how well your current party is working, from the server's record of the last few minutes.
// Per member: XP/hour, kills, deaths, and how their time went: fighting, travelling, idle (alive, not fighting, not
// moving: usually waiting), dead, and with the group (within 50 yd of another member). For you: XP/hour in this
// party vs before you joined it. Reads the server's recorder log (positions every second, XP, deaths).
import { openSync, readSync, fstatSync, closeSync } from "node:fs";

const LOG = new URL("../../server/azerothcore/env/dist/logs/recorder.jsonl", import.meta.url).pathname;
const me = process.env.WOWBENCH_CHAR ?? "";
const mins = Number(process.argv[2] ?? 10);
const now = Date.now(), from = now - Math.max(mins, 30) * 60_000; // read 30 min back for the "before" comparison

// the tail of the log only: ~0.4 MB per player-minute at worst
const fd = openSync(LOG, "r"), size = fstatSync(fd).size, want = Math.min(size, 40 * 1024 * 1024);
const buf = Buffer.alloc(want);
readSync(fd, buf, 0, want, size - want);
closeSync(fd);
type Ev = { t: number; ev: string; n: string; [k: string]: any };
const evs: Ev[] = [];
for (const l of buf.toString("utf8").split("\n")) {
  if (!l.includes('"ev":"pos"') && !l.includes('"ev":"xp"') && !l.includes('"ev":"death"')) continue;
  try { const e = JSON.parse(l); if (e.t >= from) evs.push(e); } catch {}
}
const pos = evs.filter((e) => e.ev === "pos");
const last = new Map<string, Ev>();
for (const p of pos) last.set(p.n, p);
const mine = last.get(me);
if (!mine) { console.log(JSON.stringify({ error: `no recent record of ${me || "you (WOWBENCH_CHAR unset)"}` })); process.exit(0); }
if (!mine.grp) { console.log(JSON.stringify({ group: null, note: "you're not in a group" })); process.exit(0); }
const members = [...last.values()].filter((p) => p.grp === mine.grp).map((p) => p.n);

// when you joined this group: your first sample in it after the last sample outside it
const myPos = pos.filter((p) => p.n === me);
let joined = from;
for (const p of myPos) if (p.grp !== mine.grp) joined = p.t;
const winFrom = Math.max(now - mins * 60_000, joined);
const hours = Math.max(1 / 60, (now - winFrom) / 3_600_000);

// per second buckets of each member's position, for "with the group"
const at = new Map<string, Map<number, Ev>>();
for (const p of pos) if (members.includes(p.n) && p.t >= winFrom) {
  if (!at.has(p.n)) at.set(p.n, new Map());
  at.get(p.n)!.set(Math.floor(p.t / 1000), p);
}
const near = (n: string, sec: number, p: Ev) => members.some((m) => {
  if (m === n) return false;
  const q = at.get(m)?.get(sec) ?? at.get(m)?.get(sec - 1) ?? at.get(m)?.get(sec + 1);
  return q && q.m === p.m && Math.hypot(q.x - p.x, q.y - p.y) < 50;
});
const pct = (a: number, b: number) => (b ? Math.round((100 * a) / b) : 0);
const round = (v: number) => Math.round(v);
const out = members.map((n) => {
  const samples = [...(at.get(n)?.entries() ?? [])].sort((a, b) => a[0] - b[0]);
  let dead = 0, combat = 0, moving = 0, idle = 0, withGroup = 0, prev: Ev | undefined;
  for (const [sec, p] of samples) {
    const moved = prev && Math.hypot(p.x - prev.x, p.y - prev.y) > 1.5;
    if (!p.alive) dead++;
    else if (p.combat) combat++;
    else if (moved) moving++;
    else idle++;
    if (near(n, sec, p)) withGroup++;
    prev = p;
  }
  const xp = evs.filter((e) => e.ev === "xp" && e.n === n && e.t >= winFrom);
  const deaths = evs.filter((e) => e.ev === "death" && e.n === n && e.t >= winFrom).length;
  const s = samples.length;
  return { name: n, you: n === me || undefined, level: last.get(n)?.lvl, xp_per_hour: round(xp.reduce((a, e) => a + e.amount, 0) / hours),
    kills: xp.filter((e) => e.victim).length, deaths, with_group_pct: pct(withGroup, s), fighting_pct: pct(combat, s),
    travelling_pct: pct(moving, s), idle_pct: pct(idle, s), dead_pct: pct(dead, s) };
});
const myXpBefore = evs.filter((e) => e.ev === "xp" && e.n === me && e.t < joined && e.t >= from).reduce((a, e) => a + e.amount, 0);
const beforeHours = (joined - from) / 3_600_000;
const avg = (k: keyof (typeof out)[0]) => round(out.reduce((a, m) => a + Number(m[k] ?? 0), 0) / out.length);
console.log(JSON.stringify({
  group: mine.grp, minutes: round((now - winFrom) / 60_000), members: out,
  party: { xp_per_hour: out.reduce((a, m) => a + m.xp_per_hour, 0), kills: out.reduce((a, m) => a + m.kills, 0),
    deaths: out.reduce((a, m) => a + m.deaths, 0), avg_with_group_pct: avg("with_group_pct"), avg_idle_pct: avg("idle_pct"),
    avg_dead_pct: avg("dead_pct") },
  you: { xp_per_hour_in_party: out.find((m) => m.you)?.xp_per_hour,
    xp_per_hour_before_joining: beforeHours > 0.05 ? round(myXpBefore / beforeHours) : undefined },
}));
