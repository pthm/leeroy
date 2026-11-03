// How a task's results read, by its goal kind (goalKind): the leaderboard's columns, a model's result in a cell, a
// player's result and outcome, a run's summary. The pages ask the task's view instead of checking the goal themselves,
// so a new kind of task is one more view here.
import {
  ALL,
  duration,
  dollars,
  goalKind,
  hasCheckpoints,
  levelsGained,
  meanRate,
  thousands,
  totals,
  xpRate,
  type Kind,
  type ModelRow,
  type Player,
  type Run,
} from './results';

export interface Col<T> {
  head: string;
  cell: (x: T) => string;
  num?: boolean;
  tone?: (x: T) => string | undefined; // a text class
  title?: (x: T) => string | undefined;
  wide?: boolean; // left out of the compact leaderboard (the home page)
}
export interface Tile {
  v: string;
  k: string;
}

export interface View {
  kind: Kind;
  label: string; // what the task measures, for an eyebrow: "XP per hour", "Goal", "Head to head"
  unit: string; // a cross-task column's subheading
  board: Col<ModelRow>[]; // the leaderboard's columns after model and harness (cost and tokens follow)
  boardNote: string; // how to read them
  cell: (r: ModelRow) => string; // a model's result in a word (index cards, cross-task cells)
  sub: (r: ModelRow) => string; // under it
  value: (r: ModelRow) => number; // higher is better (cross-task colours)
  cost: { value: (r: ModelRow) => number | undefined; axis: string; higher: boolean }; // the cost chart's y
  player: Col<Player>; // a player's result in a players table
  playerOrder: (a: Player, b: Player) => number;
  outcome: (p: Player) => { text: string; good: boolean };
  runTiles: (r: Run) => Tile[]; // a run report's summary
  taskTile: (ps: Player[]) => Tile; // a task's headline across its attempts
  strip: Strip; // every attempt on one axis, by model
}

// The by-model strip: each attempt's value as a dot, the model's mean as a line.
export interface Strip {
  title: string;
  sub: string;
  value: (p: Player) => number | undefined; // undefined: not placed (✕: the goal not met)
  mean: (r: ModelRow) => number | undefined;
  label: (v?: number) => string;
  max?: number; // a fixed end of the axis (else the largest value)
  step: (max: number) => number;
  tick: (t: number) => string;
}

const pct = (n: number, d: number) => (d ? `${Math.round((100 * n) / d)}%` : '–');
const xp = (v: number | undefined, unit: string) => (v == null ? '–' : `${Math.round(v).toLocaleString('en')} ${unit}`);
const gaveUpOrDied = (p: Player) => (p.died ? { text: 'died', good: false } : p.gave_up ? { text: 'gave up', good: false } : undefined);
const common = (r: Run): Tile[] => [
  { v: dollars(totals(r).cost), k: 'cost at list prices' },
  { v: thousands(totals(r).output), k: 'output tokens' },
];

function levelling(kind: 'rate' | 'total'): View {
  const unit = kind === 'total' ? 'XP' : 'XP/h';
  const name = kind === 'total' ? 'total XP' : 'XP per hour';
  return {
    kind,
    label: kind === 'total' ? 'Total XP' : 'XP per hour',
    unit,
    board: [
      { head: 'Attempts', cell: r => String(r.attempts), num: true },
      { head: 'Mean', cell: r => xp(r.meanRate, unit), num: true, tone: () => 'text-fg' },
      { head: 'Best', cell: r => xp(r.bestRate, unit), num: true },
      { head: 'Levels', cell: r => r.meanLevels?.toFixed(1) ?? '–', num: true },
    ],
    boardNote:
      (kind === 'total'
        ? 'XP is the total earned before the first death or the end of the clock, sampled every 15 s. '
        : 'XP/h is XP earned per hour played, sampled every 15 s. ') +
      'Mean and best are over all attempts; Levels is the mean number gained. ',
    cell: r => xp(r.meanRate, unit).replace(/ XP(\/h)?$/, ''),
    sub: r => `n=${r.attempts}`,
    value: r => r.meanRate ?? 0,
    cost: { value: r => r.meanRate, axis: `mean ${name}`, higher: true },
    player: { head: unit, cell: p => xp(xpRate(p), unit), num: true },
    playerOrder: (a, b) => (xpRate(b) ?? 0) - (xpRate(a) ?? 0),
    outcome: p => gaveUpOrDied(p) ?? (kind === 'total' ? { text: 'survived', good: true } : { text: 'played', good: true }),
    runTiles: r => [
      { v: xp(Math.max(0, ...r.players.map(p => xpRate(p) ?? 0)), unit), k: 'best' },
      { v: (r.players.reduce((n, p) => n + (levelsGained(p) ?? 0), 0) / r.players.length).toFixed(1), k: 'levels gained, mean' },
      { v: String(totals(r).deaths), k: 'deaths' },
      { v: String(totals(r).quests), k: 'quests completed' },
      ...common(r),
    ],
    taskTile: ps => ({ v: xp(meanRate(ps), unit), k: `mean ${name}` }),
    strip: {
      title: `${name[0].toUpperCase()}${name.slice(1)}, every attempt`,
      sub: 'Each dot is one attempt. The line is the mean.',
      value: p => xpRate(p),
      mean: r => r.meanRate,
      label: v => xp(v, unit),
      step: max => [100, 200, 500, 1000, 2000, 5000, 10000, 20000].find(s => max / s <= 6) ?? 50000,
      tick: t => (t >= 1000 ? `${t / 1000}k` : String(t)),
    },
  };
}

