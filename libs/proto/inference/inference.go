// Package inference is the canonical JSON-RPC contract aish speaks to
// any inference plugin (cloud, local, WASM, remote, future categories).
//
// Wire shape:
//
//   - The plugin is a separate binary launched by aish.
//   - Requests flow inbound on the plugin's stdin, one JSON-RPC envelope
//     per line (NDJSON).
//   - Responses flow outbound on the plugin's stdout, also NDJSON.
//   - Streaming responses emit one Frame per token until a Frame of
//     Kind=`complete` terminates the stream.
//
// This package carries types ONLY — no transport, no fetch logic, no
// implementation. Plugins import and serialise these types via stdlib
// `encoding/json`; aish (the consumer) does the same in reverse.
//
// See GOALS.md §"Inference Plugin Contract" for the broader design.
package inference

// Version is the JSON-RPC protocol version aish speaks. Always "2.0".
const Version = "2.0"

// Method names. Plugins advertise capability by handling these.
const (
	// MethodInfer asks the plugin to compile an intent → invocation,
	// optionally streaming tokens as they are produced.
	MethodInfer = "infer"
	// MethodPing is a liveness check. The response is a single Frame
	// of Kind=`pong`. Plugins MUST implement this.
	MethodPing = "ping"
	// MethodEmbed asks the plugin to embed an intent into a vector
	// for similarity-based cache lookup. Used by v0.1-2 cache. Optional
	// for v0.1-3 plugins; SHOULD be supported by any inference plugin
	// long-term.
	MethodEmbed = "embed"
)

// Request is one inbound JSON-RPC request envelope.
type Request struct {
	// JSONRPC is the protocol version. Always "2.0".
	JSONRPC string `json:"jsonrpc"`
	// ID is the per-request identifier. Responses echo it. Plugins MUST
	// preserve ID verbatim across all frames of a streaming response.
	ID string `json:"id"`
	// Method is one of the MethodXxx constants.
	Method string `json:"method"`
	// Params carries the method-specific payload. Concrete shape depends
	// on Method — see InferParams, PingParams, EmbedParams.
	Params InferParams `json:"params,omitempty"`
}

// InferParams is the payload for Method=infer.
type InferParams struct {
	// Intent is the user's natural-language description of what they
	// want done (e.g. "delete log files older than 30 days").
	Intent string `json:"intent"`
	// Context is the optional runtime context aish provides to bias
	// resolution (cwd-aware paths, history hints, etc.).
	Context InferContext `json:"context,omitempty"`
	// Stream requests token-by-token frames as they are produced.
	// When false, the plugin returns one Complete frame at the end.
	Stream bool `json:"stream,omitempty"`
	// Model optionally pins the model identifier (e.g. "claude-opus-4-7").
	// When empty, the plugin chooses its default.
	Model string `json:"model,omitempty"`
}

// EmbedParams is the helper view consumers use when calling MethodEmbed.
// It is NOT the on-wire shape — the rpc.Dispatcher carries Request.Params
// as InferParams; handlers map InferParams -> EmbedParams at the
// dispatch boundary (Intent -> Text, Model -> Model). Keeping EmbedParams
// here serves as the documented contract for what an embed request
// requires.
//
// See plan: .artifacts/plans/v0.1-2-embed.md §"Wire-shape decision".
type EmbedParams struct {
	// Text is the natural-language string to embed.
	Text string `json:"text"`
	// Model optionally pins the embedding model identifier
	// (e.g. "voyage-3", "claude-embed-v1"). When empty, the plugin
	// chooses its default.
	Model string `json:"model,omitempty"`
}

// EmbedResult is the helper view consumers use when reading the result
// of MethodEmbed. The wire representation is a Frame with
// Type=KindEmbedding carrying Vector and Cost; this struct documents
// the semantic shape (model id + vector + cost) for callers that want
// a typed projection.
type EmbedResult struct {
	// Vector is the embedding produced by the plugin. Dimensionality is
	// model-specific; callers MUST NOT assume a fixed length.
	Vector []float32 `json:"vector"`
	// Model is the model identifier the plugin used to produce Vector.
	// Distinct from the inference model; recorded in Cost.Model for the
	// telemetry pipeline.
	Model string `json:"model,omitempty"`
	// Cost is the per-request token + USD telemetry attached to the
	// embedding request. Same shape as the Infer Cost block; the Model
	// field distinguishes embed vs infer in the aggregated cost log.
	Cost *Cost `json:"cost,omitempty"`
}

