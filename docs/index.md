---
title: Home
layout: default
nav_order: 1
description: "aish — an AI-native, OS-insensitive, reversible shell. Manual pages."
permalink: /
---

# aish — manual
{: .fs-9 }

An AI-native, OS-insensitive, reversible shell. POSIX commands stay
on the hot path; natural-language intents flow through a local SQLite
intent cache and a pluggable LLM gateway; destructive operations are
snapshotted and reversible via `undo`.
{: .fs-5 .fw-300 }

[Get started](#getting-started){: .btn .btn-primary .fs-5 .mb-4 .mb-md-0 .mr-2 }
[View on GitHub](https://github.com/convergent-systems-co/aish){: .btn .fs-5 .mb-4 .mb-md-0 }

---

## What is aish?

`aish` ("AI shell") is a shell binary in the same family as `bash`,
`zsh`, and `fish` — same dispatch model, same job-control discipline,
same `$PATH` lookup. It differs in two ways:

1. **AI-native dispatch.** Lines that don't resolve to a known
   built-in or `$PATH` binary are treated as **intents** in natural
   language. The shell consults a local SQLite cache; on miss, a
   pluggable inference plugin (default: cloud LLM) compiles the
   intent into a shell invocation and the result is cached for the
   whole machine forever after.
2. **Reversibility.** Destructive operations (`rm`, `mv`, `dd`, …)
   are intercepted, snapshotted into `~/.aish/snapshots/`, and
   reversible via the `undo` built-in.

Everything else — pipes, redirects, env vars, process groups, job
control — behaves the way a POSIX shell user expects.

## Getting started

```
make build
./shell/dist/aish
```

The shell creates `~/.aish/` on first run. See the [Files][files]
manpage for the full layout. To enable cloud inference set
`CS_API_KEY=cs_...` in the environment before launching.

## Dispatch tier order

```
1. built-in           cd, export, alias, set, unset, source, exec, logout,
                      jobs, fg, bg, cache, theme, persona, secret,
                      identity, history, undo, restore, stats, plugin,
                      community, install, service, process, env, network,
                      run, explain, migrate
2. known binary       first token resolves on $PATH → parser + exec
3. cache hit          (intent_hash, os) → cached invocation → exec
4. plugin infer       aish-inference-cloud → invocation → cache → exec
5. legacy fallback    parser + exec (exit 127 for unknown command)
```

## Built-in manpages

The full set lives under [`man/`][man]. The most-used pages:

- [aish(1)][aish] — top-level invocation, options, exit codes.
- [cd(1)][cd], [export(1)][export], [set(1)][set], [unset(1)][unset],
  [alias(1)][alias], [source(1)][source] — environment + sessions.
- [jobs(1)][jobs], [fg(1)][fg], [bg(1)][bg] — POSIX job control.
- [undo(1)][undo], [restore(1)][restore], [history(1)][history] —
  reversibility.
- [cache(1)][cache], [stats(1)][stats], [plugin(1)][plugin],
  [community(1)][community] — AI dispatch + telemetry.
- [persona(1)][persona], [secret(1)][secret], [identity(1)][identity]
  — multi-identity workflows.
- [theme(1)][theme] — prompt branding.
- [run(1)][run], [explain(1)][explain], [migrate(1)][migrate] —
  bash/zsh/fish script translation.
- [install(1)][install], [service(1)][service], [process(1)][process],
  [env(1)][env], [network(1)][network] — Windows-native built-ins
  (v1.0-2).
- [logout(1)][logout] — session control.

See also: [Files][files] (state on disk), [Environment][env-vars]
(variables aish reads), [Signals][signals] (TTY routing rules).

[man]: ./man/
[aish]: ./man/aish/
[cd]: ./man/cd/
[export]: ./man/export/
[set]: ./man/set/
[unset]: ./man/unset/
[alias]: ./man/alias/
[source]: ./man/source/
[jobs]: ./man/jobs/
[fg]: ./man/fg/
[bg]: ./man/bg/
[undo]: ./man/undo/
[restore]: ./man/restore/
[history]: ./man/history/
[cache]: ./man/cache/
[stats]: ./man/stats/
[plugin]: ./man/plugin/
[community]: ./man/community/
[persona]: ./man/persona/
[secret]: ./man/secret/
[identity]: ./man/identity/
[theme]: ./man/theme/
[run]: ./man/run/
[explain]: ./man/explain/
[migrate]: ./man/migrate/
[install]: ./man/install/
[service]: ./man/service/
[process]: ./man/process/
[env]: ./man/env/
[network]: ./man/network/
[logout]: ./man/logout/
[files]: ./files/
[env-vars]: ./environment/
[signals]: ./signals/
