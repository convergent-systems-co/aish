package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConsent_MissingFile_WritesDefaultsAndReturnsThem(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	c := LoadConsent(home)
	if !c.OptInLocal {
		t.Errorf("default OptInLocal = false, want true")
	}
	if c.OptInAggregate {
		t.Errorf("default OptInAggregate = true, want false (privacy default)")
	}
	// The file should now exist with the documented header.
	data, err := os.ReadFile(filepath.Join(home, ConsentFilename))
	if err != nil {
		t.Fatalf("default file not written: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "opt_in_local") || !strings.Contains(body, "opt_in_aggregate") {
		t.Errorf("default file missing documented keys: %q", body)
	}
}

func TestLoadConsent_AggregateOptIn(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, ConsentFilename)
	if err := os.WriteFile(path, []byte("[telemetry]\nopt_in_local = true\nopt_in_aggregate = true\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := LoadConsent(home)
	if !c.OptInLocal {
		t.Errorf("OptInLocal = false, want true")
	}
	if !c.OptInAggregate {
		t.Errorf("OptInAggregate = false, want true")
	}
}

func TestLoadConsent_PartialFile_DefaultsMissingKeys(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	// Only opt_in_aggregate set; opt_in_local should default to true.
	path := filepath.Join(home, ConsentFilename)
	if err := os.WriteFile(path, []byte("[telemetry]\nopt_in_aggregate = true\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := LoadConsent(home)
	if !c.OptInLocal {
		t.Errorf("OptInLocal not defaulted to true: %+v", c)
	}
	if !c.OptInAggregate {
		t.Errorf("OptInAggregate not honored: %+v", c)
	}
}

func TestLoadConsent_MalformedFile_DefaultsToOptOut(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, ConsentFilename)
	if err := os.WriteFile(path, []byte("this is not toml { } [["), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := LoadConsent(home)
	// CRITICAL: malformed file MUST default to opt-out for aggregate.
	// This is the privacy guarantee in #39.
	if c.OptInAggregate {
		t.Errorf("malformed file enabled OptInAggregate — privacy violation: %+v", c)
	}
}

func TestLoadConsent_ExplicitOptOut(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, ConsentFilename)
	if err := os.WriteFile(path, []byte("[telemetry]\nopt_in_local = false\nopt_in_aggregate = false\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := LoadConsent(home)
	if c.OptInLocal {
		t.Errorf("explicit opt_in_local=false not honored: %+v", c)
	}
	if c.OptInAggregate {
		t.Errorf("OptInAggregate stayed false as expected: %+v", c)
	}
}

func TestLoadConsent_EmptyDir_ReturnsDefaults(t *testing.T) {
	t.Parallel()
	c := LoadConsent("")
	if c != DefaultConsent() {
		t.Errorf("empty dir = %+v, want %+v", c, DefaultConsent())
	}
}

func TestLoadConsent_ExistingFile_NotOverwritten(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, ConsentFilename)
	custom := "[telemetry]\nopt_in_local = false\nopt_in_aggregate = true\n# user's custom comment\n"
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = LoadConsent(home)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if string(got) != custom {
		t.Errorf("file was overwritten:\n got:  %q\n want: %q", string(got), custom)
	}
}
