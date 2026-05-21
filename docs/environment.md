---
title: Environment
layout: default
nav_order: 4
permalink: /environment/
---

# Environment variables

This page lists environment variables aish **reads** from the
process env. Variables aish **writes** for child processes are
documented under [export(1)](../man/export/) and
[set(1)](../man/set/).

## Cloud inference

`CS_API_KEY`
: Convergent Systems gateway bearer token. Mint via the
  `core-infra` CLI. When set, the inference plugin is spawned at
  startup and natural-language intents are compiled by the cloud
  model.

`ANTHROPIC_API_KEY`
: Legacy alias for `CS_API_KEY`. Read for backward compatibility;
  prefer `CS_API_KEY` in new configs.

`ANTHROPIC_BASE_URL`
: Override the inference plugin's gateway URL. Default:
  `https://api.convergent-systems.co/llm/v1`.

`AISH_INFERENCE_PLUGIN`
: Absolute path to a plugin binary. Overrides both `$PATH`
  resolution and the v0.3-2 plugin registry. Use to point at a
  local Ollama bridge or a custom plugin during development.

## Login + RC

`HOME` (POSIX) / `USERPROFILE` (Windows)
: Resolved by `cd ~` and used to find `~/.aish/`. aish prefers
  `HOME` when both are set so the same code path works
  cross-platform.

`AISH_VERSION`
: Written by `aish --login` startup from the build-time version
  string. Read-only from the shell's perspective — set it via
  `Options.Version` in callers of `NewWithOptions`.

`PATH`
: Standard. aish pivots `os.Setenv("PATH", ...)` through the
  shell env before each `LookPath` so an in-session
  `export PATH=...` is honored.

## Editor / pager

aish has no opinion. Child processes inherit `EDITOR`, `PAGER`,
`VISUAL` etc. from the live env table; commands like
[secret(1)](../man/secret/) `set` use the OS keychain for input
rather than spawning an editor.

## PTY routing

`AISH_PTY_WS`
: Test-only. `rows:cols` initial window size for the PTY when
  aish dispatches an interactive child. Ignored in production
  builds outside the test harness; the shell mirrors the parent
  TTY's actual size when this is unset.

## Telemetry

aish does **not** read telemetry env vars at the shell layer.
Telemetry consent is stored in `~/.aish/telemetry.toml`.

## Provenance

Every var read by aish is grepped for under `shell/internal/`
in the source tree — this page is generated from the actual
read sites, not from a separate list that could drift. When in
doubt, `grep -r 'e.Get("\|os.Getenv("' shell/internal/`.
