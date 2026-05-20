#!/usr/bin/env python3
"""
Create the 'aish Delivery' GitHub Project under convergent-systems-co,
add 4 custom single-select fields (Status, Domain, Risk, Iteration), and
backfill all 160 existing issues from .artifacts/spawn/issue-manifest.json
with the correct field values.

Views (Pipeline / Roadmap / By Domain) and the auto-add workflow must be
toggled in the UI — the gh CLI does not expose them.

Idempotent: caches project + field + option IDs in .artifacts/spawn/
project-manifest.json so re-runs continue where they stopped.
"""
from __future__ import annotations

import json
import sys
import time
from pathlib import Path

# Reuse the helpers and epic spec from generate.py.
sys.path.insert(0, str(Path(__file__).parent))
from generate import (  # noqa: E402
    REPO, gh_token, run, EPICS, MILESTONES,
)

OWNER = "convergent-systems-co"
PROJECT_TITLE = "aish Delivery"
ISSUE_MANIFEST = Path(".artifacts/spawn/issue-manifest.json")
PROJ_MANIFEST = Path(".artifacts/spawn/project-manifest.json")

PIPELINE_OPTIONS = ["Backlog", "In Plan", "TDD", "Coding", "Tester", "Review", "Merged"]
RISK_OPTIONS = ["Low", "Medium", "High"]
ITERATION_OPTIONS = ["v0.1", "v0.2", "v0.3", "v1.0", "v1.5"]

LABEL_TO_DOMAIN = {  # domain:<x>  ->  display name
    "domain:shell-runtime": "shell-runtime",
    "domain:cache": "cache",
    "domain:inference": "inference",
    "domain:history": "history",
    "domain:telemetry": "telemetry",
    "domain:ui": "ui",
    "domain:os-translation": "os-translation",
    "domain:theming": "theming",
    "domain:plugin": "plugin",
    "domain:secrets": "secrets",
    "domain:persona": "persona",
    "domain:build": "build",
    "domain:terminal": "terminal",
}
DOMAIN_OPTIONS = list(LABEL_TO_DOMAIN.values())

MILESTONE_TO_ITERATION = {
    MILESTONES[0][0]: "v0.1",
    MILESTONES[1][0]: "v0.2",
    MILESTONES[2][0]: "v0.3",
    MILESTONES[3][0]: "v1.0",
    MILESTONES[4][0]: "v1.5",
}

RISK_LABEL_TO_NAME = {
    "risk:low": "Low",
    "risk:medium": "Medium",
    "risk:high": "High",
}


def load_project_manifest():
    if PROJ_MANIFEST.exists():
        return json.loads(PROJ_MANIFEST.read_text())
    return {"project": None, "fields": {}, "items": {}}


def save_project_manifest(m):
    PROJ_MANIFEST.write_text(json.dumps(m, indent=2))


def find_existing_project(token):
    r = run(
        ["gh", "project", "list", "--owner", OWNER,
         "--format", "json", "--limit", "200"],
        token, check=False,
    )
    if r.returncode != 0:
        return None
    data = json.loads(r.stdout)
    for p in data.get("projects", []):
        if p.get("title") == PROJECT_TITLE:
            return p
    return None


def ensure_project(token, m):
    if m.get("project"):
        print(f"= project #{m['project']['number']} {PROJECT_TITLE} (cached)")
        return m["project"]
    existing = find_existing_project(token)
    if existing:
        print(f"= project #{existing['number']} {PROJECT_TITLE} (found existing)")
        m["project"] = existing
        save_project_manifest(m)
        return existing
    r = run(
        ["gh", "project", "create",
         "--owner", OWNER, "--title", PROJECT_TITLE,
         "--format", "json"],
        token,
    )
    proj = json.loads(r.stdout)
    print(f"+ project #{proj['number']} {PROJECT_TITLE}")
    m["project"] = proj
    save_project_manifest(m)
    return proj


def list_project_fields(token, project_number):
    r = run(
        ["gh", "project", "field-list", str(project_number),
         "--owner", OWNER, "--format", "json", "--limit", "100"],
        token,
    )
    return json.loads(r.stdout).get("fields", [])


def ensure_field(token, m, project_number, name, options):
    """Create a single-select field with the given options. Idempotent."""
    cached = m["fields"].get(name)
    if cached:
        print(f"  = field {name} (cached)")
        return cached

    fields = list_project_fields(token, project_number)
    for f in fields:
        if f.get("name") == name:
            print(f"  = field {name} (found existing)")
            m["fields"][name] = f
            save_project_manifest(m)
            return f

    r = run(
        ["gh", "project", "field-create", str(project_number),
         "--owner", OWNER, "--name", name,
         "--data-type", "SINGLE_SELECT",
         "--single-select-options", ",".join(options),
         "--format", "json"],
        token,
    )
    field = json.loads(r.stdout)
    print(f"  + field {name} ({len(options)} options)")
    m["fields"][name] = field
    save_project_manifest(m)
    return field


def option_id_map(field):
    return {o["name"]: o["id"] for o in field.get("options", [])}


