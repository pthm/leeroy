# site

The LEEROY website: benchmark results, write-ups and a few pages of docs, published to GitHub Pages
(https://leeroy.dev, its custom domain: `public/CNAME`) by `.github/workflows/site.yml` on every push to `main` that touches `site/`.
Astro, on a trimmed copy of the [Terminal Noir](https://github.com/nandomoreirame/terminalnoir-astro-theme) theme
(MIT, `LICENSE-theme`): its layout, palette, fonts and code colours, without its translations and demo content.

```sh
just site-install   # npm ci in site/ (Node from mise.toml)
just site-dev       # http://localhost:4321/
just site-build     # into dist/; SITE_BASE=/ builds for the root of a domain
just site-check     # build, then check every internal link and anchor
```

| | |
|---|---|
| `.env.production` | `PUBLIC_ASSETS_URL`: the public R2 assets bucket (`https://assets.leeroy.dev`), where run pages load icons, map images and downloads from (`just upload` puts them there) |
| `.env.development` | `PUBLIC_ASSETS_URL=/assets`: the dev server serves `var/assets` (`public/assets` links to it; `just site-dev` makes the link), so a published run shows locally before `just upload` |
| `src/data/runs/*.json` | one file per published run, written by `just publish <run dir>` (needs the server's database up, for names); never edited by hand |
| `var/assets/` (repo root, not committed) | what `publish` writes for each run besides its JSON, laid out as the bucket keeps it: `icons/<name>.png` (shared), `runs/<run>/maps/` (a continent's detail map in grey at page and enlarged size, or an instance's navmesh floor plan), `runs/<run>/trajectories/<agent>.json` (ATIF) and `runs/<run>/game/<char>.jsonl.gz` |
| `src/lib/results.ts` | the runs grouped by task and model; `TASKS` titles and describes each task |
| `src/pages/results/` | every task; a page per task (tiles, the leaderboard, time against cost, the runs); a page per run (tiles, the roster, level over time with wipes and instance time or time to the goal, milestones and zones, route maps, fighting (damage, healing, kills, killing blows and damage by creature, killers and places), time use, how the agents worked (tool calls from their trajectories, progress by agent state, idle gaps, the tooling's reliability), calls and errors, chat, who heard whom and time together, gear and casts, cost, its notes); task pages compare models across runs (every attempt, agent time, errors) |
| `src/content/runs/<run id>.md` | optional notes for a run's page: a title, a lede, findings at a glance (frontmatter) and the story (body) |
| `src/content/blog/` | write-ups (Markdown; images relative, from `src/assets/media`) |
| `src/content/docs/` | the docs pages, in sidebar order by `order` |
| `src/assets/media/` | screenshots (also used by the repo README) |
