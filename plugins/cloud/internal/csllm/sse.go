package csllm

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// openAIStreamChunk is the projection of one OpenAI chat-completions
// streaming chunk the parser cares about. The wire shape is:
//
//	data: {"id":"chatcmpl-...","object":"chat.completion.chunk",
//	       "choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},
//	                   "finish_reason":null}]}
//
// followed eventually by:
//
//	data: {"id":"...","choices":[{"delta":{},"finish_reason":"stop"}]}
//	data: [DONE]
//
// Only `choices[].delta.content` (the token text) and `finish_reason`
// are inspected; everything else is tolerated and ignored.
type openAIStreamChunk struct {
	ID      string         `json:"id,omitempty"`
	Model   string         `json:"model,omitempty"`
	Choices []openAIChoice `json:"choices,omitempty"`
	Usage   *openAIUsage   `json:"usage,omitempty"`
}

type openAIChoice struct {
	Index        int         `json:"index"`
	Delta        openAIDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason,omitempty"`
}

type openAIDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// openAIUsage is the optional usage block some OpenAI-compatible
// streaming endpoints append on the final chunk. Workers AI does not
// emit usage in v1 (worker.js returns zeros in the non-streaming path
// and nothing in the streaming path); the type stays here so future
// usage emission lands without a parser change.
type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// doneSentinel is the literal terminator the OpenAI streaming protocol
// sends as the last data line: `data: [DONE]`. The pump treats this as
// the authoritative end of the stream and emits the Complete frame.
const doneSentinel = "[DONE]"

// pumpSSE consumes the streaming HTTP body, emits one proto.KindToken
// frame per content-bearing delta, and a single terminal
// proto.KindComplete frame on the [DONE] sentinel. The output channel
// is closed before returning. The function owns body and closes it.
//
// requestModel is the model id we sent in the request, used as the
// fallback when the upstream chunks do not echo a model.
//
// On ctx cancellation the pump returns promptly without leaking the
// background goroutine that owns body.
func pumpSSE(ctx context.Context, body io.ReadCloser, requestModel string, out chan<- proto.Frame) {
	defer close(out)
	defer body.Close()

	// We track tokens in arrival order so the Complete frame can carry
	// the assembled invocation. Each non-empty delta.content fragment
	// becomes one Token frame plus a contribution to the assembly.
	var assembled strings.Builder
	var tokensOut int
	var tokensIn int
	model := requestModel

	scanner := bufio.NewScanner(body)
	// Allow long data lines; Workers AI occasionally emits multi-KB
	// chunks when usage telemetry lands on the same frame. 1 MiB is
	// generous and well under the JSON-RPC line-limit downstream.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	completeEmitted := false
	for scanner.Scan() {
		// Cooperative cancellation between lines. Once Body is closed
		// (deferred above), the Scan loop also terminates promptly.
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()
		if line == "" {
			// Block separator — ignore.
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			// SSE event-name or comment lines — ignore. OpenAI streams
			// do not use the `event:` line; only `data:` carries the
			// payload.
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == doneSentinel {
			// Authoritative terminator. Emit Complete and stop reading.
			emitComplete(ctx, out, model, assembled.String(), tokensIn, tokensOut)
			completeEmitted = true
			return
		}

		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// Skip malformed payloads rather than tearing the stream
			// down — Workers AI occasionally emits heartbeats during
			// long generations (and the worker may layer in non-JSON
			// keep-alive comments in future versions).
			continue
		}

		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Usage != nil {
			if chunk.Usage.PromptTokens > 0 {
				tokensIn = chunk.Usage.PromptTokens
			}
			if chunk.Usage.CompletionTokens > 0 {
				tokensOut = chunk.Usage.CompletionTokens
			}
		}

		for _, choice := range chunk.Choices {
			// Empty content (role-only delta on the first chunk, or
			// finish-reason-only delta on the last) is tolerated and
			// must NOT produce a Token frame. Only non-empty content
			// fragments are user-visible tokens.
			if choice.Delta.Content == "" {
				continue
			}
			assembled.WriteString(choice.Delta.Content)
			select {
			case out <- proto.Frame{Type: proto.KindToken, Data: choice.Delta.Content}:
			case <-ctx.Done():
				return
			}
		}
	}

	if completeEmitted {
		return
	}

	// If we fell out of the loop without seeing [DONE], the upstream
	// stream closed early. Callers MUST always observe a terminal
	// frame, so we synthesize one. Confidence is 0.0 to signal "stream
	// did not close cleanly," and Cost is populated from whatever
	// usage we observed (zero by default for Workers AI in v1).
	if err := scanner.Err(); err == nil || err == io.EOF {
		emitCompleteUnclean(ctx, out, model, assembled.String(), tokensIn, tokensOut)
	}
}

// emitComplete sends a clean Complete frame (Confidence 1.0) and
// returns; the caller is responsible for closing `out` (the deferred
// close in pumpSSE handles that).
func emitComplete(ctx context.Context, out chan<- proto.Frame, model, invocation string, tokensIn, tokensOut int) {
	cost := &proto.Cost{
		Model:     model,
		TokensIn:  tokensIn,
		TokensOut: tokensOut,
		USD:       estimateUSD(model, tokensIn, tokensOut),
	}
	select {
	case out <- proto.Frame{
		Type:       proto.KindComplete,
		Invocation: invocation,
		Confidence: 1.0,
		Cost:       cost,
	}:
	case <-ctx.Done():
	}
}

// emitCompleteUnclean is emitComplete with Confidence 0.0, used when
// the upstream stream closed without a [DONE] sentinel. The Cost is
// still populated so the cost-log path remains uniform across clean
// and unclean terminations.
func emitCompleteUnclean(ctx context.Context, out chan<- proto.Frame, model, invocation string, tokensIn, tokensOut int) {
	cost := &proto.Cost{
		Model:     model,
		TokensIn:  tokensIn,
		TokensOut: tokensOut,
		USD:       estimateUSD(model, tokensIn, tokensOut),
	}
	select {
	case out <- proto.Frame{
		Type:       proto.KindComplete,
		Invocation: invocation,
		Confidence: 0.0,
		Cost:       cost,
	}:
	case <-ctx.Done():
	}
}
