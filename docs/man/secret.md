---
title: secret(1)
parent: Manual pages
permalink: /man/secret/
---

# secret(1)

## NAME

**secret** — manage the encrypted local secret vault.

## SYNOPSIS

```
secret set NAME
secret get NAME
secret list
secret rm NAME
secret lock
```

## DESCRIPTION

aish keeps secret values (API keys, tokens, anything you don't
want in the shell history) in an encrypted vault under
`~/.aish/vault/vault.json`. Encryption is **AES-256-GCM** with
keys derived from a per-vault passphrase via **Argon2id** (KDF).

The first secret command in a session prompts for the vault
passphrase on stdin (no echo when stdin is a TTY). The passphrase
is cached on the running shell so subsequent commands in the same
session don't re-prompt; it is **zeroed on `lock`, on shell
close, and on Open-vault failure**.

`secret set NAME`
: Read a value from stdin and store it under `NAME`. When stdin is
  a TTY, input echo is suppressed. The value is **never** written
  to history, telemetry, logs, or any output stream.

`secret get NAME`
: Decrypt the named entry and copy it to the operating system
  clipboard. The value is **never** printed to stdout. On macOS
  this calls `pbcopy`; on Linux, `xclip`/`wl-copy`; on Windows,
  PowerShell `Set-Clipboard`.

`secret list`
: List stored secret names, sorted. Values are never displayed.

`secret rm NAME` (alias `delete`, `remove`)
: Delete the named entry.

`secret lock`
: Zero the cached passphrase. The next command re-prompts.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Subcommand succeeded. |
| 1 | Subcommand failed (vault locked, decrypt error, etc.). |
| 2 | Missing or unknown subcommand. |

## EXAMPLES

```
$ secret set CS_API_KEY
Passphrase: ********
Value (stdin): ********
$ secret list
CS_API_KEY
GITHUB_TOKEN
$ secret get CS_API_KEY
copied to clipboard.
$ secret lock
```

## SECURITY

- Choose a strong passphrase. There is **no recovery path** —
  forgetting the passphrase means losing the vault.
- The first time a vault is initialized, aish prints the KDF cost
  parameters (Argon2id memory + iteration count) to stderr so you
  can confirm them.
- The vault is plain JSON wrapping ciphertext; the file can be
  backed up like any other.
- Secret values **never** appear in history, telemetry, or
  prompts. The `get` path enforces clipboard-only transfer
  (Common.md §4 secret-handling rule applied).

## FILES

`~/.aish/vault/vault.json` — the encrypted vault.

## SEE ALSO

[identity(1)](../identity/) for persona-bound secrets,
[Files](../../files/).
