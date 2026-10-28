// bun server/recorder/replay.ts <run-dir | team-dir> [--out replay.html] [--log server/azerothcore/env/dist/logs/recorder.jsonl]
//                [--chars A,B] [--from <ms|ISO>] [--to <ms|ISO>]
// Builds a self-contained HTML replay of a run from the mod-recorder log: a terrain map rendered from the
// server's own map files (data/maps: heights, water, zone borders) with every player's route, a seek bar,
// and chat / combat / event panels synced to the playhead. Characters and the time window come from the
// run's judge.json (or team.json), or from --chars/--from/--to.
import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { CLASS, COLOR, SERVER_LOG, areas, clientChatFilter, itemNames, labels as areaLabels, nameUnits, parseLines, renderMap, runSources, serverChat, trackRow, transcriptEvents } from "./map.ts";

const [dirArg, ...rest] = process.argv.slice(2);
const flag = (k: string) => { const i = rest.indexOf(`--${k}`); return i >= 0 ? rest[i + 1] : undefined; };
const time = (s?: string) => (s ? (/^\d+$/.test(s) ? Number(s) : Date.parse(s)) : undefined);

// ---------- which characters, which window ----------
let chars = flag("chars")?.split(",") ?? [];
let from = time(flag("from")), to = time(flag("to"));
let title = "WoW run";
if (dirArg && existsSync(dirArg)) {
  title = dirArg.replace(/\/$/, "").split("/").pop()!;
  const judges = existsSync(join(dirArg, "team.json")) ? JSON.parse(readFileSync(join(dirArg, "team.json"), "utf8")).agents
    : existsSync(join(dirArg, "judge.json")) ? [JSON.parse(readFileSync(join(dirArg, "judge.json"), "utf8"))] : [];
  if (!chars.length) chars = judges.map((j: { char: string }) => j.char);
  const ends = judges.map((j: { at: string }) => Date.parse(j.at));
  to ??= ends.length ? Math.max(...ends) + 5000 : undefined;
  from ??= existsSync(join(dirArg, "go")) ? Number(readFileSync(join(dirArg, "go"), "utf8")) * 1000
    : judges.length ? Math.min(...judges.map((j: { at: string; elapsed_s: number }) => Date.parse(j.at) - j.elapsed_s * 1000)) : undefined;
}
// Sources: --log, else the run's own client-side records (azsdk -record: record.jsonl, <agent>.record.jsonl),
// else the server's mod-recorder log.
const runRecords = dirArg && existsSync(dirArg) ? readdirSync(dirArg).filter((f) => /(^|\.)record\.jsonl$/.test(f)).map((f) => join(dirArg, f)) : [];
const logFiles = flag("log") ? [flag("log")!] : runRecords.length ? runRecords
  : [new URL("../azerothcore/env/dist/logs/recorder.jsonl", import.meta.url).pathname];
const logFile = logFiles.join(", ");
const transcripts = dirArg && existsSync(dirArg) ? runSources(dirArg).transcripts.filter((t) => existsSync(t.path)) : [];
// with transcripts (a bench run): player chat once, from the server log, instead of every agent's received copy
const runChars = new Set(transcripts.map((t) => t.name)), keepClient = clientChatFilter();
const fromLogs = logFiles.flatMap((f) => parseLines(readFileSync(f, "utf8")));
const all = [...(runChars.size ? fromLogs.filter(keepClient) : fromLogs),
  ...(runChars.size && existsSync(SERVER_LOG) ? serverChat(readFileSync(SERVER_LOG, "utf8"), runChars) : []),
  ...transcripts.flatMap((t) => transcriptEvents(readFileSync(t.path, "utf8"), t.name))].sort((a, b) => a.t - b.t);
nameUnits(all);
const evs = all.filter((e) => (!chars.length || chars.includes(e.n ?? "")) && (!from || e.t >= from) && (!to || e.t <= to));
if (!evs.length) throw new Error(`no recorder events for ${chars.join(",") || "anyone"} in that window (${logFile})`);
if (!chars.length) chars = [...new Set(evs.map((e) => e.n!).filter(Boolean))];
const t0 = from ?? evs[0].t, t1 = to ?? evs[evs.length - 1].t;

// ---------- tracks ----------
const pos = evs.filter((e) => e.ev === "pos");
const mapCount = new Map<number, number>();
for (const p of pos) mapCount.set(p.m as number, (mapCount.get(p.m as number) ?? 0) + 1);
const mapId = [...mapCount].sort((a, b) => b[1] - a[1])[0]?.[0] ?? 0;
const players = chars.map((n) => {
  const login = all.findLast((e) => e.n === n && e.ev === "login" && e.t <= t1); // may predate the window
  const cls = Number(login?.cls ?? 0);
  return {
    name: n, cls: CLASS[cls] ?? "", color: COLOR[cls] ?? "#888",
    track: pos.filter((p) => p.n === n).map((p) => trackRow(p, t0)),
  };
});
const events = evs.filter((e) => e.ev !== "pos").map((e) => ({ ...e, t: e.t - t0 }));

// ---------- map image from data/maps ----------
const inMap = pos.filter((p) => p.m === mapId);
let xMin = Math.min(...inMap.map((p) => p.x as number)), xMax = Math.max(...inMap.map((p) => p.x as number));
let yMin = Math.min(...inMap.map((p) => p.y as number)), yMax = Math.max(...inMap.map((p) => p.y as number));
const pad = Math.max(250, 0.15 * Math.max(xMax - xMin, yMax - yMin));
xMin -= pad; xMax += pad; yMin -= pad; yMax += pad;
const res = Math.max(1.5, Math.ceil((Math.max(xMax - xMin, yMax - yMin) / 1800) * 2) / 2); // yards per pixel
const { png, w: W, h: H } = renderMap(mapId, xMin, xMax, yMin, yMax, res);
const labels = areaLabels(mapId, [xMin, xMax, yMin, yMax]);
const names = areas(mapId).names;
const items = itemNames([...new Set(events.filter((e) => e.ev === "loot").map((e) => Number(e.item)))]);

const data = { title, start: t0, duration: t1 - t0, map: { id: mapId, xMax, yMax, res, w: W, h: H }, players, events, labels, zones: Object.fromEntries(names), items };
const html = readFileSync(new URL("replay.html", import.meta.url).pathname, "utf8")
  .replace("__TITLE__", title)
  .replace("__MAP__", `data:image/png;base64,${png.toString("base64")}`)
  .replace("__DATA__", JSON.stringify(data).replace(/</g, "\\u003c"));
const out = flag("out") ?? join(dirArg && existsSync(dirArg) ? dirArg : ".", "replay.html");
writeFileSync(out, html);
console.log(`${out}: ${players.length} players, ${pos.length} positions, ${events.length} events, map ${W}x${H} @ ${res} yd/px, ${(html.length / 1e6).toFixed(1)} MB`);
