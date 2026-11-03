import versionList from '../data/versions.json';
// Benchmark results: the runs `azbench publish` wrote into src/data/runs, grouped by task and by model.

export interface Player {
  char: string;
  runtime: string;
  model: string;
  class: string;
  spec: string;
  race: string;
  met: boolean;
  time_to_goal_s?: number;
  value?: number;
  score?: number;
  level?: number;
  deaths?: number;
  hours?: number;
  cost_usd?: number;
  calls?: number;
  gave_up?: string;
  died?: boolean;
  tokens?: { input: number; output: number; cache_read: number; cache_write: number };
  effort?: string; // reasoning effort: low ... max, or "default" (the runtime's own, unrecorded)
  spec_name?: string; // "Warrior - Protection"
  role?: string; // Tank, Healer, DPS
  series?: [number, number, number, number, number, number][]; // [elapsed s, level, XP earned (-1: not recorded), deaths, quests, kills]
  deaths_at?: number[]; // elapsed s
  track?: [number, number, number, number, number][]; // [s from the start, map, x, y, group id]; map -1: a teleport
  death_list?: { t: number; map: number; x: number; y: number; killer: string; place: string }[];
  failures?: { op: string; code: string; count: number }[];
  time_use?: { seconds: number; fighting_pct: number; travelling_pct: number; idle_pct: number; dead_pct: number; with_group_pct: number; broken_weapon_pct?: number };
  time_pct_by_state?: Record<string, number>; // the agent: thinking, waiting (a script running), in_call, no_script
  combat?: { damage_dealt: number; damage_taken: number; healing_done: number; kills: number; party_kills?: number; deaths?: number; xp?: number };
  think_median_s?: number;
  // from the game log: milestones (s from the start), loot by quality, zones in order, killing blows and damage by
  // creature ([name, n], most first), players' chat heard by speaker, time with a pet out (%)
  milestones?: { t: number; kind: 'quest' | 'achievement' | 'level' | 'loot'; what: string; quality?: string }[];
  loot?: Record<string, number>;
  places?: { t: number; zone: string }[];
  kills_by?: [string, number][];
  damage_to?: [string, number][];
  heard_from?: Record<string, number>;
  pet_pct?: number;
  downloads?: { trajectory?: string; game_log?: string }; // paths in the assets bucket (PUBLIC_ASSETS_URL), from just upload
  // from the agent's trajectory (ATIF): tool calls by what they ran, failures, and model calls where the runtime marks them
  play?: {
    tools: { name: string; calls: number; failed: number; median_s?: number }[];
    calls: number;
    failed_pct: number;
    model_calls?: number;
    output_per_call?: number;
  };
  // the report's: progress per hour while the agent was thinking, waiting on a script, in a call or with nothing running
  progress_per_hour_by_state?: Record<string, number | null>;
  coop?: { assist_share: number; assists: number; useful: number; hits: number; contested_hits: number };
  idle_gaps?: { n: number; median_s: number; p90_s: number; total_min: number }; // stretches with no script running
  stalls?: { no_xp: number | null; slow: number };
  // the tooling's counts (null: not instrumented)
  reliability?: { movement_preemptions: number | null; movement_wait_ms: number | null; route_timeouts: number | null; recovery_attempts: number | null };
  kit?: {
    gear: { slot: number; entry: number; name: string; quality: number; level: number; icon?: string }[];
    talents: { id: number; name: string; rank?: string; icon?: string }[];
    abilities: { id: number; name: string; rank?: string; icon?: string }[];
  };
  casts?: Record<string, number>; // spell name -> times cast in the run
  cast_icons?: Record<string, string>; // spell name -> icon (public/icons/<icon>.png)
  trajectory?: [number, number, number, string, number][]; // [minute, SDK calls succeeded, failed, top failure "op code", its count]
  cost_series?: [number, number][]; // [s from the start, cumulative USD]
  checkpoints?: { name: string; at_s: number | null }[]; // the task's checkpoints (a dungeon's bosses), at_s when reached
  hardcore?: boolean; // set on load: scored on total XP earned before death or the clock, not XP per hour
  // provider outages waited out with the clock held (azbench run outage.go)
  outages?: { why: string; start: string; held_s: number; safe_wait_s: number; probes: number; outcome: string }[];
}

