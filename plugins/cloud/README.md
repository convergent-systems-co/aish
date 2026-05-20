# plugins/cloud — aish-inference-cloud

The Anthropic Cloud inference plugin for aish. Reads JSON-RPC requests
on stdin, dispatches `infer` (and later `embed`, `ping`) to the
Anthropic API, and emits NDJSON token + complete frames on stdout.

This is v0.1-3 of the aish roadmap — the first inference plugin, the
one that proves the JSON-RPC contract is implementable end-to-end. The
intent-cache (v0.1-2) sits on top and calls `embed` / `infer` for cache
misses.

## Layout

```
plugins/cloud/
├─ go.mod                              # module + replace ../../libs/proto
├─ Makefile                            # build/test/lint/ci/release
├─ cmd/aish-inference-cloud/main.go    # entry: parse flags, build dispatcher, Run
└─ internal/
   ├─ rpc/                             # NDJSON JSON-RPC dispatcher
   ├─ anthropic/                       # Anthropic SSE client (T2)
   └─ reliab/                          # timeout + retry + cost telemetry (T3)
```

## Run

```sh
export ANTHROPIC_API_KEY=sk-...
echo '{"jsonrpc":"2.0","id":"1","method":"infer","params":{"intent":"list large files","stream":true}}' \
  | aish-inference-cloud
```

The plugin emits NDJSON to stdout — one token frame per chunk, terminal
`complete` frame with the assembled invocation + cost.

## Build

```sh
make build                # host platform → dist/aish-inference-cloud
make build-all            # all 6 platforms × architectures
make ci                   # full pre-merge gate
```

## Wire protocol

See [`libs/proto/inference`](../../libs/proto/inference) for the
canonical contract. Plugin authors implement the same types in their
language of choice; this module is the Go reference.

## Env vars

| Var | Required | Purpose |
|---|---|---|
| `ANTHROPIC_API_KEY` | yes | auth for `api.anthropic.com` |
| `ANTHROPIC_BASE_URL` | no | override the base URL (used by `httptest`-stubbed integration tests) |
| `AISH_COST_LOG` | no | path to the per-request JSONL cost log; default `~/.aish/cost-log.jsonl` |

## Out of scope (deferred)

- Local-model plugins (Ollama, WASM, Remote) — separate epics (v0.3-2)
- Plugin registry / install / list — v0.3-2
- Tainted-secret handling — v0.3-3 (proper secrets engine)