// InferContext is the runtime context block carried in InferParams.
type InferContext struct {
	// CWD is the shell's working directory at request time.
	CWD string `json:"cwd,omitempty"`
	// OS is the target operating system ("darwin", "linux", "windows").
	// Plugins SHOULD compile invocations appropriate for this OS.
	OS string `json:"os,omitempty"`
	// HistorySummary is a short prose summary of recent history aish
	// considers relevant. Length-bounded; not raw history.
	HistorySummary string `json:"history_summary,omitempty"`
	// CacheMiss is true when this request is being made because the
	// local intent cache had no match. Plugins MAY log differently.
	CacheMiss bool `json:"cache_miss,omitempty"`
}

// Response is one outbound JSON-RPC response envelope. Either Result or
// Error is set; never both, never neither.
type Response struct {
	JSONRPC string `json:"jsonrpc"`
	// ID echoes the originating Request.ID.
	ID string `json:"id"`
	// Result is the success payload — a single Frame. For streaming
	// responses, each frame is its own Response with the same ID.
	Result *Frame `json:"result,omitempty"`
	// Error is the failure payload — present iff Result is absent.
	Error *Error `json:"error,omitempty"`
}

// Frame is one frame in a (possibly streaming) response.
type Frame struct {
	// Type discriminates the frame.
	Type Kind `json:"type"`

	// --- For Kind=`token` ---
	// Data is the token text (zero or more characters).
	Data string `json:"data,omitempty"`

	// --- For Kind=`complete` ---
	// Invocation is the final compiled shell-ready command. May be empty
	// for plugins that only stream tokens without a full command target.
	Invocation string `json:"invocation,omitempty"`
	// Confidence is the plugin's self-reported confidence in Invocation,
	// 0.0–1.0. Plugins that cannot estimate confidence SHOULD return 1.0.
	Confidence float64 `json:"confidence,omitempty"`
	// Cost is the per-request cost telemetry (tokens + USD).
	Cost *Cost `json:"cost,omitempty"`

	// --- For Kind=`pong` ---
	// (no additional fields)

	// --- For Kind=`embedding` ---
	// Vector is the embedding produced by a MethodEmbed handler.
	// Dimensionality is model-specific. omitempty keeps this field off
	// the wire for non-embedding frames.
	Vector []float32 `json:"vector,omitempty"`
}

// Kind is the Frame discriminator.
type Kind string

const (
	// KindToken is a partial-content frame in a streaming response.
	KindToken Kind = "token"
	// KindComplete is the terminator frame; carries the assembled
	// Invocation and Cost.
	KindComplete Kind = "complete"
	// KindPong is the response to MethodPing.
	KindPong Kind = "pong"
	// KindEmbedding is the terminal frame for MethodEmbed. Carries
	// Vector and Cost; no token-streaming intermediate frames.
	KindEmbedding Kind = "embedding"
)

// Cost is the per-request token + USD telemetry attached to a Complete
// frame. Plugins SHOULD populate this; aish (or v0.1-5 telemetry)
// aggregates across requests.
type Cost struct {
	// Model is the model identifier billed for this request.
	Model string `json:"model"`
	// TokensIn is the prompt-token count.
	TokensIn int `json:"tokens_in"`
	// TokensOut is the completion-token count.
	TokensOut int `json:"tokens_out"`
	// USD is the estimated dollar cost. Plugins use a per-model price
	// table; the value is an estimate, not a billing-authoritative
	// figure.
	USD float64 `json:"usd"`
}

// Error is the JSON-RPC error object — present in a Response when the
// request could not be fulfilled.
type Error struct {
	// Code is one of the CodeXxx constants.
	Code int `json:"code"`
	// Message is a single-line human-readable description. Plugins MUST
	// NOT leak secrets (API keys, tokens, PII) into this field.
	Message string `json:"message"`
}

// JSON-RPC standard error codes, plus plugin-specific extensions.
const (
	// JSON-RPC 2.0 reserved codes.
	CodeParseError     = -32700 // malformed JSON
	CodeInvalidRequest = -32600 // JSON ok but not a valid Request
	CodeMethodNotFound = -32601 // Method unknown to this plugin
	CodeInvalidParams  = -32602 // Params malformed for the named Method
	CodeInternal       = -32603 // server-side error

	// Plugin-specific application codes (–32099 to –32000 reserved range).
	CodeAuthFailed  = -32001 // API key missing/rejected
	CodeRateLimited = -32002 // upstream rate-limited
	CodeTimeout     = -32003 // request exceeded plugin's timeout
)