export interface MapBox {
  map: number;
  name: string;
  file?: string; // src/assets/runs/<file>: a relief image of the box (continents)
  x_min: number;
  x_max: number;
  y_min: number;
  y_max: number;
}

export interface Run {
  id: string;
  task: string;
  scenario: string;
  description: string;
  goal: string;
  minutes: number;
  hardcore: boolean;
  level: number;
  started: string;
  players: Player[];
  version: string; // benchmark version (suite/versions.yaml)
  sdk_rev?: string;
  maps?: MapBox[];
  together?: number[]; // per minute: 1 when all players stood within 40 yd
  chat_per_min?: Record<string, number>[]; // per minute: messages between players by kind
}

// What each task is, for the pages. A task without an entry here still shows, under its id.
export const TASKS: Record<string, { title: string; short: string; summary: string; scoring: string; order: number }> = {
  travel: {
    title: 'Travel from Northshire to Darnassus',
    short: 'Travel',
    summary:
      'A new level-30 Human warrior starts in Northshire and has to reach Darnassus on the other continent. The route needs two boats and a portal. Each model plays one character, all at the same time, with 90 minutes.',
    scoring: 'Whether the character arrives, then how long it takes. Cost is at list prices.',
    order: 1,
  },
  hogger: {
    title: 'Hogger from level 1',
    short: 'Hogger',
    summary:
      'Three new level-1 characters start in Northshire: a Protection Paladin, a Holy Priest and a Frost Mage. The goal is the quest "Wanted: Hogger", whose target is an elite level-11 gnoll. The clock is three hours.',
    scoring: 'Whether the quest is completed, then how long it takes.',
    order: 2,
  },
  hardcore: {
    title: 'Hardcore levelling',
    short: 'Hardcore',
    summary:
      'A new level-1 Human Arms warrior starts in Northshire with four hours. The first death ends the run.',
    scoring: 'Total XP earned before the first death or the end of the four hours. Level reached is shown alongside.',
    order: 3,
  },
  'deadmines-geared': {
    title: 'The Deadmines at level 20',
    short: 'Deadmines',
    summary:
      'Five level-20 characters start in Northshire with green gear and their class spells trained: a tank, a healer and three damage dealers. The goal is to clear the Deadmines and kill Edwin VanCleef. The clock is two hours.',
    scoring: 'Whether the party earns the Deadmines achievement, then how long it takes. Each boss killed is a checkpoint, so an unfinished run shows how far it got.',
    order: 4,
  },
  ubrs: {
    title: 'Upper Blackrock Spire at level 60',
    short: 'UBRS',
    summary:
      'Ten level-60 characters, every Alliance class but the death knight, start at Morgan\'s Vigil in pre-raid dungeon gear: two tanks, three healers and five damage dealers. The goal is to clear Upper Blackrock Spire and kill General Drakkisath. The clock is two hours.',
    scoring: 'Whether the party earns the Upper Blackrock Spire achievement, then how long it takes. Each boss killed is a checkpoint, so an unfinished run shows how far it got.',
    order: 5,
  },
};

const runs: Run[] = Object.values(
  import.meta.glob<Run>('../data/runs/*.json', { eager: true, import: 'default' }),
).sort((a, b) => a.started.localeCompare(b.started));
// hardcore is scored on total XP earned before death or the clock (how far it got without dying), not on a rate
for (const r of runs) if (r.hardcore) for (const p of r.players) p.hardcore = true;

// Benchmark versions (suite/versions.yaml, copied by azbench publish). Results from different versions aren't
// compared: every list below takes a version, or 'all'.
export interface Version {
  version: string;
  from: string;
  notes: string;
}
export const VERSIONS: Version[] = versionList;
export const ALL = 'all';
export const LATEST = VERSIONS.at(-1)?.version ?? ALL;
const inVersion = (r: Run, v: string) => v === ALL || r.version === v;

