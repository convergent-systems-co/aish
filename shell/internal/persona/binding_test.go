package persona

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBinding_WriteReadRoundTrip(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := WriteBinding(home, "work", "mentor"); err != nil {
		t.Fatalf("WriteBinding: %v", err)
	}
	if got := ReadBinding(home, "work"); got != "mentor" {
		t.Errorf("ReadBinding(work) = %q; want mentor", got)
	}
}

func TestBinding_PreservesSiblings(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := WriteBinding(home, "work", "mentor"); err != nil {
		t.Fatalf("WriteBinding work: %v", err)
	}
	if err := WriteBinding(home, "personal", "playful"); err != nil {
		t.Fatalf("WriteBinding personal: %v", err)
	}
	all, err := AllBindings(home)
	if err != nil {
		t.Fatalf("AllBindings: %v", err)
	}
	if all["work"] != "mentor" || all["personal"] != "playful" {
		t.Errorf("AllBindings = %v; want work=mentor personal=playful", all)
	}
}

func TestBinding_RemoveOnEmptyPersona(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := WriteBinding(home, "work", "mentor"); err != nil {
		t.Fatal(err)
	}
	if err := WriteBinding(home, "work", ""); err != nil {
		t.Fatalf("WriteBinding empty: %v", err)
	}
	if got := ReadBinding(home, "work"); got != "" {
		t.Errorf("ReadBinding after remove = %q; want empty", got)
	}
}

func TestBinding_RejectsInvalidPersonaName(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := WriteBinding(home, "work", "Bad Name"); err == nil {
		t.Errorf("WriteBinding with invalid persona name should fail")
	}
}

func TestBinding_AbsentFileReadsEmpty(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if got := ReadBinding(home, "work"); got != "" {
		t.Errorf("ReadBinding on absent file = %q; want empty", got)
	}
}

func TestBinding_FileHasComment(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := WriteBinding(home, "work", "mentor"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ConfigDirName, BindingFileName))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !strings.Contains(string(raw), "# aish identity → persona bindings") {
		t.Errorf("missing header comment in bindings file:\n%s", raw)
	}
}
