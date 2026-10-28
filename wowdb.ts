// wowdb: structured, read-only lookups over the world DB + the wowbench schema (bun dbsetup.ts), aowow-style:
// each entity comes back with its relations resolved (where an item drops, where a quest giver stands...).
// Usage: wowdb <command> [args] [--flags]; prints JSON. `wowdb help` lists commands.
import { execFileSync } from "node:child_process";

type Row = Record<string, string | number | null>;
const LIMIT = 15;

function q(sql: string): Row[] {
  const out = execFileSync("docker", ["exec", "ac-database", "mysql", "-uworldreader", "-pworldreader", "-B", "acore_world", "-e", sql],
    { stdio: ["ignore", "pipe", "ignore"], maxBuffer: 1 << 28 }).toString();
  const [head, ...lines] = out.split("\n").filter(Boolean);
  if (!head) return [];
  const cols = head.split("\t");
  return lines.map((l) => Object.fromEntries(l.split("\t").map((v, i) => {
    const s = v.replace(/\\n/g, "\n").replace(/\\t/g, "\t").replace(/\\\\/g, "\\");
    return [cols[i], s === "NULL" ? null : s !== "" && /^-?\d+(\.\d+)?$/.test(s) && s.length < 16 ? Number(s) : s];
  })));
}
const q1 = (sql: string) => q(sql)[0];
const num = (v: string | undefined, what: string) => {
  if (!v || !/^-?\d+(\.\d+)?$/.test(v)) throw new Error(`${what}: expected a number, got ${v ?? "nothing"}`);
  return Number(v);
};
const str = (s: string) => `'${s.replace(/\\/g, "\\\\").replace(/'/g, "\\'")}'`;
const ids = (rows: Row[], k: string) => rows.map((r) => Number(r[k])).filter((n) => n) as number[];
const inList = (a: number[]) => (a.length ? a.join(",") : "NULL");

const NPC_FLAGS: [number, string][] = [[0x1, "gossip"], [0x2, "questgiver"], [0x10, "trainer"], [0x80, "vendor"],
  [0x1000, "repair"], [0x2000, "flightmaster"], [0x4000, "spirithealer"], [0x10000, "innkeeper"], [0x20000, "banker"],
  [0x200000, "auctioneer"], [0x400000, "stablemaster"]];
const npcFlags = (f: number) => NPC_FLAGS.filter(([b]) => f & b).map(([, n]) => n);
const RANK = ["normal", "elite", "rare elite", "boss", "rare"];
const QUALITY = ["poor", "common", "uncommon", "rare", "epic", "legendary", "artifact", "heirloom"];
const SLOT = ["none", "head", "neck", "shoulder", "shirt", "chest", "waist", "legs", "feet", "wrists", "hands", "finger",
  "trinket", "one-hand", "shield", "ranged", "back", "two-hand", "bag", "tabard", "robe", "main hand", "off hand",
  "held in off-hand", "ammo", "thrown", "ranged (right)", "quiver", "relic"];
const STAT: Record<number, string> = { 0: "mana", 1: "health", 3: "agility", 4: "strength", 5: "intellect", 6: "spirit",
  7: "stamina", 31: "hit", 32: "crit", 35: "resilience", 36: "haste", 38: "attack power", 45: "spell power", 43: "mp5" };

// Spawn summary: count, zones, and the first few points (x, y, z, area). `table` = creature_spawn | gameobject_spawn
function spawns(table: string, id: number, near?: [number, number], limit = 5) {
  const order = near ? `ORDER BY POW(s.x - ${near[0]}, 2) + POW(s.y - ${near[1]}, 2)` : "ORDER BY s.guid";
  const zones = q(`SELECT z.name zone, COUNT(*) n FROM wowbench.${table} s LEFT JOIN wowbench.area z ON z.id = s.zone
    WHERE s.id = ${id} GROUP BY s.zone ORDER BY n DESC LIMIT 5`);
  const points = q(`SELECT s.map, ROUND(s.x) x, ROUND(s.y) y, ROUND(s.z) z, a.name area FROM wowbench.${table} s
    LEFT JOIN wowbench.area a ON a.id = s.area WHERE s.id = ${id} ${order} LIMIT ${limit}`);
  return { count: zones.reduce((n, z) => n + Number(z.n), 0), zones, points };
}