export const allRuns = (v = ALL) => runs.filter(r => inVersion(r, v));
export const runById = (id: string) => runs.find(r => r.id === id);
export const runsOf = (task: string, v = ALL) => runs.filter(r => r.task === task && inVersion(r, v));
export const tasks = (v = ALL) =>
  [...new Set(allRuns(v).map(r => r.task))].sort((a, b) => (TASKS[a]?.order ?? 99) - (TASKS[b]?.order ?? 99));
// the versions a task (or any task) has runs in, oldest first
export const versionsOf = (task?: string) => VERSIONS.filter(v => runs.some(r => r.version === v.version && (!task || r.task === task)));
// a task's default version: the latest it was run in
export const latestFor = (task: string) => versionsOf(task).at(-1)?.version ?? ALL;
export const versionLabel = (v: string) => (v === ALL ? 'All versions' : `v${v}`);
export const taskTitle = (task: string) => TASKS[task]?.title ?? task;
export const taskShort = (task: string) => TASKS[task]?.short ?? task;

// A progress task (goal score:...) ranks by XP per hour; the others by goals met, then time to them.
export const isProgress = (task: string) => runsOf(task)[0]?.goal.startsWith('score:') ?? false;

// A levelling player's result, from the judge's samples: XP earned per hour of play, or on a hardcore run the total
// XP earned (hardcore: how far it got before dying or the clock ran out).
export function xpRate(p: Player): number | undefined {
  const s = p.series;
  if (!s || s.length < 2 || s.at(-1)![0] <= 0 || s[0][2] < 0) return undefined; // -1: not recorded (the first runs)
  const xp = s.at(-1)![2] - s[0][2];
  return p.hardcore ? xp : xp / (s.at(-1)![0] / 3600);
}
export const levelsGained = (p: Player) => (p.series?.length ? p.series.at(-1)![1] - p.series[0][1] : undefined);
// a levelling result with its unit: XP/h, or XP for a hardcore task (total XP)
export const isTotal = (task?: string) => !!task && !!runsOf(task)[0]?.hardcore;
export const rate = (v?: number, task?: string) => (v == null ? '–' : `${Math.round(v).toLocaleString('en')} ${isTotal(task) ? 'XP' : 'XP/h'}`);
export const unit = (task: string) => (isTotal(task) ? 'XP' : 'XP/h');
export const metricName = (task: string) => (isTotal(task) ? 'total XP' : 'XP per hour');

// claude-sonnet-5-5 -> Sonnet 5.5, gpt-6.1-terra -> GPT-6.1 Terra
export function modelName(model: string): string {
  model = model.replace(/^openrouter\//, '').replace(/^(anthropic|openai|google|deepseek|moonshotai|x-ai)\//, '');
  const dotted = model.match(/^claude-([a-z]+)-(\d+)\.(\d+)/); // claude-sonnet-5.5 (OpenRouter's spelling)
  if (dotted) return `${dotted[1][0].toUpperCase()}${dotted[1].slice(1)} ${dotted[2]}.${dotted[3]}`;
  const claude = model.match(/^claude-([a-z]+)-(\d+)-(\d+)/);
  if (claude) return `${claude[1][0].toUpperCase()}${claude[1].slice(1)} ${claude[2]}.${claude[3]}`;
  const gpt = model.match(/^gpt-([\d.]+)-?(.*)$/);
  if (gpt) return `GPT-${gpt[1]}${gpt[2] ? ' ' + gpt[2][0].toUpperCase() + gpt[2].slice(1) : ''}`;
  // gemini-4-argon -> Gemini 4 Argon, deepseek-v4-pro -> DeepSeek V4 Pro
  return model
    .split('-')
    .map(w => (w === 'deepseek' ? 'DeepSeek' : /^v\d/.test(w) ? w.toUpperCase() : w[0].toUpperCase() + w.slice(1)))
    .join(' ');
}

export const harnessName = (runtime: string) =>
  // ponytail: opencode runs name their provider in the model id
  ({ claude: 'Claude Code', codex: 'Codex CLI', opencode: 'opencode' })[runtime] ?? runtime;

export interface ModelRow {
  key: string; // model and effort (entryKey)
  model: string;
  effort?: string;
  lab: string;
  runtime: string;
  attempts: number;
  met: number;
  meanTime?: number; // seconds, over the attempts that met the goal
  bestTime?: number;
  meanCost?: number;
  costPerSuccess?: number; // all its attempts' cost / goals met (goal tasks)
  meanOutputTokens?: number;
  meanRate?: number; // XP per hour (progress tasks)
  bestRate?: number;
  meanLevels?: number;
  meanScore?: number; // the judge's score: goal tasks with checkpoints rank by it
  meanCheckpoints?: number; // checkpoints reached, over all attempts
  checkpoints?: number; // how many the task has
}

const mean = (xs: number[]) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : undefined);

