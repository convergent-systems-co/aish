# HANDOFF — T2 Anthropic API client (v0.1-3, Wave 2)

**Author:** Coder T2 (Claude Opus 4.7)
**Date (UTC):** 2026-05-20
**Branch:** `feat/v0.1-3-cloud-inference`
**Partition:** `plugins/cloud/internal/anthropic/**`

---

## Status

Implementation **complete**. 9 of 10 contract tests **pass**; the 10th
(`TestInfer_CtxDeadline_ReturnsTimeout`) hangs in `t.Cleanup` due to a
test-handler defect, not an implementation defect. See "Open question
for TL" below for the one-line test fix.

Verification gates:

| Gate | Status |
|---|---|
| `go vet ./internal/anthropic/...` | passes (exit 0) |
| 9/10 tests pass with `-race -count=1` | yes |
| `TestInfer_CtxDeadline_ReturnsTimeout` | **hangs in cleanup** (root cause below) |
| `make build` from repo root | passes |
| `git status` clean (modulo this handoff + new files) | yes |
| Only `plugins/cloud/internal/anthropic/` files modified | yes |
| `grep 'sk-test-' plugins/cloud/internal/anthropic/*.go` outside `_test.go` | empty (no key leakage) |

Implementation files (this commit):

- `plugins/cloud/internal/anthropic/anthropic.go` — `Client`, `NewClient`,
  `String`, `Infer`, status-to-CodedError mapping, USD pricing table,
  `CodedError`, `ErrMissingAPIKey`.
- `plugins/cloud/internal/anthropic/sse.go` — `pumpSSE` SSE parser that
  consumes Anthropic Messages-API `content_block_delta` and
  `message_stop` events, emits `proto.KindToken` and a terminal
  `proto.KindComplete` frame with assembled invocation + cost.

API behaviour matches the spawn brief:

- `NewClient` rejects empty `apiKey` with `ErrMissingAPIKey`. Defaults
  `baseURL=https://api.anthropic.com` and `httpClient=&http.Client{Timeout: 30s}`.
- `Client.String()` emits `anthropic.Client(baseURL=…, apiKey=[REDACTED])`
  — no key value, no `sk-` prefix.
- `Infer` POSTs `/v1/messages` with `x-api-key`, `anthropic-version:
  2023-06-01`, `content-type: application/json`, `stream: true`. Body is
  `{model, max_tokens=1024, stream=true, messages=[{user, intent}]}`.
- HTTP status mapping: 401 → `CodeAuthFailed`, 429 → `CodeRateLimited`,
  5xx → `CodeInternal`, ctx cancel → `CodeTimeout`. Error messages
  never contain the API key.
- Model defaults to `claude-opus-4-7` when `InferParams.Model` is empty.
- Confidence on `Complete` frame is `1.0`.
- USD pricing table (per 1M tokens):
  - `claude-opus-4-7`: $15 / $75
  - `claude-sonnet-4-6`: $3 / $15
  - `claude-haiku-4-5-20251001`: $0.80 / $4
  - unknown model: 0.0 (non-fatal)
- Mid-stream ctx cancel: the SSE pump selects on `ctx.Done()` for every
  send and between every read. The channel is closed before return.
  No goroutine leak.

---

## The one failing test — root cause

`TestInfer_CtxDeadline_ReturnsTimeout` (lines 282–312 in
`anthropic_test.go`):

```go
block := make(chan struct{})
t.Cleanup(func() { close(block) })  // registered FIRST
srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
    // Hold the connection open until the test cleans up.
    select {
    case <-r.Context().Done():
    case <-block:
    }
})  // registers t.Cleanup(srv.Close) SECOND
```

**The assertion path passes.** `Infer` returns
`&CodedError{Code: CodeTimeout, Message: "anthropic: request cancelled
or deadline exceeded"}`. The test's `t.Errorf` lines never fire. The
test function body returns normally.

