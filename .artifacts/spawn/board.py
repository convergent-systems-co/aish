#!/usr/bin/env python3
"""
board.py — /spawn distributed-lock CLI over the GitHub Project Pipeline.

The Project's `Pipeline` field is the authoritative lock on every issue.
Anything not in `Backlog` is already in flight; DevOps must not re-grab it.

Subcommands
-----------
  available [--domain X] [--milestone v0.X] [-v]
        Print issue numbers whose Pipeline == Backlog.

  claim <issue> [--to "In Plan"]
        Atomic Backlog -> <target>. Fails if current != Backlog.

  transition <issue> --to <state>
        Unconditional Pipeline -> <state>. Use only for orderly progress
        (Plan -> TDD -> Coding -> Tester -> Review -> Merged) and rollback.

  release <issue>
        Pipeline -> Backlog. Use on TL/coder/tester failure to free the
        issue for re-grab.

  status <issue>
        Print the current Pipeline value of one issue.

Reads `.artifacts/spawn/project-manifest.json` for the project + field IDs
(populated by setup-project.py). Uses the `polliard` keyring identity per
~/.ai/Common.md §4.7.
"""
from __future__ import annotations

import argparse
import json
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path

REPO = "convergent-systems-co/aish"
OWNER = "convergent-systems-co"
MANIFEST = Path(".artifacts/spawn/project-manifest.json")

VALID_STATES = ["Backlog", "In Plan", "TDD", "Coding", "Tester", "Review", "Merged"]


def gh_token() -> str:
    r = subprocess.run(
        ["gh", "auth", "token", "--user", "polliard"],
        capture_output=True, text=True, check=True,
    )
    return r.stdout.strip()


