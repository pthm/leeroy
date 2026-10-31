# Tooling reliability acceptance

Evidence collected on 2026-10-30. These are execution and contract checks, not a claim that an agent's encounter strategy will succeed. GM fixtures use only non-scored `Devgm` and `Devmage`; fresh lifecycle and benchmark characters have separate accounts/workspaces. Failed fixture attempts are retained alongside the successful reruns.

## Passing checks

| Check | Result | Saved evidence |
|---|---|---|
| Complete Go suite | Pass: `go test ./... ./azeroth-sdk/...` | `/tmp/reliability-full-tests.log` |
| Race suite | Pass: script, session, pathfinding, diagnostics and run packages | `/tmp/reliability-race-tests.log` |
| Real-map paths and concurrency | Pass: real terrain/collision agreement, multiple maps, floor reference and concurrent path/height access | `/tmp/reliability-realdata-tests.log` |
| Combined GM suite | 25/25 pass: contracts, routes, errands, full bags, crafting, gathering, death and recovery | `bench/runs/20261030-reliability-gm-complete/summary.json` |
| Casting | 6/6 pass: stop owned movement, cooldown, lost target, overlapping same-spell correlation, facing, blocked healing across floors | `bench/runs/20261030-reliability-casting3/summary.json` |
| Two-session coordination | 2/2 pass: moving-target cast without chasing; recovery preempts travel and a queued errand | `bench/runs/20261030-reliability-coordination3/summary.json` |
| Fresh skill/lifecycle | Pass: actual Cooking training, SDK/server rank agreement, pause, saved provisional report, resume, session restart, natural completion, three session diagnostic streams | `bench/runs/20261030-reliability-lifecycle/` |
| Claude shell | All 15 command families; background completion measured from notification, not launch | `bench/runs/20261030-reliability-shell-notify/claude/` |
| Codex shell | All 15 command families; requested early yield and completion polling | `bench/runs/20261030-reliability-shell-poll/codex/` |
| OpenCode shell | All 15 command families through authenticated `openai/gpt-6.1-sol` | `bench/runs/20261030-reliability-shell-authenticated/opencode/` |
| Comparison utility | Two unit tests: common-horizon sampling/missing evidence, overlapping episode duration | `python3 -m unittest discover -s bench/acceptance -p 'test_*.py'` |

Each GM directory contains `replies.jsonl` with timestamps, exact Lua calls, replies and command exit status. A Lua assertion/task failure cannot count as a pass. Runtime directories contain stamped transcripts and `commands.json`. The lifecycle fixture saves the complete run. Run artifacts are intentionally ignored by version control; this record and the reproducible fixtures are tracked.

Installed runtime versions: Claude Code **2.1.292**, Codex CLI **0.165.0**, OpenCode **1.18.22**. Claude no longer exposes `TaskOutput`: its background Bash jobs report structured completion notifications. The harness now retains the pending command until that notification. Codex's exported command events show completion but do not expose every internal polling call separately.

The first OpenCode attempts used an unavailable OpenRouter provider. Enabling error logs revealed `ProviderModelNotFoundError`; no tool executed. The session has OpenAI OAuth and Anthropic credentials, and no `OPENROUTER_API_KEY`. The later authenticated OpenAI run passes. The runner now rejects an unconfigured OpenRouter model prefix before starting game sessions and retains structured runtime errors even when the runtime exits zero. Replaying every historical scenario is outside this acceptance pass; the Hogger trio is the selected confirmation run. The OpenRouter mage matrix is not replayed, and the passing smoke test is not represented as that benchmark.

## Reproduce

Build binaries and regenerate the reference before starting the fixtures:

```sh
go run ./azbench apidoc
python3 bench/acceptance/reliability.py --artifacts bench/runs/NEW-GM-DIRECTORY
go run ./azbench gm --class mage
python3 bench/acceptance/reliability.py --port 8113 --suite casting --artifacts bench/runs/NEW-CAST-DIRECTORY
python3 bench/acceptance/reliability.py --suite coordination --artifacts bench/runs/NEW-COORDINATION-DIRECTORY
```

