# data/plugins/ — v0.3-2 plugin registry scaffolds

This directory holds **reference manifests + documentation** for the
bundled plugins that ship with an aish release. The actual installed
state lives at `~/.aish/plugins/<name>/manifest.json` after the user
(or the installer) runs `aish plugin install`.

## Layout

```
data/plugins/
  README.md                — this file
  cloud/                   — the bundled aish-inference-cloud plugin
    README.md
  ollama/                  — scaffold for the community Ollama plugin
    README.md              — author guide; the binary lives in a separate repo
```

## Why this directory exists

A v0.3-2 plugin is a **separate binary** referenced by an
absolute path in `manifest.json`. We do NOT ship binary blobs in the
aish repo. The directory under `data/plugins/` ships:

1. The author guide for each plugin (README.md).
2. (Future) A small `install.sh` that picks the right per-platform
   binary, signs it with the release key, and runs `aish plugin install`.

## Signing

Trust anchors are compiled into `libs/proto/registry/trust.go`. The
v0.3-2 development key (`aish-dev`) is the only signer; production
anchors land in a follow-up PR alongside the actual key-management
process.

See `.artifacts/plans/v0.3-2.md` for the full architecture.
