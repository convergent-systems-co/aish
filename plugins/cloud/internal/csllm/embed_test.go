package csllm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// TestEmbed_ReturnsErrEmbedNotImplemented pins the post-#178 behaviour:
// the CS gateway does not (yet) serve an embeddings endpoint, so the
// plugin's Embed call MUST return the typed sentinel
// proto.ErrEmbedNotImplemented without making any HTTP request. The
// caller (shell/internal/cache.Cache.Resolve) branches on
// errors.Is(err, proto.ErrEmbedNotImplemented) to skip the similarity
// branch — see plan §"Alternatives — embeddings-disable mechanism".
//
// The test wires a "fail-if-called" httptest.Server: any request that
// reaches it fails the test, proving Embed never sent one.
func TestEmbed_ReturnsErrEmbedNotImplemented(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Errorf("unexpected HTTP request to embed-disabled gateway: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(fakeAPIKey, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = c.Embed(context.Background(), proto.EmbedParams{Text: "list files"})
	if err == nil {
		t.Fatal("Embed: expected error, got nil")
	}
	if !errors.Is(err, proto.ErrEmbedNotImplemented) {
		t.Errorf("Embed: err = %v; want errors.Is(err, proto.ErrEmbedNotImplemented)", err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("Embed made %d HTTP request(s); want 0 (sentinel must short-circuit before transport)", got)
	}
}
