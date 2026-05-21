---
title: undo(1)
parent: Manual pages
permalink: /man/undo/
---

# undo(1)

## NAME

**undo** — reverse the most recent destructive operation.

## SYNOPSIS

```
undo
```

## DESCRIPTION

Reads the most-recent restorable event from `~/.aish/history.db`
and replays each snapshotted path back to its original location
from `~/.aish/snapshots/`.

Destructive operations the shell intercepts include `rm`, `mv`,
`dd`, `chmod`, `chown`, and others tracked by the history
interceptor. Before the operation runs, the affected paths are
copied to a content-addressed snapshot store. `undo` walks the
snapshot manifest and restores each file in place.

`undo N` (revert the last N operations) is described in `GOALS.md`
and reserved for a future release. Passing an argument today is
rejected with a clear message rather than silently performing a
single-step undo.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | All snapshotted paths restored. |
| 1 | No restorable event, or restore failed (snapshot rot, conflict, …). |
| 2 | Argument passed (not yet supported). |

## EXAMPLES

```
$ rm -rf important/
$ undo
restored: important/
restored: important/data.json
restored: important/notes.md
```

## NOTES

If the history engine failed to open at shell startup
(`~/.aish` unwritable, disk full, etc.), `undo` prints
"history not available" and exits 1. The shell stays usable but
destructive operations during that session run unobserved
(POSIX-default behavior).

## SEE ALSO

[restore(1)](../restore/), [history(1)](../history/).
