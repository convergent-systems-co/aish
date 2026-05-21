---
title: theme(1)
parent: Manual pages
permalink: /man/theme/
---

# theme(1)

## NAME

**theme** — manage prompt branding.

## SYNOPSIS

```
theme list
theme show [<name>]
theme set <name>
theme preview <name> [--plain]
theme sync
```

## DESCRIPTION

aish renders the prompt through a **theme** — a named bundle that
declares prompt segments, color roles, glyphs, and syntax-tier
colors. Bundled themes ship with the binary; users can sync
additional themes from `theme-atoms.com`.

`theme list`
: List bundled and synced themes; the active theme carries a `*`.

`theme show [<name>]`
: Show segments, roles, and glyphs for `<name>` (default: active).

`theme set <name>`
: Activate `<name>` atomically and persist to
  `~/.aish/config.toml`.

`theme preview <name> [--plain]`
: Render a sample prompt with `<name>` without activating. With
  `--plain`, strip ANSI escapes so the output is plain text.

`theme sync`
: Pull theme bundles from `theme-atoms.com` into
  `~/.aish/themes/`. Existing themes are updated; new themes are
  added.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Subcommand succeeded. |
| 1 | User error (unknown theme, bad input). |
| 2 | Internal error (persistence write failed, etc.). |

## EXAMPLES

```
theme list
theme set tokyo-night
theme preview nord --plain
theme sync
```

## FILES

`~/.aish/themes/` — synced bundles.<br>
`~/.aish/config.toml` — active theme pointer.

## SEE ALSO

[persona(1)](../persona/).