Artifact directories must be new. Run these sequentially because they share the two GM sessions. `all` covers the 25 warrior-GM checks; casting and coordination are separate because they require the mage session. Tests restore the GM's position, alive state, GM/god mode and equipment durability. Profession fixtures deliberately learn skills and consume/create items on the disposable GM.

```sh
WOWBENCH_LIVE_HARNESS=/absolute/fresh/directory go test ./azbench/run -run TestLifecycleAndSkillGoalLive -count=1 -timeout=8m -v
WOWBENCH_LIVE_RUNTIMES=claude,codex,opencode WOWBENCH_SMOKE_MODEL_OPENCODE=openai/gpt-6.1-sol WOWBENCH_LIVE_ARTIFACTS=/absolute/fresh/directory go test ./azbench/run -run TestRuntimePermissionsLive -count=1 -timeout=15m -v
AC_DATA_DIR=/Users/pthm/wowbench/data go test ./azeroth-sdk/pathfinding -run 'TestTerrainContextSampleMatchesRealData|TestFindPath_(Map|ZMatches|SharedService)|TestPathFindingService_Concurrent' -count=1 -timeout=90s
```

The runtime smoke uses configured paid model access; normal unit tests skip both opt-in live Go tests. The live skill fixture requires the local server and database. The real-map test requires extracted game data. These dependencies are distinct from unit-test failures.

## Matched benchmark

Final candidate: `bench/runs/20261030-172848-hogger-reliability`, agents a57–a59, started **17:29:44 PDT**, configured for 120 minutes and observed for a 15-minute regression smoke window. It uses the SDK through `ee8f7b91` and harness through `f171f5cf`, plus the fresh-name scenario change. Runtime, model, level, classes, specs, initial location/resources and goal match the saved original scenario. Workspaces are fresh. No tactics or code are supplied to the agents during play.

Baseline: `bench/runs/20261030-100559-hogger-trio`, configured for 120 minutes but with **5,236 seconds** of saved judge samples per character. Its original report remains untouched. The comparison utility uses the first 900 seconds on each character’s judge-sample clock for progress, XP, deaths and kills. The final candidate is deliberately paused at that observation boundary; its score remains provisional, and the test does not establish full-run encounter performance. Full-run diagnostic durations have their own coverage and are not silently normalized to that horizon.

The earlier candidate `20261030-164436-hogger-reliability` used 60 minutes and was paused after about 37 minutes when the saved baseline mismatch was found. `20261030-172246-hogger-reliability` was stopped during startup to include the healer-observation fix. Neither is counted as a matched trial.

The smoke trace exposed a report-only classification issue: a batch of pending requests cancelled when a task stopped could appear as an active retry episode. Operation totals still include these outcomes, while episode aggregation now closes the stopped task and excludes its cancellation batch. This has focused regression coverage and does not alter running SDK behavior or judge scores.

Pending: final candidate report and comparison. After completion:

```sh
python3 bench/acceptance/compare.py bench/runs/20261030-100559-hogger-trio bench/runs/20261030-172848-hogger-reliability --seconds 900 > bench/runs/20261030-172848-hogger-reliability/comparison.json
```

Sample size is one three-agent run per condition. The shared world contains persistent changes and other sessions; GM probes used separate characters and were completed before the final benchmark. Contention and model variation prevent causal attribution or model ranking from this sample. The original baseline lacks the new failure-episode, ownership, route-timeout and sampled dead/broken-weapon counters; these comparisons must remain unavailable, not zero.

## Contract limits

Healer cost is explicitly unknown beyond the observed durability consequence. Spatial cancellation is cooperative, including bounded reads and cancellable lock/search traversal; an in-progress kernel filesystem call is not a hard real-time operation. Local danger, line of sight and nominal range are evidence, not guarantees of server acceptance. Cast sequence IDs have a bounded late-reply quarantine. Party strategy, readiness, wipe budgets and quest progression remain agent-authored.
