---
title: source(1)
parent: Manual pages
permalink: /man/source/
---

# source(1)

## NAME

**source** — read an RC file into the live shell session.

## SYNOPSIS

```
source <file>
```

## DESCRIPTION

Reads `<file>` and applies its declarations to the running shell.
`source` accepts two formats:

1. **TOML aishrc** — same schema as the login RC at
   `~/.aish/aishrc.toml`. Honored sections:
   - `[env]`: each `key = "value"` is applied to the live env.
   - `[aliases]`: each `key = "value"` is added to the alias map.
   - `[shell].umask` is **intentionally NOT applied** here.
     Interactive `source` should not silently change the process
     umask; the login-time RC pass is where umask belongs.
2. **POSIX-ish env lines** — when the file does not parse as TOML,
   `source` falls back to reading line-by-line and accepting `K=V`
   per line (the dominant `.env` convention). Blank lines and
   lines starting with `#` are treated as comments. Lines
   beginning with `export ` are tolerated (the prefix is stripped).
   Anything else surfaces as a per-line warning on stderr — the
   file is not rejected wholesale.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | File read successfully (or partial apply with warnings). |
| 1 | File missing or unreadable; or no entries applied. |
| 2 | Missing argument. |

## EXAMPLES

```
source ~/.aish/aishrc.toml
source ./project.env
```

## SEE ALSO

[set(1)](../set/), [alias(1)](../alias/), [aish(1)](../aish/) for
how login RCs are sourced automatically, [Files](../../files/) for
the RC layout.
