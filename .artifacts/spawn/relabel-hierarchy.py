#!/usr/bin/env python3
"""Apply the EPIC/Task hierarchy labels to existing issues from the manifest.

Idempotent: --add-label is safe to re-run (gh ignores duplicates).
"""
import json
import os
import subprocess
import sys
import time
from pathlib import Path

REPO = "convergent-systems-co/aish"
MANIFEST = Path(".artifacts/spawn/issue-manifest.json")


def gh_token():
    return subprocess.check_output(
        ["gh", "auth", "token", "--user", "polliard"]
    ).decode().strip()


def add_label(token, number, label):
    env = {**os.environ, "GH_TOKEN": token}
    r = subprocess.run(
        ["gh", "issue", "edit", str(number),
         "-R", REPO, "--add-label", label],
        env=env, capture_output=True, text=True,
    )
    if r.returncode == 0:
        return True, ""
    return False, (r.stderr or r.stdout or "").strip()


def main():
    m = json.loads(MANIFEST.read_text())
    epics = [e["number"] for e in m["epics"].values()]
    tasks = [s["number"] for e in m["epics"].values() for s in e["subs"]]

    token = gh_token()

    print(f"Adding 'epic' label to {len(epics)} epic issues...")
    ok = fail = 0
    for n in epics:
        success, msg = add_label(token, n, "epic")
        if success:
            ok += 1
        else:
            fail += 1
            print(f"  fail #{n}: {msg[:120]}")
        time.sleep(0.10)
    print(f"  → {ok} ok, {fail} fail")

    print(f"\nAdding 'task' label to {len(tasks)} sub-issues...")
    ok = fail = 0
    for i, n in enumerate(tasks, 1):
        success, msg = add_label(token, n, "task")
        if success:
            ok += 1
        else:
            fail += 1
            print(f"  fail #{n}: {msg[:120]}")
        if i % 20 == 0:
            print(f"  ... {i}/{len(tasks)} ({ok} ok, {fail} fail)")
        time.sleep(0.10)
    print(f"  → {ok} ok, {fail} fail")


if __name__ == "__main__":
    main()
