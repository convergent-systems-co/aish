#!/usr/bin/env python3
"""
Promote GOALS.md roadmap into GitHub milestones, epics, and sub-issues.

Idempotent: re-running skips existing labels / milestones / issues by exact
title match. Writes .artifacts/spawn/issue-manifest.json with everything
created.

Run: python3 .artifacts/spawn/generate.py [--epics v0.1-1,v0.1-2,...|all]
"""
from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import time
from pathlib import Path

REPO = "convergent-systems-co/aish"
ARTIFACTS = Path(".artifacts/spawn")
MANIFEST = ARTIFACTS / "issue-manifest.json"


def gh_token() -> str:
    r = subprocess.run(
        ["gh", "auth", "token", "--user", "polliard"],
        capture_output=True, text=True, check=True,
    )
    return r.stdout.strip()


TRANSIENT_MARKERS = (
    "timeout", "tls handshake", "temporary failure",
    "connection reset", "EOF", "i/o timeout", "no such host",
    "503 ", "502 ", "504 ", "Bad Gateway",
)


def run(args: list[str], token: str, check: bool = True,
        retries: int = 4) -> subprocess.CompletedProcess:
    env = {**os.environ, "GH_TOKEN": token}
    delay = 1.5
    for attempt in range(retries + 1):
        r = subprocess.run(args, env=env, capture_output=True, text=True)
        if r.returncode == 0:
            return r
        blob = (r.stderr or "") + (r.stdout or "")
        if attempt < retries and any(m.lower() in blob.lower()
                                     for m in TRANSIENT_MARKERS):
            print(f"  ~ transient ({blob.strip().splitlines()[-1][:80]}); "
                  f"retry {attempt + 1}/{retries} in {delay:.1f}s",
                  file=sys.stderr)
            time.sleep(delay)
            delay *= 2
            continue
        if check:
            print(f"FAIL: {' '.join(args)}", file=sys.stderr)
            print(r.stderr, file=sys.stderr)
            sys.exit(1)
        return r
    return r


# ---------- Labels ----------
LABELS = [
    ("type:epic", "5319e7", "Multi-issue epic — has sub-issues"),
    ("type:feature", "a2eeef", "New capability"),
    ("type:chore", "cfd3d7", "Setup, plumbing, infra glue"),
    ("type:research", "fbca04", "Spike / measurement / prove-or-kill"),
    ("domain:shell-runtime", "0e8a16", "Parser, exec, PTY, job control, built-ins"),
    ("domain:cache", "0e8a16", "Intent cache (L1/L2/L3)"),
    ("domain:inference", "0e8a16", "Inference plugin contract + providers"),
    ("domain:history", "0e8a16", "Event log, snapshots, undo, rollback"),
    ("domain:telemetry", "0e8a16", "Metrics, dashboards, measurement"),
    ("domain:ui", "0e8a16", "TUI, prompt, completions, syntax highlight"),
    ("domain:os-translation", "0e8a16", "Cross-platform OS abstractions and script translation"),
    ("domain:theming", "0e8a16", "Brand Atoms integration, themes"),
    ("domain:plugin", "0e8a16", "Plugin registry + lifecycle"),
    ("domain:secrets", "0e8a16", "Secrets, identity, taint tracking"),
    ("domain:persona", "0e8a16", "Embedded personality / voice / character"),
    ("domain:build", "0e8a16", "Build pipeline, installers, distribution"),
    ("domain:terminal", "0e8a16", "aish-term terminal emulator"),
    ("risk:low", "c2e0c6", "Standard work"),
    ("risk:medium", "fef2c0", "Notable unknowns"),
    ("risk:high", "f9d0c4", "Thesis-critical or platform-novel"),
    ("os:darwin", "ededed", "macOS-specific"),
    ("os:linux", "ededed", "Linux-specific"),
    ("os:windows", "ededed", "Windows-specific"),
    ("blocked", "b60205", "Hard stop until precondition met"),
]


