---
title: unset(1)
parent: Manual pages
permalink: /man/unset/
---

# unset(1)

## NAME

**unset** — remove environment-variable bindings.

## SYNOPSIS

```
unset NAME [NAME ...]
```

## DESCRIPTION

Removes each `NAME` from the session environment. Unsetting an
unbound name is a no-op (matches bash). Each name is processed
independently; one failure does not abort the others.

`unset -f` (functions) and `unset -v` (explicit variable mode) are
not supported in this build — aish has no shell functions yet, so
the default "unset variable" behavior is all that applies.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | All names processed (including unbound names). |
| 2 | No names given, or an empty name argument. |

## EXAMPLES

```
unset DEBUG
unset CS_API_KEY ANTHROPIC_API_KEY
```

## SEE ALSO

[set(1)](../set/), [export(1)](../export/).
