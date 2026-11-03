#!/usr/bin/env python3
"""GM ride tests for the stored transport routines (azsdk/session/transports.json).

ride.py <jobs...>   a job is <entry>[:<stop>] (stop 1-based, default 1), optionally @<index> to pick one of an
                    entry's spawns (elevators): each runs on its own GM character, all at once.
Each character goes to the stop's wait point, boards, rides until the transport has left, leaves at the next stop,
and reports where it ended: the client's position and the server's (.gps), against the stop's wait point.
"""
import json, os, subprocess, sys, time, re, concurrent.futures as cf

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../../.."))
TABLE = json.load(open(f"{REPO}/azsdk/session/transports.json"))
LETTERS = "abcdefghijklmnop"


def wow(port, lua, timeout=30):
    r = subprocess.run([f"{REPO}/bin/wow", lua], env={**os.environ, "WOW_PORT": str(port)}, capture_output=True, text=True, timeout=timeout)
    return r.stdout.strip() + r.stderr.strip()


def start(i):
    char, acct, port = "Rider" + LETTERS[i], "ridetest" + LETTERS[i], 8120 + i
    subprocess.run([f"{REPO}/bin/azbench", "account", acct, acct, "3"], capture_output=True)
    subprocess.run(["pkill", "-f", f"-char {char} "], capture_output=True)
    time.sleep(1)
    subprocess.run(["rm", "-rf", f"/tmp/{char.lower()}-events"])  # a fresh history: no result from a run before
    log = open(f"/tmp/{char.lower()}.log", "w")
    subprocess.Popen([f"{REPO}/bin/azsdk", "-char", char, "-account", acct, "-race", "1", "-class", "8",
                      "-listen", f"127.0.0.1:{port}", "-data", f"{REPO}/data", "-events", f"/tmp/{char.lower()}-events"],
                     stdout=log, stderr=log, start_new_session=True)
    for _ in range(90):
        if '"result":1' in wow(port, "return 1"):
            break
        time.sleep(1)
    else:
        raise SystemExit(f"{char} didn't come up: /tmp/{char.lower()}.log")
    wow(port, 'chat.command(".gm on") chat.command(".cheat god on") return 1')
    return char, port


RIDE = r'''
task.spawn("ride", function()
  local function gps(mark)
    log("GPSMARK", mark)
    chat.command(".gps")
    wait(2)
    return mark
  end
  local function find()
    for _, t in ipairs(transports.list()) do
      if t.entry == %(entry)d and t.seen and (t.guid ~= nil) then
        local best, bd = nil, 1e9
        local p = me.position()
        if t.stops[%(stop)d] and t.stops[%(stop)d].wait then
          local w = t.stops[%(stop)d].wait
          local d = math.abs(w.x - %(wx)f) + math.abs(w.y - %(wy)f)
          if d < 5 then return t end
        end
      end
    end
  end
  -- walk off the wait point and back, as an agent arrives: a walk before boarding must not haunt the ride (the
  -- movement controller once put a character back at its last walk's end after leaving)
  local p = me.position()
  await(move.to(p.x + 5, p.y, p.z), 20)
  await(move.to(%(wx)f, %(wy)f, %(wz)f), 20)
  local t
  for i = 1, %(timeout)d do t = find(); if t then break end; wait(1) end
  if not t then log("RIDE", {res = "not_seen"}); return end
  local b = await(transports.board(t.guid), %(timeout)d)
  if not b.ok then log("RIDE", {res = "board_failed", err = b.error}); return end
  local boarded = me.position()
  repeat wait(1); t = find() or t until not (t.docked and t.docked.wait and math.abs(t.docked.wait.x - %(wx)f) < 5)
  local lr = transports.leave() -- at once: an elevator waits a few seconds at a stop
  wait(1.5)
  local mid_client, mid_gps = me.position(), gps("mid")
  local l = await(lr, %(timeout)d)
  if not l.ok then log("RIDE", {res = "leave_failed", err = l.error, pos = me.position()}); return end
  wait(3)
  log("RIDE", {res = "left", client = me.position(), gps = gps("end"), boarded = boarded, mid_client = mid_client, mid_gps = mid_gps})
end)
return 1
'''


