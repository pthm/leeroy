// bun bench/judge/judge.ts <character> <goal> [--timeout 4h] [--every 15s] [--samples file.jsonl]
// Goals: level:<n> | item:<entry> | quality:<0-5> (any owned item of that quality or better; 3 = rare)
//        | money:<copper> | quests:<n> (quests turned in) | quest:<id> (that quest turned in)
//        | achpoints:<n> (achievement points earned since the judge started; needs `bun azeroth-sdk/worlddata/dbsetup.ts`.
//          Use a large n to score "as many as possible" by the value at timeout)
//        | score:<n> (composite: weighted change in every signal since the judge started; weights in score.json,
//          signals in signals.ts. Same "large n" convention)
// Reads the character DB (saved every 15s, see server/override.yml), so it can't be gamed from the client.
// Prints one JSON line when the goal is met or the timeout hits; exit 0 = met, 1 = timed out.
// --samples appends {elapsed_s, level, xp, money, value, rate, ..., signals} each poll, for partial credit and curves.
// --record also appends each sample as a {"ev":"score"} event to a recorder log, so the replay/live viewers chart it.
//
// Rate of progress (all goals, from the value curve):
//   rate         value per hour since the start (the first 10 minutes count as 10)
//   recent_rate  value per hour over the last 10 minutes
//   pace         the time-average of value so far (area under the curve / time)
//   best_window  the best rate over any 15-minute window
//   score        (rate + best_window) / 2: the headline for score goals. Sustained progress counts, and so does
//                finding a better strategy: time spent trying one lowers the rate but not the best window. Deaths
//                and stalls cost through the time they lose. For score goals, progress is the game's own rewards:
//                XP earned (score.json); gear, money and the rest are reported, not scored.
// --board <dir> writes <dir>/<character>.json with the latest result each poll (the agents' `standings` tool reads them).
// --resume <samples.jsonl> continues a paused run from its samples.
import { appendFileSync, readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { sql, signals, weights, composite } from "./signals.ts";

const [char, goal, ...rest] = process.argv.slice(2);
if (!char || !/^[A-Za-z]+$/.test(char) || !/^(level|item|quality|money|quests|quest|achpoints|score):\d+$/.test(goal ?? ""))
  throw new Error("usage: bun bench/judge/judge.ts <character> <level|item|quality|money|quests|quest|achpoints|score>:<n> [--timeout 4h] [--every 15s]");
const flag = (name: string, def: string) => { const i = rest.indexOf(`--${name}`); return i >= 0 ? rest[i + 1] : def; };
const dur = (s: string) => { const [, n, u] = s.match(/^(\d+)(s|m|h)$/)!; return +n * { s: 1e3, m: 6e4, h: 36e5 }[u]!; };
const timeout = dur(flag("timeout", "4h")), every = dur(flag("every", "15s")), samples = flag("samples", ""), record = flag("record", "");
const board = flag("board", ""); // dir: the latest result per character, for the agents' `standings`
if (board) mkdirSync(board, { recursive: true });

const [kind, n] = goal.split(":");
const owned = `FROM character_inventory ci JOIN item_instance ii ON ii.guid = ci.item
  JOIN acore_world.item_template it ON it.entry = ii.itemEntry
  WHERE ci.guid = (SELECT guid FROM characters WHERE name = '${char}')`;
const query = {
  level: `SELECT level FROM characters WHERE name = '${char}'`,
  money: `SELECT money FROM characters WHERE name = '${char}'`,
  item: `SELECT COUNT(*) ${owned} AND it.entry = ${n}`,
  quality: `SELECT COUNT(*) ${owned} AND it.Quality >= ${n}`,
  quests: `SELECT COUNT(*) FROM character_queststatus_rewarded WHERE guid = (SELECT guid FROM characters WHERE name = '${char}')`,
  quest: `SELECT COUNT(*) FROM character_queststatus_rewarded WHERE quest = ${n} AND guid = (SELECT guid FROM characters WHERE name = '${char}')`,
  achpoints: `SELECT COALESCE(SUM(a.points), 0) FROM character_achievement ca JOIN wowbench.achievement a ON a.id = ca.achievement
    WHERE ca.guid = (SELECT guid FROM characters WHERE name = '${char}')`,
  score: "",
}[kind]!;
// --resume <samples.jsonl>: continue a paused run from its samples: same baseline, same curve, and the clock
// picks up at the last sample (the pause doesn't count)
const prior = flag("resume", "") ? readFileSync(flag("resume", ""), "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l)) : [];
const baseline = kind === "achpoints" ? (prior.length ? Number(sql(query) || 0) - prior.at(-1).value : Number(sql(query) || 0)) : 0; // only count what's earned during the run
const base = prior[0]?.signals ?? signals(char), w = kind === "score" ? weights() : {};
const need = kind === "item" || kind === "quality" || kind === "quest" ? 1 : +n;

const start = Date.now() - (prior.at(-1)?.elapsed_s ?? 0) * 1000;
const curve: [number, number][] = prior.map((p) => [p.elapsed_s / 3600, p.value]); // [elapsed h, value]
let area = 0; // integral of value dt, in value-hours
for (let i = 1; i < curve.length; i++) area += ((curve[i - 1][1] + curve[i][1]) / 2) * (curve[i][0] - curve[i - 1][0]);
// best_window: the best rate over any WINDOW of the run (value gained in the window / its length). Trying a new
// strategy costs some average rate but not the best window, so the score rewards finding something better as well
// as sustaining it.
const WINDOW = 0.25; // hours
const windowRate = (k: number) => {
  const [hk, vk] = curve[k];
  if (hk < WINDOW) return vk / Math.max(hk, 1 / 6);
  let j = k;
  while (j > 0 && curve[j - 1][0] >= hk - WINDOW) j--;
  return (vk - curve[j][1]) / Math.max(hk - curve[j][0], 1 / 60);
};
let bestWindow = 0;
for (let k = 0; k < curve.length; k++) bestWindow = Math.max(bestWindow, windowRate(k));
for (;;) {
  const sig = signals(char);
  const value = kind === "score" ? composite(sig, base, w) : Number(sql(query) || 0) - baseline;
  const [level, xp, money] = sql(`SELECT level, xp, money FROM characters WHERE name = '${char}'`).split("\t").map(Number);
  const elapsed_s = Math.round((Date.now() - start) / 1000), h = elapsed_s / 3600;
  const prev = curve.at(-1);
  if (prev) area += ((prev[1] + value) / 2) * (h - prev[0]);
  curve.push([h, value]);
  const ago = curve.find(([t]) => t >= h - 1 / 6) ?? curve[0]; // ~10 minutes back
  const round = (v: number) => Math.round(v * 10) / 10;
  const pace = h > 0 ? area / h : value;
  // rate: progress points per hour over the run so far (the first 10 minutes count as 10, so a quick early quest
  // doesn't read as a huge rate). score = (rate + best 15-minute window rate) / 2
  const rate = value / Math.max(h, 1 / 6);
  bestWindow = Math.max(bestWindow, windowRate(curve.length - 1));
  const rates = { rate: round(rate), recent_rate: round(h - ago[0] > 0 ? (value - ago[1]) / (h - ago[0]) : 0),
    best_window: round(bestWindow), pace: round(pace), score: round((rate + bestWindow) / 2) };
  const sample = { elapsed_s, level, xp, money, value, ...rates, signals: sig };
  if (samples) appendFileSync(samples, JSON.stringify(sample) + "\n");
  if (board) writeFileSync(`${board}/${char}.json`, JSON.stringify({ char, level: round(sig.level), progress: value, ...rates, elapsed_s,
    money: sig.money_copper, avg_item_level: sig.avg_item_level }));
  if (record) appendFileSync(record, JSON.stringify({ t: Date.now(), ev: "score", n: char, value, level, ...rates }) + "\n");
  if (value >= need || Date.now() - start > timeout) {
    console.log(JSON.stringify({ char, goal, met: value >= need, ...sample, at: new Date().toISOString() }));
    process.exit(value >= need ? 0 : 1);
  }
  await Bun.sleep(every);
}
