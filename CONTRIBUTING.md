# Contributing

Issues and pull requests are welcome: code, docs, and benchmark results.

## Code

- **Build and test:** `just build`, `just test` (some tests need `data/` and the server up). `just check` runs
  everything CI does plus the tests and the site's links; run it before a pull request.
- **Style:** `just fmt` (gofumpt) and `just lint` (golangci-lint). CI builds both modules without `go.work`, as an
  outside user would.
- **Two modules:** `azsdk/` and the root. In the checkout, `go.work` builds them together, so SDK changes are used at
  once; `just release <version>` tags both.
- **Tools, not strategies:** the SDK and the agents' docs may show anything a player could see or do, but must not
  decide how to play. A helper that encodes a tactic (a pull routine, a crowd-control lock) changes what the benchmark
  measures.
- **Results:** a change that makes results incomparable with earlier runs bumps the benchmark version
  ([suite/versions.yaml](suite/versions.yaml)).

## Submitting benchmark results

Runs take hours and cost real money, so results from other people's runs are welcome: new models, more attempts on a
task, other runtimes. You open a pull request with the run's results and attach an archive of its files; a maintainer
reviews it, uploads the files the run's page loads and merges.

You don't need any keys for the bucket. Everything on your side is local: `just publish` writes the run's files into
`var/assets`, `just site-dev` serves them from there so you can check the page, and `just submit` packs them. The
upload to the public assets bucket (assets.leeroy.dev) is the maintainer's step, from your archive.

### Keep it comparable

- **Run an unmodified checkout** of `main` or a release tag. `run.json` records the benchmark version and the SDK
  revision; a run with local changes to `suite/`, `azsdk/` or `internal/` can't be compared with anyone else's.
- **Run a task as written**, from `suite/tasks`, with `--reset` so it starts like every other run of it. A model not in
  [suite/models.yaml](suite/models.yaml) goes into the same pull request.
- **Submit what happened**, including runs that failed or ran out of time: a benchmark only reporting its best runs
  isn't one.

### Steps

```sh
just run travel --model <model key> --reset   # the run, into var/runs/<run>
just publish var/runs/<run>                    # its results for the site (needs the server up)
just site-dev                                  # check its page on http://localhost:4321/results
just submit <run>                              # pack it: var/submissions/<run>.tar.gz
```

Then open a pull request that:

- commits `site/src/data/runs/<run>.json` (and `suite/models.yaml` for a new model; optionally notes for the run's page
  in `site/src/content/runs/<run>.md`),
- attaches `var/submissions/<run>.tar.gz` (GitHub takes up to 25 MB; for a bigger one, upload it somewhere public and
  link it),
- says what it ran on and anything unusual (a crash, a resume, a provider outage).

The archive holds the run's published JSON, what its page loads (route maps, trajectories, gzipped game logs, icons)
and the evidence it's reviewed by: `run.json`, the scenario, the team, the report card, the judge's samples and the
diagnostics. The agents' raw transcripts stay with you; their ATIF trajectories are published. Home directories are
written `~`, so it doesn't name your account, and `just submit` refuses to pack anything that looks like an API key or
a private key, or holds the value of a key, token, secret or password from your environment or `.env` (an agent's
runtime has its provider's key in its environment, and an agent can read files). Still, look through it before you
share it: it is published as it is, and an agent may have read or written something you'd rather keep.

### For maintainers

```sh
gh pr checkout <number>
just intake <archive file or URL>   # checks and unpacks it: evidence in var/submissions/<run>/, assets into var/assets
just site-dev                       # look at the run's page, with its maps
just upload <run>                   # its assets to the public bucket
```

Then merge. `intake` treats the archive as untrusted: it refuses entries outside the run's directory, `..`, links,
and a run id already published (here or in the bucket), and warns when the pull request's JSON differs from the
archive's.
