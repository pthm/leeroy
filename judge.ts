// bun judge.ts <character> <goal> [--timeout 4h] [--every 15s] [--samples file.jsonl]
// Goals: level:<n> | item:<entry> | quality:<0-5> (any owned item of that quality or better; 3 = rare)
//        | money:<copper> | quests:<n> (quests turned in) | quest:<id> (that quest turned in)
//        | achpoints:<n> (achievement points earned since the judge started; needs `bun dbsetup.ts`.
//          Use a large n to score "as many as possible" by the value at timeout)
//        | score:<n> (composite: weighted change in every signal since the judge started; weights in score.json,
//          signals in signals.ts. Same "large n" convention)
// Reads the character DB (saved every 15s, see server.override.yml), so it can't be gamed from the client.
// Prints one JSON line when the goal is met or the timeout hits; exit 0 = met, 1 = timed out.
// --samples appends {elapsed_s, level, xp, money, value, signals} each poll, for partial credit and progress curves.
import { appendFileSync } from "node:fs";
import { sql, signals, weights, composite } from "./signals.ts";

const [char, goal, ...rest] = process.argv.slice(2);
if (!char || !/^[A-Za-z]+$/.test(char) || !/^(level|item|quality|money|quests|quest|achpoints|score):\d+$/.test(goal ?? ""))
  throw new Error("usage: bun judge.ts <character> <level|item|quality|money|quests|quest|achpoints|score>:<n> [--timeout 4h] [--every 15s]");
const flag = (name: string, def: string) => { const i = rest.indexOf(`--${name}`); return i >= 0 ? rest[i + 1] : def; };
const dur = (s: string) => { const [, n, u] = s.match(/^(\d+)(s|m|h)$/)!; return +n * { s: 1e3, m: 6e4, h: 36e5 }[u]!; };
const timeout = dur(flag("timeout", "4h")), every = dur(flag("every", "15s")), samples = flag("samples", "");

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
const baseline = kind === "achpoints" ? Number(sql(query) || 0) : 0; // only count what's earned during the run
const base = signals(char), w = kind === "score" ? weights() : {};
const need = kind === "item" || kind === "quality" || kind === "quest" ? 1 : +n;

const start = Date.now();
for (;;) {
  const sig = signals(char);
  const value = kind === "score" ? composite(sig, base, w) : Number(sql(query) || 0) - baseline;
  const [level, xp, money] = sql(`SELECT level, xp, money FROM characters WHERE name = '${char}'`).split("\t").map(Number);
  const elapsed_s = Math.round((Date.now() - start) / 1000);
  const sample = { elapsed_s, level, xp, money, value, signals: sig };
  if (samples) appendFileSync(samples, JSON.stringify(sample) + "\n");
  if (value >= need || Date.now() - start > timeout) {
    console.log(JSON.stringify({ char, goal, met: value >= need, ...sample, at: new Date().toISOString() }));
    process.exit(value >= need ? 0 : 1);
  }
  await Bun.sleep(every);
}
