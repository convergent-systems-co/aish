// Command aish-inference-cloud is the cloud inference plugin for aish.
// It reads JSON-RPC requests on stdin, dispatches to handlers, and
// writes NDJSON responses on stdout.
//
// Wire shape is Anthropic Messages API-compatible. The default endpoint
// is the Convergent Systems LLM gateway
// (api.convergent-systems.co/llm/v1), which fans out to Anthropic and
// other providers behind a single auth surface. Override with --api-url
// or $ANTHROPIC_BASE_URL when pointing at upstream Anthropic, a local
// proxy, or an httptest stub.
//
// Configuration:
//
//	ANTHROPIC_API_KEY   required; bearer key for the LLM gateway
//	ANTHROPIC_BASE_URL  optional; override the base URL (test stubs etc.)
//	AISH_COST_LOG       optional; path to the JSONL cost log
//
// Flags:
//
//	--version, -v   print version + build time and exit 0
//	--help, -h      print usage and exit 0
//	--api-url URL   override the base URL (wins over $ANTHROPIC_BASE_URL)
//
// On a missing $ANTHROPIC_API_KEY the binary writes a single-line error
// (no value, no env-dump) to stderr and exits 2. Panics are caught by a
// top-level firewall and exit 3 after redacting the API key from any
// stringified state.
//
// Typed errors from the Anthropic client (auth failed, rate limited,
// timeout) are surfaced as JSON-RPC error responses with the right
// proto.Code. Because rpc.Dispatcher collapses every handler error to
// CodeInternal, the MethodInfer handler writes the typed error
// response itself through a mutex-guarded stdout writer that the
// dispatcher also uses — preserving line atomicity without modifying
// the rpc package.
//
// See libs/proto/inference for the wire-protocol types.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
	"github.com/convergent-systems-co/aish/plugins/cloud/internal/anthropic"
	"github.com/convergent-systems-co/aish/plugins/cloud/internal/reliab"
	"github.com/convergent-systems-co/aish/plugins/cloud/internal/rpc"
)

// Build-time identity, populated via -ldflags by the Makefile.
var (
	version   = "dev"
	buildTime = "unknown"
)

// usage prints the long-form help text to w. Kept identical regardless
// of which flag triggered it so stdout is stable across --help and -h.
func usage(w io.Writer) {
	fmt.Fprintln(w, "aish-inference-cloud — Anthropic Cloud inference plugin for aish")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage: aish-inference-cloud [--api-url URL]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Reads JSON-RPC requests on stdin (NDJSON), writes responses on stdout.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --api-url URL        override the Anthropic base URL (wins over $ANTHROPIC_BASE_URL)")
	fmt.Fprintln(w, "  --version, -v        print version and exit")
	fmt.Fprintln(w, "  --help, -h           print this help and exit")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Env vars:")
	fmt.Fprintln(w, "  ANTHROPIC_API_KEY    required")
	fmt.Fprintln(w, "  ANTHROPIC_BASE_URL   optional (override endpoint)")
	fmt.Fprintln(w, "  AISH_COST_LOG        optional (default ~/.aish/cost-log.jsonl)")
}

// redactKey replaces every occurrence of key in s with "[REDACTED]".
// Returns s unchanged when key is empty (nothing to redact).
func redactKey(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "[REDACTED]")
}

// syncWriter is an io.Writer that serialises concurrent Write calls
// through a mutex. The dispatcher writes NDJSON responses through one
// reference; the MethodInfer handler writes typed-error responses
// through the same reference. Each Write is a complete NDJSON line,
// so the mutex preserves line atomicity even when both producers race.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (sw *syncWriter) Write(p []byte) (int, error) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.w.Write(p)
}

// writeErrorResponse emits one JSON-RPC error response with the given
// id, code, and message. Used by the MethodInfer handler to surface
// typed errors (CodeAuthFailed, CodeRateLimited, CodeTimeout) that the
// dispatcher would otherwise collapse to CodeInternal.
//
// The message MUST already be redacted of any secret; callers that
// build the message from upstream error text should pass it through
// redactKey first.
func writeErrorResponse(w io.Writer, id string, code int, message string) error {
	resp := proto.Response{
		JSONRPC: proto.Version,
		ID:      id,
		Error: &proto.Error{
			Code:    code,
			Message: message,
		},
	}
	buf, err := json.Marshal(&resp)
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	_, err = w.Write(buf)
	return err
}