def gql(token: str, query: str, variables: dict) -> dict:
    body = json.dumps({"query": query, "variables": variables}).encode()
    req = urllib.request.Request(
        "https://api.github.com/graphql",
        data=body,
        headers={
            "Authorization": f"Bearer {token}",
            "Accept": "application/vnd.github+json",
            "Content-Type": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(req) as r:
            data = json.loads(r.read())
    except urllib.error.HTTPError as e:
        sys.exit(f"HTTP {e.code}: {e.read().decode()[:500]}")
    if "errors" in data:
        sys.exit("GraphQL errors: " + json.dumps(data["errors"]))
    return data["data"]


def load_manifest() -> dict:
    if not MANIFEST.exists():
        sys.exit(f"missing {MANIFEST} — run setup-project.py first")
    return json.loads(MANIFEST.read_text())


def pipeline_field(m: dict) -> tuple[str, dict]:
    f = m["fields"]["Pipeline"]
    options = {o["name"]: o["id"] for o in f["options"]}
    missing = [s for s in VALID_STATES if s not in options]
    if missing:
        sys.exit(f"Pipeline missing options: {missing}")
    return f["id"], options


# ---------- queries ----------

Q_PROJECT_ITEMS = """
query($owner:String!, $num:Int!, $cursor:String) {
  organization(login:$owner) {
    projectV2(number:$num) {
      items(first:100, after:$cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id
          content {
            __typename
            ... on Issue {
              number state
              repository { nameWithOwner }
              milestone { title }
            }
          }
          fieldValues(first:30) {
            nodes {
              __typename
              ... on ProjectV2ItemFieldSingleSelectValue {
                name
                field { ... on ProjectV2SingleSelectField { name } }
              }
            }
          }
        }
      }
    }
  }
}
"""

Q_ISSUE_PROJECT_ITEM = """
query($owner:String!, $repo:String!, $num:Int!) {
  repository(owner:$owner, name:$repo) {
    issue(number:$num) {
      id state
      projectItems(first:10) {
        nodes {
          id
          project { id number }
          fieldValues(first:30) {
            nodes {
              __typename
              ... on ProjectV2ItemFieldSingleSelectValue {
                name
                field { ... on ProjectV2SingleSelectField { name } }
              }
            }
          }
        }
      }
    }
  }
}
"""

M_SET_SINGLE_SELECT = """
mutation($p:ID!, $i:ID!, $f:ID!, $o:String!) {
  updateProjectV2ItemFieldValue(input:{
    projectId:$p, itemId:$i, fieldId:$f,
    value:{singleSelectOptionId:$o}
  }) { projectV2Item { id } }
}
"""


# ---------- helpers ----------

def project_pipeline(item) -> tuple[str | None, str | None, str | None]:
    """Return (pipeline, domain, iteration) from a project item's field values."""
    pipeline = domain = iteration = None
    for fv in item.get("fieldValues", {}).get("nodes", []):
        if not fv:
            continue
        if fv.get("__typename") != "ProjectV2ItemFieldSingleSelectValue":
            continue
        fname = (fv.get("field") or {}).get("name")
        if fname == "Pipeline":
            pipeline = fv.get("name")
        elif fname == "Domain":
            domain = fv.get("name")
        elif fname == "Iteration":
            iteration = fv.get("name")
    return pipeline, domain, iteration


def find_project_item(token: str, project_id: str, issue_number: int,
                       repo: str = REPO) -> tuple[str | None, str | None, str]:
    owner, name = repo.split("/")
    data = gql(token, Q_ISSUE_PROJECT_ITEM,
               {"owner": owner, "repo": name, "num": int(issue_number)})
    issue = data["repository"]["issue"]
    if not issue:
        return None, None, "NOT_FOUND"
    state = issue["state"]  # OPEN / CLOSED
    for pi in issue["projectItems"]["nodes"]:
        if pi["project"]["id"] == project_id:
            pipeline, _, _ = project_pipeline(pi)
            return pi["id"], pipeline, state
    return None, None, state


def set_pipeline(token, project_id, item_id, field_id, option_id):
    gql(token, M_SET_SINGLE_SELECT, {
        "p": project_id, "i": item_id, "f": field_id, "o": option_id,
    })


# ---------- subcommands ----------

def cmd_available(args, token, manifest):
    project_number = manifest["project"]["number"]
    cursor = None
    rows = []
    while True:
        data = gql(token, Q_PROJECT_ITEMS,
                   {"owner": OWNER, "num": project_number, "cursor": cursor})
        items = data["organization"]["projectV2"]["items"]
        for n in items["nodes"]:
            content = n.get("content") or {}
            if content.get("__typename") != "Issue":
                continue
            if content.get("state") != "OPEN":
                continue
            number = content["number"]
            pipeline, domain, iteration = project_pipeline(n)
            if pipeline != "Backlog":
                continue
            if args.domain and domain != args.domain:
                continue
            if args.milestone:
                ms = (content.get("milestone") or {}).get("title", "")
                if not ms.startswith(args.milestone):
                    continue
            rows.append((number, domain, iteration,
                         content["repository"]["nameWithOwner"]))
        if not items["pageInfo"]["hasNextPage"]:
            break
        cursor = items["pageInfo"]["endCursor"]
    rows.sort()
    for number, domain, iteration, repo in rows:
        if args.verbose:
            print(f"#{number}\t{iteration or '-'}\t{domain or '-'}\t{repo}")
        else:
            print(number)
    if args.verbose:
        print(f"# total: {len(rows)} Backlog items", file=sys.stderr)


def cmd_status(args, token, manifest):
    project_id = manifest["project"]["id"]
    item_id, pipeline, issue_state = find_project_item(token, project_id, args.issue)
    if item_id is None:
        sys.exit(f"#{args.issue}: not on project (issue {issue_state})")
    print(f"#{args.issue}\tissue={issue_state}\tPipeline={pipeline}")


def cmd_claim(args, token, manifest):
    project_id = manifest["project"]["id"]
    field_id, options = pipeline_field(manifest)
    if args.to not in options:
        sys.exit(f"unknown state: {args.to}")
    item_id, current, issue_state = find_project_item(token, project_id, args.issue)
    if item_id is None:
        sys.exit(f"#{args.issue}: not on project (issue {issue_state})")
    if issue_state != "OPEN":
        sys.exit(f"#{args.issue}: issue is {issue_state} — cannot claim")
    if current != "Backlog":
        sys.exit(f"#{args.issue}: already claimed (Pipeline={current})")
    set_pipeline(token, project_id, item_id, field_id, options[args.to])
    print(f"#{args.issue}: Backlog -> {args.to}")


def cmd_transition(args, token, manifest):
    project_id = manifest["project"]["id"]
    field_id, options = pipeline_field(manifest)
    if args.to not in options:
        sys.exit(f"unknown state: {args.to}")
    item_id, current, issue_state = find_project_item(token, project_id, args.issue)
    if item_id is None:
        sys.exit(f"#{args.issue}: not on project (issue {issue_state})")
    set_pipeline(token, project_id, item_id, field_id, options[args.to])
    print(f"#{args.issue}: {current} -> {args.to}")


def cmd_release(args, token, manifest):
    args.to = "Backlog"
    cmd_transition(args, token, manifest)


# ---------- main ----------

def main():
    p = argparse.ArgumentParser(description=__doc__.splitlines()[1],
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = p.add_subparsers(dest="cmd", required=True)

    a = sub.add_parser("available", help="list Backlog issue numbers")
    a.add_argument("--domain", help="filter by Domain field")
    a.add_argument("--milestone", help="filter by milestone title prefix (e.g. v0.1)")
    a.add_argument("-v", "--verbose", action="store_true")
    a.set_defaults(func=cmd_available)

    c = sub.add_parser("claim", help="atomic Backlog -> <state> (default: 'In Plan')")
    c.add_argument("issue", type=int)
    c.add_argument("--to", default="In Plan", choices=VALID_STATES)
    c.set_defaults(func=cmd_claim)

    t = sub.add_parser("transition", help="advance Pipeline to <state>")
    t.add_argument("issue", type=int)
    t.add_argument("--to", required=True, choices=VALID_STATES)
    t.set_defaults(func=cmd_transition)

    r = sub.add_parser("release", help="reset Pipeline to Backlog")
    r.add_argument("issue", type=int)
    r.set_defaults(func=cmd_release)

    s = sub.add_parser("status", help="print Pipeline for one issue")
    s.add_argument("issue", type=int)
    s.set_defaults(func=cmd_status)

    args = p.parse_args()
    token = gh_token()
    manifest = load_manifest()
    args.func(args, token, manifest)


if __name__ == "__main__":
    main()
