package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// sseEvent is the projection of the Anthropic Messages-API SSE payload
// the parser cares about. Only the fields the client actually uses are
// declared; unknown fields are tolerated and ignored.
type sseEvent struct {
	Type    string `json:"type"`
	Delta   *delta `json:"delta,omitempty"`
	Usage   *usage `json:"usage,omitempty"`
	Message *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *usage `json:"usage,omitempty"`
	} `json:"message,omitempty"`
}

type delta struct {
	Type       string `json:"type,omitempty"`
	Text       string `json:"text,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// pumpSSE consumes the streaming HTTP body, emits one proto.KindToken
// frame per content_block_delta event, and a single terminal
// proto.KindComplete frame on message_stop. The output channel is
// closed before returning. The function owns body and closes it.
//
// requestModel is the model id we sent in the request, used as the
// fallback when the upstream message_start does not echo a model.
//
// On ctx cancellation the pump returns promptly without leaking the
// background goroutine that owns body.
func pumpSSE(ctx context.Context, body io.ReadCloser, requestModel string, out chan<- proto.Frame) {
	defer close(out)
	defer body.Close()

	// We track tokens in arrival order so the Complete frame can carry
	// the assembled invocation. Anthropic streams content_block_delta
	// events with one text fragment each.
	var assembled strings.Builder
	var tokensIn, tokensOut int
	model := requestModel

	// scanReader reads SSE blocks. Each block is "event: ...\ndata:
	// ...\n\n". We only need the data line — the event-name is also
	// present inside the JSON payload as the "type" field.
	scanner := bufio.NewScanner(body)
	// Allow long data lines; Anthropic occasionally emits multi-KB
	// content_block_start events. 1 MiB is generous and well under
	// the JSON-RPC line-limit downstream.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

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
			// "event: foo" lines and any comments — ignore. The JSON
			// payload's "type" field is the source of truth.
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var ev sseEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			// Skip malformed payloads rather than tearing the stream
			// down — Anthropic occasionally interleaves heartbeats.
			continue
		}

		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				if ev.Message.Model != "" {
					model = ev.Message.Model
				}
				if ev.Message.Usage != nil {
					tokensIn = ev.Message.Usage.InputTokens
				}
			}
		case "content_block_delta":
			if ev.Delta == nil || ev.Delta.Text == "" {
				continue
			}
			assembled.WriteString(ev.Delta.Text)
			select {
			case out <- proto.Frame{Type: proto.KindToken, Data: ev.Delta.Text}:
			case <-ctx.Done():
				return
			}
		case "message_delta":
			// Anthropic emits final usage on message_delta.
			if ev.Usage != nil {
				if ev.Usage.OutputTokens > 0 {
					tokensOut = ev.Usage.OutputTokens
				}
				if ev.Usage.InputTokens > 0 {
					tokensIn = ev.Usage.InputTokens
				}
			}
		case "message_stop":
			invocation := assembled.String()
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
			return
		default:
			// content_block_start, content_block_stop, ping, etc. —
			// not relevant to frame emission. Ignored.
		}
	}

	// If we fell out of the loop without seeing message_stop, the
	// upstream stream closed early. Emit a terminal Complete frame so
	// the consumer is not blocked waiting. Confidence stays 1.0; the
	// invocation reflects whatever we assembled.
	if err := scanner.Err(); err == nil || err == io.EOF {
		invocation := assembled.String()
		if invocation == "" {
			return
		}
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
}
