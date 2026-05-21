---
title: plugin(1)
parent: Manual pages
permalink: /man/plugin/
---

# plugin(1)

## NAME

**plugin** — manage local inference plugins.

## SYNOPSIS

```
plugin list
plugin install <path>
plugin remove <name>
plugin verify <name>
plugin status
```

## DESCRIPTION

aish dispatches AI inference through a **plugin** binary — by
default, `aish-inference-cloud`, which speaks the OpenAI
chat-completions wire shape to the Convergent Systems LLM gateway.
The plugin model lets users swap in alternatives (local Ollama,
custom hosted endpoints) without touching the shell.

`plugin list`
: List every registered plugin: name, kind (inference / theme /
  …), binary path, signing fingerprint.

`plugin install <path>`
: Install a plugin binary. Delegates to the `aish-plugin` admin
  CLI so the trust path (signature verification, hash pinning)
  lives in one place. After install, the registry entry is
  written under `~/.aish/plugins/registry.json`.

`plugin remove <name>`
: Uninstall a registered plugin. Delegates to `aish-plugin`.

`plugin verify <name>`
: Re-verify the plugin's signature and hash. Use after a
  filesystem-level change to confirm the binary hasn't drifted
  from its registered fingerprint.

`plugin status`
: One-line summary of which plugin will be spawned on the next
  inference call: registered name + fallback discoverability via
  `$PATH`.

Bare `plugin` prints a usage hint and exits 2.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Subcommand succeeded. |
| 1 | Subcommand failed (delegate error, registry I/O, etc.). |
| 2 | Missing or unknown subcommand. |

## FILES

`~/.aish/plugins/` — installed plugin binaries.<br>
`~/.aish/plugins/registry.json` — registry manifest.

## SEE ALSO

[cache(1)](../cache/), [stats(1)](../stats/).