function reach(checkpoints: boolean): View {
  const reached = (p: Player) => p.checkpoints?.filter(c => c.at_s != null).length ?? 0;
  return {
    kind: 'reach',
    label: 'Goal',
    unit: 'met · time',
    board: [
      { head: 'Met', cell: r => `${r.met}/${r.attempts}`, num: true, tone: r => (r.met === r.attempts ? 'text-success' : 'text-warning') },
      ...(checkpoints
        ? [
            { head: 'Checkpoints', cell: (r: ModelRow) => (r.meanCheckpoints != null ? `${r.meanCheckpoints.toFixed(1)}/${r.checkpoints}` : '–'), num: true },
            { head: 'Score', cell: (r: ModelRow) => r.meanScore?.toFixed(2) ?? '–', num: true, tone: () => 'text-fg' },
          ]
        : []),
      { head: 'Mean time', cell: r => duration(r.meanTime), num: true, tone: () => 'text-fg' },
      { head: 'Best', cell: r => duration(r.bestTime), num: true },
      { head: 'Cost / success', cell: r => dollars(r.costPerSuccess), num: true, wide: true },
    ],
    boardNote:
      checkpoints
        ? 'Score is 3600 / seconds to the goal; an unfinished attempt scores its share of checkpoints (bosses) reached times the score of finishing at the clock. Entries rank by mean score. Times cover only attempts that met the goal. '
        : 'Met is attempts that reached the goal in time; mean and best time cover only those. Cost per success is total cost over goals met. ',
    cell: r => `${r.met}/${r.attempts}`,
    sub: r => (r.meanTime != null ? duration(r.meanTime) : r.checkpoints ? `${r.meanCheckpoints?.toFixed(1) ?? 0}/${r.checkpoints} checkpoints` : 'none met'),
    value: r => (checkpoints ? (r.meanScore ?? 0) : r.met / r.attempts - (r.meanTime ?? 1e6) / 1e9),
    cost: { value: r => (r.meanTime != null ? r.meanTime / 60 : undefined), axis: 'mean minutes to the goal', higher: false },
    player: { head: 'Time', cell: p => (p.met ? duration(p.time_to_goal_s) : '–'), num: true },
    playerOrder: (a, b) => Number(b.met) - Number(a.met) || (a.time_to_goal_s ?? Infinity) - (b.time_to_goal_s ?? Infinity),
    outcome: p =>
      gaveUpOrDied(p) ??
      (p.met ? { text: 'met', good: true } : p.checkpoints?.length ? { text: `${reached(p)}/${p.checkpoints.length} checkpoints`, good: false } : { text: 'not met', good: false }),
    runTiles: r => {
      const t = totals(r);
      return [
        { v: `${t.met}/${r.players.length}`, k: 'reached the goal' },
        { v: t.last ? duration(t.last) : duration(t.first), k: t.last ? 'until all reached it' : t.first ? 'first to the goal' : 'none in time' },
        { v: String(t.deaths), k: 'deaths' },
        ...common(r),
      ];
    },
    taskTile: ps => ({ v: pct(ps.filter(p => p.met).length, ps.length), k: 'reached the goal' }),
    strip: {
      title: 'Time to the goal, every attempt',
      sub: 'Each dot is one attempt. The line is the mean. ✕ marks an attempt that didn’t reach the goal.',
      value: p => (p.met ? p.time_to_goal_s : undefined),
      mean: r => r.meanTime,
      label: v => duration(v),
      step: max => ([1, 2, 5, 10, 15, 30, 60, 120, 240].find(m => max / 60 / m <= 6) ?? 480) * 60,
      tick: t => (t >= 7200 ? `${t / 3600} h` : `${t / 60} min`),
    },
  };
}

