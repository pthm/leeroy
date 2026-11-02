# site

The LEEROY website: benchmark results, write-ups and a few pages of docs, published to GitHub Pages
(https://pthm.github.io/leeroy) by `.github/workflows/site.yml` on every push to `main` that touches `site/`.
Astro, on a trimmed copy of the [Terminal Noir](https://github.com/nandomoreirame/terminalnoir-astro-theme) theme
(MIT, `LICENSE-theme`): its layout, palette, fonts and code colours, without its translations and demo content.

```sh
cd site && npm install
npm run dev        # http://localhost:4321/leeroy/
npm run build      # into dist/; SITE_BASE=/ builds for the root of a domain
```

| | |
|---|---|
| `src/data/runs/*.json` | one file per published run, written by `bin/azbench publish <run dir> --task <task>`; never edited by hand |
| `src/lib/results.ts` | the runs grouped by task and model; `TASKS` titles and describes each task |
| `src/pages/results/` | every task; a page per task (tiles, the leaderboard, time against cost, the runs); a page per run (tiles, the roster, level over time and deaths or time to the goal, its notes) |
| `src/content/runs/<run id>.md` | optional notes for a run's page: a title, a lede, findings at a glance (frontmatter) and the story (body) |
| `src/content/blog/` | write-ups (Markdown; images relative, from `src/assets/media`) |
| `src/content/docs/` | the docs pages, in sidebar order by `order` |
| `src/assets/media/` | screenshots (also used by the repo README) |