# ---------- Milestones ----------
MILESTONES = [
    ("v0.1 — Thesis Validation (90 days)",
     "Prove the intent cache flywheel. Cache hit rate >50% by day 30 of typical usage. Kill criterion: stagnates below 20%."),
    ("v0.2 — Layer Polish (60 days)",
     "Make aish feel good as a layer inside any terminal. Convert trial users to daily users."),
    ("v0.3 — Real Shell (90 days)",
     "Login shell capability on macOS and Linux. Replace bash/zsh for users who want to."),
    ("v1.0 — Windows Native (120 days)",
     "First-class Windows developer platform without WSL. Highest-risk technical work."),
    ("v1.5 — aish-term (Scope TBD)",
     "Custom terminal emulator. Gated on >10,000 active aish-shell users."),
]


# ---------- Epics (in order) ----------
# Each entry: (epic_id, title, version_index_into_MILESTONES, domain_labels,
#              risk_label, extra_labels, preamble, risk_note, sub_titles)
EPICS = []


def add_epic(epic_id, title, ms_idx, domains, risk, extras, preamble, risk_note, subs):
    EPICS.append({
        "id": epic_id,
        "title": f"[epic] {epic_id} {title}",
        "milestone_idx": ms_idx,
        "labels": ["type:epic"] + domains + [risk] + extras,
        "sub_labels": ["type:feature"] + domains + [risk] + extras,
        "preamble": preamble,
        "risk_note": risk_note,
        "subs": subs,
    })


# v0.1 ────────────────────────────────────────────────────────────────
add_epic(
    "v0.1-1", "Minimum Shell", 0,
    ["domain:shell-runtime"], "risk:low", [],
    "aish runs as a command inside any terminal. Parses input, resolves to exec or inference, executes.",
    "Low. This is standard Go shell work.",
    [
        "Go project skeleton, build pipeline for macOS + Linux",
        "CLI entry point — `aish` command launches interactive prompt",
        "Command parser — tokenize input, handle quotes, flags, pipes",
        "Exec via `os/exec` for non-interactive commands",
        "Stdin/stdout/stderr piping between commands",
        "Working directory tracking",
        "Environment variable passthrough",
        "Exit code capture and `$?` support",
        "Output type detection — probe first 512 bytes, classify as text/json/ndjson",
        "Basic prompt (no Nerd Font yet — that is polish)",
    ],
)

add_epic(
    "v0.1-2", "Intent Cache L1", 0,
    ["domain:cache"], "risk:high", ["type:research"],
    "The flywheel. Personal cache on local disk, SQLite + embedding index.",
    "HIGH. This is the thesis. If similarity matching does not yield high hit rates, the project pivots or dies.",
    [
        "Cache schema design (intent, embedding, per-OS resolutions, confidence, usage count)",
        "SQLite cache store at `~/.aish/cache.db`",
        "Exact-match lookup before any inference",
        "Embedding generation for cache entries (use cloud inference plugin)",
        "Embedding similarity lookup with configurable threshold",
        "Cache write path — after inference succeeds, compile + store",
        "Per-OS resolution storage (darwin, linux entries from day one)",
        "Cache hit rate metric tracked locally",
        "`aish cache stats` command — shows hit rate, size, top intents",
    ],
)

add_epic(
    "v0.1-3", "Cloud Inference Plugin", 0,
    ["domain:inference"], "risk:low", [],
    "Single inference path for v0.1. Claude API. NDJSON streaming.",
    "Low. API integration work.",
    [
        "Inference plugin contract definition (JSON-RPC over stdin/stdout)",
        "Claude API plugin implementation",
        "Streaming NDJSON response handling",
        "Timeout and retry logic",
        "API key management via env var (proper secrets engine is v0.3)",
        "Per-request cost tracking (logged for measurement)",
    ],
)

