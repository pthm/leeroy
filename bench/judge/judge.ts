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
//   score        = rate: the headline for score goals. Sustained progress is what counts; deaths and stalls cost
//                through the time they lose. For score goals, quests are valued by difficulty (quest_points in
//                score.json).
// --board <dir> writes <dir>/<character>.json with the latest result each poll (the agents' `standings` tool reads them).
// --resume <samples.jsonl> continues a paused run from its samples.
import { appendFileSync, readFileSync, writeFileSync, mkdirSync, existsSync, statSync, openSync, readSync, closeSync } from "node:fs";
import { sql, signals, weights, composite, rewarded, questPoints, questScoring, killPoints, SERVER_LOG } from "./signals.ts";

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
// quest_points is the judge's own tally: quests seen turned in since the start, valued at your level at the time
const qcfg = kind === "score" ? questScoring() : undefined, seen = rewarded(char);
let questPts = prior.at(-1)?.signals?.quest_points ?? 0;
// kill_points: new "xp" events for this character in the server's recorder log since the judge started
const killCfg = kind === "score" ? JSON.parse(readFileSync(new URL("score.json", import.meta.url), "utf8")).kill_points : undefined;
let killPts = prior.at(-1)?.signals?.kill_points ?? 0, logOff = existsSync(SERVER_LOG) ? statSync(SERVER_LOG).size : 0, logRest = "";
function newKills() {
  if (!existsSync(SERVER_LOG)) return [];
  const size = statSync(SERVER_LOG).size;
  if (size < logOff) logOff = 0; // rotated
  if (size === logOff) return [];
  const fd = openSync(SERVER_LOG, "r"), buf = Buffer.alloc(size - logOff);
  readSync(fd, buf, 0, buf.length, logOff); closeSync(fd); logOff = size;
  const text = logRest + buf.toString("utf8"), cut = text.lastIndexOf("\n") + 1;
  logRest = text.slice(cut);
  return text.slice(0, cut).split("\n").filter((l) => l.includes('"ev":"xp"') && l.includes(`"n":"${char}"`) && l.includes('"victim"'))
    .map((l) => JSON.parse(l)).filter((e) => e.n === char && e.victim && !e.victim.pl).map((e) => ({ id: e.victim.id, lvl: e.victim.lvl }));
}
const need = kind === "item" || kind === "quality" || kind === "quest" ? 1 : +n;

const start = Date.now() - (prior.at(-1)?.elapsed_s ?? 0) * 1000;
const curve: [number, number][] = prior.map((p) => [p.elapsed_s / 3600, p.value]); // [elapsed h, value]
let area = 0; // integral of value dt, in value-hours
for (let i = 1; i < curve.length; i++) area += ((curve[i - 1][1] + curve[i][1]) / 2) * (curve[i][0] - curve[i - 1][0]);
for (;;) {
  const sig = signals(char);
  if (qcfg) {
    const fresh = [...rewarded(char)].filter((q) => !seen.has(q));
    fresh.forEach((q) => seen.add(q));
    questPts += questPoints(fresh, Math.floor(sig.level), qcfg);
    sig.quest_points = questPts;
    killPts += killPoints(newKills(), Math.floor(sig.level), killCfg, qcfg.colour);
    sig.kill_points = killPts;
  }
  const value = kind === "score" ? composite(sig, base, w) : Number(sql(query) || 0) - baseline;
  const [level, xp, money] = sql(`SELECT level, xp, money FROM characters WHERE name = '${char}'`).split("\t").map(Number);
  const elapsed_s = Math.round((Date.now() - start) / 1000), h = elapsed_s / 3600;
  const prev = curve.at(-1);
  if (prev) area += ((prev[1] + value) / 2) * (h - prev[0]);
  curve.push([h, value]);
  const ago = curve.find(([t]) => t >= h - 1 / 6) ?? curve[0]; // ~10 minutes back
  const round = (v: number) => Math.round(v * 10) / 10;
  const pace = h > 0 ? area / h : value;
  // score = rate: progress points per hour over the run so far (the first 10 minutes count as 10, so a quick
  // early quest doesn't read as a huge rate)
  const rate = value / Math.max(h, 1 / 6);
  const rates = { rate: round(rate), recent_rate: round(h - ago[0] > 0 ? (value - ago[1]) / (h - ago[0]) : 0),
    pace: round(pace), score: round(rate) };
  const sample = { elapsed_s, level, xp, money, value, ...rates, signals: sig };
  if (samples) appendFileSync(samples, JSON.stringify(sample) + "\n");
  if (board) writeFileSync(`${board}/${char}.json`, JSON.stringify({ char, level: round(sig.level), progress: value, ...rates, elapsed_s }));
  if (record) appendFileSync(record, JSON.stringify({ t: Date.now(), ev: "score", n: char, value, level, ...rates }) + "\n");
  if (value >= need || Date.now() - start > timeout) {
    console.log(JSON.stringify({ char, goal, met: value >= need, ...sample, at: new Date().toISOString() }));
    process.exit(value >= need ? 0 : 1);
  }
  await Bun.sleep(every);
}
