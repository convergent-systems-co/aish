# libs — shared Go modules (placeholder)

Reusable Go libraries the shell, terminal, and plugin modules depend on.
Each subdirectory is an independent Go module published under
`github.com/convergent-systems-co/aish/libs/<name>` so external consumers
can import it without pulling the shell binary.

| Lib | Status | Purpose |
|---|---|---|
| `proto/` | v0.1-3 | JSON-RPC contract types — inference plugin API |
| `cache/` | v0.1-2 | Intent cache primitives (SQLite + embedding similarity) |
| `history/` | v0.1-4 | Append-only signed event log + snapshot store |

Each `libs/<name>/` gets its own `go.mod` + tests + Makefile when extracted;
the root `go.work` adds `use ./libs/<name>` at that point.
