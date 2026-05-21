---
title: logout(1)
parent: Manual pages
permalink: /man/logout/
---

# logout(1)

## NAME

**logout** — terminate a login shell.

## SYNOPSIS

```
logout [n]
```

## DESCRIPTION

In **login mode** (the shell was started with `--login` or argv[0]
begins with `-`), `logout` ends the REPL cleanly. The shell
process exits with status `n` (default `0`).

In **non-login mode**, `logout` is a user error — the same as
typing it in bash from a non-login session. It prints
"logout: not login shell: use `exit`" to stderr and exits 1; the
shell stays running.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Login shell terminated cleanly. |
| 1 | Not a login shell. |
| `n` | Login shell terminated with the user-supplied status. |

## EXAMPLES

Inside a login session:

```
$ logout
```

Exit with a specific status:

```
$ logout 42
```

## SEE ALSO

[aish(1)](../aish/).
