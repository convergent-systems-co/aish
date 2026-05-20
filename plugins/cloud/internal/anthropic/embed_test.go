package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// --- Embed: happy path ---------------------------------------------------

func TestEmbed_HappyPath_ReturnsVectorAndCost(t *testing.T) {
	wantVector := []float64{0.1, -0.2, 0.3, 0.4}
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		// Verify the request lands at the right path.
		if !strings.HasSuffix(r.URL.Path, "/embeddings") {
			t.Errorf("unexpected path %q; want suffix /embeddings", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// OpenAI-/Voyage-shaped embeddings response. Mirrors the public
		// embeddings API body shape (data[].embedding); the Convergent
		// Systems gateway speaks this shape on its /llm/v1/embeddings path.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"embedding": wantVector, "index": 0},
			},
			"model": "voyage-3",
			"usage": map[string]any{
				"prompt_tokens": 7,
				"total_tokens":  7,
			},
		})
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	got, err := c.Embed(context.Background(), proto.EmbedParams{
		Text:  "list files in cwd",
		Model: "voyage-3",
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got.Vector) != len(wantVector) {
		t.Fatalf("Vector len = %d, want %d", len(got.Vector), len(wantVector))
	}
	// JSON-decoded float64 → float32 narrowing introduces representation
	// noise on the order of 1e-7 for these test values. Compare with a
	// tolerance rather than equality.
	const tol = 1e-6
	for i, v := range wantVector {
		if d := math.Abs(float64(got.Vector[i]) - v); d > tol {
			t.Errorf("Vector[%d] = %v, want %v (delta %v > %v)", i, got.Vector[i], v, d, tol)
		}
	}
	if got.Cost == nil {
		t.Fatal("Cost is nil; want populated")
	}
	if got.Cost.Model == "" {
		t.Errorf("Cost.Model empty; want a model id")
	}
	if got.Cost.TokensIn != 7 {
		t.Errorf("Cost.TokensIn = %d, want 7", got.Cost.TokensIn)
	}
}

// --- Embed: 401 → CodeAuthFailed; no API-key leak -----------------------

func TestEmbed_401_ReturnsAuthFailed_NoKeyInError(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"bad key"}}`)
	})
	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, embErr := c.Embed(context.Background(), proto.EmbedParams{Text: "x"})
	ce := codedFromErr(t, embErr)
	if ce.Code != proto.CodeAuthFailed {
		t.Errorf("Code = %d, want %d (AuthFailed)", ce.Code, proto.CodeAuthFailed)
	}
	if strings.Contains(ce.Error(), fakeAPIKey) {
		t.Errorf("error message leaks API key: %q", ce.Error())
	}
}

// --- Embed: 429 → CodeRateLimited ---------------------------------------

func TestEmbed_429_ReturnsRateLimited(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
	})
	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, embErr := c.Embed(context.Background(), proto.EmbedParams{Text: "x"})
	ce := codedFromErr(t, embErr)
	if ce.Code != proto.CodeRateLimited {
		t.Errorf("Code = %d, want %d (RateLimited)", ce.Code, proto.CodeRateLimited)
	}
}

// --- Embed: 500 → CodeInternal ------------------------------------------

func TestEmbed_500_ReturnsInternal(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"boom"}}`)
	})
	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, embErr := c.Embed(context.Background(), proto.EmbedParams{Text: "x"})
	ce := codedFromErr(t, embErr)
	if ce.Code != proto.CodeInternal {
		t.Errorf("Code = %d, want %d (Internal)", ce.Code, proto.CodeInternal)
	}
}

// --- Embed: context deadline → CodeTimeout ------------------------------

func TestEmbed_CtxDeadline_ReturnsTimeout(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		// Drain the body so httptest.Server.Close() can return promptly
		// during t.Cleanup (same trick anthropic_test.go uses).
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-block:
		}
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, embErr := c.Embed(ctx, proto.EmbedParams{Text: "hang"})
	ce := codedFromErr(t, embErr)
	if ce.Code != proto.CodeTimeout {
		t.Errorf("Code = %d, want %d (Timeout)", ce.Code, proto.CodeTimeout)
	}
	if strings.Contains(ce.Error(), fakeAPIKey) {
		t.Errorf("error message leaks API key: %q", ce.Error())
	}
}

// --- Embed: API-key header carries the key ------------------------------

func TestEmbed_SendsAPIKeyHeader(t *testing.T) {
	var seenAuth atomic.Value
	var seenXAPI atomic.Value

	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		seenAuth.Store(r.Header.Get("Authorization"))
		seenXAPI.Store(r.Header.Get("x-api-key"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"embedding": []float64{0}, "index": 0}},
			"model": "voyage-3",
		})
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.Embed(context.Background(), proto.EmbedParams{Text: "probe"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	auth, _ := seenAuth.Load().(string)
	xapi, _ := seenXAPI.Load().(string)
	bearerOK := auth == "Bearer "+fakeAPIKey
	xapiOK := xapi == fakeAPIKey
	if !bearerOK && !xapiOK {
		t.Errorf("expected Authorization=Bearer or x-api-key to carry the key; got auth=%q x-api-key=%q",
			auth, xapi)
	}
}

// --- Embed: body shape — POST with text + model -------------------------

func TestEmbed_PostsTextAndModel(t *testing.T) {
	var captured atomic.Value // map[string]any
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Errorf("body unmarshal: %v (raw=%s)", err, string(body))
		}
		captured.Store(m)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"embedding": []float64{0.0}, "index": 0}},
			"model": "voyage-3",
		})
	})

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.Embed(context.Background(), proto.EmbedParams{
		Text:  "hello world",
		Model: "voyage-3",
	}); err != nil {
		t.Fatalf("Embed: %v", err)
	}

	m, _ := captured.Load().(map[string]any)
	if m == nil {
		t.Fatal("no body captured")
	}
	// Body must carry the text under the recognized "input" key
	// (OpenAI / Voyage / Convergent Systems gateway convention).
	gotText := ""
	switch v := m["input"].(type) {
	case string:
		gotText = v
	case []any:
		if len(v) == 1 {
			if s, ok := v[0].(string); ok {
				gotText = s
			}
		}
	}
	if gotText != "hello world" {
		t.Errorf("request body input text = %q, want %q (body=%+v)", gotText, "hello world", m)
	}
	if got, _ := m["model"].(string); got != "voyage-3" {
		t.Errorf("model = %q, want %q", got, "voyage-3")
	}
}

// --- Embed: empty vector in response → CodeInternal ---------------------

func TestEmbed_MalformedResponse_ReturnsInternal(t *testing.T) {
	srv := newServerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"data":[]}`)
	})
	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, embErr := c.Embed(context.Background(), proto.EmbedParams{Text: "x"})
	ce := codedFromErr(t, embErr)
	if ce.Code != proto.CodeInternal {
		t.Errorf("Code = %d, want %d (Internal)", ce.Code, proto.CodeInternal)
	}
}
