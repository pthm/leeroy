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
