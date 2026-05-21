package csllm

import (
	"context"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// Embed always returns proto.ErrEmbedNotImplemented wrapped in a typed
// CodedError. The CS gateway at api.convergent-systems.co/v1 serves
// only /chat/completions today — see
// core-infra/terraform/cloudflare/workers-ai/src/worker.js, which
// routes only that path and 404s everything else. Sending an embed
// request would surface as a generic 404 / CodeInternal; the sentinel
// makes the "this capability is not wired up" path explicit so
// callers (shell/internal/cache.Cache.Resolve) can branch on it
// without inspecting HTTP status.
//
// The function makes no network request — the short-circuit happens
// before any HTTP client is touched, which is also what
// TestEmbed_ReturnsErrEmbedNotImplemented asserts (the test wires a
// "fail-if-called" httptest.Server).
//
// When core-infra ships an /embeddings endpoint (tracked as
// core-infra#10), this function will be replaced with the real call;
// the proto.ErrEmbedNotImplemented sentinel can remain — it just
// won't fire from this client.
func (c *Client) Embed(_ context.Context, _ proto.EmbedParams) (proto.EmbedResult, error) {
	return proto.EmbedResult{}, &CodedError{
		Code:    proto.CodeNotImplemented,
		Message: proto.ErrEmbedNotImplemented.Error(),
		cause:   proto.ErrEmbedNotImplemented,
	}
}
