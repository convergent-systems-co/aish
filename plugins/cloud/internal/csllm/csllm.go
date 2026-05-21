// Package csllm is the Convergent Systems LLM-gateway client used by
// aish-inference-cloud. It speaks OpenAI chat-completions over SSE to
// <base>/chat/completions (relative to the configured base URL — see
// DefaultBaseURL) and translates each delta into a proto.Frame,
// terminated by a Complete frame carrying the assembled invocation,
// confidence, and cost telemetry.
//
// The gateway is Cloudflare Workers AI fronted by a Bearer-token
// auth-proxy (per core-infra: workers-ai/src/worker.js,
// auth-proxy/src/index.js). The package is invoked through the
// rpc.Dispatcher; it does not touch stdin, stdout, or the JSON-RPC
// envelope shape. The bearer token is held privately and is NEVER
// written to logs, error messages, or any captured output (per
// Common.md §4).
//
// Renamed from `anthropic` in #178 — the previous package name was a
// misnomer that masked a wire-protocol mismatch.
package csllm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// DefaultBaseURL is the production CS gateway endpoint aish-inference-cloud
// points at by default. The auth-proxy at this host validates the bearer
// token and forwards the request, unmodified, to the workers-ai backend
// which speaks OpenAI chat-completions at /v1/chat/completions.
//
// Override with $CS_BASE_URL (preferred) or $ANTHROPIC_BASE_URL (legacy)
// to point at a local proxy, an httptest stub, etc. Tests inject an
// httptest.Server URL via NewClient(baseURL, ...).
const DefaultBaseURL = "https://api.convergent-systems.co/v1"

// DefaultModel is the model id used when InferParams.Model is empty.
// Mirrors the workers-ai worker's DEFAULT_MODEL env binding
// (terraform/cloudflare/workers-ai/src/worker.js §"env.DEFAULT_MODEL").
const DefaultModel = "@cf/meta/llama-3.1-8b-instruct"

// allowedModels mirrors the worker's ALLOWED_MODELS allowlist. A
// request that hits the gateway with an out-of-list model id is
// rejected at the worker with HTTP 400 (mapped here to
// proto.CodeInvalidParams). The plugin does NOT pre-validate against
// this list — the worker is the source of truth — but the constant
// is exported so future client-side code (e.g. an `aish models` UI)
// can render the menu without round-tripping.
var allowedModels = []string{
	"@cf/meta/llama-3.1-8b-instruct",
	"@cf/meta/llama-3.1-70b-instruct",
	"@cf/meta/llama-3-8b-instruct",
}

// AllowedModels returns a copy of the model allowlist served by the
// gateway. Callers must not mutate the returned slice — it is intended
// for display only.
func AllowedModels() []string {
	out := make([]string, len(allowedModels))
	copy(out, allowedModels)
	return out
}

// defaultMaxTokens caps the response length per the OpenAI wire field.
// Tuned for shell-invocation length, not prose.
const defaultMaxTokens = 1024

// defaultHTTPTimeout is the per-request timeout applied when the caller
// passes a nil *http.Client. Streaming-friendly upper bound.
const defaultHTTPTimeout = 30 * time.Second

// modelPrice is the per-1M-token pricing table (USD), keyed by model
// id. Used to estimate per-request cost. An unknown model yields zero
// rather than failing the request, per T2 directives.
//
// Cloudflare Workers AI's pricing is bundled into the platform
// subscription rather than per-token. We pin the table at 0.0 across
// the allowlist to keep the cost-telemetry shape intact while
// reflecting the actual bill-per-call cost the user pays. Future
// per-model pricing — if Cloudflare exposes it — can land here without
// changing the telemetry plumbing.
var modelPrice = map[string]struct {
	inputPerMTok  float64
	outputPerMTok float64
}{
	"@cf/meta/llama-3.1-8b-instruct":  {0.0, 0.0},
	"@cf/meta/llama-3.1-70b-instruct": {0.0, 0.0},
	"@cf/meta/llama-3-8b-instruct":    {0.0, 0.0},
}

// Client is a Convergent Systems LLM-gateway client. Construct with
// NewClient. The apiKey field is unexported and MUST NOT be reachable
// via any String, Error, or marshal path.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// NewClient constructs a Client. apiKey MUST be non-empty; otherwise
// the constructor returns ErrMissingAPIKey. baseURL defaults to
// DefaultBaseURL when empty. httpClient defaults to a 30-second
// timeout client when nil.
func NewClient(apiKey, baseURL string, httpClient *http.Client) (*Client, error) {
	if apiKey == "" {
		return nil, ErrMissingAPIKey
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    httpClient,
	}, nil
}

// String is the debug representation of the client. The bearer token
// is deliberately excluded — only the baseURL and a redaction marker
// surface.
func (c *Client) String() string {
	if c == nil {
		return "csllm.Client(nil)"
	}
	return fmt.Sprintf("csllm.Client(baseURL=%s, apiKey=[REDACTED])", c.baseURL)
}

