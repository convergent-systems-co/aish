package inference

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestZeroValueSafety — zero-value reads of every type must not panic.
// Plugins and consumers should be able to construct an empty Request and
// inspect its fields without nil-checking.
func TestZeroValueSafety(t *testing.T) {
	var req Request
	_ = req.JSONRPC
	_ = req.ID
	_ = req.Method
	_ = req.Params.Intent
	_ = req.Params.Context.CWD
	_ = req.Params.Stream

	var resp Response
	if resp.Result != nil || resp.Error != nil {
		t.Errorf("zero-value Response has non-nil Result/Error")
	}

	var frame Frame
	_ = frame.Type
	_ = frame.Data
	_ = frame.Invocation
}

// TestInferRequestRoundTrip — full request marshals + unmarshals into
// the same shape. Pins the JSON tag contract since plugins on the other
// end of the pipe deserialise via these tags.
func TestInferRequestRoundTrip(t *testing.T) {
	original := Request{
		JSONRPC: Version,
		ID:      "req_01",
		Method:  MethodInfer,
		Params: InferParams{
			Intent: "delete log files older than 30 days",
			Context: InferContext{
				CWD:            "/home/user/projects",
				OS:             "linux",
				HistorySummary: "recently ran git, ls",
				CacheMiss:      true,
			},
			Stream: true,
			Model:  "claude-opus-4-7",
		},
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Tag check: the marshalled JSON MUST use the wire-protocol names
	// (snake_case for context, camelCase for top-level per JSON-RPC).
	for _, want := range []string{
		`"jsonrpc":`,
		`"method":`,
		`"params":`,
		`"intent":`,
		`"cwd":`,
		`"history_summary":`,
		`"cache_miss":`,
		`"stream":`,
		`"model":`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("marshalled JSON missing key %q\nGot: %s", want, string(raw))
		}
	}

	var roundtripped Request
	if err := json.Unmarshal(raw, &roundtripped); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if roundtripped != original {
		t.Errorf("round-trip mismatch\nwant: %+v\ngot:  %+v", original, roundtripped)
	}
}

// TestFrameTokenAndCompleteShapes — both frame kinds marshal cleanly
// with only the fields each kind uses, and `omitempty` keeps the wire
// payload tight.
func TestFrameTokenAndCompleteShapes(t *testing.T) {
	tok := Frame{Type: KindToken, Data: "find"}
	raw, _ := json.Marshal(tok)
	got := string(raw)
	if !strings.Contains(got, `"type":"token"`) {
		t.Errorf("token frame missing type discriminator: %s", got)
	}
	// A token frame must NOT include invocation/confidence/cost in JSON.
	for _, unwanted := range []string{`"invocation":`, `"confidence":`, `"cost":`} {
		if strings.Contains(got, unwanted) {
			t.Errorf("token frame unexpectedly includes %q: %s", unwanted, got)
		}
	}

	complete := Frame{
		Type:       KindComplete,
		Invocation: "find . -name '*.log' -mtime +30 -delete",
		Confidence: 0.94,
		Cost: &Cost{
			Model:     "claude-opus-4-7",
			TokensIn:  120,
			TokensOut: 25,
			USD:       0.018,
		},
	}
	raw, _ = json.Marshal(complete)
	got = string(raw)
	for _, want := range []string{
		`"type":"complete"`,
		`"invocation":"find . -name '*.log' -mtime +30 -delete"`,
		`"confidence":0.94`,
		`"cost":`,
		`"tokens_in":120`,
		`"tokens_out":25`,
		`"usd":0.018`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("complete frame missing %q\nGot: %s", want, got)
		}
	}
}

// TestErrorEnvelope — a Response with an Error should marshal with
// no `result` key (omitempty), and the error code/message round-trip.
func TestErrorEnvelope(t *testing.T) {
	r := Response{
		JSONRPC: Version,
		ID:      "req_02",
		Error: &Error{
			Code:    CodeAuthFailed,
			Message: "API key missing",
		},
	}
	raw, _ := json.Marshal(r)
	got := string(raw)
	if strings.Contains(got, `"result":`) {
		t.Errorf("error response should omit result: %s", got)
	}
	if !strings.Contains(got, `"code":-32001`) {
		t.Errorf("CodeAuthFailed not serialised correctly: %s", got)
	}
	if !strings.Contains(got, `"message":"API key missing"`) {
		t.Errorf("error message missing: %s", got)
	}
}

// TestVersionConstantIsTwoDotZero — JSON-RPC 2.0 is the only protocol
// version this package speaks. Pinning the constant in a test prevents
// a future "let's bump to 2.1" surprise from sneaking through.
func TestVersionConstantIsTwoDotZero(t *testing.T) {
	if Version != "2.0" {
		t.Errorf("Version = %q, want %q (JSON-RPC 2.0)", Version, "2.0")
	}
}

// TestKindConstantsAreStable — the wire string values for Kind must NOT
// change without a coordinated plugin-side update.
func TestKindConstantsAreStable(t *testing.T) {
	cases := map[Kind]string{
		KindToken:    "token",
		KindComplete: "complete",
		KindPong:     "pong",
	}
	for k, want := range cases {
		if string(k) != want {
			t.Errorf("Kind %s = %q, want %q", want, string(k), want)
		}
	}
}

// TestErrorCodesAreStable — error codes are part of the wire contract.
// Plugins on the other side switch on these integers.
func TestErrorCodesAreStable(t *testing.T) {
	pairs := []struct {
		name string
		got  int
		want int
	}{
		{"CodeParseError", CodeParseError, -32700},
		{"CodeInvalidRequest", CodeInvalidRequest, -32600},
		{"CodeMethodNotFound", CodeMethodNotFound, -32601},
		{"CodeInvalidParams", CodeInvalidParams, -32602},
		{"CodeInternal", CodeInternal, -32603},
		{"CodeAuthFailed", CodeAuthFailed, -32001},
		{"CodeRateLimited", CodeRateLimited, -32002},
		{"CodeTimeout", CodeTimeout, -32003},
	}
	for _, p := range pairs {
		if p.got != p.want {
			t.Errorf("%s = %d, want %d", p.name, p.got, p.want)
		}
	}
}