func main() {
	// Capture the key once so the panic firewall (deferred below) can
	// redact it even if main() panics before we reach the redaction
	// helpers below.
	apiKey := os.Getenv("ANTHROPIC_API_KEY")

	// Top-level panic firewall. A panic anywhere below — dispatcher
	// goroutine, handler, encoder — bubbles up here. We log a redacted
	// summary to stderr and exit 3 so a supervisor can distinguish
	// "crashed" from "exited cleanly" or "exited with config error."
	defer func() {
		if r := recover(); r != nil {
			msg := redactKey(fmt.Sprintf("aish-inference-cloud: panic: %v", r), apiKey)
			fmt.Fprintln(os.Stderr, msg)
			os.Exit(3)
		}
	}()

	// --- Flag parsing -------------------------------------------------
	fs := flag.NewFlagSet("aish-inference-cloud", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { usage(os.Stderr) }

	var (
		showVersion bool
		showHelp    bool
		apiURL      string
	)
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&showVersion, "v", false, "print version and exit (shorthand)")
	fs.BoolVar(&showHelp, "help", false, "print help and exit")
	fs.BoolVar(&showHelp, "h", false, "print help and exit (shorthand)")
	fs.StringVar(&apiURL, "api-url", "", "override the Anthropic base URL (wins over $ANTHROPIC_BASE_URL)")

	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if showVersion {
		fmt.Printf("aish-inference-cloud %s (built %s)\n", version, buildTime)
		return
	}
	if showHelp {
		usage(os.Stdout)
		return
	}

	// --- Config resolution -------------------------------------------
	if apiKey == "" {
		// Common.md §4: no key value, no env-var noise in the error.
		fmt.Fprintln(os.Stderr, "aish-inference-cloud: ANTHROPIC_API_KEY is required")
		os.Exit(2)
	}
	baseURL := apiURL
	if baseURL == "" {
		baseURL = os.Getenv("ANTHROPIC_BASE_URL")
	}

	// --- Construct collaborators -------------------------------------
	client, err := anthropic.NewClient(apiKey, baseURL, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, redactKey(fmt.Sprintf("aish-inference-cloud: %v", err), apiKey))
		os.Exit(2)
	}

	// Cost-tracking is non-essential. A missing HOME or unwritable
	// path drops to a warning; the plugin still serves requests.
	cost, costErr := reliab.NewCostDefault(os.Environ())
	if costErr != nil {
		fmt.Fprintf(os.Stderr, "aish-inference-cloud: cost recorder disabled: %v\n", costErr)
		cost = nil
	}

	// Signal-aware context so Ctrl-C / SIGTERM drain the dispatcher
	// rather than aborting an in-flight stream.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Shared, line-atomic stdout. The dispatcher writes via its
	// internal bufio.Writer wrapping this; the MethodInfer handler
	// writes typed error responses directly. Each Write is a complete
	// NDJSON line, so mutex-guarding preserves wire correctness.
	out := &syncWriter{w: os.Stdout}

	d := rpc.NewDispatcher(os.Stdin, out, os.Stderr).WithContext(ctx)

	// --- MethodInfer --------------------------------------------------
	//
	// On success, forwards the upstream Frame channel verbatim and
	// records cost telemetry after the Complete frame.
	//
	// On typed failure (auth, rate-limit, timeout, internal), writes a
	// JSON-RPC error Response directly to the shared stdout writer
	// (bypassing the dispatcher's CodeInternal-only error path) and
	// returns a closed empty channel so the dispatcher emits nothing
	// further for this request.
	d.Register(proto.MethodInfer, inferHandler(client, cost, out, apiKey))

	// --- MethodPing ---------------------------------------------------
	//
	// Minimal liveness probe: emit exactly one Pong frame and close.
	d.Register(proto.MethodPing, func(_ context.Context, _ proto.InferParams) (<-chan proto.Frame, error) {
		ch := make(chan proto.Frame, 1)
		ch <- proto.Frame{Type: proto.KindPong}
		close(ch)
		return ch, nil
	})

	// --- MethodEmbed --------------------------------------------------
	//
	// Non-streaming: a single Embedding frame carrying Vector + Cost.
	// On typed failure, writes a JSON-RPC error response directly to
	// the shared stdout writer — same bypass as MethodInfer for the
	// dispatcher's CodeInternal-only error path.
	//
	// The dispatcher passes proto.InferParams; the handler maps
	// Intent -> Text and Model -> Model at the dispatch boundary per
	// the plan's "Wire-shape decision".
	d.Register(proto.MethodEmbed, embedHandler(client, cost, out, apiKey))

	// --- Run ----------------------------------------------------------
	if err := d.Run(); err != nil {
		fmt.Fprintln(os.Stderr, redactKey(fmt.Sprintf("aish-inference-cloud: %v", err), apiKey))
		os.Exit(1)
	}
}

