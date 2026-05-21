---
title: stats(1)
parent: Manual pages
permalink: /man/stats/
---

# stats(1)

## NAME

**stats** — render the local telemetry dashboard.

## SYNOPSIS

```
stats [N]
```

## DESCRIPTION

Prints a table of the `N` most-recent session rollups (default
`10`) from `~/.aish/sessions/`, plus a footer with cumulative
hit-rate and total USD spent across the window. The in-flight
session (the one running this command) is rendered as the first
row so the user sees their current activity before the shell exits.

Columns include session id, start time, command count, cache hits,
inference calls, cost (USD), and exit code distribution.

Telemetry is opt-in for aggregate reporting (see
`~/.aish/telemetry.toml`); the **local** dashboard always works as
long as the recorder opened successfully at shell startup.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Dashboard rendered (possibly empty). |
| 1 | Telemetry recorder unavailable. |
| 2 | Argument is not a positive integer. |

## EXAMPLES

```
$ stats
$ stats 30
```

## FILES

`~/.aish/sessions/` — per-session rollups.<br>
`~/.aish/telemetry.toml` — consent flags for aggregate reporting.<br>
`~/.aish/cost-log.jsonl` — per-inference cost log.

## SEE ALSO

[cache(1)](../cache/).
