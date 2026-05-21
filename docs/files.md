---
title: Files
layout: default
nav_order: 3
permalink: /files/
---

# Files

aish keeps every piece of persistent state under `~/.aish/`. The
layout is stable; built-ins create subdirectories on first use.

```
~/.aish/
├── config.toml              # active theme, active persona, login defaults
├── cache.db                 # SQLite intent cache (L1)
├── history.db               # SQLite signed event log
├── snapshots/               # pre-destructive-op snapshots for undo
├── themes/                  # synced theme bundles from theme-atoms.com
├── sessions/                # per-session telemetry rollups
├── telemetry.toml           # consent flags for aggregate reporting
├── cost-log.jsonl           # per-inference cost log
├── vault/
│   └── vault.json           # encrypted secret store (AES-GCM + Argon2id)
├── identities/              # per-identity profile directories
│   └── <name>/
├── identity.toml            # active identity pointer
├── personas/                # user persona TOML overrides
├── plugins/
│   └── registry.json        # installed plugin manifest
├── aishrc.toml              # login RC (TOML schema)
└── community-contribute.jsonl  # opt-in offline contribution queue
```

## System paths

`/etc/aish/aishrc`
: System-wide login RC. Sourced **before** the per-user RC in
  login mode. Same TOML schema as the per-user file.

## RC schema

The login RC at `~/.aish/aishrc.toml` accepts the following
sections:

```toml
[shell]
umask = "0022"     # applied at login only (not by `source`)

[env]
EDITOR    = "nvim"
CS_API_KEY = "cs_..."

[aliases]
ll  = "ls -la"
gst = "git status"

[persona]
active = "engineer"

[theme]
active = "nord"
```

`[shell].umask` is honored at login startup only. Sourcing the RC
mid-session with [source(1)](../man/source/) deliberately skips
the umask write.

## State permission expectations

aish creates `~/.aish/` with `0755` and writes regular files with
`0644`. The vault directory and `vault.json` get `0700` and `0600`
respectively. Permission failures at startup degrade gracefully —
the built-ins that depend on the affected file print a clear
"not available" message and the shell stays usable.