// inferHandler builds the MethodInfer Handler closure. Extracted so the
// handler logic is testable in isolation if a future T-task needs it.
//
// The handler reads from the rpc.Dispatcher's per-request context. On
// upstream error, it writes a typed JSON-RPC error response directly to
// `out` (a line-atomic stdout proxy) — the dispatcher would otherwise
// collapse the typed code to CodeInternal.
//
// Limitation: the rpc.Handler signature does not surface the originating
// Request.ID to the handler. The integration tests assert ID echo on
// success frames (dispatcher does that) and assert Code on error frames
// (this function does that); the error response's ID is therefore left
// empty. Plan §"Sub-tasks → T1" explicitly scopes ID-correlation to the
// dispatcher's existing path; preserving it through the error bypass
// would require modifying internal/rpc, which is out of scope.
func inferHandler(client *anthropic.Client, cost *reliab.Cost, out io.Writer, apiKey string) rpc.Handler {
	return func(hctx context.Context, params proto.InferParams) (<-chan proto.Frame, error) {
		upstream, err := client.Infer(hctx, params)
		if err != nil {
			code, msg := classifyInferError(err)
			msg = redactKey(msg, apiKey)
			if werr := writeErrorResponse(out, "", code, msg); werr != nil {
				fmt.Fprintln(os.Stderr, redactKey(fmt.Sprintf("aish-inference-cloud: write error response: %v", werr), apiKey))
			}
			// Return a closed channel so the dispatcher emits nothing
			// further for this request. The dispatcher's frame loop
			// will see an immediate close and return cleanly.
			empty := make(chan proto.Frame)
			close(empty)
			return empty, nil
		}

		ch := make(chan proto.Frame)
		go func() {
			defer close(ch)
			for f := range upstream {
				select {
				case <-hctx.Done():
					return
				case ch <- f:
				}
				if f.Type == proto.KindComplete && cost != nil && f.Cost != nil {
					if recErr := cost.Record(f.Cost.Model, f.Cost.TokensIn, f.Cost.TokensOut, f.Cost.USD); recErr != nil {
						// Cost-record failure never breaks the response stream.
						fmt.Fprintln(os.Stderr, redactKey(fmt.Sprintf("aish-inference-cloud: cost.Record: %v", recErr), apiKey))
					}
				}
			}
		}()
		return ch, nil
	}
}

// classifyInferError maps an anthropic client error into a (code,
// message) pair suitable for a JSON-RPC error response. The default
// arm is CodeInternal so unrecognized error shapes do not leak through
// as success-zero values.
func classifyInferError(err error) (int, string) {
	var ce *anthropic.CodedError
	if errors.As(err, &ce) {
		return ce.Code, ce.Message
	}
	return proto.CodeInternal, err.Error()
}

// embedHandler builds the MethodEmbed Handler closure. The handler
// maps proto.InferParams -> proto.EmbedParams at the dispatch boundary
// (the dispatcher's Handler signature uses InferParams; we treat
// InferParams.Intent as the text-to-embed and InferParams.Model as the
// embedding model id). The plan's "Wire-shape decision" §note records
// this mapping.
//
// Error handling mirrors inferHandler: typed errors bypass the
// dispatcher's CodeInternal-only error path by writing a JSON-RPC
// error response directly to the shared (line-atomic) stdout writer.
func embedHandler(client *anthropic.Client, cost *reliab.Cost, out io.Writer, apiKey string) rpc.Handler {
	return func(hctx context.Context, params proto.InferParams) (<-chan proto.Frame, error) {
		embedParams := proto.EmbedParams{
			Text:  params.Intent,
			Model: params.Model,
		}
		result, err := client.Embed(hctx, embedParams)
		if err != nil {
			code, msg := classifyInferError(err)
			msg = redactKey(msg, apiKey)
			if werr := writeErrorResponse(out, "", code, msg); werr != nil {
				fmt.Fprintln(os.Stderr, redactKey(fmt.Sprintf("aish-inference-cloud: write error response: %v", werr), apiKey))
			}
			empty := make(chan proto.Frame)
			close(empty)
			return empty, nil
		}

		// Record cost for the embedding bucket. The model field
		// distinguishes embed (e.g. "voyage-3") from infer
		// (e.g. "claude-opus-4-7") in the aggregated cost log.
		if cost != nil && result.Cost != nil {
			if recErr := cost.Record(result.Cost.Model, result.Cost.TokensIn, result.Cost.TokensOut, result.Cost.USD); recErr != nil {
				fmt.Fprintln(os.Stderr, redactKey(fmt.Sprintf("aish-inference-cloud: cost.Record: %v", recErr), apiKey))
			}
		}

		ch := make(chan proto.Frame, 1)
		ch <- proto.Frame{
			Type:   proto.KindEmbedding,
			Vector: result.Vector,
			Cost:   result.Cost,
		}
		close(ch)
		return ch, nil
	}
}
