---
title: jobs(1)
parent: Manual pages
permalink: /man/jobs/
---

# jobs(1)

## NAME

**jobs** — list active background and stopped jobs.

## SYNOPSIS

```
jobs
```

## DESCRIPTION

Prints the contents of the shell's job table — every job started
with a trailing `&`, plus any foreground job that was stopped with
`Ctrl-Z`.

Output format:

```
[<id>]<flag>  <Status>   <command>
```

- `<id>` — shell-local job number (1-based, monotonic until the
  table empties; matches the `%<id>` form accepted by `fg` and
  `bg`).
- `<flag>` — `+` for the **current** job (most-recent), `-` for
  the **previous** job, space for everything else.
- `<Status>` — `Running`, `Stopped`, or `Done`.
- `<command>` — the original command line, minus the trailing `&`.

Bash flag options (`jobs -l`, `jobs -p`) are not supported in this
build; an extra argument exits 2 rather than silently no-op.

POSIX-style job control is **Unix-only**. On Windows, `jobs` prints
"job control not supported on Windows" and exits 1. JobObject-based
equivalents are tracked in the v1.0 roadmap.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Table listed (including empty table — silent stdout). |
| 1 | Job control not available (Windows). |
| 2 | Unexpected argument. |

## EXAMPLES

```
$ sleep 30 &
[1] 24681
$ yes | head -n5 &
[2] 24682
$ jobs
[1]-  Running    sleep 30
[2]+  Running    yes | head -n5
```

## SEE ALSO

[fg(1)](../fg/), [bg(1)](../bg/), [Signals](../../signals/).
