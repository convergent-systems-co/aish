---
title: export(1)
parent: Manual pages
permalink: /man/export/
---

# export(1)

## NAME

**export** — bind an environment variable in the live shell session.

## SYNOPSIS

```
export NAME=VALUE
```

## DESCRIPTION

Sets the environment variable `NAME` to `VALUE` in the running
shell. Child processes spawned afterwards inherit the new binding.

`VALUE` may be surrounded by matching single or double quotes;
quotes are stripped before assignment. Variable expansion inside
quotes is **not** performed by `export` itself — the dispatcher
expands `$VAR` and `$(cmd)` on the line *before* `export` sees it.

Multi-assignment forms (`export A=1 B=2`) and the bare
mark-for-export form (`export NAME`) are **not** supported in this
build. Use repeated `export NAME=VALUE` lines instead.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Variable bound. |
| 1 | Malformed input — missing `=`, etc. |

## EXAMPLES

```
export CS_API_KEY=cs_...
export EDITOR="nvim"
export PATH=$HOME/bin:$PATH
```

## SEE ALSO

[set(1)](../set/), [unset(1)](../unset/), [source(1)](../source/),
[Environment](../../environment/).
