#!/usr/bin/env python3
"""Compare saved judge samples over the common observed horizon, without rescoring.

Usage: python3 bench/acceptance/compare.py BASELINE_RUN CANDIDATE_RUN
The full-run diagnostics are reported separately: old missing counters stay null.
"""
import argparse
import json
from pathlib import Path


def read_run(path):
    path=Path(path)
    report=json.loads((path / "report.json").read_text())
    agents={}
    for card in report["agents"]:
        directory=path / card["agent"]
        meta=json.loads((directory / "meta.json").read_text())
        samples=[json.loads(line) for line in (directory / "samples.jsonl").read_text().splitlines() if line]
        if not samples:
            raise ValueError(f"no samples for {directory}")
        role=meta["spec"]
        if role in agents:
            raise ValueError("comparison requires one agent per spec")
        agents[role]=(meta,card,samples)
    return agents


def episode_union_ms(episodes):
    end=None
    total=0
    for start,stop in sorted((e["start_ms"],e["end_ms"]) for e in episodes):
        total+=max(0,stop-max(start,end if end is not None else start))
        end=max(stop,end if end is not None else stop)
    return total


def summarize(agent,horizon):
    meta,card,samples=agent
    first=samples[0]
    last=max((s for s in samples if s["elapsed_s"]<=horizon),key=lambda s:s["elapsed_s"])
    delta={}
    for key in ("xp_earned","deaths","creatures_killed","quests"):
        a,b=first.get("signals",{}).get(key),last.get("signals",{}).get(key)
        delta[key]=b-a if a is not None and b is not None else None
    diagnostic=card.get("diagnostics")
    return {
        "agent":meta["agent"],"character":meta["char"],"model":meta["model"],"runtime":meta["runtime"],
        "sample_elapsed_s":last["elapsed_s"],"level":last["level"],"goal_value":last["value"],"signal_deltas":delta,
        "full_run_diagnostics":None if diagnostic is None else {
            "coverage":diagnostic["coverage"],"reliability":diagnostic["reliability"],
            "episodes":len(diagnostic["episodes"]),
            "episode_union_ms":episode_union_ms(diagnostic["episodes"]),
            "tools":diagnostic["tools"],
        },
    }


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("baseline")
    parser.add_argument("candidate")
    parser.add_argument("--seconds",type=float,help="cap the common judge-sample horizon")
    args=parser.parse_args()
    baseline,candidate=read_run(args.baseline),read_run(args.candidate)
    if baseline.keys()!=candidate.keys():
        raise ValueError("agent specs differ")
    for role in baseline:
        for key in ("model","runtime","provider","goal"):
            if baseline[role][0].get(key)!=candidate[role][0].get(key):
                raise ValueError(f"{role}: {key} differs")
    horizon=min(a[2][-1]["elapsed_s"] for a in [*baseline.values(),*candidate.values()])
    if args.seconds is not None:
        if not args.seconds>0:
            raise ValueError("seconds must be positive")
        horizon=min(horizon,args.seconds)
    print(json.dumps({"baseline":args.baseline,"candidate":args.candidate,"common_horizon_s":horizon,
        "note":"Judge signals use the common sampled horizon. Diagnostics retain each full run's own coverage; missing baseline instrumentation is not zero. One run per condition does not establish causality.",
        "agents":[{"spec":role,"baseline":summarize(baseline[role],horizon),"candidate":summarize(candidate[role],horizon)} for role in sorted(baseline)]},indent=2))


if __name__=="__main__":
    main()
