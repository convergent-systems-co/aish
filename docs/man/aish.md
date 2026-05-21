---
title: aish(1)
parent: Manual pages
nav_order: 1
permalink: /man/aish/
---

# aish(1)

## NAME

**aish** — AI-native, OS-insensitive, reversible shell.

## SYNOPSIS

```
aish [-l|--login] [-c command] [--version] [--help]
```

## DESCRIPTION

`aish` is an interactive command shell with five dispatch tiers. A
line of input is matched, in order, against:

1. **Built-ins.** In-process commands listed under
   [Manual pages](../). They handle their own argument parsing and
   produce their own exit codes.
2. **Known binaries.** The first whitespace-separated token is
   resolved against `$PATH`. On a hit, the line is tokenized by the
   parser and executed via `os/exec`.
3. **Intent cache.** The full line is hashed and looked up in
   `~/.aish/cache.db`. A hit returns a cached shell invocation that
   is then run as in tier 2.
4. **Plugin inference.** On a cache miss, the line is forwarded to
   the active inference plugin (default: `aish-inference-cloud`)
   which compiles it into a shell invocation. The result is written
   back to the cache and executed.
5. **Legacy fallback.** When no plugin is configured (no API key
   set), an unrecognised command yields exit 127, matching the
   familiar POSIX `command not found` behavior.

The shell preserves the user's `$PATH`, environment, current working
directory, and exit code (`$?`) across iterations. It also keeps a
queryable history at `~/.aish/history.db`, signed snapshots of files
before destructive operations at `~/.aish/snapshots/`, and an
encrypted secret vault at `~/.aish/vault/`.

## OPTIONS

`-l`, `--login`
: Run as a **login shell**. The shell sources `/etc/aish/aishrc` and
  `$HOME/.aish/aishrc.toml` before opening the cache, history, and
  telemetry seams, applies POSIX login env defaults (`$PATH`,
  `$AISH_VERSION`), and binds `logout` to terminate the REPL.

`-c command`
: Execute `command` and exit, without entering the REPL. The
  command runs through the full dispatch tier, so built-ins and
  intent cache work the same as in interactive mode.

`--version`
: Print the version string baked in at build time, then exit 0.

`--help`
: Print short usage, then exit 0.

## INTERACTIVE BEHAVIOR

When `aish`'s standard input is a TTY, it allocates a line editor
with multi-byte input, history navigation, tab completion, and
syntax-highlighted prompt segments driven by the active theme (see
[theme(1)](../theme/)).

When standard input is a pipe or file (e.g. a shell script piped
in), the shell falls back to a byte-by-byte stream reader. This
mode reads exactly one byte per `Read` to preserve input bytes for
downstream pipeline children (a fix for issue #167 — `cat` reading
piped lines after the shell's own prompt would otherwise lose
data).

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0    | The last command in the session exited successfully. |
| 1    | The last command exited with a generic error. |
| 2    | Parse error or usage error from a built-in. |
| 127  | Command not found (legacy fallback when no plugin). |
| 130  | The line editor received `Ctrl-C` and discarded the line. |
| 128+N | The last command was killed by signal N (POSIX convention). |

The exit code of the most-recent foreground command is also
exposed via `$?` and `${?}` in subsequent commands.

## EXAMPLES

Start an interactive session:

```
$ aish
```

Start as a login shell (`/etc/aish/aishrc` and the user RC are
sourced, `logout` is bound):

```
$ aish --login
```

Run a single command non-interactively (still passes through the
full dispatch tier, so cache + plugin still work):

```
$ aish -c 'find . -name "*.go" | wc -l'
```

## FILES

- `~/.aish/config.toml` — active theme, login defaults, persistence.
- `~/.aish/cache.db` — intent cache.
- `~/.aish/history.db` — signed history event log.
- `~/.aish/snapshots/` — pre-destructive-op snapshots for `undo`.
- `~/.aish/vault/vault.json` — encrypted secrets (see [secret(1)](../secret/)).
- `~/.aish/sessions/` — per-session telemetry rollups.
- `~/.aish/aishrc.toml` — login RC.
- `~/.aish/personas/` — user persona TOML overrides.
- `~/.aish/themes/` — synced theme bundles.
- `/etc/aish/aishrc` — system-wide login RC (sourced first in login mode).

A full description lives under [Files](../../files/).

## SEE ALSO

- [Manual pages](../) — the full built-in index.
- [Environment](../../environment/) — variables `aish` reads.
- [Signals](../../signals/) — signal-handling rules.
