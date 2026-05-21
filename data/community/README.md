# aish Community Cache (L3)

The L3 community cache is a **read-only, Ed25519-signed SQLite file**
distributed alongside an aish release. It pre-populates the v0.1-2 L1
cache for fresh installs so the first time a user types `list files`
the answer is already there.

## Files

```
data/community/
├── README.md            ← you are here
├── seed.jsonl           ← curated (intent, os, invocation) rows
├── trust-anchors.toml   ← human-readable copy of compiled-in anchors
└── (generated at build time:)
    └── dist/
        ├── manifest.json
        ├── bundle.db
        └── trust-anchors.toml
```

The seed JSONL is the source of truth for what ships. `make bundle`
reads it, builds `bundle.db` via the schema in
`shell/internal/cache/community/lookup.go`, hashes the file with
SHA-256, signs the hash with the dev Ed25519 key, and emits a
manifest.

## Seed entry shape

Each line in `seed.jsonl` is a single JSON object:

```json
{"intent": "list files", "os": "darwin", "invocation": "ls -la"}
```

- `intent` — natural-language phrasing the user might type.
  Normalised to `strings.ToLower(strings.TrimSpace(intent))` at sign
  time. Curators MUST verify the row is reachable by typing the
  intent verbatim into aish.
- `os` — `darwin`, `linux`, or `windows`. One row per OS per intent
  is the norm because the compiled invocation diverges per platform.
- `invocation` — the literal shell command that satisfies the
  intent. MUST NOT contain destructive side effects beyond what the
  intent unambiguously requests. See "Curation policy" below.

## Curation policy

The signing key is the trust boundary at the binary level. The
curation policy is the trust boundary at the *content* level. Both
must hold.

1. **Truthful.** The invocation does exactly what the intent says,
   no more, no less. No telemetry beacons. No "helpful" extra flags.
2. **Reversible by default.** Prefer non-destructive forms. `ls -la`
   over `rm -rf .`. When the intent is genuinely destructive (`delete
   logs`), the invocation MUST be the minimum destructive form a
   reasonable POSIX user would expect (`rm logs/*` not `rm -rf /var/log`).
3. **No PII, no host-specific paths.** No usernames, no hostnames,
   no absolute paths to a curator's home directory.
4. **Vetted by two reviewers.** Each PR adding seed rows requires a
   second approver besides the author.
5. **Platform-correct.** The Windows row uses PowerShell idioms, not
   POSIX. The Darwin row prefers BSD flags where they diverge from
   GNU.

## Why a signing key, not just a hash?

A hash protects against accidental corruption. A signature protects
against an attacker who can substitute a bundle in transit (CDN
breach, repo mirror tampering) and recompute a matching hash.

The compiled-in trust anchors in `shell/internal/cache/community/trust.go`
are the trust root. Rotating a compromised key requires a new aish
release — which is the right blast radius for a trust root.

## Dev signing key

The dev key is deterministic, seeded from the string
`aish-dev-community-bundle-signer` in
`shell/internal/cache/community/trust.go`. It exists so the test
suite + `make bundle` produce a runtime-verifiable bundle without an
out-of-band key distribution step.

**The dev key is publicly known and MUST NOT sign bundles distributed
to real users.** Production anchors land via a separate PR alongside
the actual key-management process (hardware-backed signer, rotation
policy, audit log).

## Stretch goal: 1000+ intents

The MVP ships ≥100 curated rows across darwin/linux/windows. The
1000+ goal in GOALS.md §"Epic v0.2-3" is deliberately a stretch — the
*format* is the load-bearing artifact for v0.2-3, and the seed grows
in follow-up PRs once the curation policy is exercised on the first
hundred rows.