add_epic(
    "v0.1-4", "Basic Reversibility", 0,
    ["domain:history"], "risk:medium", [],
    "The viral demo feature. Restore deleted files.",
    "Medium. Snapshot storage growth needs management. Configurable limits are important. v0.1 scope: delete operations only; modifications and moves come in v0.2.",
    [
        "Structured event schema (JSON, append-only)",
        "Event log at `~/.aish/history.db` (SQLite WAL)",
        "Detect `rm` and equivalent destructive commands before execution",
        "Pre-execution snapshot — copy file content to `~/.aish/snapshots/`",
        "Snapshot size limit configurable (default 100MB per file)",
        "`aish undo` — restore last destructive operation",
        "`aish restore <path>` — restore specific deleted path",
        "Skip snapshots for files matching `.gitignore`-style patterns (node_modules, etc.)",
    ],
)

add_epic(
    "v0.1-5", "Telemetry & Measurement", 0,
    ["domain:telemetry"], "risk:low", [],
    "Cannot validate the thesis without measurement.",
    "Low technically. Privacy framing matters — be explicit and honest.",
    [
        "Opt-in anonymous telemetry (clear consent on first run)",
        "Per-session metrics: total commands, cache hits, cache misses, inference calls",
        "Cache hit rate over time series",
        "Inference cost tracking (Drachma equivalent or USD)",
        "Local dashboard via `aish stats`",
        "Aggregate dashboard for the team to see across users (privacy-preserving)",
    ],
)

# v0.2 ────────────────────────────────────────────────────────────────
add_epic(
    "v0.2-1", "Interactive Shell UX", 1,
    ["domain:ui"], "risk:low", [],
    "Make aish feel genuinely good to use as a layer inside any terminal.",
    "Low.",
    [
        "BubbleTea TUI integration",
        "Auto-suggestions from cache + history (ghost text)",
        "Syntax highlighting as-you-type",
        "Smart tab completions (paths, flags, git refs)",
        "`Ctrl+R` fuzzy history search",
        "Nerd Font prompt with cwd + git + ai-tier segments",
    ],
)

add_epic(
    "v0.2-2", "PTY Support", 1,
    ["domain:shell-runtime"], "risk:medium", [],
    "Proper terminal handling for interactive programs (vim, ssh, htop).",
    "Medium. PTY behavior differs subtly across platforms; signal forwarding is fiddly.",
    [
        "`github.com/creack/pty` integration",
        "PTY allocation for interactive programs",
        "Signal forwarding — SIGINT, SIGTSTP, SIGWINCH",
        "Terminal size propagation",
        "Test against: vim, ssh, htop, less, top, az login",
    ],
)

add_epic(
    "v0.2-3", "Community Cache (L3)", 1,
    ["domain:cache"], "risk:medium", [],
    "Ship pre-populated cache with installer so new installs start warm.",
    "Medium. Signing + trust model needs to be right from day one.",
    [
        "Cache bundle format definition",
        "Curated initial community cache (1000+ common intents)",
        "Ship pre-populated cache with installer",
        "Privacy-preserving cache contribution flow (opt-in)",
        "Cache signing for trust",
    ],
)

add_epic(
    "v0.2-4", "Script Translation", 1,
    ["domain:os-translation"], "risk:medium", [],
    "Read existing scripts in any shell and execute natively. Explain, migrate, audit.",
    "Medium. Bash/zsh edge cases are deep.",
    [
        "bash script reader and intent extractor",
        "zsh script reader",
        "fish script reader",
        "`aish run <script>` — translate and execute",
        "`aish explain <script>` — plain language description",
        "`aish migrate <script>` — output aish-native script",
    ],
)

add_epic(
    "v0.2-5", "Brand Atoms Theming", 1,
    ["domain:theming"], "risk:low", [],
    "Consume Brand Atoms shell brand type. Catalog lives in Brand Atoms.",
    "Low for aish (consumer side is straightforward). Brand Atoms PR for `shell` brand type schema is a coordination dependency.",
    [
        "Brand Atoms `shell` brand type schema (PR to brand-atoms repo)",
        "Brand Atoms fetch + cache client in aish",
        "Theme loader — resolve brand to concrete theme struct",
        "ANSI escape sequence pre-compilation",
        "Atomic theme swap (`aish theme set <name>`)",
        "10 curated shell brands published to Brand Atoms at launch",
        "`aish theme list` — show available brands",
        "`aish theme preview <name>` — show theme without applying",
        "Theme persistence in `~/.aish/config.toml`",
        "Performance test — confirm sub-50ms theme switch, sub-microsecond per-character render",
    ],
)

