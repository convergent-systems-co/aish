package persona

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetaStore_RecordAndLookup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ms, err := OpenMetaStore(dir)
	if err != nil {
		t.Fatalf("OpenMetaStore: %v", err)
	}
	if err := ms.Record("evt_aaaa", "mentor", "2026-05-21T00:00:00Z"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := ms.Record("evt_bbbb", "", "2026-05-21T00:00:01Z"); err != nil {
		t.Fatalf("Record (blank persona): %v", err)
	}
	if got, ok := ms.Lookup("evt_aaaa"); !ok || got != "mentor" {
		t.Errorf("Lookup(evt_aaaa) = (%q,%v); want (mentor,true)", got, ok)
	}
	if got, ok := ms.Lookup("evt_bbbb"); !ok || got != "default" {
		t.Errorf("Lookup(evt_bbbb) = (%q,%v); want (default,true) — empty must coerce to 'default'", got, ok)
	}
	if _, ok := ms.Lookup("evt_nope"); ok {
		t.Errorf("Lookup(evt_nope) ok=true; want false")
	}
}

func TestMetaStore_RehydratesOnReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ms, err := OpenMetaStore(dir)
	if err != nil {
		t.Fatalf("OpenMetaStore: %v", err)
	}
	if err := ms.Record("evt_xyz", "playful", "2026-05-21T01:00:00Z"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	// Reopen — the cache should reflect what landed on disk.
	ms2, err := OpenMetaStore(dir)
	if err != nil {
		t.Fatalf("Reopen MetaStore: %v", err)
	}
	if got, ok := ms2.Lookup("evt_xyz"); !ok || got != "playful" {
		t.Errorf("after reopen Lookup = (%q,%v); want (playful,true)", got, ok)
	}
}

func TestMetaStore_RecordRejectsEmptyID(t *testing.T) {
	t.Parallel()
	ms, err := OpenMetaStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenMetaStore: %v", err)
	}
	if err := ms.Record("", "mentor", ""); err == nil {
		t.Errorf("Record with empty event_id should fail")
	}
}

func TestMetaStore_EmptyDotAishIsNoOp(t *testing.T) {
	t.Parallel()
	// An empty dotAish disables on-disk persistence — Record updates the
	// in-memory map but writes nothing to disk.
	ms, err := OpenMetaStore("")
	if err != nil {
		t.Fatalf("OpenMetaStore(''): %v", err)
	}
	if err := ms.Record("evt_q", "mentor", ""); err != nil {
		t.Errorf("Record on empty-dotAish store: %v", err)
	}
	if got, ok := ms.Lookup("evt_q"); !ok || got != "mentor" {
		t.Errorf("Lookup after Record = (%q,%v); want (mentor,true)", got, ok)
	}
}

func TestMetaStore_PurgeKeepingIDs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ms, err := OpenMetaStore(dir)
	if err != nil {
		t.Fatalf("OpenMetaStore: %v", err)
	}
	for id, p := range map[string]string{
		"evt_keep1": "mentor",
		"evt_keep2": "playful",
		"evt_drop1": "default",
		"evt_drop2": "socratic",
	} {
		if err := ms.Record(id, p, ""); err != nil {
			t.Fatalf("Record %s: %v", id, err)
		}
	}
	keep := map[string]bool{"evt_keep1": true, "evt_keep2": true}
	if err := ms.PurgeKeepingIDs(keep); err != nil {
		t.Fatalf("PurgeKeepingIDs: %v", err)
	}
	for id, wantOK := range map[string]bool{
		"evt_keep1": true,
		"evt_keep2": true,
		"evt_drop1": false,
		"evt_drop2": false,
	} {
		if _, ok := ms.Lookup(id); ok != wantOK {
			t.Errorf("after purge Lookup(%s) ok=%v want %v", id, ok, wantOK)
		}
	}
	// On-disk file should reflect the purge.
	raw, err := os.ReadFile(filepath.Join(dir, HistoryMetaFileName))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	if strings.Contains(string(raw), "evt_drop1") || strings.Contains(string(raw), "evt_drop2") {
		t.Errorf("dropped IDs still in sidecar:\n%s", raw)
	}
}

// TestMetaStore_OnDiskFormat — one JSON object per line, parseable
// back into HistoryMeta. Belt-and-braces for the on-wire shape.
func TestMetaStore_OnDiskFormat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ms, err := OpenMetaStore(dir)
	if err != nil {
		t.Fatalf("OpenMetaStore: %v", err)
	}
	if err := ms.Record("evt_q", "mentor", "2026-05-21T05:00:00Z"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, HistoryMetaFileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d:\n%s", len(lines), raw)
	}
	var row HistoryMeta
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if row.EventID != "evt_q" || row.Persona != "mentor" {
		t.Errorf("row = %+v; want event_id=evt_q persona=mentor", row)
	}
}
