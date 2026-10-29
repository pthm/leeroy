// bun test bench/report.test.ts: report.ts on a made-up 10-minute solo run with a known shape
import { expect, test } from "bun:test";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

test("report card", () => {
  const dir = mkdtempSync(join(tmpdir(), "report-")), t0 = Date.parse("2026-10-29T10:00:00Z"), min = 60e3;
  const lines = (f: string, xs: any[]) => writeFileSync(join(dir, f), xs.map((x) => JSON.stringify(x)).join("\n") + "\n");
  const iso = (t: number) => new Date(t).toISOString();
  writeFileSync(join(dir, "meta.json"), JSON.stringify({ agent: "a1", char: "Tester", runtime: "claude" }));
  // 10 min of samples, 100 XP per 15 s while the script runs (0-8 min), nothing after it stops
  lines("record.jsonl", Array.from({ length: 41 }, (_, i) => ({ t: t0 + i * 15e3, ev: "score", n: "Tester", value: Math.min(i, 32) * 100 })));
  lines("Tester.events.jsonl", [
    { t: t0, type: "task_started", data: { task: 1 } },
    { t: t0 + 3 * min, type: "chat", data: { kind: "npc_say", text: "You no take candle!" } },
    { t: t0 + 4 * min, type: "death", data: {} },
    { t: t0 + 8 * min, type: "task_finished", data: { task: 1, state: "failed" } },
  ]);
  const call = (id: string, cmd: string, a: number, b: number) => [
    { type: "assistant", timestamp: iso(a), message: { id, content: [{ type: "tool_use", id, input: { command: cmd } }] } },
    { type: "user", timestamp: iso(b), message: { content: [{ type: "tool_result", tool_use_id: id }] } },
  ];
  lines("agent.jsonl", [
    ...call("1", "wow wait 300 >/dev/null", t0 + 1 * min, t0 + 3 * min), // woken early by the NPC line
    ...call("2", "wow -f grind.lua", t0 + 3.2 * min, t0 + 3.3 * min),    // and restarts
    ...call("3", "wow wait 120", t0 + 4 * min, t0 + 6 * min),             // times out
  ]);
  Bun.spawnSync(["bun", join(import.meta.dir, "report.ts"), dir]);
  const c = JSON.parse(readFileSync(join(dir, "report.json"), "utf8")).agents[0];
  expect(c.duty_cycle_pct).toBe(80);                                      // 8 of 10 minutes
  expect(c.idle_gaps).toMatchObject({ n: 1, median_s: 120 });             // idle from 8 min to the end
  expect(c.progress_per_hour_by_state.no_script).toBe(0);
  expect(c.wakes).toMatchObject({ waits: 2, early: 1, by_trigger: { "chat:npc_say": { n: 1, restart: 1 } } });
  expect(c.restarts.agent_calls).toBe(1);
  expect(c.deaths).toBe(1);
});
