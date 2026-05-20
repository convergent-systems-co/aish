// Package anthropic is the Claude / Anthropic Messages API client used
// by aish-inference-cloud. It speaks SSE to /v1/messages and translates
// each delta into a proto.Frame, terminated by a Complete frame
// carrying the assembled invocation, confidence, and cost telemetry.
//
// The package is invoked through the rpc.Dispatcher; it does not touch
// stdin, stdout, or the JSON-RPC envelope shape. The API key is held
// privately and is NEVER written to logs, error messages, or any
// captured output (per Common.md §4).
//
// v0.1-3 SEED: types and constructor only. The T2 coder fills the
// bodies. Methods on the seed return placeholder errors so the package
// compiles and the test suite fails at runtime, not compile time.
package anthropic

import (
	"context"
	"errors"
	"net/http"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// DefaultBaseURL is the production Anthropic Messages API endpoint.
// Tests inject an httptest.Server URL instead.
const DefaultBaseURL = "https://api.anthropic.com"

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
// DefaultBaseURL when empty. httpClient defaults to http.DefaultClient
// when nil.
func NewClient(apiKey, baseURL string, httpClient *http.Client) (*Client, error) {
	_ = apiKey
	_ = baseURL
	_ = httpClient
	return nil, ErrNotImplemented
}

// String is the debug representation of the client. It MUST NOT include
// the API key value. Returns a fixed shape such as
// `anthropic.Client(baseURL=..., apiKey=[REDACTED])`.
func (c *Client) String() string {
	return ""
}

// Infer opens a streaming POST against the Anthropic Messages API and
// returns a channel of proto.Frame values. The channel closes after the
// terminal Complete frame is emitted (or on error).
//
// On non-2xx responses the returned error MUST carry a proto.Error code
// in {CodeAuthFailed, CodeRateLimited, CodeInternal, CodeTimeout} as
// appropriate; the error message MUST NOT include the API key.
func (c *Client) Infer(ctx context.Context, params proto.InferParams) (<-chan proto.Frame, error) {
	_ = ctx
	_ = params
	return nil, ErrNotImplemented
}

// Sentinel errors. The T2 coder MUST remove every reference to
// ErrNotImplemented from the production code before tests pass.
var (
	ErrNotImplemented = errors.New("anthropic: client not yet implemented (seed stub)")
	ErrMissingAPIKey  = errors.New("anthropic: api key is required")
)

// CodedError wraps a proto error code with a human-readable message.
// The T2 coder returns these from Infer when the upstream API rejects
// or times out the request. Message MUST NOT contain the API key
// (per Common.md §4).
type CodedError struct {
	Code    int
	Message string
}

// Error renders the message. It MUST NOT include any redacted secret.
func (e *CodedError) Error() string {
	return e.Message
}
