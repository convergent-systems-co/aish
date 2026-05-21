# cloud — `aish-inference-cloud` reference

The bundled Convergent Systems LLM-gateway inference plugin. Built
from `plugins/cloud/cmd/aish-inference-cloud/`. Ships in every aish
release.

## Registry manifest

After a release install, the user (or the installer script) runs:

```bash
aish plugin install /usr/local/bin/aish-inference-cloud \
    --name cloud --version $(aish-inference-cloud --version) --kinds inference
```

This signs the binary with the compiled-in dev key (today) or the
release-signing key (production) and writes the manifest to
`~/.aish/plugins/cloud/manifest.json`.

## Manifest schema

See `libs/proto/registry/manifest.go` for the authoritative type. The
on-disk shape:

```json
{
  "format_version": 1,
  "name": "cloud",
  "version": "0.3.2",
  "binary_path": "/usr/local/bin/aish-inference-cloud",
  "kinds": ["inference"],
  "sha256": "<hex sha256 of the binary>",
  "signer_id": "aish-dev",
  "signature": "<base64 ed25519 sig over the sha256 bytes>",
  "created_at": "2026-05-21T00:00:00Z"
}
```

## Wire protocol

The cloud plugin speaks the `libs/proto/inference` NDJSON wire shape
on stdin/stdout. See `libs/proto/inference/inference.go` for the
canonical type definitions.
