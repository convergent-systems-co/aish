---
title: env(1)
parent: Manual pages
permalink: /man/env/
---

# env(1)

## NAME

**env** — list, get, set, or unset environment variables.

## SYNOPSIS

```
env [list]
env get <name>
env set <name> <value>
env unset <name>
```

## DESCRIPTION

`env` and `env list` (equivalent)
: Print every `NAME=VALUE` binding in the session env, sorted.

`env get <name>`
: Print the value of `<name>` to stdout. Unset variable exits 1.

`env set <name> <value>`
: Bind `<name>` to `<value>` in the session env. Same effect as
  [export(1)](../export/) or `set NAME=VALUE`.

`env unset <name>`
: Remove the binding. Same as [unset(1)](../unset/).

The cross-cutting MVP is **in-process env only** — changes are
visible to subsequent commands in this session but are **not**
written to `HKCU\Environment` (Windows) or shell RC files. A
future `--persist` flag will do that; for now, edit
[aishrc](../source/) or use the OS env editor.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Subcommand succeeded. |
| 1 | Variable not found (`get`). |
| 2 | Missing or unknown subcommand. |

## EXAMPLES

```
env
env get PATH
env set EDITOR nvim
env unset DEBUG
```

## SEE ALSO

[export(1)](../export/), [set(1)](../set/), [unset(1)](../unset/),
[source(1)](../source/).
