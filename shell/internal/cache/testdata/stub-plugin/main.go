// Command stub-plugin is a minimal aish-inference-plugin used in
// cache/plugin_test.go to exercise the spawn-and-talk path without
// dragging the real cloud plugin (or the Anthropic API) into the
// shell test suite.
//
// Behaviour: read NDJSON Request envelopes on stdin. For each infer
// request emit two token frames followed by one complete frame. For
// each ping request emit one pong frame. For each embed request emit
// one deterministic embedding frame derived from the intent text.
// Exit cleanly on stdin EOF.
//
// This file is BUILT FROM TestMain via `go build` into the test's
// tempdir; it never ships in any release binary.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// StubEmbed is exported (capitalised) so the shell test package can
// call it to compute the expected vector for a given intent — keeping
// the stub and the test in lock-step without each side re-implementing
// the same hash construction.
//
// Construction: sha256(text) → 8 chunks of 4 bytes → 8 float32 values
// in [-1, 1] → L2-normalized so cosine similarity is well-defined.
func StubEmbed(text string) []float32 {
	hash := sha256.Sum256([]byte(text))
	v := make([]float32, 8)
	for i := 0; i < 8; i++ {
		u := binary.LittleEndian.Uint32(hash[i*4:])
		v[i] = float32(u)/float32(math.MaxUint32)*2 - 1
	}
	var sum float64
	for _, f := range v {
		sum += float64(f) * float64(f)
	}
	if sum == 0 {
		return v
	}
	norm := float32(math.Sqrt(sum))
	for i := range v {
		v[i] /= norm
	}
	return v
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req proto.Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			// Emit a parse-error response and continue.
			_ = enc.Encode(&proto.Response{
				JSONRPC: proto.Version,
				Error:   &proto.Error{Code: proto.CodeParseError, Message: err.Error()},
			})
			continue
		}
		switch req.Method {
		case proto.MethodPing:
			pong := proto.Frame{Type: proto.KindPong}
			_ = enc.Encode(&proto.Response{JSONRPC: proto.Version, ID: req.ID, Result: &pong})
		case proto.MethodInfer:
			// Emit two token frames + a complete frame. The completed
			// invocation echoes the intent so the test can assert the
			// channel actually carried our request through.
			t1 := proto.Frame{Type: proto.KindToken, Data: "echo "}
			t2 := proto.Frame{Type: proto.KindToken, Data: req.Params.Intent}
			c := proto.Frame{
				Type:       proto.KindComplete,
				Invocation: "echo " + req.Params.Intent,
				Confidence: 0.9,
			}
			_ = enc.Encode(&proto.Response{JSONRPC: proto.Version, ID: req.ID, Result: &t1})
			_ = enc.Encode(&proto.Response{JSONRPC: proto.Version, ID: req.ID, Result: &t2})
			_ = enc.Encode(&proto.Response{JSONRPC: proto.Version, ID: req.ID, Result: &c})
		case proto.MethodEmbed:
			// Emit one embedding frame with a deterministic vector
			// derived from the intent text — lets tests reason about
			// similarity outcomes without floating-point flakiness.
			e := proto.Frame{
				Type:   proto.KindEmbedding,
				Vector: StubEmbed(req.Params.Intent),
				Cost: &proto.Cost{
					Model:    "stub-embed-v1",
					TokensIn: len(req.Params.Intent),
				},
			}
			_ = enc.Encode(&proto.Response{JSONRPC: proto.Version, ID: req.ID, Result: &e})
		default:
			_ = enc.Encode(&proto.Response{
				JSONRPC: proto.Version,
				ID:      req.ID,
				Error:   &proto.Error{Code: proto.CodeMethodNotFound, Message: "unknown method"},
			})
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "stub-plugin:", err)
		os.Exit(1)
	}
}
