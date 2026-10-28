// wowdb upgrades: Wowhead-style "what could I wear in this slot, and where do I get it". Items your class can use at
// your level, best item level first, each with how to obtain it: a quest reward (quest, giver, where), a vendor
// (price, where) or a creature drop (mob, level, chance, where). World drops from shared loot tables are left out
// (they drop anywhere at random); `wowdb item <id>` has every source of one item.

type Row = Record<string, string | number | null>;
type Q = (sql: string) => Row[];
type Spawns = (table: string, id: number, near?: [number, number], limit?: number) => { count: number; points: Row[] };

export const CLASSES: Record<string, number> = { warrior: 1, paladin: 2, hunter: 3, rogue: 4, priest: 5, "death knight": 6,
  shaman: 7, mage: 8, warlock: 9, druid: 11 };

// what each class can wear and wield (weapons: what it can learn; some need a trainer first). Armor at levels below 40.
const ARMOR: Record<number, number[]> = { 1: [1, 2, 3, 6], 2: [1, 2, 3, 6], 3: [1, 2], 4: [1, 2], 5: [1], 6: [1, 2, 3, 4], 7: [1, 2, 6],
  8: [1], 9: [1], 11: [1, 2] };
const WEAPONS: Record<number, number[]> = {
  1: [0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 13, 15, 16, 18], 2: [0, 1, 4, 5, 6, 7, 8], 3: [0, 1, 2, 3, 6, 7, 8, 10, 13, 15, 16, 18],
  4: [0, 2, 3, 4, 7, 13, 15, 16, 18], 5: [4, 10, 15, 19], 6: [0, 1, 4, 5, 6, 7, 8], 7: [0, 1, 4, 5, 10, 13, 15],
  8: [7, 10, 15, 19], 9: [7, 10, 15, 19], 11: [4, 5, 6, 10, 13, 15] };
// slot name -> inventory types
const SLOTS: Record<string, number[]> = { head: [1], neck: [2], shoulder: [3], chest: [5, 20], waist: [6], legs: [7], feet: [8],
  wrists: [9], hands: [10], finger: [11], trinket: [12], back: [16], "main hand": [13, 17, 21], "off hand": [13, 14, 22, 23],
  ranged: [15, 25, 26, 28] };
const STAT: Record<number, string> = { 0: "mana", 1: "health", 3: "agility", 4: "strength", 5: "intellect", 6: "spirit", 7: "stamina",
  31: "hit", 32: "crit", 35: "resilience", 36: "haste", 38: "attack_power", 43: "mana_per_5s", 45: "spell_power" };
const SLOT_OF: Record<number, string> = Object.fromEntries(Object.entries(SLOTS).flatMap(([s, ts]) => ts.map((t) => [t, s])));

