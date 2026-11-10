#!/usr/bin/env python3
"""Summarizes an E05 trials.jsonl (measured trials only): counts, error kinds, p50/p95/p99/max.

Usage: e05-stats.py trials.jsonl"""
import json,sys,statistics
rows=[json.loads(l) for l in open(sys.argv[1]) if l.strip()]
m=[r for r in rows if not r.get("warmup")]
def pct(v,p):
    v=sorted(v); return v[min(len(v)-1,int(round(p/100*(len(v)-1))))]
def st(name,v): print(f"{name}: n={len(v)} p50={pct(v,50):.0f} p95={pct(v,95):.0f} p99={pct(v,99):.0f} max={max(v):.0f}")
print("measured",len(m),"ok",sum(r["ok"] for r in m),"state_bad",sum(not r["state_ok"] for r in m),"clock_bad",sum(not r["clock_ok"] for r in m),"iso_bad",sum(not r["isolation_ok"] for r in m))
st("fork_ms",[r["fork_ms"] for r in m]); st("pause_ms",[r["source_pause_ms"] for r in m])
st("full_write_ms",[int(r["capture"]["full_write_us"])/1000 for r in m])
st("skew_src_ms",[r["clock_skew_s"]["source"]*1000 for r in m]); st("skew_fork_ms",[r["clock_skew_s"]["fork"]*1000 for r in m])
st("rss_fork",[r["rss_mib"]["fork"] for r in m]); st("rss_src",[r["rss_mib"]["source"] for r in m])
print("under1000",sum(r["fork_ms"]<1000 for r in m))
print("lost_acked",sum("lost_acked=0" not in r.get("fork_check","")+r.get("source_check","") for r in m))
print("modes",set(r["restore"].get("fork_mode") for r in m))
