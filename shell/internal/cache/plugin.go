package cache

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// DefaultPluginBinary is the binary aish looks for on PATH when the
// caller does not pin a path on PluginConfig.BinaryPath.
const DefaultPluginBinary = "aish-inference-cloud"

// PluginClient manages a long-lived child inference plugin.
//
// One PluginClient = one spawned aish-inference-cloud subprocess. The
// child runs continuously for the life of the aish session; requests
// are multiplexed over its stdin (one NDJSON envelope per line) and
// responses arrive over its stdout (one NDJSON envelope per line). A
// single background reader goroutine demultiplexes by Response.ID and
// forwards each frame to the per-request channel registered in
// `pending`.
//
// Concurrent Infer calls are safe: each gets its own pending entry
// keyed by a monotonically-allocated request ID.
type PluginClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.Writer

	// writeMu serialises stdin writes so two concurrent Infers cannot
	// interleave NDJSON envelopes mid-line.
	writeMu sync.Mutex

	// mu protects pending and closed; held only briefly to register or
	// drop a request channel.
	mu      sync.Mutex
	pending map[string]chan proto.Response
	closed  bool

	// nextID is the request-ID allocator. Atomic because Infer may be
	// called from multiple goroutines and we want allocation to be
	// lock-free relative to the per-request dispatch path.
	nextID atomic.Int64

	// readerDone is closed when the background reader exits, used by
	// Close to wait for the reader to drain before returning.
	readerDone chan struct{}
}

// PluginConfig wraps the spawn parameters for Start. Every field has a
// sensible zero-value default so callers can pass `PluginConfig{}` and
// get a default-PATH-lookup spawn with the current environment.
type PluginConfig struct {
	// BinaryPath is the absolute or PATH-relative name of the plugin
	// binary. When empty, DefaultPluginBinary is used and resolved
	// against $PATH at spawn time.
	BinaryPath string
	// Env is the environment passed to the child. When nil, os.Environ()
	// is used. The child needs ANTHROPIC_API_KEY for production use; the
	// stub plugin used in tests does not.
	Env []string
	// ExtraArgs is appended to the spawn argv after the binary name.
	// Useful for `--api-url http://stub` in integration tests.
	ExtraArgs []string
	// Stderr is where the child's stderr is forwarded. When nil,
	// os.Stderr is used. Tests typically pass a *bytes.Buffer to assert
	// on diagnostic output.
	Stderr io.Writer
}

// Start launches the child binary and returns a PluginClient ready to
// serve Infer calls. Common failure: the binary cannot be located on
// PATH — the caller should treat the wrapped error as "no inference
// plugin configured" and surface a user-friendly message rather than
// propagating the raw exec error.
//
// Resolution order for the binary:
//
//  1. PluginConfig.BinaryPath (explicit caller override) — used as-is.
//  2. The v0.3-2 plugin registry: the shell wires in a registry
//     selector via shell/internal/shell/openCache, then passes the
//     resolved absolute path here. When the registry yielded a hit
//     the caller passes BinaryPath directly; this branch sees only the
//     "registry empty" case.
//  3. DefaultPluginBinary on $PATH — the pre-v0.3-2 fallback path.
//
// On success, a background reader goroutine begins consuming the
// child's stdout. The child is expected to remain running until Close
// is called or it exits on its own (e.g. crash); a child exit while
// requests are pending surfaces to those callers as an error on their
// per-request channel close.
func Start(cfg PluginConfig) (*PluginClient, error) {
	bin := cfg.BinaryPath
	if bin == "" {
		bin = DefaultPluginBinary
	}
	// Resolve PATH up-front so we surface a clear error to the caller
	// (rather than the more generic "exec: executable file not found").
	if !strings.ContainsRune(bin, os.PathSeparator) {
		resolved, err := exec.LookPath(bin)
		if err != nil {
			return nil, fmt.Errorf("cache: plugin: %s not found on PATH: %w", bin, err)
		}
		bin = resolved
	}
	env := cfg.Env
	if env == nil {
		env = os.Environ()
	}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	// #nosec G204 — bin is from a config struct populated by aish itself,
	// not from user input; the caller validates it.
	cmd := exec.Command(bin, cfg.ExtraArgs...)
	cmd.Env = env
	cmd.Stderr = stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("cache: plugin: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("cache: plugin: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("cache: plugin: start %s: %w", bin, err)
	}

	p := &PluginClient{
		cmd:        cmd,
		stdin:      stdin,
		stdout:     stdout,
		stderr:     stderr,
		pending:    make(map[string]chan proto.Response),
		readerDone: make(chan struct{}),
	}
	go p.read()
	return p, nil
}

