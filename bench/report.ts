// bun bench/report.ts <run dir | team dir>: the report card for each agent in a run, next to its score. How the agent
// spent the run, not what it scored: how much of the time a script was playing (duty cycle), progress by what the
// agent was doing (waiting on wow wait / thinking / in a call), what woke it and whether a script restart followed,
// restarts and their warm-up cost, deaths, stalls, and tokens. Writes report.json in the dir and prints a table.
// Reported, not scored. Progress is the judge's value (XP for score goals), per hour of sampled time.
import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";

const dir = process.argv[2];
if (!dir || !existsSync(dir)) throw new Error("usage: bun bench/report.ts <run dir | team dir>");
const jsonl = (f: string): any[] => existsSync(f)
  ? readFileSync(f, "utf8").split("\n").flatMap((l) => { try { return l ? [JSON.parse(l)] : []; } catch { return []; } }) : [];
const ms = (s: string) => Date.parse(s);
const RESTART = /task\.(spawn|keep|stop|kill)|wow -f|\.stop\(|dofile/;
const WAIT = /wow wait|(^|[;&| ])sleep \d|wow -w/;
// what woke a wait with no --types: the default subscription, plus the old default (level_up, error, any chat)
const WAKERS = ["attention", "death", "stall", "script_error", "task_finished", "party_invite", "trade", "gear_broken",
  "chat", "level_up", "error", "resurrected"];
const median = (a: number[]) => a.length ? Math.round([...a].sort((x, y) => x - y)[Math.floor(a.length / 2)]) : null;
const p90 = (a: number[]) => a.length ? Math.round([...a].sort((x, y) => x - y)[Math.floor(a.length * 0.9)]) : null;
const r1 = (v: number | null) => v === null || !isFinite(v) ? null : Math.round(v * 10) / 10;

// the agents in this dir: [agent, run dir, record, events dir]
function agents(): [string, string, string, string][] {
  if (existsSync(join(dir, "go"))) { // a team: bench.sh writes its run dir into go.<agent>.ready (older runs: by time)
    const runs = join(dir, ".."), t0 = Date.parse(basename(dir).slice(0, 15).replace(/(\d{4})(\d\d)(\d\d)-(\d\d)(\d\d)(\d\d)/, "$1-$2-$3T$4:$5:$6"));
    return readdirSync(dir).filter((f) => f.endsWith(".record.jsonl")).map((f) => {
      const a = f.split(".")[0], ready = join(dir, `go.${a}.ready`);
      let run = existsSync(ready) ? readFileSync(ready, "utf8").trim() : "";
      if (!run) run = join(runs, readdirSync(runs).find((d) => d.endsWith("-" + a) &&
        Math.abs(Date.parse(d.slice(0, 15).replace(/(\d{4})(\d\d)(\d\d)-(\d\d)(\d\d)(\d\d)/, "$1-$2-$3T$4:$5:$6")) - t0) < 600e3) ?? "");
      return [a, run, join(dir, f), dir];
    });
  }
  const meta = existsSync(join(dir, "meta.json")) ? JSON.parse(readFileSync(join(dir, "meta.json"), "utf8")) : {};
  return [[meta.agent ?? basename(dir), dir, join(dir, "record.jsonl"), dir]];
}

function card(agent: string, run: string, record: string, evdir: string) {
  const meta = existsSync(join(run, "meta.json")) ? JSON.parse(readFileSync(join(run, "meta.json"), "utf8")) : {};
  const score = jsonl(record).filter((e) => e.ev === "score");
  const char: string = meta.char ?? score[0]?.n;
  const events = jsonl(join(evdir, `${char}.events.jsonl`));
  // the agent's tool calls [start, end, command] (Claude: tool_use -> tool_result; Codex: item.started -> completed)
  const calls: [number, number, string][] = [], pending = new Map<string, [number, string]>();
  const usage = new Map<string, any>();
  let cost = 0, costSeen = false;
  const results = new Map<string, any>(); // Claude's result line per session: its real totals (none if it was killed)
  for (const e of jsonl(join(run, "agent.jsonl"))) {
    const t = e.timestamp ? ms(e.timestamp) : 0;
    if (e.type === "assistant" && t) {
      for (const c of e.message?.content ?? []) if (c.type === "tool_use") pending.set(c.id, [t, c.input?.command ?? c.name]);
      if (e.message?.usage) usage.set(e.message.id, { ...e.message.usage, session: e.session_id });
    } else if (e.type === "user" && t && Array.isArray(e.message?.content)) {
      for (const c of e.message.content) {
        const p = c.type === "tool_result" && pending.get(c.tool_use_id);
        if (p) { calls.push([p[0], t, p[1]]); pending.delete(c.tool_use_id); }
      }
    } else if (e.type === "item.started" && e.item?.type === "command_execution") pending.set(e.item.id, [t, e.item.command]);
    else if (e.type === "item.completed" && pending.has(e.item?.id)) { const p = pending.get(e.item.id)!; calls.push([p[0], t, p[1]]); pending.delete(e.item.id); }
    else if (e.type === "result" && typeof e.total_cost_usd === "number") { cost += e.total_cost_usd; costSeen = true; results.set(e.session_id, Object.values(e.modelUsage ?? {}).reduce((u: any, m: any) => ({ // per model; usage is partial
      input_tokens: u.input_tokens + (m.inputTokens ?? 0), output_tokens: u.output_tokens + (m.outputTokens ?? 0),
      cache_read_input_tokens: u.cache_read_input_tokens + (m.cacheReadInputTokens ?? 0),
      cache_creation_input_tokens: u.cache_creation_input_tokens + (m.cacheCreationInputTokens ?? 0) }),
      { input_tokens: 0, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 })); }
  }
  calls.sort((a, b) => a[0] - b[0]);
  // tasks running over time, from task_started/task_finished (a login clears them)
  const running = new Set<number>(), changes: [number, number][] = [], starts: number[] = [];
  for (const e of events) {
    if (e.type === "task_started") { running.add(e.data.task); starts.push(e.t); }
    else if (e.type === "task_finished") running.delete(e.data.task);
    else continue;
    changes.push([e.t, running.size]);
  }
  const at = <T>(xs: [number, T][], m: number, def: T) => { let lo = 0, hi = xs.length; while (lo < hi) { const mid = (lo + hi) >> 1; if (xs[mid][0] <= m) lo = mid + 1; else hi = mid; } return lo ? xs[lo - 1][1] : def; };
  const callAt = (m: number) => { let c: [number, number, string] | undefined; for (const x of calls) { if (x[0] > m) break; c = x; } return c; };
  const state = (m: number) => {
    const c = callAt(m);
    if (!c) return "before";
    if (m > c[1]) return "thinking";
    return WAIT.test(c[2]) && !RESTART.test(c[2]) ? "waiting" : "in_call";
  };
  // 15 s samples -> progress per hour by state; gaps over a minute (a pause, a resume) are skipped
  const by: Record<string, [number, number]> = {}, add = (k: string, dv: number, dh: number) => { (by[k] ??= [0, 0])[0] += dv; by[k][1] += dh; };
  const gaps: number[] = []; let gapStart: number | null = null;
  const sinceStart: Record<string, [number, number]> = {};
  for (let i = 1; i < score.length; i++) {
    const [a, b] = [score[i - 1], score[i]];
    if (b.t - a.t > 60e3) { gapStart = null; continue; }
    const m = (a.t + b.t) / 2, dv = b.value - a.value, dh = (b.t - a.t) / 3.6e6, n = at(changes, m, 0);
    add("all", dv, dh);
    add(n ? "script" : "no_script", dv, dh);
    if (n) add(state(m), dv, dh);
    const s = starts.filter((t) => t <= m).at(-1);
    if (s !== undefined && n) { const k = m - s < 60e3 ? "first_minute" : m - s < 600e3 ? "settled" : "long_running"; (sinceStart[k] ??= [0, 0])[0] += dv; sinceStart[k][1] += dh; }
    if (!n && gapStart === null) gapStart = a.t;
    if (n && gapStart !== null) { gaps.push((a.t - gapStart) / 1000); gapStart = null; }
  }
  if (gapStart !== null && score.length) gaps.push((score.at(-1).t - gapStart) / 1000); // still idle at the end
  const H = by.all?.[1] ?? 0, rate = (k: [number, number] | undefined) => k && k[1] > 0 ? Math.round(k[0] / k[1]) : null;
  // waits: early wakes, what woke them, and whether a restart followed within 3 calls
  const wakes: Record<string, { n: number; restart: number }> = {};
  let waits = 0, early = 0;
  calls.forEach(([s, e, cmd], k) => {
    const m = cmd.match(/wow wait([^;|&]*)/);
    if (!m) return;
    waits++;
    // its arguments, also inside a quoted wrapper (Codex: zsh -lc 'wow wait 30 --types death'): the bare number is
    // the timeout (the server's default is 60 s), --types the list
    const tok = m[1].split(/>/)[0].replace(/['"]/g, " ").trim().split(/\s+/);
    let limit = 60, types = WAKERS;
    for (let i = 0; i < tok.length; i++) {
      if (tok[i] === "--types") types = tok[++i].split(",");
      else if (tok[i].startsWith("--")) i++;
      else if (/^\d+$/.test(tok[i])) limit = +tok[i];
    }
    if ((e - s) / 1000 > limit - 3) return;
    early++;
    const hit = events.find((ev) => ev.t >= s && ev.t <= e + 1500 && types.some((p) => {
      const [t, kind] = p.split(":"), g = (pat: string, v: string) => pat === v || pat === "*" || (pat.endsWith("*") && v?.startsWith(pat.slice(0, -1)));
      return g(t, ev.type) && (!kind || g(kind, ev.data?.kind));
    }));
    const why = hit ? (hit.type === "chat" ? `chat:${hit.data?.kind}` : hit.type) : "?";
    const w = (wakes[why] ??= { n: 0, restart: 0 });
    w.n++;
    if (calls.slice(k + 1, k + 4).some((c) => RESTART.test(c[2]))) w.restart++;
  });
  // tokens per session (a resumed run has several): its result line's totals, else the sum over its messages, whose
  // usage is recorded as each message starts: right for input and cache, not for output (null then)
  const parts = [...results.values(), ...[...usage.values()].filter((u) => !results.has(u.session)).map((u) => ({ ...u, output_tokens: NaN }))];
  const tok = parts.reduce((s, u) => ({ input: s.input + (u.input_tokens ?? 0), output: s.output + (u.output_tokens ?? 0),
    cache_read: s.cache_read + (u.cache_read_input_tokens ?? 0), cache_write: s.cache_write + (u.cache_creation_input_tokens ?? 0) }),
    { input: 0, output: 0, cache_read: 0, cache_write: 0 });
  if (isNaN(tok.output)) (tok as any).output = null;
  const deaths = events.filter((e) => e.type === "death").length;
  const restarts = calls.filter((c) => RESTART.test(c[2])).length;
  const share = (k: string) => H ? r1(100 * (by[k]?.[1] ?? 0) / H) : null;
  return {
    agent, char, runtime: meta.runtime ?? null, model: meta.model ?? null,
    hours: r1(H), progress: by.all?.[0] ?? 0, progress_per_hour: rate(by.all),
    duty_cycle_pct: share("script"), // time with a script task running
    idle_gaps: { n: gaps.length, median_s: median(gaps), p90_s: p90(gaps), total_min: r1(gaps.reduce((s, g) => s + g, 0) / 60) },
    progress_per_hour_by_state: { // while a script runs: the agent blocked in wow wait/sleep, between calls, or in a call
      waiting: rate(by.waiting), thinking: rate(by.thinking), in_call: rate(by.in_call), no_script: rate(by.no_script) },
    time_pct_by_state: { waiting: share("waiting"), thinking: share("thinking"), in_call: share("in_call"), no_script: share("no_script") },
    wakes: { waits, early, per_hour: H ? r1(early / H) : null,
      by_trigger: Object.fromEntries(Object.entries(wakes).sort((a, b) => b[1].n - a[1].n)) },
    restarts: { agent_calls: restarts, per_hour: H ? r1(restarts / H) : null, script_starts_per_hour: H ? r1(starts.length / H) : null,
      warmup_progress_per_hour: { first_minute: rate(sinceStart.first_minute), settled: rate(sinceStart.settled) } },
    deaths, deaths_per_hour: H ? r1(deaths / H) : null, stalls: events.filter((e) => e.type === "stall").length,
    calls: calls.length, think_s: { median: median(calls.slice(1).map((c, i) => (c[0] - calls[i][1]) / 1000)) },
    tokens: parts.length ? tok : null, cost_usd: costSeen ? r1(cost) : null,
  };
}

const cards = agents().filter(([, run]) => run && existsSync(join(run, "agent.jsonl"))).map((a) => card(...a));
writeFileSync(join(dir, "report.json"), JSON.stringify({ agents: cards }, null, 2) + "\n");
const pad = (v: any, n: number) => String(v ?? "-").padStart(n);
console.log("agent  char         runtime  h    prog/h  duty%  idle(n,med)  wait/think/call/none prog/h        wakes/h  restarts/h  deaths/h");
for (const c of cards) {
  const s = c.progress_per_hour_by_state;
  console.log(`${c.agent.padEnd(6)} ${String(c.char).padEnd(12)} ${String(c.runtime ?? "").padEnd(7)} ${pad(c.hours, 4)} ${pad(c.progress_per_hour, 7)} ${pad(c.duty_cycle_pct, 6)}` +
    `  ${pad(c.idle_gaps.n, 4)},${pad(c.idle_gaps.median_s, 4)}s  ${pad(s.waiting, 6)}/${pad(s.thinking, 6)}/${pad(s.in_call, 6)}/${pad(s.no_script, 6)}` +
    `  ${pad(c.wakes.per_hour, 7)}  ${pad(c.restarts.per_hour, 10)}  ${pad(c.deaths_per_hour, 8)}`);
}
console.log(`wrote ${join(dir, "report.json")}`);
