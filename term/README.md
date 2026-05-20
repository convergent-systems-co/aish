# term — aish terminal emulator (placeholder)

The aish-term GPU-accelerated cross-platform terminal emulator described in
[GOALS.md §"aish-term"](../GOALS.md). **Gated on >10,000 active aish-shell
users** before any work begins (see GOALS.md v1.5 scope discipline).

When work starts, this directory becomes a Go module:

```
term/
├─ go.mod            # convergent-systems-co/aish/term
├─ Makefile
├─ cmd/aish-term/main.go
└─ internal/
```

The root `go.work` adds `use ./term` at that point. Until then this README is
the only artifact in the directory.
