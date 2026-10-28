// bun judge.ts <character> <goal> [--timeout 4h] [--every 15s] [--samples file.jsonl]
// Goals: level:<n> | item:<entry> | quality:<0-5> (any owned item of that quality or better; 3 = rare)
//        | money:<copper> | quests:<n> (quests turned in) | quest:<id> (that quest turned in)
// Reads the character DB (saved every 15s, see server.override.yml), so it can't be gamed from the client.
// Prints one JSON line when the goal is met or the timeout hits; exit 0 = met, 1 = timed out.
// --samples appends {elapsed_s, level, xp, money, value} each poll, for partial credit and progress curves.
import { execFileSync } from "node:child_process";
import { appendFileSync } from "node:fs";

const [char, goal, ...rest] = process.argv.slice(2);
if (!char || !/^[A-Za-z]+$/.test(char) || !/^(level|item|quality|money|quests|quest):\d+$/.test(goal ?? ""))
  throw new Error("usage: bun judge.ts <character> <level|item|quality|money|quests|quest>:<n> [--timeout 4h] [--every 15s]");
const flag = (name: string, def: string) => { const i = rest.indexOf(`--${name}`); return i >= 0 ? rest[i + 1] : def; };
const dur = (s: string) => { const [, n, u] = s.match(/^(\d+)(s|m|h)$/)!; return +n * { s: 1e3, m: 6e4, h: 36e5 }[u]!; };
const timeout = dur(flag("timeout", "4h")), every = dur(flag("every", "15s")), samples = flag("samples", "");

const sql = (q: string) =>
  execFileSync("docker", ["exec", "ac-database", "mysql", "-uroot", "-ppassword", "-N", "-B", "acore_characters", "-e", q],
    { stdio: ["ignore", "pipe", "ignore"] }).toString().trim();

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
}[kind]!;
const need = kind === "item" || kind === "quality" || kind === "quest" ? 1 : +n;

const start = Date.now();
for (;;) {
  const value = Number(sql(query) || 0);
  const [level, xp, money] = sql(`SELECT level, xp, money FROM characters WHERE name = '${char}'`).split("\t").map(Number);
  const elapsed_s = Math.round((Date.now() - start) / 1000);
  const sample = { elapsed_s, level, xp, money, value };
  if (samples) appendFileSync(samples, JSON.stringify(sample) + "\n");
  if (value >= need || Date.now() - start > timeout) {
    console.log(JSON.stringify({ char, goal, met: value >= need, ...sample, at: new Date().toISOString() }));
    process.exit(value >= need ? 0 : 1);
  }
  await Bun.sleep(every);
}
