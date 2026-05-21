---
title: Signals
layout: default
nav_order: 5
permalink: /signals/
---

# Signals and job control

aish implements **POSIX-style job control** on Unix. This page
documents which signals reach which process, who handles them,
and the failure modes the design protects against.

## Signal routing summary

| Signal       | Where it goes              | Why |
|--------------|----------------------------|-----|
| `SIGINT`     | Foreground process group   | The kernel routes Ctrl-C via the controlling-TTY's foreground pgrp. aish *ignores* SIGINT for itself so the REPL survives. |
| `SIGQUIT`    | Foreground process group   | Same routing as SIGINT. The shell ignores it for itself. |
| `SIGTSTP`    | Foreground process group   | Ctrl-Z. Foreground job is stopped; the shell reclaims the TTY at the next prompt. |
| `SIGTTIN`/`SIGTTOU` | Ignored by the shell | A backgrounded job that touches the TTY would suspend its own pgrp; aish ignores these for itself so the REPL stays usable. |
| `SIGCHLD`    | Package-global reaper      | Singleton goroutine drains `wait4(-1, WNOHANG|WUNTRACED|WCONTINUED)` and updates the JobTable. |
| `SIGWINCH`   | PTY children               | Forwarded so PTY-allocated children re-render on terminal resize. |

## Why the global reaper is a singleton

The kernel emits SIGCHLD process-wide. `wait4(-1, ...)` races
across goroutines for the same kernel wait status — only one
goroutine "wins" each event. Earlier prototypes spawned a reaper
per `Shell` instance, which silently dropped child events when
more than one Shell coexisted in the same process (notably during
`go test`, where many `Shell` instances share the parent pid).

The package-global singleton in `shell/internal/jobs/signals_unix.go`
fixes this by registering every Shell's JobTable into one
`globalReaper` and dispatching reaped pids to whichever table owns
them.

## Process-group setup

Every child started by the shell on Unix is placed in its own
process group via `SysProcAttr.Setpgid: true, Pgid: 0`. Stages
after the first in a pipeline join the first stage's pgid so the
whole pipeline receives Ctrl-C and Ctrl-Z **as a unit**.

`Setsid` is intentionally *not* used. Setsid would detach the
child from the controlling TTY, and we want Ctrl-C / Ctrl-Z
routing to the foreground job. Setpgid is the bash/zsh-compatible
approach.

## TTY hand-off

When [fg(1)](../man/fg/) brings a job to the foreground, the
shell uses `ioctl(fd, TIOCSPGRP, &pgid)` (POSIX `tcsetpgrp`) to
hand the controlling terminal to the job's pgrp. On return — exit
or stop — the shell calls `TIOCSPGRP` back to its own pgrp.
`defer` guarantees the hand-back fires on every path including
panic.

Go's stdlib does not expose `tcsetpgrp`;
`golang.org/x/sys/unix.IoctlSetInt` is the seam.

## Windows

POSIX-style job control with process groups, `tcsetpgrp`, and
`SIGCHLD` does **not** map to Windows. JobObject-based job
control is its own v1.0 follow-up. For now,
[jobs(1)](../man/jobs/), [fg(1)](../man/fg/), and
[bg(1)](../man/bg/) print "job control not supported on Windows"
and exit 1.
