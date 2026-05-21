---
title: restore(1)
parent: Manual pages
permalink: /man/restore/
---

# restore(1)

## NAME

**restore** — restore a specific path from its most recent snapshot.

## SYNOPSIS

```
restore <path>
```

## DESCRIPTION

Looks up the most recent snapshot of `<path>` in the history store
and writes its content back to the original location. Unlike
[undo(1)](../undo/), `restore` is **path-scoped** rather than
event-scoped — it does not walk a full operation manifest, so it's
the right tool when only one file was lost from a larger change.

`<path>` is resolved against the shell's current working directory
if not absolute.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Path restored. |
| 1 | No snapshot found, or restore failed. |
| 2 | Missing argument. |

## EXAMPLES

```
$ restore ./important/data.json
restored: important/data.json
```

## SEE ALSO

[undo(1)](../undo/), [history(1)](../history/).
