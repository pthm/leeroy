#!/usr/bin/env python3
"""Opt-in, non-scored GM acceptance. Start `azbench gm` first.

Uses only the standard library. Stores complete replies beside a concise summary;
assertions inspect task results, so a Lua task error cannot count as a pass.
"""
import argparse
import json
import math
import os
from pathlib import Path
import subprocess
import time


class Probe:
    def __init__(self, port, artifacts):
        self.repo = Path(__file__).resolve().parents[2]
        self.env = dict(os.environ, WOW_PORT=str(port))
        self.artifacts = Path(artifacts)
        self.artifacts.mkdir(parents=True, exist_ok=False)
        self.records = []
        self.replies = self.artifacts / "replies.jsonl"
        self.initial = self.call("return me.name(),me.position()")
        if self.initial[0] not in ("Devgm", "Devmage"):
            raise RuntimeError("acceptance requires a non-scored Devgm or Devmage session")

    def call(self, code):
        process = subprocess.run([str(self.repo / "bin/wow"), code], env=self.env,
                                 capture_output=True, text=True, timeout=35)
        with self.replies.open("a") as out:
            out.write(json.dumps({"at": time.time(), "code": code, "exit": process.returncode,
                                  "stdout": process.stdout, "stderr": process.stderr}) + "\n")
        if process.returncode:
            raise RuntimeError(process.stdout + process.stderr)
        reply = json.loads(process.stdout)
        if "error" in reply:
            raise RuntimeError(reply["error"])
        return reply.get("result")

    def until(self, code, predicate=bool, seconds=30):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            value = self.call(code)
            if predicate(value):
                return value
            time.sleep(.2)
        raise AssertionError(f"postcondition deadline: {code}: {value}")

    def task(self, code, seconds=30):
        self.call('acceptance=nil;task.spawn("acceptance",function() local ok,value=pcall(function() '
                  + code + ' end);acceptance={ok=ok,value=value} end)')
        result = self.until("return acceptance", seconds=seconds)
        if not result["ok"]:
            raise AssertionError(result["value"])
        return result.get("value")

    def gm(self, command):
        # Sending a generic GM command is deliberately unconfirmed; tests wait
        # for its particular postcondition separately.
        self.call("chat.command(" + json.dumps(command) + ")")

    def go(self, point):
        x, y, z, map_id = point
        self.gm(f".go xyz {x} {y} {z} {map_id}")
        return self.until("return me.position()", lambda p: p["map"] == map_id and
                          math.dist((p["x"], p["y"], p["z"]), (x, y, z)) < 3)

    def check(self, name, fn):
        started = time.monotonic()
        try:
            value = fn()
            self.records.append({"check": name, "status": "passed", "evidence": value,
                                 "seconds": round(time.monotonic() - started, 3)})
        except Exception as error:
            self.records.append({"check": name, "status": "failed", "error": str(error),
                                 "seconds": round(time.monotonic() - started, 3)})
            self.call('task.stop("acceptance")')
        (self.artifacts / "summary.json").write_text(json.dumps(self.records, indent=2))
        print(name, self.records[-1]["status"], flush=True)

    def finish(self):
        self.call('task.stop("acceptance");combat.target(me.guid())')
        self.gm(".revive")
        self.gm(".cheat god on")
        self.gm(".gm on")
        p = self.initial[1]
        self.go((p["x"], p["y"], p["z"], p["map"]))


def routes(p):
    for name, start, dest in [
        ("bridge", (-8920, 565, 94, 0), (-8885, 602, 94, 0)),
        ("road", (-9466, 74, 57, 0), (-9496, 73, 57, 0)),
        ("inn_stairs", (-9460, 8, 57, 0), (-9472, 34, 64, 0)),
        ("cave_upper", (-9797, 132, 25, 0), (-9807, 117, 24, 0)),
        ("cave_lower", (-9824, 122, 4, 0), (-9843.54, 127.525, 5.3698, 0)),
    ]:
        def route(start=start, dest=dest):
            p.go(start)
            x, y, z, _ = dest
            return p.task(f'local r=act.travel{{points={{{{{x},{y},{z}}}}}}};assert(r.ok,r.error and r.error.code);return r', 90)
        p.check(name, route)
    p.check("wrong_floor", lambda: p.task('local q=me.position();local r=await(world.path(q.x,q.y,q.z+70));assert(not r.ok and r.error.code=="route_unreachable");return r'))
    p.check("bounded_query", lambda: p.task('local q=me.position();local r=await(world.path(q.x+5000,q.y,q.z));assert(not r.ok and r.error.code=="route_limit");return r'))
    def danger():
        p.gm(".gm off")
        p.until('for _,u in ipairs(units.nearby(30)) do if u.reaction=="hostile" then return true end end return false')
        return p.task('local q=me.position();local r=act.danger{x=q.x,y=q.y,z=q.z};assert(r.state=="hostile",r.state..":"..(r.reason or ""));return r')
    p.check("danger", danger)
    p.gm(".gm on")
    p.check("danger_remote_unknown", lambda: p.task('local q=me.position();local r=act.danger{x=q.x+500,y=q.y,z=q.z};assert(r.state=="unknown");return r'))


def contracts(p):
    p.check("corpse_absent", lambda: p.task('local r=await(death.corpse());assert(not r.ok and r.error.code=="not_found");return r'))
    p.check("item_compare", lambda: p.task('local r=await(items.compare(117));assert(r.ok);return r'))
    p.check("missing_quest_object", lambda: p.task('local r=act.use_object{entry=1618,quest_id=99999999};assert(not r.ok and r.error.code=="not_in_log");return r'))
    p.check("invalid_spell", lambda: p.task('local r=act.cast(99999999);assert(not r.ok and r.error.code=="not_known" and r.data.attempts==0);return r'))
    p.check("skills", lambda: p.call('return skills.list()'))
    p.check("alive_recovery", lambda: p.task('local r=act.recover{rest=false};assert(r.ok and r.data.alive);return r'))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=8112)
    ap.add_argument("--artifacts", required=True)
    ap.add_argument("--suite", choices=("all","contracts","routes"), default="all")
    args = ap.parse_args()
    p = Probe(args.port, args.artifacts)
    try:
        p.gm(".gm on")
        p.gm(".cheat god on")
        if args.suite in ("all", "contracts"): contracts(p)
        if args.suite in ("all", "routes"): routes(p)
    finally:
        p.finish()
    raise SystemExit(any(r["status"] == "failed" for r in p.records))


if __name__ == "__main__":
    main()