// A goal task with checkpoints (a dungeon's bosses): an unfinished run scores the share it reached (the judge's
// checkpointScore), so its entries rank by mean score.
export const hasCheckpoints = (task: string, v = ALL) => runsOf(task, v).some(r => r.players.some(p => p.checkpoints?.length));
const reachedOf = (p: Player) => p.checkpoints?.filter(c => c.at_s != null).length;

// One row per model across a task's runs, best first: most goals met, then the fastest mean time (with checkpoints,
// the highest mean score).
export function leaderboard(task: string, v = ALL): ModelRow[] {
  const by = new Map<string, Player[]>();
  for (const r of runsOf(task, v)) for (const p of r.players) by.set(entryKey(p), [...(by.get(entryKey(p)) ?? []), p]);
  return [...by.entries()]
    .map(([key, ps]) => {
      const times = ps.filter(p => p.met && p.time_to_goal_s).map(p => p.time_to_goal_s!);
      const costs = ps.flatMap(p => (p.cost_usd != null ? [p.cost_usd] : []));
      const met = ps.filter(p => p.met).length;
      return {
        key,
        model: ps[0].model,
        effort: effortOf(ps[0]),
        lab: labOf(ps[0].model),
        runtime: ps[0].runtime,
        attempts: ps.length,
        met: ps.filter(p => p.met).length,
        meanTime: mean(times),
        bestTime: times.length ? Math.min(...times) : undefined,
        meanCost: mean(costs),
        // over attempts whose cost is known, so a missing cost doesn't make successes look cheap
        costPerSuccess: met && costs.length === ps.length ? costs.reduce((a, b) => a + b, 0) / met : undefined,
        meanOutputTokens: mean(ps.flatMap(p => (p.tokens ? [p.tokens.output] : []))),
        meanRate: mean(ps.flatMap(p => (xpRate(p) != null ? [xpRate(p)!] : []))),
        bestRate: bestRate(ps),
        meanLevels: mean(ps.flatMap(p => (levelsGained(p) != null ? [levelsGained(p)!] : []))),
        meanScore: mean(ps.map(p => p.score ?? 0)),
        meanCheckpoints: mean(ps.flatMap(p => (reachedOf(p) != null ? [reachedOf(p)!] : []))),
        checkpoints: Math.max(0, ...ps.map(p => p.checkpoints?.length ?? 0)) || undefined,
      };
    })
    .sort(
      isProgress(task)
        ? (a, b) => (b.meanRate ?? 0) - (a.meanRate ?? 0)
        : hasCheckpoints(task, v)
          ? (a, b) => (b.meanScore ?? 0) - (a.meanScore ?? 0) || (a.meanTime ?? Infinity) - (b.meanTime ?? Infinity)
          : (a, b) => b.met / b.attempts - a.met / a.attempts || (a.meanTime ?? Infinity) - (b.meanTime ?? Infinity),
    );
}

export function duration(s?: number): string {
  if (s == null) return '–';
  if (s < 3600) return `${(s / 60).toFixed(1)} min`;
  const h = Math.floor(s / 3600);
  return `${h} h ${String(Math.round((s % 3600) / 60)).padStart(2, '0')} min`;
}

export const dollars = (v?: number) => (v == null ? '–' : `$${v < 10 ? v.toFixed(2) : v.toFixed(0)}`);
export const thousands = (v?: number) => (v == null ? '–' : `${Math.round(v / 1000)}k`);
// From the run's id (<YYYYMMDD-HHMMSS>-<scenario>, the supervisor's local time): "1 Nov 2026, 17:45"
export function runDate(r: Run): string {
  const [, y, mo, d, h, mi] = r.id.match(/^(\d{4})(\d\d)(\d\d)-(\d\d)(\d\d)/)!;
  const month = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'][+mo - 1];
  return `${+d} ${month} ${y}, ${h}:${mi}`;
}

