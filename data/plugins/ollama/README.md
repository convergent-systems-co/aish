# ollama — local Ollama inference plugin (scaffold)

Scaffold for the community Ollama inference plugin. The binary lives
in a **separate repo** to be added in v0.3-2-followup; this directory
documents the integration so an author can stand it up against the
existing v0.3-2 registry without coordinating with the aish core team.

## Scope

A v0.3-2 community plugin is a Go binary that:

1. Imports `github.com/convergent-systems-co/aish/libs/proto/inference`.
2. Reads NDJSON `Request` envelopes on stdin.
3. Dispatches `MethodInfer`, `MethodPing`, and (optionally)
   `MethodEmbed` to local Ollama via its HTTP API.
4. Writes NDJSON `Response` envelopes on stdout, with `Kind=token`
   frames for streaming inference and a terminating `Kind=complete`
   frame carrying the assembled invocation.

The scaffold MUST follow the same panic-firewall + secret-redaction
patterns as `plugins/cloud/cmd/aish-inference-cloud/main.go`.

## Registry manifest

After building the binary, the user runs:

```bash
aish plugin install ./aish-inference-ollama \
    --name ollama --version 0.1.0 --kinds inference
```

The shell's plugin selector returns the first registered
`inference`-kind plugin alphabetically. To prefer Ollama over the
cloud plugin, name it `0-ollama` (sort order) or remove the cloud
plugin manifest with `aish plugin remove cloud`.

## Signing

At v0.3-2 the only available signer is the development anchor
(`aish-dev`). A production Ollama plugin signed by a community key
will require a new trust-anchor PR. See `libs/proto/registry/trust.go`.

## Status

**Scaffold only.** The binary is not in this repo; the manifest
above and the integration steps are documented so the plugin can be
brought online in a follow-up without coordinating with the aish
core team.
