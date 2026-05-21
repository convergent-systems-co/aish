---
title: identity(1)
parent: Manual pages
permalink: /man/identity/
---

# identity(1)

## NAME

**identity** — switch between named identity profiles.

## SYNOPSIS

```
identity list
identity show [<name>]
identity use <name>
identity create <name>
identity help
```

## DESCRIPTION

An **identity** binds together a gateway URL, a signer pubkey
hash, a persona, and an optional set of persona-bound secrets. It
is the unit of "which person is this shell being right now?" —
useful when one human juggles multiple accounts (work,
open-source, personal).

`identity list` (alias `ls`)
: Print profile names, sorted.

`identity show [<name>]`
: Render the active profile, or the named one if given. Includes
  gateway URL, signer fingerprint, bound persona, and secret
  names (not values).

`identity use <name>`
: Set the active identity pointer. Subsequent inference calls
  route to the identity's gateway; the bound persona becomes
  active; bound secrets are unlocked.

`identity create <name>`
: Interactive create. Reads gateway URL and signer pubkey hash
  from stdin and writes a new profile under
  `~/.aish/identities/<name>/`.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Subcommand succeeded. |
| 1 | User error (no such identity, etc.). |
| 2 | I/O error. |

## EXAMPLES

```
identity list
identity create work
identity use work --persona engineer
identity show
```

## FILES

`~/.aish/identity.toml` — active identity pointer.<br>
`~/.aish/identities/<name>/` — per-profile config.

## SEE ALSO

[persona(1)](../persona/), [secret(1)](../secret/).
