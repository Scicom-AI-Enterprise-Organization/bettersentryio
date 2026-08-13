#!/usr/bin/env python3
"""Print the monitors wall as a table. Development convenience only."""
import json
import sys
import urllib.request

base = sys.argv[1] if len(sys.argv) > 1 else "http://localhost:9090"
with urllib.request.urlopen(f"{base}/api/0/monitors", timeout=5) as resp:
    monitors = json.load(resp)["monitors"]

if not monitors:
    print("   (no monitors yet)")
    sys.exit(0)

for m in monitors:
    beat = (m["last_beat_at"] or "-")[11:19]
    progress = m["last_progress"] if m["last_progress"] is not None else "-"
    print(
        f"   {m['monitor']:<14} {m['status'].upper():<8}"
        f" last_beat={beat:<9} progress={progress:<5} every={m['expected_every_s']}s"
    )
