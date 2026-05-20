package shell

import (
	"bytes"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	if New() == nil {
		t.Fatal("New() returned nil")
	}
}

func TestRunSeed(t *testing.T) {
	// Seed-commit-level contract: Run accepts stdin/stdout/stderr and returns
	// without error when stdin is empty. The interactive REPL behaviour is
	// owned by the v0.1-1 coder sub-tasks; this test exists so the seed has
	// at least one passing test per Code.md §3.
	s := New()
	var out, errBuf bytes.Buffer
	if err := s.Run(strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("Run() returned error on empty stdin: %v", err)
	}
}
