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

// DefaultBaseURL is the production LLM-gateway endpoint aish points at
// by default. It's Convergent Systems' multi-provider routing layer,
// which speaks an Anthropic-Messages-API-compatible shape on its
// /llm/v1/* paths. Override with $LLM_BASE_URL (preferred) or
// $ANTHROPIC_BASE_URL (legacy) to point at upstream Anthropic, a local
// proxy, an httptest stub, etc.
//
// Tests inject an httptest.Server URL via NewClient(baseURL, ...).
const DefaultBaseURL = "https://api.convergent-systems.co/llm/v1"

// defaultModel is the model id used when InferParams.Model is empty.
const defaultModel = "claude-opus-4-7"

// anthropicVersion is the API version sent on every request. Required
// by the Messages API.
const anthropicVersion = "2023-06-01"

// defaultMaxTokens caps the response length per Anthropic's required
// max_tokens field. Tuned for shell-invocation length, not prose.
const defaultMaxTokens = 1024

// defaultHTTPTimeout is the per-request timeout applied when the caller
// passes a nil *http.Client. Streaming-friendly upper bound.
const defaultHTTPTimeout = 30 * time.Second

// modelPrice is the per-1M-token pricing table (USD), keyed by model
// id. Used to estimate per-request cost. An unknown model yields zero
// rather than failing the request, per T2 directives.
//
// Source: Anthropic public pricing as of plan v0.1-3. A drift here
// only affects estimates, never billing.
var modelPrice = map[string]struct {
	inputPerMTok  float64
	outputPerMTok float64
}{
	"claude-opus-4-7":           {15.0, 75.0},
	"claude-sonnet-4-6":         {3.0, 15.0},
	"claude-haiku-4-5-20251001": {0.80, 4.0},
}

// Client is an Anthropic Messages API client. Construct with NewClient.
// The apiKey field is unexported and MUST NOT be reachable via any
// String, Error, or marshal path.
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

// String is the debug representation of the client. The API key is
// deliberately excluded — only the baseURL and a redaction marker
// surface.
func (c *Client) String() string {
	if c == nil {
		return "anthropic.Client(nil)"
	}
	return fmt.Sprintf("anthropic.Client(baseURL=%s, apiKey=[REDACTED])", c.baseURL)
}

// messagesRequest is the JSON body sent to <base>/messages.
type messagesRequest struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	Stream    bool             `json:"stream"`
	Messages  []messageContent `json:"messages"`
}

// messageContent is one message in the request.
type messageContent struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Infer opens a streaming POST against the Anthropic Messages API and
// returns a channel of proto.Frame values. The channel closes after
// the terminal Complete frame is emitted (or on error / ctx cancel).
//
// HTTP status mapping:
//
//	401  -> *CodedError{Code: CodeAuthFailed}
//	429  -> *CodedError{Code: CodeRateLimited}
//	5xx  -> *CodedError{Code: CodeInternal}
//	ctx  -> *CodedError{Code: CodeTimeout}
//
// The returned error MUST NOT contain the API-key value.
func (c *Client) Infer(ctx context.Context, params proto.InferParams) (<-chan proto.Frame, error) {
	model := params.Model
	if model == "" {
		model = defaultModel
	}

	body := messagesRequest{
		Model:     model,
		MaxTokens: defaultMaxTokens,
		Stream:    true,
		Messages: []messageContent{
			{Role: "user", Content: params.Intent},
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		// Should be impossible — fixed-shape struct, primitive fields.
		return nil, &CodedError{
			Code:    proto.CodeInternal,
			Message: "anthropic: failed to marshal request body",
		}
	}

	// Path note: the default base URL ALREADY ends in /llm/v1, so we
	// append `/messages` without re-stating /v1. When a caller overrides
	// the base URL to plain `https://api.anthropic.com` (legacy /v1 in
	// the path), they need to point at `https://api.anthropic.com/v1`
	// so this concatenation lands at the right place. The plugin
	// `--api-url` flag is the supported override surface.
	url := c.baseURL + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, &CodedError{
			Code:    proto.CodeInternal,
			Message: "anthropic: failed to build request",
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		// Context cancellation / deadline → Timeout.
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, &CodedError{
				Code:    proto.CodeTimeout,
				Message: "anthropic: request cancelled or deadline exceeded",
			}
		}
		return nil, &CodedError{
			Code:    proto.CodeInternal,
			Message: "anthropic: transport error",
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
// surfaced — Anthropic's error bodies can contain echoes of headers
// in some edge cases, and we never want the key to leak there.
func statusToCodedError(status int) *CodedError {
	switch {
	case status == http.StatusUnauthorized:
		return &CodedError{
			Code:    proto.CodeAuthFailed,
			Message: "anthropic: authentication failed (401)",
		}
	case status == http.StatusTooManyRequests:
		return &CodedError{
			Code:    proto.CodeRateLimited,
			Message: "anthropic: rate limited (429)",
		}
	case status >= 500 && status <= 599:
		return &CodedError{
			Code:    proto.CodeInternal,
			Message: fmt.Sprintf("anthropic: upstream server error (%d)", status),
		}
	default:
		return &CodedError{
			Code:    proto.CodeInternal,
			Message: fmt.Sprintf("anthropic: unexpected status %d", status),
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
	ErrMissingAPIKey = errors.New("anthropic: api key is required")
)

// CodedError wraps a proto error code with a human-readable message.
// Infer returns these when the upstream API rejects or times out the
// request. Message MUST NOT contain the API key (per Common.md §4).
type CodedError struct {
	Code    int
	Message string
}

// Error renders the message. It MUST NOT include any redacted secret.
func (e *CodedError) Error() string {
	return e.Message
}
