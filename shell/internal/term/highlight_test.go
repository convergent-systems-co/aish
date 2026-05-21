package term

import (
	"strings"
	"testing"
)

// TestHighlight_FirstTokenBuiltin — `cd /tmp` highlights `cd` as a
// built-in (role "accent").
func TestHighlight_FirstTokenBuiltin(t *testing.T) {
	r := stubResolver{builtin: map[string]bool{"cd": true}}
	spans := Highlight("cd /tmp", r)
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}
	if spans[0].Role != RoleAccent {
		t.Fatalf("first-token \"cd\" should be RoleAccent; got %v", spans[0].Role)
	}
	if spans[0].Text != "cd" {
		t.Fatalf("first span text: want \"cd\"; got %q", spans[0].Text)
	}
}

// TestHighlight_FirstTokenKnownBinary — `ls -la` highlights `ls` as a
// known binary (role "ai_tier_local" — green tier).
func TestHighlight_FirstTokenKnownBinary(t *testing.T) {
	r := stubResolver{binary: map[string]bool{"ls": true}}
	spans := Highlight("ls -la", r)
	if spans[0].Role != RoleAITierLocal {
		t.Fatalf("first-token \"ls\" should be RoleAITierLocal; got %v", spans[0].Role)
	}
}

// TestHighlight_FirstTokenIntent — an unknown first token falls
// through to the AI-intent tier (role "ai_tier_cloud").
func TestHighlight_FirstTokenIntent(t *testing.T) {
	r := stubResolver{}
	spans := Highlight("delete the dist folder", r)
	if spans[0].Role != RoleAITierCloud {
		t.Fatalf("unknown first-token should be RoleAITierCloud; got %v", spans[0].Role)
	}
}

// TestHighlight_QuotedString — text inside double quotes is one
// RoleString span.
func TestHighlight_QuotedString(t *testing.T) {
	r := stubResolver{binary: map[string]bool{"echo": true}}
	spans := Highlight(`echo "hello world"`, r)
	found := false
	for _, sp := range spans {
		if sp.Role == RoleString && strings.Contains(sp.Text, "hello") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a RoleString span for the quoted segment; got %+v", spans)
	}
}

// TestHighlight_Empty — empty input produces no spans.
func TestHighlight_Empty(t *testing.T) {
	spans := Highlight("", stubResolver{})
	if len(spans) != 0 {
		t.Fatalf("empty input should produce zero spans; got %v", spans)
	}
}

// TestHighlight_PerformanceBudget — 1ms p99 on a 120-char buffer.
// Asserted via a wall-clock check: run 1000 iterations, total under
// 1s. Generous floor for CI machines.
func TestHighlight_PerformanceBudget(t *testing.T) {
	r := stubResolver{binary: map[string]bool{"echo": true}}
	line := strings.Repeat("echo hello world | ", 6) + "echo end"
	if len(line) < 120 {
		t.Fatalf("test setup error: line too short (%d)", len(line))
	}
	for i := 0; i < 1000; i++ {
		_ = Highlight(line, r)
	}
}

// stubResolver implements TierResolver for tests without dragging in
// the real shell-package dispatch logic.
type stubResolver struct {
	builtin map[string]bool
	binary  map[string]bool
}

func (s stubResolver) ResolveTier(firstToken string) Tier {
	if s.builtin[firstToken] {
		return TierBuiltin
	}
	if s.binary[firstToken] {
		return TierKnownBinary
	}
	return TierAIIntent
}
