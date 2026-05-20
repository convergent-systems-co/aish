# plugins — aish inference plugins (placeholder)

Each subdirectory is an independent Go module that implements the JSON-RPC
inference plugin contract published by `libs/proto/`. Plugins are spawned by
the shell over stdin/stdout — they ship as separate binaries.

| Plugin | Status | Use case |
|---|---|---|
| `cloud/` | v0.1-3 | Cloud APIs (Claude, OpenAI, Groq, …) |
| `ollama/` | v0.3-2 | Local GPU/NPU via Ollama |
| `wasm/` | future | Purpose-built shell model, WASM-portable |
| `remote/` | future | Team-shared remote Ollama endpoint |

See [GOALS.md §"Inference Plugin Contract"](../GOALS.md) for the contract
shape. Each plugin gets its own `go.mod` + `Makefile` when work begins; the
root `go.work` adds `use ./plugins/<name>` at that point.