// A run's totals, for its tiles.
export function totals(r: Run) {
  const ps = r.players;
  const sum = (f: (p: Player) => number | undefined) => ps.reduce((n, p) => n + (f(p) ?? 0), 0);
  const times = ps.filter(p => p.met && p.time_to_goal_s).map(p => p.time_to_goal_s!);
  return {
    met: ps.filter(p => p.met).length,
    first: times.length ? Math.min(...times) : undefined,
    last: times.length === ps.length ? Math.max(...times) : undefined,
    deaths: sum(p => p.deaths),
    quests: sum(p => p.series?.at(-1)?.[4]),
    cost: sum(p => p.cost_usd),
    output: sum(p => p.tokens?.output),
  };
}

// The game's class colours, as a CSS class (global.css): stroke, fill and text follow currentColor.
export const classColor = (cls: string) => `cc-${cls.replace(/\s+/g, '-')}`;

// "Warrior - Protection" -> "Protection Warrior"; else the scenario's class.
export const specLabel = (p: Player) => {
  const [cls, spec] = (p.spec_name ?? '').split(' - ');
  return spec ? `${spec} ${cls}` : p.class.replace(/^./, c => c.toUpperCase());
};

// The hours axis for a run: every 1, 2, 4, 6 or 12 h, or minutes for short runs.
// A round interval for a chart's bars over a run of endS seconds: minutes, so there are at most about `bars` of them.
export function barInterval(endS: number, bars = 40): number {
  return [1, 2, 5, 10, 15, 30, 60, 120, 240].find(m => endS / 60 / m <= bars) ?? 480;
}

export const intervalName = (m: number) => (m === 1 ? 'minute' : m < 60 ? `${m} minutes` : m === 60 ? 'hour' : `${m / 60} hours`);

// A time in minutes as a clock reading: "45 min", "2 h 05".
export const clock = (m: number) => (m >= 60 ? `${Math.floor(m / 60)} h ${String(m % 60).padStart(2, '0')}` : `${m} min`);

export function timeTicks(maxS: number): { s: number; label: string }[] {
  if (maxS <= 2 * 3600) {
    const step = [5, 10, 15, 30].find(m => maxS / 60 / m <= 8)! * 60;
    return Array.from({ length: Math.floor(maxS / step) + 1 }, (_, i) => ({ s: i * step, label: `${(i * step) / 60} min` }));
  }
  const step = [1, 2, 4, 6, 12, 24].find(h => maxS / 3600 / h <= 9)! * 3600;
  return Array.from({ length: Math.floor(maxS / step) + 1 }, (_, i) => ({ s: i * step, label: `${(i * step) / 3600} h` }));
}

// Each player's colour: their class colour when every class is different, else one of eight distinct colours.
export function colors(run: Run): Record<string, string> {
  const unique = new Set(run.players.map(p => p.class)).size === run.players.length;
  return Object.fromEntries(run.players.map((p, i) => [p.char, unique ? classColor(p.class) : `pc-${i % 8}`]));
}

export const isContinent = (map: number) => [0, 1, 530, 571].includes(map);

// Wipes: three or more of the party dead within two minutes, from the judge's death times. [start, end] s.
export function wipes(run: Run): [number, number][] {
  if (run.players.length < 3) return [];
  const ds = run.players.flatMap(p => (p.deaths_at ?? []).map(t => ({ t, c: p.char }))).sort((a, b) => a.t - b.t);
  const out: [number, number][] = [];
  for (let i = 0; i < ds.length; ) {
    let j = i;
    while (j + 1 < ds.length && ds[j + 1].t - ds[i].t <= 120) j++;
    if (new Set(ds.slice(i, j + 1).map(d => d.c)).size >= 3) {
      out.push([ds[i].t, ds[j].t]);
      i = j + 1;
    } else i++;
  }
  return out;
}

