---
title: history(1)
parent: Manual pages
permalink: /man/history/
---

# history(1)

## NAME

**history** — query and manage the shell event log.

## SYNOPSIS

```
history
history list [N]
history show <id>
history search <query>
history purge --before <ts>
history checkpoint <name>
history rollback <name>
```

## DESCRIPTION

The history engine records every command the shell dispatches —
not just for `Up`-arrow recall, but as a signed event log
(Ed25519) with snapshot pointers for reversibility. Each event
carries the command line, timestamp, exit code, working directory,
active persona, and (for destructive ops) the snapshot manifest.

Subcommands:

`history` (bare) or `history list [N]`
: List the `N` most-recent events (default 10). Output columns: id,
  timestamp, persona, exit code, command.

`history show <id>`
: Print the full event payload as JSON.

`history search <query>`
: SQLite FTS5 / substring search across command lines. Quoted
  phrases match exactly; bare words match any.

`history purge --before <ts>`
: Permanently delete events older than `<ts>` (RFC 3339).

`history checkpoint <name>`
: Write a named checkpoint event — a marker the user can later
  roll back to.

`history rollback <name>`
: Roll back to the checkpoint named `<name>`. Replays every
  reversible event between now and the checkpoint in reverse
  order. Non-reversible events (read-only commands) are skipped.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Subcommand succeeded. |
| 1 | History engine unavailable, or subcommand failed. |
| 2 | Usage error / unknown subcommand. |

## EXAMPLES

```
history
history list 25
history search 'rm -rf'
history show 42
history checkpoint pre-refactor
history rollback pre-refactor
```

## SEE ALSO

[undo(1)](../undo/), [restore(1)](../restore/),
[Files](../../files/) for `history.db` location.
