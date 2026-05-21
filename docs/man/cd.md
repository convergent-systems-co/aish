---
title: cd(1)
parent: Manual pages
permalink: /man/cd/
---

# cd(1)

## NAME

**cd** — change the shell's working directory.

## SYNOPSIS

```
cd [path]
```

## DESCRIPTION

Changes the shell's current working directory to `path`. `path` may
be absolute or relative; relative paths resolve against the current
working directory. A leading `~` (alone or as `~/...`) expands to
`$HOME` on POSIX or `$USERPROFILE` on Windows.

A bare `cd` with no argument is equivalent to `cd ~`.

The shell calls `os.Chdir` so child processes inherit the new
working directory. The stored cwd is re-read via `os.Getwd` to
reflect symlink resolution consistent with what child processes
will see.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Directory changed. |
| 1 | `path` does not exist, is not a directory, or `$HOME` is unset for bare `cd`. |

## EXAMPLES

```
cd /tmp
cd ../sibling-dir
cd ~/.aish
cd            # → $HOME
```

## SEE ALSO

[aish(1)](../aish/), [export(1)](../export/).
