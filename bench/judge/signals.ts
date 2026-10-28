// Progress signals for one character, read from the character DB (the server's record, not the client's).
// `bun bench/judge/signals.ts <character>` prints them; judge.ts samples them every poll and scores `score:<n>` goals
// as the weighted sum of each signal's change since the judge started (weights: score.json).
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";

export const sql = (q: string) =>
  execFileSync("docker", ["exec", "ac-database", "mysql", "-uroot", "-ppassword", "-N", "-B", "acore_characters", "-e", q],
    { stdio: ["ignore", "pipe", "ignore"] }).toString().trim();

// Profession skill lines: first aid, cooking, fishing + the primary professions.
const PROFESSIONS = [129, 185, 356, 164, 165, 171, 182, 186, 197, 202, 333, 393, 755, 773];
const STAT_DEATHS = 111; // achievement criteria counters ("Total deaths" statistic)
const STAT_KILLS = 107; // "Creatures killed" statistic achievement (sum of its criteria)

const bits = (s: string) => s.split(" ").reduce((n, w) => {
  let v = Number(w) >>> 0, c = 0;
  while (v) { c += v & 1; v >>>= 1; }
  return n + c;
}, 0);

export type Signals = Record<string, number>;

export function signals(char: string): Signals {
  if (!/^[A-Za-z]+$/.test(char)) throw new Error(`bad character name ${char}`);
  const g = `(SELECT guid FROM characters WHERE name = '${char}')`;
  const crit = (ids: string) => `(SELECT COALESCE(SUM(counter), 0) FROM character_achievement_progress WHERE guid = ${g} AND criteria IN (${ids}))`;
  const row = sql(`SELECT c.level, c.xp, COALESCE(x.Experience, 0), c.money, c.totalHonorPoints, c.arenaPoints, c.totalKills,
      c.totaltime, c.exploredZones, c.taximask,
      (SELECT COALESCE(SUM(a.points), 0) FROM character_achievement ca JOIN wowbench.achievement a ON a.id = ca.achievement WHERE ca.guid = c.guid),
      (SELECT COUNT(*) FROM character_queststatus_rewarded WHERE guid = c.guid),
      (SELECT COALESCE(SUM(standing), 0) FROM character_reputation WHERE guid = c.guid),
      (SELECT COALESCE(SUM(value), 0) FROM character_skills WHERE guid = c.guid AND skill IN (${PROFESSIONS})),
      (SELECT COALESCE(SUM(it.ItemLevel), 0) FROM character_inventory ci JOIN item_instance ii ON ii.guid = ci.item
         JOIN acore_world.item_template it ON it.entry = ii.itemEntry WHERE ci.guid = c.guid AND ci.bag = 0 AND ci.slot < 19 AND ci.slot NOT IN (3, 18)),
      (SELECT COALESCE(SUM(it.SellPrice * ii.count), 0) FROM character_inventory ci JOIN item_instance ii ON ii.guid = ci.item
         JOIN acore_world.item_template it ON it.entry = ii.itemEntry WHERE ci.guid = c.guid),
      ${crit(String(STAT_DEATHS))},
      ${crit(`SELECT id FROM wowbench.achievement_criteria WHERE achievement = ${STAT_KILLS}`)}
    FROM characters c LEFT JOIN acore_world.player_xp_for_level x ON x.Level = c.level WHERE c.name = '${char}'`);
  if (!row) throw new Error(`no character ${char}`);
  const f = row.split("\t");
  const n = (i: number) => Number(f[i]) || 0;
  return {
    level: n(0) + (n(2) ? n(1) / n(2) : 0), // fractional: 10.5 = halfway to 11
    achievement_points: n(10),
    // ponytail: mean over the 17 gear slots (shirt and tabard excluded), empty = 0; no two-hander/off-hand adjustment
    avg_item_level: Math.round((n(14) / 17) * 10) / 10,
    wealth_copper: n(3) + n(15), // money + vendor value of everything carried, worn or banked
    money_copper: n(3),
    honor: n(4),
    arena_points: n(5),
    honorable_kills: n(6),
    quests: n(11),
    reputation: n(12),
    profession_skill: n(13),
    explored_areas: bits(f[8] ?? ""),
    flight_paths: bits(f[9] ?? ""),
    deaths: n(16),
    creatures_killed: n(17),
    played_s: n(7),
  };
}

// --- quest difficulty: a quest is worth more the harder it was for you when you turned it in ---

// WoW's difficulty colour of content at level `lvl` for a player at `pl` (AzerothCore Formulas.h GetColorCode)
export function colour(pl: number, lvl: number) {
  const grey = pl <= 5 ? 0 : pl <= 39 ? pl - 5 - Math.floor(pl / 10) : pl <= 59 ? pl - 1 - Math.floor(pl / 5) : pl - 9;
  return lvl >= pl + 5 ? "red" : lvl >= pl + 3 ? "orange" : lvl >= pl - 2 ? "yellow" : lvl > grey ? "green" : "grey";
}

export type QuestScoring = { base: number; colour: Record<string, number>; type: Record<string, number> };
export const questScoring = (file = new URL("score.json", import.meta.url).pathname): QuestScoring =>
  JSON.parse(readFileSync(file, "utf8")).quest_points;

export const rewarded = (char: string) => new Set(sql(`SELECT quest FROM character_queststatus_rewarded
  WHERE guid = (SELECT guid FROM characters WHERE name = '${char}')`).split("\n").filter(Boolean).map(Number));

// Points for quests just turned in at player level `pl`: base x colour x type (QuestInfoID: 1 group, 81 dungeon...).
// QuestLevel -1 means "your level".
export function questPoints(ids: number[], pl: number, cfg: QuestScoring) {
  if (!ids.length) return 0;
  return sql(`SELECT QuestLevel, QuestInfoID FROM acore_world.quest_template WHERE ID IN (${ids.join(",")})`)
    .split("\n").filter(Boolean).map((l) => l.split("\t").map(Number))
    .reduce((s, [ql, info]) => s + cfg.base * (cfg.colour[colour(pl, ql < 0 ? pl : ql)] ?? 0) * (cfg.type[info] ?? 1), 0);
}

export function weights(file = new URL("score.json", import.meta.url).pathname): Record<string, number> {
  return JSON.parse(readFileSync(file, "utf8")).weights;
}

// Weighted sum of each signal's change since `base`.
export function composite(now: Signals, base: Signals, w: Record<string, number>) {
  return Math.round(Object.entries(w).reduce((s, [k, wt]) => s + wt * ((now[k] ?? 0) - (base[k] ?? 0)), 0) * 10) / 10;
}

if (import.meta.main && process.argv[2] === "--check") { // self-check of the colour bands at level 6 and 20
  const want: [number, number, string][] = [[6, 1, "grey"], [6, 2, "green"], [6, 4, "yellow"], [6, 9, "orange"], [6, 11, "red"],
    [5, 1, "green"], [20, 13, "grey"], [20, 14, "green"], [20, 18, "yellow"]];
  for (const [pl, l, c] of want) if (colour(pl, l) !== c) throw new Error(`colour(${pl}, ${l}) = ${colour(pl, l)}, want ${c}`);
  console.log("ok");
} else if (import.meta.main) console.log(JSON.stringify(signals(process.argv[2] ?? ""), null, 1));
