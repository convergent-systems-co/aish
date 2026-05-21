---
title: set(1)
parent: Manual pages
permalink: /man/set/
---

# set(1)

## NAME

**set** — list or bind session environment variables.

## SYNOPSIS

```
set
set NAME=VALUE [NAME=VALUE ...]
```

## DESCRIPTION

With no arguments, `set` lists every `NAME=VALUE` binding in the
session environment, sorted alphabetically (POSIX "list variables"
semantics).

With one or more `NAME=VALUE` arguments, `set` binds each in the
session env. Unlike [export(1)](../export/), `set` does not mark
the variable for child-process inheritance — but because the
current build passes the full env table to every child anyway, the
distinction is informational today. The mark-for-export flag
becomes load-bearing in a future epic.

Option flags (`set -e`, `set -o`, `set -u`, `set -x`, …) are **not
supported** in this build. Invoking `set -*` prints a warning to
stderr and exits 2; the option does not silently no-op.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Listing or binding succeeded. |
| 2 | Option flag passed (`-e`, `-o`, …) — not yet supported. |

## EXAMPLES

```
set                          # list all session env entries
set FOO=bar
set DEBUG=1 VERBOSE=yes
```

## SEE ALSO

[export(1)](../export/), [unset(1)](../unset/), [env(1)](../env/),
[source(1)](../source/).
