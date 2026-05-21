package translate

import (
	"bytes"
	"strings"
	"testing"
)

func TestMigrateDeterministic(t *testing.T) {
	script := &Script{
		Dialect: DialectFish,
		Statements: []Statement{
			Comment{Text: "# header"},
			Assign{Name: "name", Value: "world", Exported: true},
			Command{Name: "echo", Args: []string{"hi"}},
		},
	}
	var b1, b2 bytes.Buffer
	if err := Migrate(&b1, script); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := Migrate(&b2, script); err != nil {
		t.Fatalf("Migrate (2nd run): %v", err)
	}
	if b1.String() != b2.String() {
		t.Errorf("Migrate not deterministic")
	}
	out := b1.String()
	for _, want := range []string{"#!/usr/bin/env aish", "# header", "export name=world", "echo hi"} {
		if !strings.Contains(out, want) {
			t.Errorf("Migrate output missing %q in:\n%s", want, out)
		}
	}
}

func TestMigratePreservesComments(t *testing.T) {
	script := &Script{
		Statements: []Statement{
			Comment{Text: "# important context"},
			Command{Name: "ls"},
			Comment{Text: "# trailing"},
		},
	}
	var buf bytes.Buffer
	if err := Migrate(&buf, script); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !strings.Contains(buf.String(), "# important context") {
		t.Errorf("leading comment lost: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "# trailing") {
		t.Errorf("trailing comment lost: %s", buf.String())
	}
}

func TestMigrateSurfacesUnknownAsTODO(t *testing.T) {
	script := &Script{
		Statements: []Statement{
			Unknown{Reason: "heredoc unsupported", Source: "cat <<EOF"},
		},
	}
	var buf bytes.Buffer
	_ = Migrate(&buf, script)
	if !strings.Contains(buf.String(), "MIGRATE-TODO") {
		t.Errorf("Unknown not surfaced as MIGRATE-TODO: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "heredoc unsupported") {
		t.Errorf("MIGRATE-TODO missing reason: %s", buf.String())
	}
}
