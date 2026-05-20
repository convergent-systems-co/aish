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
		KindToken:     "token",
		KindComplete:  "complete",
		KindPong:      "pong",
		KindEmbedding: "embedding",
	}
	for k, want := range cases {
		if string(k) != want {
			t.Errorf("Kind %s = %q, want %q", want, string(k), want)
		}
	}
}

// TestMethodEmbedConstantIsStable — plugins on the other side switch on
// this string. Pinning it in a test prevents a silent rename.
func TestMethodEmbedConstantIsStable(t *testing.T) {
	if MethodEmbed != "embed" {
		t.Errorf("MethodEmbed = %q, want %q", MethodEmbed, "embed")
	}
}

// TestEmbedParamsRoundTrip — the helper view marshals with snake_case
// fields. EmbedParams is not carried directly on the wire (the dispatcher
// uses InferParams), but a plugin that surfaces an embed-shaped payload
// to the user (e.g. in a JSONL log) needs the documented field names.
func TestEmbedParamsRoundTrip(t *testing.T) {
	original := EmbedParams{
		Text:  "list files in cwd",
		Model: "voyage-3",
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{
		`"text":"list files in cwd"`,
		`"model":"voyage-3"`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("marshalled EmbedParams missing %q\nGot: %s", want, string(raw))
		}
	}
	var rt EmbedParams
	if err := json.Unmarshal(raw, &rt); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if rt != original {
		t.Errorf("round-trip mismatch\nwant: %+v\ngot:  %+v", original, rt)
	}
}

// TestEmbedParams_ModelOmitempty — empty Model must not surface on the
// wire (lets the plugin pick its default without bloating the payload).
func TestEmbedParams_ModelOmitempty(t *testing.T) {
	raw, _ := json.Marshal(EmbedParams{Text: "x"})
	if strings.Contains(string(raw), `"model":`) {
		t.Errorf("empty Model leaked onto the wire: %s", string(raw))
	}
}

// TestFrameEmbeddingShape — a Frame with Type=embedding carries Vector
// + Cost and omits the infer-specific fields by virtue of omitempty.
func TestFrameEmbeddingShape(t *testing.T) {
	f := Frame{
		Type:   KindEmbedding,
		Vector: []float32{0.1, -0.2, 0.3},
		Cost: &Cost{
			Model:     "voyage-3",
			TokensIn:  10,
			TokensOut: 0,
			USD:       0.0001,
		},
	}
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(raw)
	for _, want := range []string{
		`"type":"embedding"`,
		`"vector":[0.1,-0.2,0.3]`,
		`"cost":`,
		`"model":"voyage-3"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("embedding frame missing %q\nGot: %s", want, got)
		}
	}
	// Embedding frames MUST NOT include infer-specific fields.
	for _, unwanted := range []string{`"data":`, `"invocation":`, `"confidence":`} {
		if strings.Contains(got, unwanted) {
			t.Errorf("embedding frame unexpectedly includes %q: %s", unwanted, got)
		}
	}

	// Round-trip the Vector to confirm float32 values survive intact.
	var rt Frame
	if err := json.Unmarshal(raw, &rt); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if rt.Type != KindEmbedding {
		t.Errorf("Type = %q, want %q", rt.Type, KindEmbedding)
	}
	if len(rt.Vector) != 3 {
		t.Fatalf("Vector len = %d, want 3", len(rt.Vector))
	}
	for i, v := range []float32{0.1, -0.2, 0.3} {
		if rt.Vector[i] != v {
			t.Errorf("Vector[%d] = %v, want %v", i, rt.Vector[i], v)
		}
	}
}

// TestFrameTokenOmitsVector — Vector must omitempty on non-embedding
// frames so the token-streaming hot path stays small on the wire.
func TestFrameTokenOmitsVector(t *testing.T) {
	tok := Frame{Type: KindToken, Data: "ls"}
	raw, _ := json.Marshal(tok)
	if strings.Contains(string(raw), `"vector":`) {
		t.Errorf("token frame leaked vector field: %s", string(raw))
	}
}

// TestEmbedResultRoundTrip — the helper result type round-trips with the
// expected tag names.
func TestEmbedResultRoundTrip(t *testing.T) {
	original := EmbedResult{
		Vector: []float32{1.5, 2.5},
		Model:  "voyage-3",
		Cost:   &Cost{Model: "voyage-3", TokensIn: 5, USD: 0.0001},
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{
		`"vector":[1.5,2.5]`,
		`"model":"voyage-3"`,
		`"cost":`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("marshalled EmbedResult missing %q\nGot: %s", want, string(raw))
		}
	}
	var rt EmbedResult
	if err := json.Unmarshal(raw, &rt); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if rt.Model != original.Model || len(rt.Vector) != 2 {
		t.Errorf("round-trip mismatch: got %+v", rt)
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