// Stretches anyone in the run spent in an instance (off the continents), merged: [start, end] s.
export function instanceTime(run: Run): [number, number][] {
  const spans: [number, number][] = [];
  for (const p of run.players) {
    const tr = p.track ?? [];
    for (let i = 0; i < tr.length; i++) {
      if (isContinent(tr[i][1]) || tr[i][1] < 0) continue; // map -1: a teleport mark
      const start = tr[i][0];
      while (i + 1 < tr.length && !isContinent(tr[i + 1][1]) && tr[i + 1][1] >= 0) i++;
      spans.push([start, tr[Math.min(i + 1, tr.length - 1)][0]]);
    }
  }
  spans.sort((a, b) => a[0] - b[0]);
  const out: [number, number][] = [];
  for (const s of spans) {
    const last = out.at(-1);
    if (last && s[0] <= last[1] + 300) last[1] = Math.max(last[1], s[1]);
    else out.push([...s]);
  }
  return out;
}

// A player's outcome in a word: met / not met; a progress task has no goal to meet, so played, died or gave up.
export function outcome(p: Player, progress: boolean): { text: string; good: boolean } {
  if (p.died) return { text: 'died', good: false };
  if (p.gave_up) return { text: 'gave up', good: false };
  if (progress) return { text: 'played', good: true };
  if (p.met) return { text: 'met', good: true };
  const cp = p.checkpoints;
  if (cp?.length) return { text: `${cp.filter(c => c.at_s != null).length}/${cp.length} checkpoints`, good: false };
  return { text: 'not met', good: false };
}

// A harness's colour, as a background and a text class: Claude Code amber, Codex blue, opencode green.
export const harnessColor = (runtime: string) =>
  ({ claude: { bg: 'bg-accent', text: 'text-accent', fill: 'var(--color-accent)' }, codex: { bg: 'bg-info', text: 'text-info', fill: 'var(--color-info)' } })[
    runtime
  ] ?? { bg: 'bg-success', text: 'text-success', fill: 'var(--color-success)' };

// The best and the mean XP per hour of some players, over those whose XP was recorded; undefined if none was.
export function bestRate(ps: Player[]): number | undefined {
  const rs = ps.flatMap(p => (xpRate(p) != null ? [xpRate(p)!] : []));
  return rs.length ? Math.max(...rs) : undefined;
}
export function meanRate(ps: Player[]): number | undefined {
  const rs = ps.flatMap(p => (xpRate(p) != null ? [xpRate(p)!] : []));
  return rs.length ? rs.reduce((a, b) => a + b, 0) / rs.length : undefined;
}

// A leaderboard entry is a model at a reasoning effort: the same model at high and low effort rank apart.
export const effortOf = (p: Player) => (p.effort && p.effort !== 'default' ? p.effort : undefined);
export const entryKey = (p: Player) => (effortOf(p) ? `${p.model}|${effortOf(p)}` : p.model);
export const entryName = (r: { model: string; effort?: string }) => (r.effort ? `${modelName(r.model)} · ${r.effort}` : modelName(r.model));

// The lab behind a model, and its colour on every chart.
export const LABS: Record<string, string> = {
  Anthropic: '#d97757',
  OpenAI: '#10a37f',
  Google: '#4f8ff7',
  DeepSeek: '#7c8cff',
  xAI: '#c9c9cf',
  Moonshot: '#a78bfa',
  Mistral: '#fa7a35',
  Meta: '#38bdf8',
  Zhipu: '#e879f9',
  Alibaba: '#fbbf24',
  Other: '#86868f',
};
export function labOf(model: string): string {
  const m = model.toLowerCase();
  const rules: [RegExp, string][] = [
    [/claude|anthropic|haiku|sonnet|opus|fable/, 'Anthropic'],
    [/gpt|openai|codex|\bo\d/, 'OpenAI'],
    [/gemini|google|gemma/, 'Google'],
    [/deepseek/, 'DeepSeek'],
    [/grok|x-ai|xai/, 'xAI'],
    [/kimi|moonshot/, 'Moonshot'],
    [/mistral|magistral|codestral/, 'Mistral'],
    [/llama|muse|meta/, 'Meta'],
    [/glm|zhipu|z-ai/, 'Zhipu'],
    [/qwen|qvq|alibaba/, 'Alibaba'],
  ];
  return rules.find(([re]) => re.test(m))?.[1] ?? 'Other';
}