# v0.3 ────────────────────────────────────────────────────────────────
add_epic(
    "v0.3-1", "Login Shell Capabilities", 2,
    ["domain:shell-runtime"], "risk:medium", [],
    "Make aish a viable login shell on macOS and Linux.",
    "Medium. Built-ins and job control are standard but signal handling is fiddly.",
    [
        "Job control — fg, bg, jobs",
        "Process group management",
        "Login shell registration (`/etc/shells`, `chsh`)",
        "RC file loading from `~/.aish/config.toml`",
        "Shell built-ins: cd, export, alias, source, set, unset",
        "Brace expansion, glob, command substitution",
    ],
)

add_epic(
    "v0.3-2", "Plugin Registry", 2,
    ["domain:plugin"], "risk:medium", [],
    "JSON-RPC plugin contract. Local-first registry. Lifecycle managed by aish.",
    "Medium. Plugin lifecycle and crash isolation needs care.",
    [
        "Plugin JSON-RPC contract finalized",
        "`aish plugin install/list/remove/build` commands",
        "Local plugin manifest format",
        "First community plugin: Ollama inference",
        "Plugin lifecycle management (spawn, health, restart)",
    ],
)

add_epic(
    "v0.3-3", "Identity & Secrets Engine", 2,
    ["domain:secrets"], "risk:high", [],
    "Identity and secrets under one engine. Taint tracking, signed events, atomic persona swap.",
    "HIGH. Secrets correctness has zero tolerance for error. Taint propagation through pipes is novel.",
    [
        "Taint bit type system in Go",
        "`aish secret set/get/list/remove`",
        "History event exclusion for tainted values",
        "Pipe taint propagation",
        "macOS Keychain integration",
        "Linux libsecret integration",
        "Identity persona schema and storage",
        "`aish identity use/list/show/create`",
        "Atomic persona switching — SSH, cloud profiles, kube context, git config",
        "Persona-bound secrets (secrets that activate with a persona)",
        "Signed history events for every persona switch and secret access",
    ],
)

add_epic(
    "v0.3-4", "History Engine Maturity", 2,
    ["domain:history"], "risk:medium", [],
    "Signed events, checkpoints, semantic search, mod/rename tracking.",
    "Medium. Signing and semantic search need to interact cleanly with existing history events.",
    [
        "Ed25519 signing for all history events",
        "`aish checkpoint <name>` and `aish rollback <name>`",
        "Modification snapshots (not just deletes)",
        "Move/rename tracking",
        "Semantic history search using embeddings",
        "`aish history search \"<query>\"`",
    ],
)

add_epic(
    "v0.3-5", "Persona Engine (Embedded Personality)", 2,
    ["domain:persona"], "risk:medium", [],
    "The shell embodies a personality. Voice, tone, prompt segments, and AI-mediated responses reflect the active persona. Distinct from identity (v0.3-3): identity answers *who you are*; persona answers *who the shell is being for you*.",
    "Medium. Novel surface. The plugin contract extension coordinates with v0.1-3 and v0.3-2; the safety floor is non-negotiable.",
    [
        "Persona schema (`personas/<name>.toml` — voice, system prompt, tone parameters, prompt overrides)",
        "Persona storage at `~/.aish/personas/`",
        "Built-in starter personas: `default`, `mentor`, `terse-veteran`, `playful`",
        "`aish persona use <name>` — activate; signed history event",
        "`aish persona list` — installed personas",
        "`aish persona show <name>` — inspect schema and bindings",
        "`aish persona create <name>` — guided bootstrap",
        "Inference plugin contract extension — persona context in `infer` request params",
        "System-prompt injection that shapes AI tone without bypassing safety",
        "Prompt segment overrides — greeting glyph, voice phrase, accent character",
        "History events record active persona at command time",
        "Persona persistence in `~/.aish/config.toml`",
        "Persona bundle format for sharing (signed, versioned, community-installable)",
        "Optional identity-persona binding (`aish identity use work --persona mentor`)",
        "Safety floor — personas MUST NOT bypass vendor safety policy, secret-handling, or destructive-action gates",
    ],
)

