package cache

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestResolve_WithSystemPromptSource_InjectsIntoInfer — v0.3-5 persona
// seam. When a non-nil systemPromptSource returns a non-empty string,
// the intent passed to plugin.Infer is the source's output followed
// by <user>...intent...</user>.
//
// The stub-plugin echoes the intent verbatim into the invocation
// ("echo <intent>"), so we can grep for the wrapped form in the
// resulting invocation.
func TestResolve_WithSystemPromptSource_InjectsIntoInfer(t *testing.T) {
	c := newCacheWithStub(t)
	c.WithSystemPromptSource(func() string {
		return "<persona-system>\nbe terse\n</persona-system>"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, _, err := c.Resolve(ctx, "list files", "darwin")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Stub plugin emits "echo <intent>" — so the persona-system block
	// should appear inside that echoed string.
	if !strings.Contains(got, "<persona-system>") {
		t.Errorf("invocation missing <persona-system> wrapper: %q", got)
	}
	if !strings.Contains(got, "be terse") {
		t.Errorf("invocation missing persona prompt text: %q", got)
	}
	if !strings.Contains(got, "<user>") || !strings.Contains(got, "list files") {
		t.Errorf("invocation missing <user> wrap or user intent: %q", got)
	}
}

// TestResolve_NilSystemPromptSource_UnchangedBehaviour — when no source
// is installed, Resolve calls plugin.Infer with the bare intent.
// Regression seatbelt for v0.1-2.
func TestResolve_NilSystemPromptSource_UnchangedBehaviour(t *testing.T) {
	c := newCacheWithStub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, _, err := c.Resolve(ctx, "list files", "darwin")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.Contains(got, "<persona-system>") {
		t.Errorf("nil source leaked persona wrapper into intent: %q", got)
	}
}

// TestResolve_EmptySystemPrompt_UnchangedBehaviour — when the source
// returns "" the cache treats it the same as nil source.
func TestResolve_EmptySystemPrompt_UnchangedBehaviour(t *testing.T) {
	c := newCacheWithStub(t)
	c.WithSystemPromptSource(func() string { return "" })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, _, err := c.Resolve(ctx, "list files", "darwin")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.Contains(got, "<persona-system>") {
		t.Errorf("empty source leaked persona wrapper: %q", got)
	}
}