// The cost frontier: the entries no other beats on both cost and result (cheapest first). better(a, b): a's result
// is better than b's.
export function frontier<T extends { cost: number; y: number }>(pts: T[], higherIsBetter: boolean): T[] {
  const out: T[] = [];
  for (const p of pts.toSorted((a, b) => a.cost - b.cost || (higherIsBetter ? b.y - a.y : a.y - b.y))) {
    const last = out.at(-1);
    if (!last || (higherIsBetter ? p.y > last.y : p.y < last.y)) out.push(p);
  }
  return out;
}

// Where each entry stands in each task, for the cross-task table: 1 for a task's best, 0 for its worst (linear in rank).
export function percentiles(task: string, v = ALL): Map<string, number> {
  const rows = leaderboard(task, v);
  return new Map(rows.map((r, i) => [r.key, rows.length > 1 ? 1 - i / (rows.length - 1) : 1]));
}

// Names for a set of entries, with the harness added where two share a name (Opus 5.5 on Claude Code and on opencode).
export function displayNames(rows: { key: string; model: string; effort?: string; runtime: string }[]): Map<string, string> {
  const count = new Map<string, number>();
  for (const r of rows) count.set(entryName(r), (count.get(entryName(r)) ?? 0) + 1);
  return new Map(rows.map(r => [r.key, count.get(entryName(r))! > 1 ? `${entryName(r)} (${harnessName(r.runtime)})` : entryName(r)]));
}

// Label positions that don't overlap each other or other points. Each label tries the right of its point, then the
// left, then each side a line up or down, and so on; the first spot clear of placed labels and every other point wins.
// ponytail: greedy, fine for a few dozen labels.
export function placeLabels(
  labels: { x: number; y: number; w: number; r?: number }[],
  points: { x: number; y: number; r: number }[] = [],
  box = { left: -Infinity, right: Infinity, top: -Infinity, bottom: Infinity }, // where labels may go
  h = 13,
): { x: number; y: number; anchor: 'start' | 'end' }[] {
  const placed: { x0: number; x1: number; y: number }[] = [];
  const clear = (x0: number, x1: number, y: number, self: { x: number; y: number }) =>
    !placed.some(q => Math.abs(q.y - y) < h && x0 < q.x1 && q.x0 < x1) &&
    !points.some(p => !(p.x === self.x && p.y === self.y) && p.x + p.r > x0 && p.x - p.r < x1 && p.y + p.r > y - h + 3 && p.y - p.r < y + 3);
  return labels.map(l => {
    const gap = (l.r ?? 5) + 5;
    for (const dy of [0, -h, h, -2 * h, 2 * h, 3 * h]) {
      for (const side of ['start', 'end'] as const) {
        const x0 = side === 'start' ? l.x + gap : l.x - gap - l.w;
        const y = l.y + 4 + dy;
        const inside = x0 >= box.left && x0 + l.w <= box.right && y - h + 3 >= box.top && y <= box.bottom;
        if (inside && clear(x0, x0 + l.w, y, l)) {
          placed.push({ x0, x1: x0 + l.w, y });
          return { x: side === 'start' ? l.x + gap : l.x - gap, y, anchor: side };
        }
      }
    }
    placed.push({ x0: l.x + gap, x1: l.x + gap + l.w, y: l.y + 4 });
    return { x: l.x + gap, y: l.y + 4, anchor: 'start' };
  });
}

// A note on a player whose provider had outages: how many, and how long the clock was held.
export function outageNote(p: Player): string | undefined {
  const os = p.outages ?? [];
  if (!os.length) return undefined;
  const held = Math.round(os.reduce((n, o) => n + (o.held_s ?? 0), 0) / 60);
  return `${os.length} provider outage${os.length === 1 ? '' : 's'}, clock held ${held} min (${os.map(o => o.why).join('; ')})`;
}