def add_item(token, project_id, project_number, issue_number):
    r = run(
        ["gh", "project", "item-add", str(project_number),
         "--owner", OWNER,
         "--url", f"https://github.com/{REPO}/issues/{issue_number}",
         "--format", "json"],
        token, check=False,
    )
    if r.returncode != 0:
        blob = (r.stderr or "") + (r.stdout or "")
        # Already added? gh prints "already exists" or similar.
        if "already" in blob.lower():
            return None
        print(f"  ! item-add issue #{issue_number}: {blob}", file=sys.stderr)
        return None
    return json.loads(r.stdout)


def set_single_select(token, project_id, item_id, field_id, option_id):
    r = run(
        ["gh", "project", "item-edit",
         "--id", item_id,
         "--project-id", project_id,
         "--field-id", field_id,
         "--single-select-option-id", option_id],
        token, check=False,
    )
    if r.returncode != 0:
        print(f"  ! item-edit item={item_id} field={field_id}: "
              f"{(r.stderr or '').strip()[:120]}", file=sys.stderr)
        return False
    return True


def main():
    token = gh_token()
    pm = load_project_manifest()

    print(f"Owner: {OWNER}  |  Title: {PROJECT_TITLE}\n")
    project = ensure_project(token, pm)
    project_id = project["id"]
    project_number = project["number"]
    print()

    print("Fields:")
    f_pipeline = ensure_field(token, pm, project_number, "Pipeline", PIPELINE_OPTIONS)
    f_domain = ensure_field(token, pm, project_number, "Domain", DOMAIN_OPTIONS)
    f_risk = ensure_field(token, pm, project_number, "Risk", RISK_OPTIONS)
    f_iter = ensure_field(token, pm, project_number, "Iteration", ITERATION_OPTIONS)
    print()

    pipeline_opts = option_id_map(f_pipeline)
    domain_opts = option_id_map(f_domain)
    risk_opts = option_id_map(f_risk)
    iter_opts = option_id_map(f_iter)

    issue_manifest = json.loads(ISSUE_MANIFEST.read_text())
    epic_meta = {}  # epic_id -> (domain_display, risk_display, iteration_display)
    for e in EPICS:
        d = next(l for l in e["labels"] if l.startswith("domain:"))
        rk = next(l for l in e["labels"] if l.startswith("risk:"))
        ms = MILESTONES[e["milestone_idx"]][0]
        epic_meta[e["id"]] = (
            LABEL_TO_DOMAIN[d], RISK_LABEL_TO_NAME[rk],
            MILESTONE_TO_ITERATION[ms],
        )

    items = []  # (issue_number, epic_id)
    for epic_id, info in issue_manifest["epics"].items():
        items.append((info["number"], epic_id))
        for s in info["subs"]:
            items.append((s["number"], epic_id))

    print(f"Items ({len(items)}): adding & setting fields")
    done = 0
    skipped = 0
    for issue_number, epic_id in items:
        cached = pm["items"].get(str(issue_number))
        if cached and cached.get("pipeline_set") and cached.get("domain_set"):
            skipped += 1
            continue

        if cached and cached.get("item_id"):
            item_id = cached["item_id"]
        else:
            added = add_item(token, project_id, project_number, issue_number)
            if added is None:
                # Re-list to find it if it was already added
                r = run(
                    ["gh", "project", "item-list", str(project_number),
                     "--owner", OWNER, "--format", "json", "--limit", "500"],
                    token,
                )
                items_list = json.loads(r.stdout).get("items", [])
                match = next(
                    (it for it in items_list
                     if it.get("content", {}).get("number") == issue_number),
                    None,
                )
                if not match:
                    print(f"  ! could not find item for #{issue_number}",
                          file=sys.stderr)
                    continue
                item_id = match["id"]
            else:
                item_id = added["id"]

        domain_name, risk_name, iter_name = epic_meta[epic_id]
        ok_p = set_single_select(token, project_id, item_id,
                                 f_pipeline["id"], pipeline_opts["Backlog"])
        ok_d = set_single_select(token, project_id, item_id,
                                 f_domain["id"], domain_opts[domain_name])
        ok_r = set_single_select(token, project_id, item_id,
                                 f_risk["id"], risk_opts[risk_name])
        ok_i = set_single_select(token, project_id, item_id,
                                 f_iter["id"], iter_opts[iter_name])
        pm["items"][str(issue_number)] = {
            "item_id": item_id,
            "epic_id": epic_id,
            "pipeline_set": ok_p, "domain_set": ok_d,
            "risk_set": ok_r, "iter_set": ok_i,
        }
        save_project_manifest(pm)
        done += 1
        if done % 20 == 0:
            print(f"  ... {done} items processed")
        time.sleep(0.10)

    print(f"\nDone. {done} items configured, {skipped} cached.")
    print(f"Project: https://github.com/orgs/{OWNER}/projects/{project_number}")
    print(f"Manifest: {PROJ_MANIFEST}")


if __name__ == "__main__":
    main()
