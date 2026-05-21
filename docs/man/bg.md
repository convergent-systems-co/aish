---
title: bg(1)
parent: Manual pages
permalink: /man/bg/
---

# bg(1)

## NAME

**bg** — continue a stopped job in the background.

## SYNOPSIS

```
bg [%n | %+ | %- | n]
```

## DESCRIPTION

Sends `SIGCONT` to the target job's process group and flips the
job's status from `Stopped` to `Running`. Selector forms match
[fg(1)](../fg/).

If the target job is already running, `bg` is a no-op (matches bash:
SIGCONT to a running process is harmless). If the target is already
gone (ESRCH from `kill(-pgid, SIGCONT)`), `bg` treats the job as
`Done` and the next prompt prints the Done notice.

After SIGCONT, `bg` prints `[<id>]+ <command> &` to stdout — the
same shape bash uses.

POSIX-style job control is **Unix-only**. On Windows, `bg` prints
"job control not supported on Windows" and exits 1.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Signal sent (or job already gone). |
| 1 | No such job; or job control unavailable. |
| 2 | Usage error. |

## EXAMPLES

```
$ sleep 30
^Z
[1]+  Stopped    sleep 30
$ bg %1
[1]+ sleep 30 &
$ jobs
[1]+  Running    sleep 30
```

## NOTES

`Ctrl-Z` is intercepted by the shell — the kernel routes `SIGTSTP`
to the foreground process group via the controlling TTY. The shell
ignores `SIGTSTP` for itself so only the foreground job is
suspended, not the REPL.

## SEE ALSO

[jobs(1)](../jobs/), [fg(1)](../fg/), [Signals](../../signals/).
