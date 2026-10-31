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
        self.call('task.stop("acceptance")')
        self.task('return await(combat.target(me.guid()))')
        self.gm(".revive")
        self.gm(".cheat god on")
        self.gm(".gear repair")
        self.gm(".gm on")
        p = self.initial[1]
        self.go((p["x"], p["y"], p["z"], p["map"]))


def routes(p):
    for name, start, dest in [
        ("bridge", (-8930, 545, 94.715, 0), (-8880, 590, 93.402, 0)),
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


def clearance(p):
    """Static clearance acceptance, including the known inn bake mismatch."""
    for name, start, dest in [
        ("bridge_clearance", (-8930, 545, 94.715, 0), (-8880, 590, 93.402, 0)),
        ("road_clearance", (-9466, 74, 57, 0), (-9496, 73, 57, 0)),
        ("cave_upper_clearance", (-9797, 132, 25, 0), (-9807, 117, 24, 0)),
        ("cave_lower_repair", (-9824, 122, 4, 0), (-9843.54, 127.525, 5.3698, 0)),
    ]:
        def route(start=start, dest=dest, name=name):
            p.go(start)
            x, y, z, _ = dest
            return p.task(f'local check=await(world.path({x},{y},{z}));assert(check.ok,check.error and check.error.code);'
                          'assert(check.data.collision.state=="clear" and check.data.collision.scope=="static");'
                          + ('assert(check.data.collision.replanned);' if name == "cave_lower_repair" else '')
                          + f'local r=act.travel{{points={{{{{x},{y},{z}}}}}}};assert(r.ok,r.error and r.error.code);return {{check=check,travel=r,position=me.position()}}', 90)
        p.check(name, route)
    def inn_blocked():
        p.go((-9460, 8, 57, 0))
        return p.task('local before=me.position();local q=await(world.path(-9472,34,64));'
                      'assert(not q.ok and q.error.code=="route_blocked");'
                      'assert(q.data.collision.model=="Goldshireinn.wmo");'
                      'local r=await(move.to(-9472,34,64));assert(not r.ok and r.error.code=="route_blocked");'
                      'local after=me.position();assert(math.abs(after.x-before.x)<0.05 and math.abs(after.y-before.y)<0.05);'
                      'return {check=q,move=r,before=before,after=after}')
    p.check("inn_collision_mismatch_refused", inn_blocked)


def contracts(p):
    p.check("corpse_absent", lambda: p.task('local r=await(death.corpse());assert(not r.ok and r.error.code=="not_found");return r'))
    p.check("item_compare", lambda: p.task('local r=await(items.compare(117));assert(r.ok);return r'))
    p.check("missing_quest_object", lambda: p.task('local r=act.use_object{entry=1618,quest_id=99999999};assert(not r.ok and r.error.code=="not_in_log");return r'))
    p.check("invalid_spell", lambda: p.task('local r=act.cast(99999999);assert(not r.ok and r.error.code=="not_known" and r.data.attempts==0);return r'))
    p.check("skills", lambda: p.call('return skills.list()'))
    p.check("alive_recovery", lambda: p.task('local r=act.recover{rest=false};assert(r.ok and r.data.alive);return r'))


def errands(p):
    money=p.call("return me.money()")
    try:
        p.gm(".modify money 10000")
        p.until("return me.money()",lambda n:n>=money+10000)
        def vendor():
            p.go((-9465,10,57,0))
            before=p.call("return items.count(7073)")
            p.gm(".additem 7073 1")
            p.until("return items.count(7073)",lambda n:n>before)
            p.task("return await(items.info(7073))")
            return p.task('local r=act.vendor{npc_entry=151,sell_junk=true,buy={{99999999,1}}};assert(not r.ok);assert(r.steps[1].ok and #r.steps>=2);return r',45)
        p.check("vendor_partial",vendor)
        def trainer():
            p.go((-9467,-5,57,0))
            if p.call("return skills.get(185)"):
                p.gm(".unlearn 2550")
                p.until("return skills.get(185)",lambda v:v is None)
            r=p.task('local r=act.train{npc_entry=1430,spells={2551,99999999}};assert(not r.ok);assert(#r.data.learned==1 and r.data.learned[1]==2551);return r',45)
            p.until("return skills.get(185)")
            return r
        p.check("trainer_partial",trainer)
        def full_bags():
            p.go((-9465,10,57,0))
            count=p.call("return items.free_slots()")
            try:
                if count:p.gm(f".additem 25 {count}")
                p.until("return items.free_slots()",lambda n:n==0)
                # The offered item is unstackable; existing partial stacks cannot mask full bags.
                return p.task('local n=act.npc(151);local r=await(vendor.buy(n.guid,4496,1));assert(not r.ok and r.error.code=="bags_full",r.error and r.error.code);return r')
            finally:
                if count:p.gm(f".additem 25 -{count}")
        p.check("full_bags",full_bags)
    finally:
        current=p.call("return me.money()")
        if current!=money:p.gm(f".modify money {money-current}")


def professions(p):
    def crafting():
        p.go((-9467,-5,57,0))
        if not p.call("return spells.known(2538)"):
            raise AssertionError("run errands first to learn Apprentice Cooking")
        before=p.call("return items.count(2679)")
        meat=p.call("return items.count(2672)")
        p.gm(".additem 2672 1")
        p.until("return items.count(2672)",lambda n:n>meat)
        r=p.task('local r=await(spells.cast(2538));assert(r.ok,r.error and r.error.code);return r')
        after=p.until("return items.count(2679)",lambda n:n>before)
        return {"action":r,"created":after-before,"skill":p.call("return skills.get(185)")}
    p.check("crafting",crafting)
    def gathering():
        for point in [(-9410.12,-32.9812,64.1018,0),(-9582.88,16.6436,60.1078,0),(-9350.43,49.4977,61.2849,0)]:
            p.go(point)
            try:
                obj=p.until('for _,o in ipairs(objects.nearby(80)) do if o.gather=="herbalism" and o.can_use then return o end end',seconds=3)
            except AssertionError:
                continue
            if obj:
                p.go((obj["x"],obj["y"],obj["z"],0))
                before=p.call("return items.count(2447)+items.count(765)")
                r=p.task('local r=await(objects.use('+json.dumps(obj["guid"])+'));assert(r.ok,r.error and r.error.code);return r')
                after=p.until("return items.count(2447)+items.count(765)",lambda n:n>before)
                return {"action":r,"gathered":after-before,"skill":p.call("return skills.get(182)")}
        raise AssertionError("no available herb node")
    p.check("gathering",gathering)


def recovery(p):
    def kill_at(point):
        p.go(point)
        p.gm(".gm off")
        p.gm(".cheat god off")
        p.task('local r=await(combat.target(me.guid()));assert(r.ok);return r')
        p.gm(".die")
        p.until("return me.alive()",lambda alive:not alive)
    def normal():
        kill_at((-8949,-132,83,0))
        # Repeated fixture deaths can impose the server's 120-second reclaim delay.
        r=p.task('return act.recover{rest=false,timeout_s=180}',190)
        assert r["ok"] and r["data"]["alive"],json.dumps(r)
        return r
    p.check("death_and_reclaim",normal)
    if not p.call("return me.alive()"):
        p.gm(".revive")
        p.until("return me.alive()")
    def refused_reclaim():
        return p.task('local r=await(death.reclaim());assert(not r.ok and r.error.code=="not_a_ghost");return r')
    p.check("failed_reclaim",refused_reclaim)
    def camped():
        point=(-9843.54,127.525,5.3698,0)
        kill_at(point)
        p.task('local r=await(death.release());assert(r.ok);return r')
        p.go(point)
        r=p.task('return act.corpse_run{timeout_s=35}',45)
        assert not r["ok"] and r["error"]["code"]=="unsafe_recovery",json.dumps(r)
        assert not p.call("return me.alive()")
        return r
    p.check("camped_corpse_refusal",camped)
    p.gm(".revive")
    p.until("return me.alive()")
    def healer_and_broken_weapon():
        p.gm(".gear repair")
        results=[]
        for _ in range(5):
            kill_at((-9466,74,57,0))
            r=p.task('local r=act.recover{spirit=true,rest=false,timeout_s=45};assert(me.alive() and (r.ok or r.error.code=="needs_repair"),r.error and r.error.code);return r',55)
            results.append(r)
            if not r["ok"]:
                assert r["error"]["code"]=="needs_repair"
                assert r["data"]["gear"]["weapon"]["broken"]
                return results
        raise AssertionError("weapon did not break within five authorized healer cycles")
    p.check("healer_and_broken_weapon",healer_and_broken_weapon)
    p.gm(".revive")
    p.until("return me.alive()")
    p.gm(".gear repair")
    p.gm(".gm on")
    def rest_timeout():
        p.task('return await(combat.target(me.guid()))')
        p.gm(".cheat god off")
        p.gm(".damage 50")
        p.until('local h=me.health();return h.current<h.max')
        return p.task('local r=act.rest{hp=1,max_s=0.05};assert(not r.ok and r.error.code=="rest_timeout",r.error and r.error.code);return r')
    p.check("rest_timeout",rest_timeout)
    p.gm(".cheat god on")


def casting(p):
    p.gm(".levelup 10")
    for spell in (133,168,122,2050,1459):
        if not p.call(f"return spells.known({spell})"):
            p.gm(f".learn {spell}")
            p.until(f"return spells.known({spell})")
    p.go((-8949,-132,83,0))
    def moving():
        return p.task('local q=me.position();local walk=move.to(q.x+20,q.y,q.z);wait(0.5);local r=act.cast(168);assert(r.ok,r.error and r.error.code);assert(not me.movement().owner);return {cast=r,walk=result(walk.id)}')
    p.check("cast_stops_owned_movement",moving)
    def cooldown():
        time.sleep(2)
        return p.task('local first=act.cast(122);assert(first.ok,first.error and first.error.code);local second=act.cast(122);assert(not second.ok and second.error.code=="cooldown" and second.data.attempts==0);return {first=first,second=second}')
    p.check("cast_cooldown",cooldown)
    def lost_target():
        time.sleep(2)
        return p.task('local r=act.cast(133,"17379390999999999999");assert(not r.ok and r.error.code=="unit_gone" and r.data.attempts==0);return r')
    p.check("cast_lost_target",lost_target)
    def overlapping():
        time.sleep(2)
        return p.task('local a=spells.cast(168);local b=spells.cast(168);local ar=await(a);local br=await(b);assert(ar.id~=br.id);assert(ar.ok~=br.ok,"same-spell outcomes were not distinct");return {first=ar,second=br}')
    p.check("cast_same_spell_correlation",overlapping)
    def facing():
        p.gm(".gm off")
        p.go((-9843.54,127.525,5.3698,0))
        u=p.until('for _,u in ipairs(units.nearby(50)) do if u.alive and not u.player and u.reaction=="hostile" then return u end end')
        p.go((u["x"]+6,u["y"],u["z"],0))
        time.sleep(2)
        return p.task('local guid='+json.dumps(u["guid"])+';assert(await(move.turn(0)).ok);local before=spells.check(133,guid);assert(before.facing==false);local r=act.cast(133,guid);assert(r.ok,r.error and r.error.code);assert(r.data.attempts<=2);return {before=before,cast=r}')
    p.check("cast_corrects_facing",facing)
    def floor_heal():
        p.go((-9472,34,64,0))
        time.sleep(2)
        # Select an observed friendly unit on the lower floor with blocked LOS.
        u=p.until('for _,u in ipairs(units.nearby(35)) do if u.alive and u.reaction=="friendly" and math.abs(u.z-me.position().z)>3 and units.in_sight(u.guid)==false then return u end end')
        r=p.task('local before=me.position();local r=act.cast(2050,'+json.dumps(u["guid"])+');assert(not r.ok and r.error.code=="line_of_sight" and r.data.attempts==0);assert(act.dist(before.x,before.y,before.z)<0.5);return r')
        return {"target":u,"cast":r}
    p.check("heal_other_floor_does_not_move",floor_heal)


def coordination(p):
    peer=Probe(8113,p.artifacts / "peer")
    try:
        def moving_target():
            p.gm(".gm off");peer.gm(".gm off")
            p.go((-9466,74,57,0));peer.go((-9466,74,57,0))
            guid=p.call("return me.guid()")
            peer.until('return units.get('+json.dumps(guid)+')')
            p.call('coord_walk=nil;task.spawn("acceptance-walk",function() coord_walk=act.go_to{x=-9496,y=74,z=57} end)')
            start=p.until('return me.position()',lambda q:q["x"]<-9468)
            r=peer.task('local before=me.position();local r=act.cast(1459,'+json.dumps(guid)+');assert(r.ok,r.error and r.error.code);assert(r.data.attempts<=2 and act.dist(before.x,before.y,before.z)<0.5);return r')
            end=p.call("return me.position()")
            assert math.dist((start["x"],start["y"]),(end["x"],end["y"]))>0.5
            p.until("return coord_walk")
            return {"cast":r,"target_start":start,"target_end":end}
        p.check("cast_moving_target_without_chasing",moving_target)
        def competing_recovery():
            p.go((-9466,74,57,0))
            p.task('return await(combat.target(me.guid()))')
            p.gm(".gm off");p.gm(".cheat god off")
            p.call('normal_result=nil;errand_result=nil;task.spawn("acceptance-normal",function() normal_result=move.with_control{max_s=30,fn=function() local r=act.go_to{x=-9540,y=74,z=57};wait(5);return r end} end)')
            p.until('local m=me.movement();return m.owner and m.owner.phase=="walking"')
            p.call('task.spawn("acceptance-errand",function() errand_result=act.vendor{npc_entry=151,repair=true} end)')
            p.gm(".die");p.until("return me.alive()",lambda alive:not alive)
            r=p.task('return act.recover{spirit=true,rest=false,timeout_s=45}',55)
            assert r["ok"] and r["data"]["alive"],json.dumps(r)
            normal=p.until("return normal_result");errand=p.until("return errand_result")
            assert not normal["ok"] and normal["error"]["code"]=="preempted",json.dumps(normal)
            assert not errand["ok"] and errand["error"]["code"] in ("teleported","preempted","movement_busy"),json.dumps(errand)
            return {"recovery":r,"normal":normal,"errand":errand}
        p.check("recovery_preempts_travel_and_queued_errand",competing_recovery)
    finally:
        for name in ("acceptance-walk","acceptance-normal","acceptance-errand"):
            p.call('task.stop('+json.dumps(name)+')')
        peer.finish()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=8112)
    ap.add_argument("--artifacts", required=True)
    ap.add_argument("--suite", choices=("all","contracts","routes","clearance","errands","professions","recovery","casting","coordination"), default="all")
    args = ap.parse_args()
    p = Probe(args.port, args.artifacts)
    try:
        p.gm(".gm on")
        p.gm(".cheat god on")
        if args.suite in ("all", "contracts"): contracts(p)
        if args.suite in ("all", "routes"): routes(p)
        if args.suite == "clearance": clearance(p)
        if args.suite in ("all", "errands"): errands(p)
        if args.suite in ("all", "professions"): professions(p)
        if args.suite in ("all", "recovery"): recovery(p)
        if args.suite == "casting": casting(p)
        if args.suite == "coordination": coordination(p)
    finally:
        p.finish()
    raise SystemExit(any(r["status"] == "failed" for r in p.records))


if __name__ == "__main__":
    main()
