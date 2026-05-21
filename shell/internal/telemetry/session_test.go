package telemetry

import (
	"regexp"
	"testing"
	"time"
)

func TestNewSessionID_Unique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		id, err := NewSessionID()
		if err != nil {
			t.Fatalf("NewSessionID: %v", err)
		}
		if seen[id] {
			t.Fatalf("NewSessionID collision after %d ids: %s", i, id)
		}
		seen[id] = true
	}
}

func TestNewSessionID_RFC4122v4(t *testing.T) {
	t.Parallel()
	// RFC 4122 v4: 8-4-4-4-12 hex, with version nibble '4' and
	// variant high bits in {8,9,a,b}.
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for i := 0; i < 16; i++ {
		id, err := NewSessionID()
		if err != nil {
			t.Fatalf("NewSessionID: %v", err)
		}
		if !pattern.MatchString(id) {
			t.Fatalf("ID %q does not match RFC 4122 v4 layout", id)
		}
	}
}

func TestFormatChronon_RoundTripsRFC3339Nano(t *testing.T) {
	t.Parallel()
	want := time.Date(2026, 5, 20, 14, 30, 22, 123456789, time.UTC)
	s := FormatChronon(want)
	got, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("round trip: got %v, want %v", got, want)
	}
}

func TestCounters_ZeroValue(t *testing.T) {
	t.Parallel()
	// Sanity: a Counters zero value is a meaningful empty record;
	// dashboard renderers should be able to display it as the
	// in-progress state for a brand-new session.
	var c Counters
	if c.Commands != 0 || c.CacheHits != 0 || c.CacheMisses != 0 ||
		c.InferenceCalls != 0 || c.FailedCommands != 0 || c.WallTimeMs != 0 {
		t.Errorf("zero Counters has non-zero fields: %+v", c)
	}
}
