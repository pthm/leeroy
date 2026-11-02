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
  spec_name?: string; // "Warrior - Protection"
  role?: string; // Tank, Healer, DPS
  series?: [number, number, number, number, number, number][]; // [elapsed s, level, XP earned, deaths, quests, kills]
  deaths_at?: number[]; // elapsed s
  track?: [number, number, number, number, number][]; // [s from the start, map, x, y, group id]
  death_list?: { t: number; map: number; x: number; y: number; killer: string; place: string }[];
  failures?: { op: string; code: string; count: number }[];
  time_use?: { seconds: number; fighting_pct: number; travelling_pct: number; idle_pct: number; dead_pct: number; with_group_pct: number };
  time_pct_by_state?: Record<string, number>; // the agent: thinking, waiting (a script running), in_call, no_script
  combat?: { damage_dealt: number; damage_taken: number; healing_done: number; kills: number };
  think_median_s?: number;
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
  maps?: MapBox[];
  together?: number[]; // per 5 minutes: the share of minutes all players stood within 40 yd
  chat_per_hour?: Record<string, number>[];
}

// What each task is, for the pages. A task without an entry here still shows, under its id.
export const TASKS: Record<string, { title: string; summary: string; scoring: string; order: number }> = {
  travel: {
    title: 'Travel: Northshire to Darnassus',
    summary:
      'A new level-30 Human warrior in Northshire must reach Darnassus on the other continent: two boats and a portal, no map handed over. One character per model, all at once, 90 minutes.',
    scoring: 'Arrived or not, then time to arrive. Cost is what the run spent at list prices.',
    order: 1,
  },
  deadmines: {
    title: 'The Deadmines from level 1',
    summary:
      'Five new level-1 characters in Northshire, a tank, a healer and three damage dealers, with one goal: clear the Deadmines and kill Edwin VanCleef. A week on the clock.',
    scoring: 'Met or not (the Deadmines achievement), then time to it.',
    order: 2,
  },
};

const runs: Run[] = Object.values(
  import.meta.glob<Run>('../data/runs/*.json', { eager: true, import: 'default' }),
).sort((a, b) => a.started.localeCompare(b.started));

export const allRuns = () => runs;
export const runById = (id: string) => runs.find(r => r.id === id);
export const runsOf = (task: string) => runs.filter(r => r.task === task);
export const tasks = () =>
  [...new Set(runs.map(r => r.task))].sort((a, b) => (TASKS[a]?.order ?? 99) - (TASKS[b]?.order ?? 99));
export const taskTitle = (task: string) => TASKS[task]?.title ?? task;

// claude-sonnet-5-5 -> Sonnet 5.5, gpt-6.1-terra -> GPT-6.1 Terra
export function modelName(model: string): string {
  const claude = model.match(/^claude-([a-z]+)-(\d+)-(\d+)/);
  if (claude) return `${claude[1][0].toUpperCase()}${claude[1].slice(1)} ${claude[2]}.${claude[3]}`;
  const gpt = model.match(/^gpt-([\d.]+)-?(.*)$/);
  if (gpt) return `GPT-${gpt[1]}${gpt[2] ? ' ' + gpt[2][0].toUpperCase() + gpt[2].slice(1) : ''}`;
  return model;
}

export const harnessName = (runtime: string) =>
  ({ claude: 'Claude Code', codex: 'Codex CLI', opencode: 'opencode' })[runtime] ?? runtime;

export interface ModelRow {
  model: string;
  runtime: string;
  attempts: number;
  met: number;
  meanTime?: number; // seconds, over the attempts that met the goal
  bestTime?: number;
  meanCost?: number;
  meanOutputTokens?: number;
}

const mean = (xs: number[]) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : undefined);

// One row per model across a task's runs, best first: most goals met, then the fastest mean time.
export function leaderboard(task: string): ModelRow[] {
  const by = new Map<string, Player[]>();
  for (const r of runsOf(task)) for (const p of r.players) by.set(p.model, [...(by.get(p.model) ?? []), p]);
  return [...by.entries()]
    .map(([model, ps]) => {
      const times = ps.filter(p => p.met && p.time_to_goal_s).map(p => p.time_to_goal_s!);
      return {
        model,
        runtime: ps[0].runtime,
        attempts: ps.length,
        met: ps.filter(p => p.met).length,
        meanTime: mean(times),
        bestTime: times.length ? Math.min(...times) : undefined,
        meanCost: mean(ps.flatMap(p => (p.cost_usd != null ? [p.cost_usd] : []))),
        meanOutputTokens: mean(ps.flatMap(p => (p.tokens ? [p.tokens.output] : []))),
      };
    })
    .sort((a, b) => b.met / b.attempts - a.met / a.attempts || (a.meanTime ?? Infinity) - (b.meanTime ?? Infinity));
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
      if (isContinent(tr[i][1])) continue;
      const start = tr[i][0];
      while (i + 1 < tr.length && !isContinent(tr[i + 1][1])) i++;
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