export function upgrades(q: Q, spawns: Spawns, o: { cls: number; level: number; slot?: string; near?: [number, number]; map?: number; radius?: number; minIlvl?: number; limit?: number }) {
  const { cls, level } = o;
  const types = o.slot ? SLOTS[o.slot] : Object.values(SLOTS).flat();
  if (!types) throw new Error(`unknown slot ${o.slot}: one of ${Object.keys(SLOTS).join(", ")}`);
  const usable = `it.InventoryType IN (${[...new Set(types)].join(",")}) AND it.RequiredLevel <= ${level}
    AND (it.AllowableClass = -1 OR it.AllowableClass & ${1 << (cls - 1)}) AND it.Quality >= 1 AND it.ItemLevel >= ${o.minIlvl ?? 1}
    AND ((it.class = 4 AND it.subclass IN (0, ${ARMOR[cls].join(",")})) OR (it.class = 2 AND it.subclass IN (${WEAPONS[cls].join(",")})))
    AND (it.armor > 0 OR it.dmg_max1 > 0 OR it.stat_value1 <> 0)`; // not cosmetic clothing
  // a creature that spawns outside holiday events
  const spawned = (col: string) => `EXISTS (SELECT 1 FROM creature cr WHERE cr.id = ${col} AND cr.guid NOT IN (SELECT guid FROM game_event_creature))`;
  const cols = `it.entry, it.name, it.Quality quality, it.ItemLevel item_level, it.RequiredLevel required_level, it.InventoryType inv,
    it.armor, it.dmg_min1, it.dmg_max1, it.delay, ${[...Array(10).keys()].map((i) => `it.stat_type${i + 1}, it.stat_value${i + 1}`).join(", ")}`;
  const alliance = "(qt.AllowableRaces = 0 OR qt.AllowableRaces & 1101)"; // human, dwarf, night elf, gnome, draenei
  // quest rewards you could still get around your level
  const rewardCols = [1, 2, 3, 4].map((i) => `RewardItem${i}`).concat([1, 2, 3, 4, 5, 6].map((i) => `RewardChoiceItemID${i}`));
  const fromQuests = q(`SELECT ${cols}, qt.ID quest, qt.LogTitle quest_name, qt.QuestLevel quest_level, qt.MinLevel min_level, st.id giver
    FROM quest_template qt JOIN item_template it ON it.entry IN (${rewardCols.map((c) => `qt.${c}`).join(",")})
    LEFT JOIN creature_queststarter st ON st.quest = qt.ID
    WHERE ${usable} AND ${alliance} AND qt.MinLevel <= ${level + 2} AND qt.QuestLevel BETWEEN ${level - 4} AND ${level + 6}`);
  const fromVendors = q(`SELECT ${cols}, v.entry vendor, c.name vendor_name, it.BuyPrice price FROM npc_vendor v
    JOIN item_template it ON it.entry = v.item JOIN creature_template c ON c.entry = v.entry WHERE ${usable} AND ${spawned("v.entry")}`);
  const fromDrops = q(`SELECT ${cols}, c.entry mob, c.name mob_name, c.minlevel mob_lo, c.maxlevel mob_hi, c.\`rank\` mob_rank, l.Chance chance
    FROM creature_loot_template l JOIN item_template it ON it.entry = l.Item AND l.Reference = 0
    JOIN creature_template c ON c.lootid = l.Entry
    WHERE ${usable} AND l.Chance >= 0.5 AND c.maxlevel BETWEEN ${level - 3} AND ${level + 5} AND ${spawned("c.entry")}`);
  const items = new Map<number, Row & { sources: Row[] }>();
  const where = (table: string, id: number) => {
    const s = spawns(table, id, o.near, 1);
    if (!s.count) return undefined;
    const p = s.points[0];
    if (o.near && (Number(p.map) !== (o.map ?? 0) || Math.hypot(Number(p.x) - o.near[0], Number(p.y) - o.near[1]) > (o.radius ?? 3000))) return undefined;
    return { x: p.x, y: p.y, z: p.z, area: p.area, ...(o.near ? { distance: Math.round(Math.hypot(Number(p.x) - o.near[0], Number(p.y) - o.near[1])) } : {}) };
  };
  const add = (r: Row, src: Row) => {
    const id = Number(r.entry);
    if (!items.has(id)) {
      const stats: Record<string, number> = {};
      for (let i = 1; i <= 10; i++) if (r[`stat_value${i}`]) stats[STAT[Number(r[`stat_type${i}`])] ?? `stat${r[`stat_type${i}`]}`] = Number(r[`stat_value${i}`]);
      items.set(id, { entry: id, name: r.name, slot: SLOT_OF[Number(r.inv)], quality: r.quality, item_level: r.item_level,
        required_level: r.required_level, armor: r.armor || undefined, stats,
        dps: r.dmg_max1 ? Math.round(((Number(r.dmg_min1) + Number(r.dmg_max1)) / 2 / (Number(r.delay) / 1000)) * 10) / 10 : undefined,
        sources: [] } as never);
    }
    items.get(id)!.sources.push(src);
  };
  for (const r of fromQuests) add(r, { type: "quest", quest: r.quest, name: r.quest_name, quest_level: r.quest_level, min_level: r.min_level,
    giver: r.giver ?? undefined, npc: r.giver ?? undefined });
  for (const r of fromVendors) add(r, { type: "vendor", vendor: r.vendor, name: r.vendor_name, price: r.price, npc: r.vendor });
  for (const r of fromDrops) add(r, { type: "drop", mob: r.mob, name: r.mob_name, npc: r.mob,
    level: r.mob_lo === r.mob_hi ? r.mob_lo : `${r.mob_lo}-${r.mob_hi}`, elite: Number(r.mob_rank) > 0 || undefined, chance: r.chance });
  const list = [...items.values()].sort((a, b) => Number(b.item_level) - Number(a.item_level));
  const limit = o.limit ?? (o.slot ? 12 : 3);
  const picked: (Row & { sources: Row[] })[] = [];
  const bySlot: Record<string, unknown[]> = {};
  // best first; locations are looked up only for candidates, and with --near only sources within --radius count
  const d = (s: Row) => Number((s.at as Row | undefined)?.distance ?? 1e9);
  for (const it of list) {
    const s = String(it.slot);
    if (o.slot ? picked.length >= limit : (bySlot[s] ??= []).length >= limit) continue;
    for (const src of it.sources) { if (src.npc) src.at = where("creature_spawn", Number(src.npc)) as never; delete src.npc; }
    it.sources = it.sources.filter((x) => x.at || (!o.near && x.type === "quest")).sort((a, b) => d(a) - d(b)).slice(0, 3);
    if (!it.sources.length) continue;
    picked.push(it);
    if (!o.slot) bySlot[s].push(it);
  }
  return o.slot ? picked : bySlot;
}
