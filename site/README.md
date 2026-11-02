# site

The LEEROY website: benchmark results, write-ups and a few pages of docs, published to GitHub Pages
(https://pthm.github.io/leeroy) by `.github/workflows/site.yml` on every push to `main` that touches `site/`.
Astro, on a trimmed copy of the [Terminal Noir](https://github.com/nandomoreirame/terminalnoir-astro-theme) theme
(MIT, `LICENSE-theme`): its layout, palette, fonts and code colours, without its translations and demo content.

```sh
cd site && npm install
npm run dev        # http://localhost:4321/leeroy/
npm run build      # into dist/; SITE_BASE=/ builds for the root of a domain
npm run check:links  # after a build: every internal link and anchor resolves
```

| | |
|---|---|
| `src/data/runs/*.json` | one file per published run, written by `bin/azbench publish <run dir> --task <task>` (needs the server's database up, for names); never edited by hand |
| `src/assets/runs/*` | the maps behind a run's routes, written by `publish`: the viewer's detail map in grey (continents, `.jpg`) or the navmesh floor plan (instances, `.png`) |
| `src/lib/results.ts` | the runs grouped by task and model; `TASKS` titles and describes each task |
| `src/pages/results/` | every task; a page per task (tiles, the leaderboard, time against cost, the runs); a page per run (tiles, the roster, level over time with wipes and instance time or time to the goal, route maps, killers and places, time use, chat and time together, cost and errors, its notes); task pages compare models across runs (every attempt, agent time, errors) |
| `src/content/runs/<run id>.md` | optional notes for a run's page: a title, a lede, findings at a glance (frontmatter) and the story (body) |
| `src/content/blog/` | write-ups (Markdown; images relative, from `src/assets/media`) |
| `src/content/docs/` | the docs pages, in sidebar order by `order` |
| `src/assets/media/` | screenshots (also used by the repo README) |
