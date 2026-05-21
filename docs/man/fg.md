---
title: fg(1)
parent: Manual pages
permalink: /man/fg/
---

# fg(1)

## NAME

**fg** — resume a job in the foreground.

## SYNOPSIS

```
fg [%n | %+ | %- | n]
```

## DESCRIPTION

Resumes the job identified by the argument as a foreground job,
giving it the controlling terminal and waiting for it to exit or
stop.

Job selectors:

- `%n` — the job with id `n`.
- `%+` (or bare `fg`) — the current (`+`) job.
- `%-` — the previous (`-`) job.
- `n` — bare numeric id, accepted as a convenience.

When `fg` takes a job to the foreground it:

1. Echoes the command line to stdout (matches bash).
2. Hands the controlling TTY to the job's process group via
   `ioctl(TIOCSPGRP)`. If stdin is not a real terminal (the
   session is piped), this step is silently skipped — the job
   still runs to completion, but `Ctrl-C` and `Ctrl-Z` routing
   is degraded.
3. Sends `SIGCONT` to the job's process group so a stopped job
   resumes.
4. Blocks until the job table reports the job `Done` or
   `Stopped`. The package-global SIGCHLD reaper updates the
   table; `fg` polls every 25 ms.
5. Reclaims the TTY for the shell's own process group on every
   exit path, including panic.

POSIX-style job control is **Unix-only**. On Windows, `fg` prints
"job control not supported on Windows" and exits 1.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Job ran and exited 0. |
| 1 | No such job, signal-send failure, or job control unavailable. |
| 2 | Usage error (more than one argument). |
| 148 | Job was stopped (`Ctrl-Z`) — `128 + SIGTSTP` per POSIX. |
| 128+N | Job was killed by signal `N`. |

## EXAMPLES

```
$ sleep 30 &
[1] 24681
$ fg %1
sleep 30
$ echo $?
0
```

Send the foreground sleep to the background with `Ctrl-Z` then
resume with `bg`:

```
$ sleep 30
^Z
[1]+  Stopped    sleep 30
$ bg %1
[1]+ sleep 30 &
```

## SEE ALSO

[jobs(1)](../jobs/), [bg(1)](../bg/), [Signals](../../signals/).