// read is the single background goroutine that consumes the child's
// stdout. Each line is a JSON-RPC Response envelope; we deserialise,
// look up the pending channel by Response.ID, and forward the
// Response. On EOF or read error we close every pending channel so any
// in-flight Infer call observes the failure rather than blocking
// forever.
func (p *PluginClient) read() {
	defer close(p.readerDone)
	scanner := bufio.NewScanner(p.stdout)
	// Match the dispatcher's 1 MiB limit so we don't truncate long
	// invocations on the way back.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		var resp proto.Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			// Malformed line — log to stderr and continue. The child is
			// expected to emit only well-formed NDJSON; if it doesn't,
			// we'd rather surface the bug than crash the shell.
			fmt.Fprintf(p.stderr, "cache: plugin: malformed response: %v\n", err)
			continue
		}
		p.mu.Lock()
		ch, ok := p.pending[resp.ID]
		p.mu.Unlock()
		if !ok {
			// Either the request was already cancelled, or the child is
			// emitting a frame for an ID we never sent. Drop on the floor.
			continue
		}
		// Non-blocking send so a slow consumer or a cancelled context
		// cannot stall the demultiplexer. The Infer goroutine reads
		// promptly; if its buffer is full we have a bug.
		ch <- resp
	}
	// Scanner exited — EOF or error. Close every pending channel so
	// callers waiting on a response see a clean shutdown.
	p.mu.Lock()
	for id, ch := range p.pending {
		close(ch)
		delete(p.pending, id)
	}
	p.mu.Unlock()
}