# v1.0 ────────────────────────────────────────────────────────────────
add_epic(
    "v1.0-1", "Windows Build Pipeline", 3,
    ["domain:build"], "risk:medium", ["os:windows"],
    "Windows binary cross-compilation, signed installer, Windows Terminal compat.",
    "Medium.",
    [
        "Windows binary cross-compilation from CI",
        "Signed Windows installer (MSI or modern equivalent)",
        "Windows Terminal compatibility verification",
        "PowerShell compatibility for hybrid users",
    ],
)

add_epic(
    "v1.0-2", "Win32 OS Translation", 3,
    ["domain:os-translation"], "risk:high", ["os:windows"],
    "Win32 CGO bindings for the subset of OS APIs aish translates against.",
    "HIGH. Win32 CGO bindings are complex. Defer features to v1.1 if blocking.",
    [
        "Win32 CGO bindings (subset needed)",
        "`aish install` via winget",
        "`aish service` via Win32 Service Control Manager",
        "`aish process` via Win32 process APIs",
        "`aish env` via Win32 environment APIs",
        "`aish network` basic via Win32 networking APIs",
    ],
)

add_epic(
    "v1.0-3", "Windows Script Translation", 3,
    ["domain:os-translation"], "risk:medium", ["os:windows"],
    "PowerShell and cmd/bat script readers; test against common Windows admin scripts.",
    "Medium.",
    [
        "PowerShell script reader and intent extractor",
        "cmd/bat script reader",
        "Test against common Windows admin scripts",
    ],
)

add_epic(
    "v1.0-4", "Windows Secrets", 3,
    ["domain:secrets"], "risk:medium", ["os:windows"],
    "Windows Credential Manager integration; `aish secret` parity across platforms.",
    "Medium.",
    [
        "Windows Credential Manager CGO integration",
        "`aish secret` parity with macOS/Linux behavior",
    ],
)

add_epic(
    "v1.0-5", "Windows Login Shell", 3,
    ["domain:shell-runtime"], "risk:high", ["os:windows"],
    "Windows console host, ConPTY, default-shell registry. The hardest part of v1.0.",
    "HIGH. ConPTY semantics differ from POSIX PTY; default-shell registry is advanced setup.",
    [
        "Windows console host integration",
        "PTY support on Windows (ConPTY)",
        "Registry changes for default shell (advanced setup)",
    ],
)

# v1.5 ────────────────────────────────────────────────────────────────
add_epic(
    "v1.5-1", "aish-term", 4,
    ["domain:terminal"], "risk:high", ["blocked"],
    "Custom OpenGL terminal emulator. GATED on >10,000 active aish-shell users across all platforms.",
    "HIGH. Ghostty took years. Do not start until the shell is genuinely successful.",
    [
        "OpenGL renderer (cross-platform, identical pixels)",
        "`libghostty-vt` integration for VT parsing",
        "Built-in tabs, splits, panes (no tmux dependency)",
        "Single TOML config across all platforms",
        "Native shell integration with aish",
        "Kitty graphics protocol support",
        "Nerd Font defaults",
    ],
)


# ---------- Execution ----------
def ensure_label(token, name, color, desc):
    r = run(
        ["gh", "label", "create", name,
         "--color", color, "--description", desc,
         "-R", REPO],
        token, check=False,
    )
    if r.returncode == 0:
        print(f"  + label {name}")
        return "created"
    if "already exists" in (r.stderr or "") + (r.stdout or ""):
        return "exists"
    print(f"  ! label {name}: {r.stderr}", file=sys.stderr)
    return "error"


