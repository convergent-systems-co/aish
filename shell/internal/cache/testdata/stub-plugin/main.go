// Command stub-plugin is a minimal aish-inference-plugin used in
// cache/plugin_test.go to exercise the spawn-and-talk path without
// dragging the real cloud plugin (or the Anthropic API) into the
// shell test suite.
//
// Behaviour: read NDJSON Request envelopes on stdin. For each infer
// request emit two token frames followed by one complete frame, all
// tagged with the originating Request.ID. For each ping request emit
// one pong frame. Exit cleanly on stdin EOF.
//
// This file is BUILT FROM TestMain via `go build` into the test's
// tempdir; it never ships in any release binary.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

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