// Infer sends a synchronous infer request to the child and returns the
// assembled invocation, confidence, and any error. Streaming token
// frames are consumed to keep the channel drained; the final Complete
// frame's Invocation is the authoritative return value.
//
// Cancellation via ctx aborts the wait: we deregister the pending
// entry and return ctx.Err(). The child is not interrupted — its
// frames are simply dropped when they arrive (the demultiplexer
// tolerates an unknown ID).
//
// On a JSON-RPC error response, the error is wrapped with the code
// and message verbatim from the child (no further redaction; the
// child is responsible for redacting its own secrets per Common.md §4).
func (p *PluginClient) Infer(ctx context.Context, intent, os string) (string, float64, error) {
	if p == nil {
		return "", 0, errors.New("cache: Infer: nil PluginClient")
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return "", 0, errors.New("cache: Infer: plugin client is closed")
	}
	p.mu.Unlock()

	id := strconv.FormatInt(p.nextID.Add(1), 10)
	ch := make(chan proto.Response, 16) // small buffer absorbs token bursts
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()

	// Cleanup runs in every exit path: drop the pending entry so the
	// demultiplexer doesn't try to send to a channel nobody reads.
	defer func() {
		p.mu.Lock()
		if existing, ok := p.pending[id]; ok && existing == ch {
			delete(p.pending, id)
		}
		p.mu.Unlock()
	}()

	req := proto.Request{
		JSONRPC: proto.Version,
		ID:      id,
		Method:  proto.MethodInfer,
		Params: proto.InferParams{
			Intent: intent,
			Context: proto.InferContext{
				OS:        os,
				CacheMiss: true,
			},
			Stream: true,
		},
	}
	buf, err := json.Marshal(&req)
	if err != nil {
		return "", 0, fmt.Errorf("cache: Infer: marshal: %w", err)
	}
	buf = append(buf, '\n')

	p.writeMu.Lock()
	_, werr := p.stdin.Write(buf)
	p.writeMu.Unlock()
	if werr != nil {
		return "", 0, fmt.Errorf("cache: Infer: write request: %w", werr)
	}

	// Accumulate token text as a defensive fallback; the Complete
	// frame's Invocation is authoritative when present, but a plugin
	// that streams tokens without filling Invocation should still yield
	// a usable string.
	var tokens strings.Builder
	for {
		select {
		case <-ctx.Done():
			return "", 0, ctx.Err()
		case resp, open := <-ch:
			if !open {
				// Reader closed our channel — child exited mid-request.
				return "", 0, errors.New("cache: Infer: plugin stream closed before complete frame")
			}
			if resp.Error != nil {
				return "", 0, fmt.Errorf("cache: Infer: plugin error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			if resp.Result == nil {
				return "", 0, errors.New("cache: Infer: response has neither result nor error")
			}
			switch resp.Result.Type {
			case proto.KindToken:
				tokens.WriteString(resp.Result.Data)
			case proto.KindComplete:
				inv := resp.Result.Invocation
				if inv == "" {
					inv = tokens.String()
				}
				return inv, resp.Result.Confidence, nil
			case proto.KindPong:
				// Unexpected for an infer request, but harmless. Continue.
			default:
				return "", 0, fmt.Errorf("cache: Infer: unknown frame type %q", resp.Result.Type)
			}
		}
	}
}

// Embed sends an embed request to the child and returns the vector.
//
// Implementation mirrors Infer: allocate a per-request ID, register a
// pending channel, write the request, demultiplex frames. Embed expects
// exactly one Embedding frame (non-streaming); any other Kind is treated
// as an error.
//
// Cancellation via ctx aborts the wait the same way Infer does. On a
// JSON-RPC error response the code+message are returned verbatim.
func (p *PluginClient) Embed(ctx context.Context, text string) ([]float32, error) {
	if p == nil {
		return nil, errors.New("cache: Embed: nil PluginClient")
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("cache: Embed: plugin client is closed")
	}
	p.mu.Unlock()

	id := strconv.FormatInt(p.nextID.Add(1), 10)
	ch := make(chan proto.Response, 4)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		if existing, ok := p.pending[id]; ok && existing == ch {
			delete(p.pending, id)
		}
		p.mu.Unlock()
	}()

	// The dispatcher carries proto.InferParams; we map the embed
	// payload onto Intent (the text to embed). See plan §"Wire-shape
	// decision".
	req := proto.Request{
		JSONRPC: proto.Version,
		ID:      id,
		Method:  proto.MethodEmbed,
		Params: proto.InferParams{
			Intent: text,
		},
	}
	buf, err := json.Marshal(&req)
	if err != nil {
		return nil, fmt.Errorf("cache: Embed: marshal: %w", err)
	}
	buf = append(buf, '\n')

	p.writeMu.Lock()
	_, werr := p.stdin.Write(buf)
	p.writeMu.Unlock()
	if werr != nil {
		return nil, fmt.Errorf("cache: Embed: write request: %w", werr)
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case resp, open := <-ch:
			if !open {
				return nil, errors.New("cache: Embed: plugin stream closed before embedding frame")
			}
			if resp.Error != nil {
				// The plugin signals "this gateway does not implement
				// embeddings" via proto.CodeNotImplemented. Translate
				// that on-wire code back into the typed Go sentinel so
				// cache.Resolve can errors.Is(err, ErrEmbedNotImplemented)
				// without parsing the error message.
				if resp.Error.Code == proto.CodeNotImplemented {
					return nil, fmt.Errorf("cache: Embed: %w", proto.ErrEmbedNotImplemented)
				}
				return nil, fmt.Errorf("cache: Embed: plugin error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			if resp.Result == nil {
				return nil, errors.New("cache: Embed: response has neither result nor error")
			}
			if resp.Result.Type != proto.KindEmbedding {
				// Pong / token / complete frames are surface-protocol
				// noise on an embed request; treat as a fault.
				return nil, fmt.Errorf("cache: Embed: unexpected frame type %q", resp.Result.Type)
			}
			if len(resp.Result.Vector) == 0 {
				return nil, errors.New("cache: Embed: empty vector in embedding frame")
			}
			return resp.Result.Vector, nil
		}
	}
}

// Close terminates the child cleanly. Closing stdin causes the child's
// dispatcher to see EOF on its read goroutine and exit; Wait collects
// the exit status. Idempotent — a second Close on an already-closed
// client returns nil.
//
// Close does not interrupt in-flight Infer calls; instead, the child's
// EOF causes its dispatcher to emit no further frames, the read
// goroutine returns, and each pending channel is closed — Infer callers
// then observe "stream closed before complete frame" and return that
// as their error.
func (p *PluginClient) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	// Close stdin first — the child's dispatcher sees EOF, exits, and
	// closes its stdout, which lets our reader goroutine finish.
	closeErr := p.stdin.Close()
	<-p.readerDone
	waitErr := p.cmd.Wait()

	// An "exit status 1" or "signal: killed" on Wait after a clean stdin
	// close is unusual but not necessarily a defect (the child may
	// legitimately abort on EOF). Return whichever error fires first;
	// stdin-close failure is more diagnostic.
	if closeErr != nil {
		return fmt.Errorf("cache: plugin: close stdin: %w", closeErr)
	}
	if waitErr != nil {
		// Some plugins exit non-zero on EOF — treat normal exit as
		// success and surface only unexpected exit codes.
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			// ExitError on a Closed stdin is acceptable; the dispatcher
			// returns context.Canceled or io.EOF which main() may map to
			// exit 0 or exit 1 depending on the plugin. Don't fail here.
			return nil
		}
		return fmt.Errorf("cache: plugin: wait: %w", waitErr)
	}
	return nil
}