**The hang is in `t.Cleanup`.** `t.Cleanup` runs registrations in LIFO
order, so the order is:

1. `srv.Close()` (registered last, runs first)
2. `close(block)` (registered first, runs last)

`httptest.Server.Close()` deliberately does **not** close active
connections. From `src/net/http/httptest/server.go:252-254`:

> The docs for Server.Close say we wait for "outstanding requests", so
> we don't close things in StateActive.

So `srv.Close()` waits for the handler to return. The handler is in a
`select` on `r.Context().Done()` or `block`. `block` won't close until
*after* `srv.Close()` finishes. Deadlock unless `r.Context().Done()`
fires.

`r.Context().Done()` would fire when the server-side `connReader`
detects the client closing the connection. But the Go HTTP server only
starts background-reading the connection **after the request body is
fully consumed** (`src/net/http/server.go:2059-2063`):

```go
if requestBodyRemains(req.Body) {
    registerOnHitEOF(req.Body, w.conn.r.startBackgroundRead)
} else {
    w.conn.r.startBackgroundRead()
}
```

The test handler **never reads `r.Body`**. The POST request body
(`{"model":"...","max_tokens":1024,"stream":true,"messages":[{"role":"user","content":"hang"}]}`)
remains unconsumed → `registerOnHitEOF` never fires →
`startBackgroundRead` never runs → server cannot detect that the
client side has closed the TCP connection → `r.Context().Done()`
never fires → handler blocks forever → `srv.Close()` blocks forever.

I verified this with a reduced repro (POST with non-empty body vs.
GET / POST with empty body): the empty-body and GET cases unblock
correctly; non-empty POST without server-side body drain hangs every
time. This is a Go behavior, not a bug.

### What I tried (none worked)

- `req.Close = true` — no effect (server still doesn't read body)
- `Connection: close` header — same
- `req.Header.Set("Expect", "100-continue")` — same
- `Transport.CancelRequest(req)` after ctx.Done() — same
- `Transport.CloseIdleConnections()` after ctx.Done() — connection
  isn't idle on the server side
- `srv.CloseClientConnections()` — same
- Various body-reader wrappers (ctx-aware, no-Content-Length to force
  chunked) — same

The fix has to be server-side (handler reads body) or in the test's
cleanup ordering.

### One-line fix the TL can apply (test side)

Add a single line inside the handler:

```go
srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
    _, _ = io.Copy(io.Discard, r.Body)   // ← add this
    select {
    case <-r.Context().Done():
    case <-block:
    }
})
```

Draining the body triggers `registerOnHitEOF`, which starts the server's
background-read on the connection, which lets the server notice the
client's TCP close, which fires `r.Context().Done()`, which lets the
handler return, which lets `srv.Close()` return.

I verified this fixes it: with the drain in place, `srv.Close()` returns
in microseconds and the test exits cleanly. Without it,
`srv.Close()` waits indefinitely (httptest logs "blocked in Close after
5 seconds" but does not give up — Go test runner panics on its own
timeout).

Alternative fix: reverse the cleanup registration order so `close(block)`
runs before `srv.Close()`.

I did **not** apply either fix because the spawn brief is explicit:

> Do NOT touch `anthropic_test.go`. If a test seems wrong, write
> `HANDOFF.md` and stop.

---

## Open question for TL

Apply the `io.Copy(io.Discard, r.Body)` one-liner in
`TestInfer_CtxDeadline_ReturnsTimeout`, or reverse the cleanup
ordering? Either makes the test pass. I prefer the body-drain because
it matches what a real server would do (Anthropic reads the body it's
sent).

After the fix, the full suite should pass with the implementation as
shipped. No coder-side changes needed.

---

## What's committed in this branch (T2)

- `plugins/cloud/internal/anthropic/anthropic.go` — full Client impl
- `plugins/cloud/internal/anthropic/sse.go` — SSE pump

Commit subject:
`feat(anthropic): implement Claude API client with SSE streaming + cost telemetry`