def run(i, job):
    m = re.fullmatch(r"(\d+)(?::(\d+))?(?:@(\d+))?", job)
    entry, stop, idx = int(m[1]), int(m[2] or 1), int(m[3] or 0)
    cands = [t for t in TABLE if t["entry"] == entry]
    tt = cands[idx]
    st = tt["stops"][stop - 1]
    nxt = tt["stops"][stop % len(tt["stops"])]
    char, port = start(i)
    w = st["wait"]
    wow(port, f'chat.command(".go xyz {w[0]:.2f} {w[1]:.2f} {w[2] + 0.5:.2f} {st["map"]}") return 1')
    time.sleep(8)
    period = (tt["anim"][-1][0] / 1000) if tt.get("anim") else 900
    timeout = int(period + 60)
    wow(port, RIDE % dict(entry=entry, stop=stop, wx=w[0], wy=w[1], wz=w[2], timeout=timeout))
    t0 = time.time()
    while time.time() - t0 < 2 * timeout + 60:
        ev = subprocess.run([f"{REPO}/bin/wow", "events", "--types", "log", "--last", "20"], env={**os.environ, "WOW_PORT": str(port)},
                            capture_output=True, text=True).stdout
        for line in ev.splitlines()[::-1]:
            if "RIDE" in line:
                try:
                    d = json.loads(line)
                except Exception:
                    continue
                if d.get("msg") != "RIDE":
                    continue
                data = d.get("data") or {}
                res = data.get("res") if isinstance(data, dict) else None
                if res:
                    hist = subprocess.run([f"{REPO}/bin/wow", "events", "--last", "300"], env={**os.environ, "WOW_PORT": str(port)},
                                          capture_output=True, text=True).stdout.splitlines()
                    for key in ("mid_gps", "gps"):
                        mark = data.get(key)
                        if not mark:
                            continue
                        seen, txt = False, ""
                        for hl in hist:
                            try:
                                h = json.loads(hl)
                            except Exception:
                                continue
                            if h.get("type") == "log" and h.get("msg") == "GPSMARK" and h.get("data") == mark:
                                seen = True
                            elif seen and h.get("type") == "chat" and "X:" in (h.get("text") or ""):
                                txt = h["text"]
                                break
                        data[key] = txt
                    return verdict(job, tt, nxt, data)
        time.sleep(5)
    return f"{job:14} {tt['name'][:34]:34} TIMEOUT"


def verdict(job, tt, nxt, data):
    if data["res"] != "left":
        return f"{job:14} {tt['name'][:34]:34} {data['res']}: {json.dumps(data.get('err'))[:160]} {json.dumps(data.get('pos'))[:80]}"
    g = re.search(r"X:\s*(-?[\d.]+)\s*Y:\s*(-?[\d.]+)\s*Z:\s*(-?[\d.]+)", data.get("gps", ""))
    c = data["client"]
    w = nxt["wait"]
    cd = ((c["x"] - w[0]) ** 2 + (c["y"] - w[1]) ** 2) ** 0.5
    if g:
        sx, sy, sz = map(float, g.groups())
        sd = ((sx - w[0]) ** 2 + (sy - w[1]) ** 2) ** 0.5
        srv = f"server {sd:5.1f} yd off (z {sz:.1f} vs {w[2]:.1f})"
        ok = sd < 4 and abs(sz - w[2]) < 3 and c["map"] == nxt["map"]
    else:
        srv, ok = "server ? (no .gps)", False
    mg = re.search(r"X:\s*(-?[\d.]+)\s*Y:\s*(-?[\d.]+)\s*Z:\s*(-?[\d.]+)", data.get("mid_gps", ""))
    mc = data.get("mid_client") or {}
    if mg and mc:
        mx, my, mz = map(float, mg.groups())
        md = ((mx - mc["x"]) ** 2 + (my - mc["y"]) ** 2 + (mz - mc["z"]) ** 2) ** 0.5
        mid = f"mid-ride server vs client {md:5.1f} yd"
        ok = ok and md < 6
    else:
        mid = "mid-ride ?"
    return f"{job:14} {tt['name'][:34]:34} {'PASS' if ok else 'FAIL'} end: client {cd:5.1f} yd off, map {c['map']}/{nxt['map']}, {srv}; {mid}"


if __name__ == "__main__":
    jobs = sys.argv[1:]
    with cf.ThreadPoolExecutor(len(jobs)) as ex:
        def safe(a):
            try:
                return run(*a)
            except Exception as e:
                return f"{a[1]:14} ERROR {e!r}"
        for r in ex.map(safe, enumerate(jobs)):
            print(r, flush=True)