def list_milestones(token):
    r = run(
        ["gh", "api", f"repos/{REPO}/milestones?state=all&per_page=100"],
        token,
    )
    return json.loads(r.stdout)


def ensure_milestone(token, title, description, existing):
    for m in existing:
        if m["title"] == title:
            return m["number"]
    r = run(
        ["gh", "api", "--method", "POST", f"repos/{REPO}/milestones",
         "-f", f"title={title}",
         "-f", f"description={description}",
         "-f", "state=open"],
        token,
    )
    n = json.loads(r.stdout)["number"]
    print(f"  + milestone #{n} {title}")
    return n


def search_issue_by_title(token, title):
    """Return (number, id) of the first open or closed issue with this exact
    title, else (None, None)."""
    # gh search issues is rate-limited; use repo-scoped list with state=all.
    # Page through up to 1000.
    page = 1
    while page <= 10:
        r = run(
            ["gh", "api",
             f"repos/{REPO}/issues?state=all&per_page=100&page={page}"],
            token,
        )
        items = json.loads(r.stdout)
        if not items:
            return None, None
        for item in items:
            if "pull_request" in item:
                continue
            if item["title"] == title:
                return item["number"], item["id"]
        if len(items) < 100:
            return None, None
        page += 1
    return None, None


def epic_body(epic, milestone_title):
    return (
        f"**Source:** GOALS.md §\"Epic {epic['id']} — "
        f"{epic['title'].split(' ', 2)[2]}\"\n"
        f"**Milestone:** {milestone_title}\n\n"
        f"> {epic['preamble']}\n\n"
        f"## Sub-issues\n\n"
        f"GitHub renders the sub-issue tree below this issue body.\n\n"
        f"## Risk\n\n{epic['risk_note']}\n\n"
        f"---\n\n"
        f"See [GOALS.md](../blob/main/GOALS.md) for the full roadmap.\n"
    )


def sub_body(epic, milestone_title, epic_number, sub_title):
    return (
        f"**Parent epic:** #{epic_number}\n"
        f"**Source:** GOALS.md §\"Epic {epic['id']}\"\n"
        f"**Milestone:** {milestone_title}\n\n"
        f"{sub_title}\n"
    )


def create_issue(token, title, body, labels, milestone_title):
    args = ["gh", "issue", "create", "-R", REPO,
            "--title", title, "--body", body,
            "--milestone", milestone_title]
    for l in labels:
        args += ["--label", l]
    r = run(args, token)
    url = r.stdout.strip().splitlines()[-1]
    number = int(url.rsplit("/", 1)[1])
    rid = run(
        ["gh", "api", f"repos/{REPO}/issues/{number}", "--jq", ".id"],
        token,
    )
    return number, int(rid.stdout.strip()), url


def link_sub_issue(token, parent_number, sub_id):
    r = run(
        ["gh", "api", "--method", "POST",
         f"repos/{REPO}/issues/{parent_number}/sub_issues",
         "-F", f"sub_issue_id={sub_id}"],
        token, check=False,
    )
    if r.returncode == 0:
        return True
    # Already linked? GitHub returns 422 with "already a sub-issue".
    body = (r.stderr or "") + (r.stdout or "")
    if "already" in body.lower() or "sub_issue" in body.lower():
        return True
    print(f"  ! link parent={parent_number} sub_id={sub_id}: {body}",
          file=sys.stderr)
    return False


def load_manifest():
    if MANIFEST.exists():
        return json.loads(MANIFEST.read_text())
    return {
        "labels": [],
        "milestones": {},   # title -> number
        "epics": {},        # epic_id -> {number,id,subs:[{title,number,id,linked}]}
    }


