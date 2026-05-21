---
title: persona(1)
parent: Manual pages
permalink: /man/persona/
---

# persona(1)

## NAME

**persona** — switch between AI personas (system-prompt presets).

## SYNOPSIS

```
persona list
persona show <name>
persona set <name>
persona use <name>
persona active
persona create <name>
persona install <dir>
persona bundles
```

## DESCRIPTION

A **persona** is a named system-prompt preset that shapes
inference dispatch — voice, safety floor, response style. aish
ships with a bundled set ("default", plus a small library); users
may add their own TOML files under `~/.aish/personas/` and
overrides take precedence over bundled names.

`persona list`
: Print every persona name with a one-line description.

`persona show <name>`
: Render the full schema of `<name>`: system prompt, safety floor,
  tone, knobs.

`persona set <name>` / `persona use <name>`
: Activate `<name>`. The choice is persisted to
  `~/.aish/config.toml` so it survives across sessions. `use` is
  an alias for `set`.

`persona active`
: Print the currently-active persona name (or `default`).

`persona create <name>`
: Interactive bootstrap. Reads voice, safety, and tone from stdin
  and writes a new TOML to `~/.aish/personas/<name>.toml`.

`persona install <dir>`
: Install a signed persona bundle from `<dir>`. Refuses unsigned
  bundles.

`persona bundles`
: List installed signed bundles with signer fingerprints.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Subcommand succeeded. |
| 1 | Persona registry unavailable, or subcommand failed. |
| 2 | Missing or unknown subcommand. |

## SECURITY

Personas can change tone and style but **cannot** lower the safety
floor below the system minimum. Inference dispatch refuses to send
a persona-shaped prompt that drops the floor; see the
`persona/loader.go` source for the enforced lower bound.

## FILES

`~/.aish/personas/` — user TOML overrides.<br>
`~/.aish/config.toml` — active persona pointer.

## SEE ALSO

[identity(1)](../identity/), [theme(1)](../theme/),
[cache(1)](../cache/).
