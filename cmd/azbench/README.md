# azbench

The LEEROY benchmark in one binary: it runs the benchmark's tasks from YAML (`bench/tasks`, `bench/models.yaml`),
judges and reports them, and is the agents' `standings` (`bench/agent/bin/standings` is a link to it: it acts as the
one it's called by). The agents' other tools are the game's (`wow`, in `azsdk/cmd/wow`) and the world's
(`azworld`). Built into `bin/azbench` by `./build.sh`. How to use it:
[bench/README.md](../../bench/README.md).

| Package | |
|---|---|
| `main.go`, `bench.go` | the commands |
| [`config`](../../internal/bench/config) | the YAML: scenarios, matrices, `bench/specs.yaml`, `bench/places.yaml` (strict: unknown fields are errors) |
| [`run`](../../internal/bench/run) | the supervisor: one process per run owns every session (`bin/azsdk`), judge and agent; the prompt (`prompt.tmpl`), starting conditions, clean stops, alerts, the control API (pause, restart, status), resume, `gm`, `apidoc`, `reset` |
| [`judge`](../../internal/bench/judge) | the judge, the report card and the progress signals ([judge/README.md](../../internal/bench/judge/README.md)); measures from [`azsdk/metrics`](../../azsdk/metrics) |
| [`diagnostics`](../../internal/bench/diagnostics) | saved structured action/tool evidence, repeated-failure episodes and coverage; shared by reports and live status |
| [`tools`](../../internal/bench/tools) | the agents' `standings` |