const versus: View = {
  kind: 'versus',
  label: 'Head to head',
  unit: 'rating',
  board: [
    {
      head: 'Rating',
      cell: r => (r.rating != null ? String(Math.round(r.rating)) : '–'),
      num: true,
      tone: () => 'text-fg',
      title: r => (r.ratingLo != null ? `95% range ${Math.round(r.ratingLo)}–${Math.round(r.ratingHi!)}` : undefined),
    },
    { head: 'Range', cell: r => (r.ratingLo != null ? `${Math.round(r.ratingLo)}–${Math.round(r.ratingHi!)}` : '–'), num: true, wide: true },
    { head: 'Won–lost', cell: r => `${r.gamesWon ?? 0}–${(r.gamesPlayed ?? 0) - (r.gamesWon ?? 0)}`, num: true },
    { head: 'Won', cell: r => pct(r.gamesWon ?? 0, r.gamesPlayed ?? 0), num: true },
  ],
  boardNote:
    'Rating is a Bradley–Terry (batch Elo) fit over every game: 1500 is average, 400 points are 10:1 odds. Range is its 95% interval; entries whose ranges overlap aren’t clearly separated. Won–lost counts games (in a duel task, the first five of each pairing). ',
  cell: r => (r.rating != null ? String(Math.round(r.rating)) : '–'),
  sub: r => `${r.gamesWon ?? 0}–${(r.gamesPlayed ?? 0) - (r.gamesWon ?? 0)} · ${pct(r.gamesWon ?? 0, r.gamesPlayed ?? 0)}`,
  value: r => r.rating ?? 0,
  cost: { value: r => r.rating, axis: 'rating', higher: true },
  player: { head: 'Against', cell: p => p.opponent ?? '–' }, // the outcome column already gives won–lost
  playerOrder: (a, b) => (b.games?.won ?? 0) - (a.games?.won ?? 0),
  outcome: p => {
    const won = p.games?.won ?? 0;
    const n = p.games?.played ?? 0;
    return { text: `${won}–${n - won}`, good: won * 2 > n };
  },
  runTiles: r => {
    const t = totals(r);
    return [
      { v: String(r.matches?.length ?? 0), k: 'matches, at once' },
      { v: String((r.matches ?? []).reduce((n, m) => n + m.games.length, 0)), k: 'games decided' },
      { v: t.last ? duration(t.last) : '–', k: t.last ? 'until the last was decided' : 'not all decided' },
      ...common(r),
    ];
  },
  taskTile: ps => ({ v: String(ps.reduce((n, p) => n + (p.games?.played ?? 0), 0) / 2), k: 'games decided' }),
  strip: {
    title: 'Share of games won, every match',
    sub: 'Each dot is one match (a pairing), placed by the share of its games the model won. The line is its share over all of them.',
    value: p => (p.games?.played ? p.games.won / p.games.played : undefined),
    mean: r => (r.gamesPlayed ? (r.gamesWon ?? 0) / r.gamesPlayed : undefined),
    label: v => (v == null ? '–' : `${Math.round(v * 100)}%`),
    max: 1,
    step: () => 0.25,
    tick: t => `${Math.round(t * 100)}%`,
  },
};

// The view for a task (in a version, or across all of them).
export function view(task: string, v = ALL): View {
  const kind = goalKind(task, v);
  if (kind === 'rate' || kind === 'total') return levelling(kind);
  if (kind === 'versus') return versus;
  return reach(hasCheckpoints(task, v));
}
