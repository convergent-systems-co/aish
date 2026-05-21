---
title: install(1)
parent: Manual pages
permalink: /man/install/
---

# install(1)

## NAME

**install** — install a package via the host package manager.

## SYNOPSIS

```
install <pkg>
```

## DESCRIPTION

Windows MVP: delegates to `winget install --silent <pkg>`. The
`--silent` flag survives scripted invocations by suppressing
interactive prompts.

On macOS and Linux, `install` exits 2 with a clear "not supported
on `<GOOS>`" message. POSIX install verbs (`apt install`,
`brew install`) are reserved for a separate epic.

The built-in does **not** auto-elevate. If `winget` needs admin
privileges, re-run from an elevated shell.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | `winget install` succeeded. |
| 1 | `winget` returned a non-zero status. |
| 2 | Not running on Windows, or missing argument. |

## EXAMPLES

```
install Microsoft.PowerShell
install git.git
```

## SEE ALSO

[service(1)](../service/), [process(1)](../process/),
[network(1)](../network/).