def save_manifest(m):
    MANIFEST.write_text(json.dumps(m, indent=2))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--epics", default="all",
                    help="comma-list of epic ids (e.g. v0.1-1,v0.1-2) or 'all'")
    ap.add_argument("--skip-labels", action="store_true")
    ap.add_argument("--skip-milestones", action="store_true")
    args = ap.parse_args()

    target = (None if args.epics == "all"
              else set(s.strip() for s in args.epics.split(",")))

    ARTIFACTS.mkdir(parents=True, exist_ok=True)
    manifest = load_manifest()

    token = gh_token()
    print(f"Using polliard token. Repo: {REPO}\n")

    # Labels
    if not args.skip_labels:
        print(f"Labels ({len(LABELS)}):")
        for name, color, desc in LABELS:
            ensure_label(token, name, color, desc)
            if name not in manifest["labels"]:
                manifest["labels"].append(name)
        save_manifest(manifest)
        print()

    # Milestones
    if not args.skip_milestones:
        print(f"Milestones ({len(MILESTONES)}):")
        existing = list_milestones(token)
        for title, desc in MILESTONES:
            num = ensure_milestone(token, title, desc, existing)
            manifest["milestones"][title] = num
        save_manifest(manifest)
        print()

    # Epics + sub-issues
    print("Epics:")
    for epic in EPICS:
        if target and epic["id"] not in target:
            continue
        ms_title = MILESTONES[epic["milestone_idx"]][0]
        ms_number = manifest["milestones"].get(ms_title)
        if ms_number is None:
            print(f"  ! no milestone for {epic['id']}", file=sys.stderr)
            continue

        # epic itself
        stored = manifest["epics"].get(epic["id"])
        if stored and stored.get("number"):
            epic_number, epic_id = stored["number"], stored["id"]
            print(f"  = epic {epic['id']} #{epic_number} (cached)")
        else:
            existing_n, existing_i = search_issue_by_title(token, epic["title"])
            if existing_n:
                epic_number, epic_id = existing_n, existing_i
                print(f"  = epic {epic['id']} #{epic_number} (found existing)")
            else:
                epic_number, epic_id, url = create_issue(
                    token, epic["title"], epic_body(epic, ms_title),
                    epic["labels"], ms_title,
                )
                print(f"  + epic {epic['id']} #{epic_number} {url}")
            manifest["epics"][epic["id"]] = {
                "number": epic_number, "id": epic_id, "subs": [],
            }
            save_manifest(manifest)

        # sub-issues
        sub_titles_seen = {s["title"]: s for s in
                           manifest["epics"][epic["id"]].get("subs", [])}
        for raw in epic["subs"]:
            sub_title = f"{epic['id']}: {raw}"
            cached = sub_titles_seen.get(sub_title)
            if cached and cached.get("linked"):
                continue
            if cached and cached.get("number"):
                sub_number, sub_id = cached["number"], cached["id"]
            else:
                existing_n, existing_i = search_issue_by_title(token, sub_title)
                if existing_n:
                    sub_number, sub_id = existing_n, existing_i
                else:
                    sub_number, sub_id, _ = create_issue(
                        token, sub_title,
                        sub_body(epic, ms_title, epic_number, raw),
                        epic["sub_labels"], ms_title,
                    )
                    print(f"      + sub #{sub_number} {sub_title[:70]}…")
            linked = link_sub_issue(token, epic_number, sub_id)
            entry = {"title": sub_title, "number": sub_number,
                     "id": sub_id, "linked": linked}
            # upsert
            subs = manifest["epics"][epic["id"]]["subs"]
            replaced = False
            for i, s in enumerate(subs):
                if s["title"] == sub_title:
                    subs[i] = entry
                    replaced = True
                    break
            if not replaced:
                subs.append(entry)
            save_manifest(manifest)
            time.sleep(0.15)  # gentle pacing

    print()
    print(f"Manifest: {MANIFEST}")
    total_epics = sum(1 for e in EPICS
                      if (not target or e["id"] in target))
    total_subs = sum(len(e["subs"]) for e in EPICS
                     if (not target or e["id"] in target))
    print(f"Targeted: {total_epics} epics, {total_subs} sub-issues.")


if __name__ == "__main__":
    main()