// Loot entries that contain `item`, following reference_loot_template up to 3 levels (shared drop tables)
function lootEntries(table: string, item: number) {
  let refs = ids(q(`SELECT DISTINCT Entry FROM reference_loot_template WHERE Item = ${item} AND Reference = 0`), "Entry");
  for (let depth = 0, frontier = refs; depth < 2 && frontier.length; depth++) {
    frontier = ids(q(`SELECT DISTINCT Entry FROM reference_loot_template WHERE Reference IN (${inList(frontier)})`), "Entry");
    refs = [...refs, ...frontier];
  }
  return { direct: `${table}.Item = ${item} AND ${table}.Reference = 0`, viaRef: `${table}.Reference IN (${inList(refs)})`, refs };
}

const commands: Record<string, { usage: string; run: (a: string[], f: Record<string, string>) => unknown }> = {
  search: {
    usage: "search <text> [--type item|quest|npc|object|spell|zone|achievement]  name search (15 per type)",
    run: ([text], f) => {
      if (!text) throw new Error("search: give some text");
      const like = str(`%${text}%`), t = f.type;
      const parts: Record<string, string> = {
        item: `SELECT entry id, name, ItemLevel level, Quality quality FROM item_template WHERE name LIKE ${like} ORDER BY ItemLevel`,
        quest: `SELECT ID id, LogTitle name, QuestLevel level FROM quest_template WHERE LogTitle LIKE ${like} ORDER BY QuestLevel`,
        npc: `SELECT entry id, name, subname, minlevel level FROM creature_template WHERE name LIKE ${like} ORDER BY minlevel`,
        object: `SELECT entry id, name, type FROM gameobject_template WHERE name LIKE ${like}`,
        spell: `SELECT id, name, \`rank\` FROM wowbench.spell WHERE name LIKE ${like} ORDER BY level`,
        zone: `SELECT id, name, zone parent_zone, level FROM wowbench.area WHERE name LIKE ${like}`,
        achievement: `SELECT id, name, points FROM wowbench.achievement WHERE name LIKE ${like} AND flags & 1 = 0`,
      };
      return Object.fromEntries(Object.entries(parts).filter(([k]) => !t || k === t).map(([k, sql]) => [k, q(`${sql} LIMIT ${LIMIT}`)]));
    },
  },

  item: {
    usage: "item <id>  stats and every source: drops, containers, vendors, quest rewards, crafting; quests that need it",
    run: ([a]) => {
      const id = num(a, "item");
      const it = q1(`SELECT * FROM item_template WHERE entry = ${id}`);
      if (!it) throw new Error(`no item ${id}`);
      const stats = Object.fromEntries([...Array(10).keys()].map((i) => [it[`stat_type${i + 1}`], it[`stat_value${i + 1}`]])
        .filter(([, v]) => v).map(([k, v]) => [STAT[Number(k)] ?? `stat${k}`, v]));
      const cl = lootEntries("l", id);
      const drops = q(`SELECT c.entry, c.name, c.minlevel, c.maxlevel, c.\`rank\`, l.Chance chance, l.QuestRequired quest_only,
          r.to_alliance, r.to_horde FROM creature_loot_template l JOIN creature_template c ON c.lootid = l.Entry
          LEFT JOIN wowbench.reaction r ON r.faction_template = c.faction WHERE ${cl.direct} ORDER BY l.Chance DESC LIMIT ${LIMIT}`)
        .map((d) => ({ ...d, rank: RANK[Number(d.rank)], spawns: spawns("creature_spawn", Number(d.entry), undefined, 3) }));
      const drops_total = q1(`SELECT COUNT(DISTINCT c.entry) n FROM creature_loot_template l JOIN creature_template c ON c.lootid = l.Entry WHERE ${cl.direct}`)?.n;
      const shared = cl.refs.length ? q1(`SELECT COUNT(DISTINCT c.entry) creatures, MIN(c.minlevel) min_level, MAX(c.maxlevel) max_level
          FROM creature_loot_template l JOIN creature_template c ON c.lootid = l.Entry WHERE ${cl.viaRef}`) : undefined;
      const go = lootEntries("l", id);
      const objects = q(`SELECT g.entry, g.name, l.Chance chance FROM gameobject_loot_template l
          JOIN gameobject_template g ON g.Data1 = l.Entry AND g.type IN (3, 25) WHERE ${go.direct} OR ${go.viaRef} LIMIT ${LIMIT}`)
        .map((o) => ({ ...o, spawns: spawns("gameobject_spawn", Number(o.entry), undefined, 3) }));
      const vendors = q(`SELECT c.entry, c.name, c.subname, v.maxcount stock FROM npc_vendor v JOIN creature_template c ON c.entry = v.entry
          WHERE v.item = ${id} LIMIT ${LIMIT}`).map((v) => ({ ...v, spawns: spawns("creature_spawn", Number(v.entry), undefined, 2) }));
      const rewardCols = [1, 2, 3, 4].map((i) => `RewardItem${i} = ${id}`).concat([1, 2, 3, 4, 5, 6].map((i) => `RewardChoiceItemID${i} = ${id}`));
      const reward_of = q(`SELECT ID id, LogTitle name, QuestLevel level FROM quest_template WHERE ${rewardCols.join(" OR ")} LIMIT ${LIMIT}`);
      const needed_by = q(`SELECT ID id, LogTitle name, QuestLevel level FROM quest_template WHERE ${[1, 2, 3, 4, 5, 6].map((i) => `RequiredItemId${i} = ${id}`).join(" OR ")} LIMIT ${LIMIT}`);
      const crafted_by = q(`SELECT id spell, name, \`rank\` FROM wowbench.spell WHERE create_item = ${id} LIMIT ${LIMIT}`);
      const containers = q(`SELECT i.entry, i.name, l.Chance chance FROM item_loot_template l JOIN item_template i ON i.entry = l.Entry
          WHERE l.Item = ${id} LIMIT ${LIMIT}`);
      return {
        id, name: it.name, quality: QUALITY[Number(it.Quality)], item_level: it.ItemLevel, required_level: it.RequiredLevel,
        slot: SLOT[Number(it.InventoryType)], class: it.class, subclass: it.subclass, stackable: it.stackable,
        buy_price: it.BuyPrice, sell_price: it.SellPrice, armor: it.armor || undefined,
        damage: it.dmg_max1 ? { min: it.dmg_min1, max: it.dmg_max1, speed_ms: it.delay } : undefined, stats,
        allowable_class: it.AllowableClass, allowable_race: it.AllowableRace, quest_item: it.class === 12,
        sources: { drops_total, drops, shared_drop: shared, objects, vendors, reward_of, crafted_by, containers }, needed_by,
      };
    },
  },

  quest: {
    usage: "quest <id>  requirements, giver/turn-in with locations, objectives with where to find them, rewards, chain",
    run: ([a]) => {
      const id = num(a, "quest");
      const qt = q1(`SELECT * FROM quest_template q LEFT JOIN quest_template_addon x ON x.ID = q.ID WHERE q.ID = ${id}`);
      if (!qt) throw new Error(`no quest ${id}`);
      const who = (t: string, g: string) => [
        ...q(`SELECT c.entry, c.name, 'npc' kind FROM ${t} s JOIN creature_template c ON c.entry = s.id WHERE s.quest = ${id}`)
          .map((r) => ({ ...r, spawns: spawns("creature_spawn", Number(r.entry), undefined, 2) })),
        ...q(`SELECT o.entry, o.name, 'object' kind FROM ${g} s JOIN gameobject_template o ON o.entry = s.id WHERE s.quest = ${id}`)
          .map((r) => ({ ...r, spawns: spawns("gameobject_spawn", Number(r.entry), undefined, 2) })),
      ];
      const objectives = [1, 2, 3, 4].filter((i) => qt[`RequiredNpcOrGo${i}`]).map((i) => {
        const t = Number(qt[`RequiredNpcOrGo${i}`]), count = qt[`RequiredNpcOrGoCount${i}`], text = qt[`ObjectiveText${i}`] || undefined;
        if (t > 0) {
          const c = q1(`SELECT c.name, c.minlevel, c.maxlevel, r.to_alliance, r.to_horde FROM creature_template c
            LEFT JOIN wowbench.reaction r ON r.faction_template = c.faction WHERE c.entry = ${t}`);
          return { kill_or_interact: { npc: t, ...c }, count, text, spawns: spawns("creature_spawn", t) };
        }
        return { use_object: { object: -t, name: q1(`SELECT name FROM gameobject_template WHERE entry = ${-t}`)?.name }, count, text,
          spawns: spawns("gameobject_spawn", -t) };
      });
      const items = [1, 2, 3, 4, 5, 6].filter((i) => qt[`RequiredItemId${i}`]).map((i) => {
        const it = Number(qt[`RequiredItemId${i}`]), cl = lootEntries("l", it);
        const from = q(`SELECT c.entry, c.name, c.minlevel, c.maxlevel, l.Chance chance FROM creature_loot_template l
            JOIN creature_template c ON c.lootid = l.Entry WHERE ${cl.direct} OR ${cl.viaRef} ORDER BY l.Chance DESC LIMIT 5`)
          .map((d) => ({ ...d, spawns: spawns("creature_spawn", Number(d.entry), undefined, 3) }));
        const objs = q(`SELECT g.entry, g.name, l.Chance chance FROM gameobject_loot_template l JOIN gameobject_template g
            ON g.Data1 = l.Entry AND g.type IN (3, 25) WHERE ${cl.direct} LIMIT 5`)
          .map((o) => ({ ...o, spawns: spawns("gameobject_spawn", Number(o.entry), undefined, 3) }));
        return { item: it, name: q1(`SELECT name FROM item_template WHERE entry = ${it}`)?.name, count: qt[`RequiredItemCount${i}`],
          dropped_by: from, from_objects: objs };
      });
      const rewardItems = [1, 2, 3, 4].filter((i) => qt[`RewardItem${i}`]).map((i) => ({ item: qt[`RewardItem${i}`], count: qt[`RewardAmount${i}`] }));
      const choice = [1, 2, 3, 4, 5, 6].filter((i) => qt[`RewardChoiceItemID${i}`]).map((i) => ({ item: qt[`RewardChoiceItemID${i}`], count: qt[`RewardChoiceItemQuantity${i}`] }));
      const named = (list: { item: unknown }[]) => list.map((r) => ({ ...r, ...q1(`SELECT name, ItemLevel item_level, InventoryType slot FROM item_template WHERE entry = ${r.item}`) }));
      return {
        id, name: qt.LogTitle, level: qt.QuestLevel, min_level: qt.MinLevel, allowable_races: qt.AllowableRaces,
        allowable_classes: qt.AllowableClasses, objective_summary: qt.LogDescription, details: qt.QuestDescription,
        starts_at: who("creature_queststarter", "gameobject_queststarter"), ends_at: who("creature_questender", "gameobject_questender"),
        objectives, items, required_kills_players: qt.RequiredPlayerKills || undefined,
        rewards: { money: qt.RewardMoney, xp_difficulty: qt.RewardXPDifficulty, spell: qt.RewardSpell || undefined,
          items: named(rewardItems), choose_one: named(choice), reputation: [1, 2, 3, 4, 5].filter((i) => qt[`RewardFactionID${i}`])
            .map((i) => ({ faction: q1(`SELECT name FROM wowbench.faction WHERE id = ${qt[`RewardFactionID${i}`]}`)?.name, value: qt[`RewardFactionValue${i}`] })) },
        chain: { previous: qt.PrevQuestID || undefined, next: qt.NextQuestID || qt.RewardNextQuest || undefined, exclusive_group: qt.ExclusiveGroup || undefined },
      };
    },
  },

  npc: {
    usage: "npc <id> [--near x,y]  level, rank, hostility, spawns, loot, vendor stock, trainer spells, quests",
    run: ([a], f) => {
      const id = num(a, "npc");
      const c = q1(`SELECT c.*, r.to_alliance, r.to_horde, fa.name faction_name FROM creature_template c
        LEFT JOIN wowbench.reaction r ON r.faction_template = c.faction LEFT JOIN wowbench.faction fa ON fa.id = r.faction WHERE c.entry = ${id}`);
      if (!c) throw new Error(`no npc ${id}`);
      const near = f.near ? f.near.split(",").map(Number) as [number, number] : undefined;
      return {
        id, name: c.name, subname: c.subname || undefined, level: [c.minlevel, c.maxlevel], rank: RANK[Number(c.rank)],
        faction: c.faction_name, reaction: { alliance: c.to_alliance, horde: c.to_horde }, services: npcFlags(Number(c.npcflag)),
        spawns: spawns("creature_spawn", id, near),
        loot: q(`SELECT i.entry, i.name, l.Chance chance, l.QuestRequired quest_only FROM creature_loot_template l
            JOIN item_template i ON i.entry = l.Item WHERE l.Entry = ${Number(c.lootid) || -1} AND l.Reference = 0 ORDER BY l.Chance DESC LIMIT ${LIMIT}`),
        sells: q(`SELECT i.entry, i.name, i.BuyPrice price, i.ItemLevel item_level, i.RequiredLevel required_level FROM npc_vendor v
            JOIN item_template i ON i.entry = v.item WHERE v.entry = ${id} LIMIT 60`),
        teaches: q(`SELECT ts.SpellId spell, s.name, s.\`rank\`, ts.MoneyCost cost, ts.ReqLevel level FROM creature_default_trainer d
            JOIN trainer_spell ts ON ts.TrainerId = d.TrainerId LEFT JOIN wowbench.spell s ON s.id = ts.SpellId
            WHERE d.CreatureId = ${id} ORDER BY ts.ReqLevel LIMIT 80`),
        starts_quests: q(`SELECT q.ID id, q.LogTitle name, q.QuestLevel level FROM creature_queststarter s JOIN quest_template q ON q.ID = s.quest WHERE s.id = ${id}`),
        ends_quests: q(`SELECT q.ID id, q.LogTitle name FROM creature_questender s JOIN quest_template q ON q.ID = s.quest WHERE s.id = ${id}`),
      };
    },
  },

  spell: {
    usage: "spell <id>  name, rank, description, cost, cast time, range, cooldowns, item made, trainers that teach it",
    run: ([a]) => {
      const id = num(a, "spell");
      const s = q1(`SELECT * FROM wowbench.spell WHERE id = ${id}`);
      if (!s) throw new Error(`no spell ${id}`);
      return {
        ...s, power_type: ({ 0: "mana", 1: "rage", 3: "energy", 6: "runic power", [-2]: "health" } as Record<number, string>)[Number(s.power_type)],
        cost_note: Number(s.power_type) === 1 || Number(s.power_type) === 6 ? "x10, as get_power()" : undefined,
        create_item: s.create_item ? { id: s.create_item, name: q1(`SELECT name FROM item_template WHERE entry = ${s.create_item}`)?.name } : undefined,
        taught_by: q(`SELECT c.entry, c.name, c.subname, ts.MoneyCost cost, ts.ReqLevel level FROM trainer_spell ts
            JOIN creature_default_trainer d ON d.TrainerId = ts.TrainerId JOIN creature_template c ON c.entry = d.CreatureId
            WHERE ts.SpellId = ${id} LIMIT ${LIMIT}`).map((t) => ({ ...t, spawns: spawns("creature_spawn", Number(t.entry), undefined, 1) })),
      };
    },
  },

  object: {
    usage: "object <id>  type, spawns, loot (chests, herbs, ore), quests",
    run: ([a]) => {
      const id = num(a, "object");
      const o = q1(`SELECT * FROM gameobject_template WHERE entry = ${id}`);
      if (!o) throw new Error(`no object ${id}`);
      return {
        id, name: o.name, type: o.type, spawns: spawns("gameobject_spawn", id),
        loot: [3, 25].includes(Number(o.type)) ? q(`SELECT i.entry, i.name, l.Chance chance FROM gameobject_loot_template l
            JOIN item_template i ON i.entry = l.Item WHERE l.Entry = ${o.Data1} ORDER BY l.Chance DESC LIMIT ${LIMIT}`) : undefined,
        starts_quests: q(`SELECT q.ID id, q.LogTitle name FROM gameobject_queststarter s JOIN quest_template q ON q.ID = s.quest WHERE s.id = ${id}`),
        ends_quests: q(`SELECT q.ID id, q.LogTitle name FROM gameobject_questender s JOIN quest_template q ON q.ID = s.quest WHERE s.id = ${id}`),
      };
    },
  },

  zone: {
    usage: "zone <id|name>  sub-areas with locations, mobs by level, graveyards, services (flight master, inn, vendors, trainers)",
    run: ([a]) => {
      const z = /^\d+$/.test(a ?? "") ? q1(`SELECT * FROM wowbench.area WHERE id = ${a}`)
        : q1(`SELECT * FROM wowbench.area WHERE name = ${str(a ?? "")} ORDER BY zone = 0 DESC LIMIT 1`);
      if (!z) throw new Error(`no zone ${a}`);
      const id = Number(z.zone) || Number(z.id);
      const svc = (bit: number) => q(`SELECT c.entry, c.name, c.subname, ROUND(s.x) x, ROUND(s.y) y, ROUND(s.z) z, r.to_alliance, r.to_horde
          FROM wowbench.creature_spawn s JOIN creature_template c ON c.entry = s.id LEFT JOIN wowbench.reaction r ON r.faction_template = c.faction
          WHERE s.zone = ${id} AND c.npcflag & ${bit} LIMIT ${LIMIT}`);
      return {
        id, name: q1(`SELECT name FROM wowbench.area WHERE id = ${id}`)?.name, map: z.map,
        areas: q(`SELECT a.id, a.name, l.x, l.y FROM wowbench.area a LEFT JOIN wowbench.area_location l ON l.area = a.id AND l.map = a.map
            WHERE a.zone = ${id} ORDER BY a.name`),
        mobs: q(`SELECT c.entry, c.name, c.minlevel, c.maxlevel, c.\`rank\`, r.to_alliance, r.to_horde, COUNT(*) spawns,
            ROUND(AVG(s.x)) x, ROUND(AVG(s.y)) y FROM wowbench.creature_spawn s JOIN creature_template c ON c.entry = s.id
            LEFT JOIN wowbench.reaction r ON r.faction_template = c.faction WHERE s.zone = ${id} AND c.npcflag = 0
            GROUP BY c.entry ORDER BY c.minlevel, spawns DESC LIMIT 60`).map((m) => ({ ...m, rank: RANK[Number(m.rank)] })),
        graveyards: q(`SELECT g.ID id, ROUND(g.x) x, ROUND(g.y) y, ROUND(g.z) z, g.Comment name,
            CASE gz.Faction WHEN 469 THEN 'alliance' WHEN 67 THEN 'horde' ELSE 'any' END faction
            FROM graveyard_zone gz JOIN game_graveyard g ON g.ID = gz.ID WHERE gz.GhostZone = ${id}`),
        flight_masters: svc(0x2000), innkeepers: svc(0x10000), vendors: svc(0x80), trainers: svc(0x10), quest_givers: svc(0x2),
      };
    },
  },

  near: {
    usage: "near <x> <y> [--map 0] [--kind vendor|trainer|questgiver|flightmaster|innkeeper|repair|banker|mob|graveyard|object]\n" +
      "     [--level 8-12] [--hostile] [--faction alliance|horde] [--limit 10]  nearest things to a point",
    run: ([ax, ay], f) => {
      const x = num(ax, "x"), y = num(ay, "y"), map = Number(f.map ?? 0), limit = Math.min(Number(f.limit ?? 10), 50);
      const side = f.faction === "horde" ? "to_horde" : "to_alliance", kind = f.kind ?? "mob";
      const dist = `ROUND(SQRT(POW(s.x - ${x}, 2) + POW(s.y - ${y}, 2)))`;
      const box = `s.map = ${map} AND s.x BETWEEN ${x - 3000} AND ${x + 3000} AND s.y BETWEEN ${y - 3000} AND ${y + 3000}`;
      if (kind === "graveyard")
        return q(`SELECT s.ID id, s.Comment name, ROUND(s.x) x, ROUND(s.y) y, ROUND(s.z) z, ${dist} distance FROM game_graveyard s
          WHERE s.Map = ${map} ORDER BY distance LIMIT ${limit}`);
      if (kind === "object")
        return q(`SELECT o.entry, o.name, o.type, ROUND(s.x) x, ROUND(s.y) y, ROUND(s.z) z, ${dist} distance FROM wowbench.gameobject_spawn s
          JOIN gameobject_template o ON o.entry = s.id WHERE ${box} ORDER BY distance LIMIT ${limit}`);
      const bit = NPC_FLAGS.find(([, n]) => n === kind)?.[0];
      if (kind !== "mob" && bit === undefined) throw new Error(`near: unknown --kind ${kind}`);
      const lv = f.level?.split("-").map(Number);
      const where = kind === "mob" ? `c.npcflag = 0${f.hostile !== undefined ? ` AND r.${side} = 'hostile'` : ""}`
        : `c.npcflag & ${bit} AND r.${side} IN ('friendly', 'neutral')`;
      return q(`SELECT c.entry, c.name, c.subname, c.minlevel, c.maxlevel, r.${side} reaction, ROUND(s.x) x, ROUND(s.y) y, ROUND(s.z) z,
          a.name area, ${dist} distance FROM wowbench.creature_spawn s JOIN creature_template c ON c.entry = s.id
          LEFT JOIN wowbench.reaction r ON r.faction_template = c.faction LEFT JOIN wowbench.area a ON a.id = s.area
          WHERE ${box} AND ${where}${lv ? ` AND c.maxlevel >= ${lv[0]} AND c.minlevel <= ${lv[1] ?? lv[0]}` : ""}
          ORDER BY distance LIMIT ${limit}`);
    },
  },

  quests: {
    usage: "quests --level <n> --race <id> --class <id> [--near x,y] [--map 0] [--limit 25]  quests you can pick up, nearest givers first",
    run: (_, f) => {
      const level = num(f.level, "--level"), race = num(f.race, "--race"), cls = num(f.class, "--class");
      const limit = Math.min(Number(f.limit ?? 25), 100), near = f.near?.split(",").map(Number);
      const dist = near ? `MIN(SQRT(POW(s.x - ${near[0]}, 2) + POW(s.y - ${near[1]}, 2)))` : "0";
      return q(`SELECT q.ID id, q.LogTitle name, q.QuestLevel level, q.MinLevel min_level, x.PrevQuestID requires_previous,
          c.entry giver, c.name giver_name, ROUND(MIN(s.x)) x, ROUND(MIN(s.y)) y, ROUND(${dist}) distance
        FROM quest_template q LEFT JOIN quest_template_addon x ON x.ID = q.ID
        JOIN creature_queststarter qs ON qs.quest = q.ID JOIN creature_template c ON c.entry = qs.id
        JOIN wowbench.creature_spawn s ON s.id = c.entry AND s.map = ${Number(f.map ?? 0)}
        WHERE q.MinLevel <= ${level} AND (q.QuestLevel = -1 OR q.QuestLevel BETWEEN ${level - 6} AND ${level + 4})
          AND (q.AllowableRaces = 0 OR q.AllowableRaces & ${1 << (race - 1)})
          AND (x.AllowableClasses IS NULL OR x.AllowableClasses = 0 OR x.AllowableClasses & ${1 << (cls - 1)})
          AND (x.RequiredSkillID IS NULL OR x.RequiredSkillID = 0)
        GROUP BY q.ID, c.entry ORDER BY ${near ? "distance" : "q.QuestLevel DESC"} LIMIT ${limit}`);
    },
  },

  area: {
    usage: "area <x> <y> [--map 0]  which area and zone a point is in",
    run: ([ax, ay], f) => {
      const x = num(ax, "x"), y = num(ay, "y"), S = 1600 / 3;
      return q1(`SELECT a.id area, a.name, z.id zone, z.name zone_name, a.level FROM wowbench.area_cell g JOIN wowbench.area a ON a.id = g.area
        LEFT JOIN wowbench.area z ON z.id = IF(a.zone = 0, a.id, a.zone)
        WHERE g.map = ${Number(f.map ?? 0)} AND g.ix = ${Math.floor(16 * (32 - x / S))} AND g.iy = ${Math.floor(16 * (32 - y / S))}`) ?? null;
    },
  },

  sql: {
    usage: "sql <query>  raw read-only SQL (acore_world; wowbench.* for the derived tables)",
    run: ([s]) => q(s ?? ""),
  },
};

function main(argv: string[]) {
  const [cmd, ...rest] = argv;
  const args: string[] = [], flags: Record<string, string> = {};
  for (let i = 0; i < rest.length; i++) {
    if (rest[i].startsWith("--")) {
      const k = rest[i].slice(2);
      flags[k] = rest[i + 1] && !rest[i + 1].startsWith("--") ? rest[++i] : "";
    } else args.push(rest[i]);
  }
  const c = commands[cmd ?? ""];
  if (!c) {
    console.log("wowdb <command> ...  (JSON out)\n" + Object.entries(commands).map(([k, v]) => `  ${v.usage.startsWith(k) ? "" : k + " "}${v.usage}`).join("\n"));
    return cmd && cmd !== "help" ? 1 : 0;
  }
  try {
    console.log(JSON.stringify(c.run(args, flags), (_, v) => (v === null || v === "" ? undefined : v)));
    return 0;
  } catch (e) {
    console.log(JSON.stringify({ error: (e as Error).message }));
    return 1;
  }
}

process.exit(main(process.argv.slice(2)));
