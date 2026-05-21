---
title: alias(1)
parent: Manual pages
permalink: /man/alias/
---

# alias(1)

## NAME

**alias** — define or list command aliases.

## SYNOPSIS

```
alias
alias NAME
alias NAME=COMMAND
```

## DESCRIPTION

With no arguments, `alias` lists every registered alias in
`alias NAME='COMMAND'` form, sorted by name. With a single `NAME`
argument and no `=`, prints just that alias or exits 1 if `NAME`
is unbound. With `NAME=COMMAND`, registers `NAME → COMMAND` in the
live session.

Aliases declared in the login RC file's `[aliases]` table (see
[source(1)](../source/)) are seeded into the alias map at shell
startup. This built-in writes to the **live** map only; persistence
to the RC file is the user's responsibility — edit the file
directly to make an alias survive restart. (Bash's behavior
matches: `alias` without `-p` doesn't persist.)

Alias expansion runs **after** brace, glob, and command-substitution
expansion and **before** known-binary / cache dispatch. Recursion
is bounded by a 16-step expansion cap so a cycle (`alias x=y; alias
y=x`) cannot loop.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Listing or registration succeeded. |
| 1 | Lookup form: `NAME` is unbound. |

## EXAMPLES

```
alias                            # list all aliases
alias ll='ls -la'                # register
alias ll                         # print just `ll`
alias gst='git status'
```

## NOTES

The simple `\NAME` escape-bypass form bash supports (run the binary
even when an alias of the same name exists) is **not** implemented.
Use the absolute path or `command NAME` workaround instead.

## SEE ALSO

[source(1)](../source/), [set(1)](../set/).