// chatCompletionsRequest is the JSON body sent to <base>/chat/completions.
// Field shape mirrors the OpenAI chat-completions API — the workers-ai
// worker passes recognised fields through to Workers AI verbatim and
// drops unknown ones (per worker.js §"Pass through only the parameters
// Workers AI is documented to accept").
type chatCompletionsRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	Stream    bool          `json:"stream"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

// chatMessage is one message in the OpenAI chat-completions request.
// Roles are "system", "user", or "assistant" — see OpenAI docs.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Infer opens a streaming POST against the CS gateway's chat-completions
// endpoint and returns a channel of proto.Frame values. The channel
// closes after the terminal Complete frame is emitted (or on error /
// ctx cancel).
//
// HTTP status mapping:
//
//	400  -> *CodedError{Code: CodeInvalidParams}     (bad model id, etc.)
//	401  -> *CodedError{Code: CodeAuthFailed}        (auth-proxy rejected)
//	429  -> *CodedError{Code: CodeRateLimited}
//	5xx  -> *CodedError{Code: CodeInternal}
//	ctx  -> *CodedError{Code: CodeTimeout}
//
// The returned error MUST NOT contain the bearer token.
func (c *Client) Infer(ctx context.Context, params proto.InferParams) (<-chan proto.Frame, error) {
	model := params.Model
	if model == "" {
		model = DefaultModel
	}

	body := chatCompletionsRequest{
		Model:     model,
		Stream:    true,
		MaxTokens: defaultMaxTokens,
		Messages: []chatMessage{
			{Role: "user", Content: params.Intent},
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		// Should be impossible — fixed-shape struct, primitive fields.
		return nil, &CodedError{
			Code:    proto.CodeInternal,
			Message: "csllm: failed to marshal request body",
		}
	}

	// Path note: the default base URL ends in /v1, so we append
	// `/chat/completions`. When a caller overrides the base URL to
	// something other than the production gateway (httptest stub,
	// local proxy), the concatenation lands the request at the right
	// place provided the override mirrors the same /v1 convention.
	url := c.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, &CodedError{
			Code:    proto.CodeInternal,
			Message: "csllm: failed to build request",
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		// Context cancellation / deadline → Timeout.
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, &CodedError{
				Code:    proto.CodeTimeout,
				Message: "csllm: request cancelled or deadline exceeded",
			}
		}
		return nil, &CodedError{
			Code:    proto.CodeInternal,
			Message: "csllm: transport error",
		}
	}

	if resp.StatusCode != http.StatusOK {
		// Drain and close before returning the coded error.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return nil, statusToCodedError(resp.StatusCode)
	}

	// Successful streaming response. Hand off body to the SSE pump.
	out := make(chan proto.Frame)
	go pumpSSE(ctx, resp.Body, model, out)
	return out, nil
}

// statusToCodedError maps a non-2xx HTTP status to a CodedError with
// the appropriate proto.Code. The HTTP body is intentionally NOT
// surfaced — the auth-proxy can echo headers in some 5xx paths, and we
// never want the bearer token to leak there.
func statusToCodedError(status int) *CodedError {
	switch {
	case status == http.StatusBadRequest:
		return &CodedError{
			Code:    proto.CodeInvalidParams,
			Message: "csllm: invalid request (400) — check model id or message shape",
		}
	case status == http.StatusUnauthorized:
		return &CodedError{
			Code:    proto.CodeAuthFailed,
			Message: "csllm: authentication failed (401)",
		}
	case status == http.StatusTooManyRequests:
		return &CodedError{
			Code:    proto.CodeRateLimited,
			Message: "csllm: rate limited (429)",
		}
	case status >= 500 && status <= 599:
		return &CodedError{
			Code:    proto.CodeInternal,
			Message: fmt.Sprintf("csllm: upstream server error (%d)", status),
		}
	default:
		return &CodedError{
			Code:    proto.CodeInternal,
			Message: fmt.Sprintf("csllm: unexpected status %d", status),
		}
	}
}

// estimateUSD returns the dollar estimate for the (model, tokensIn,
// tokensOut) tuple. An unknown model returns 0.0 (non-fatal — the
// caller still emits the Cost frame; only the dollar field is zero).
func estimateUSD(model string, tokensIn, tokensOut int) float64 {
	p, ok := modelPrice[model]
	if !ok {
		return 0.0
	}
	const perMillion = 1_000_000.0
	return (float64(tokensIn)*p.inputPerMTok + float64(tokensOut)*p.outputPerMTok) / perMillion
}

// Sentinel errors.
var (
	// ErrMissingAPIKey is returned by NewClient when apiKey is empty.
	// Its message intentionally contains no value — the empty string
	// would not be a secret in any case, but consistency matters.
	ErrMissingAPIKey = errors.New("csllm: api key is required")
)

// CodedError wraps a proto error code with a human-readable message.
// Infer returns these when the upstream API rejects or times out the
// request. Message MUST NOT contain the bearer token (per Common.md §4).
//
// The `cause` field is unexported and reachable only through Unwrap —
// it carries a typed sentinel (e.g. proto.ErrEmbedNotImplemented) so
// callers can errors.Is against a stable identity without depending on
// the human-readable Message string.
type CodedError struct {
	Code    int
	Message string
	cause   error
}

// Error renders the message. It MUST NOT include any redacted secret.
func (e *CodedError) Error() string {
	return e.Message
}

// Unwrap surfaces the typed cause (if any) so errors.Is works against
// sentinels like proto.ErrEmbedNotImplemented.
func (e *CodedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}
