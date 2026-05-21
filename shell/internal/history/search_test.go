package history

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStoreWithEvents(t *testing.T, cmds ...string) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "h.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for i, c := range cmds {
		ev := &Event{
			ID:        NewEventID(),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second).UTC(),
			Kind:      KindSnapshot,
			Command:   c,
		}
		if err := st.Append(ev); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		if err := st.Finalize(ev.ID, 0, 0); err != nil {
			t.Fatalf("Finalize %d: %v", i, err)
		}
	}
	return st
}

func TestStore_List(t *testing.T) {
	st := newTestStoreWithEvents(t,
		"rm /tmp/a",
		"rm -rf /tmp/b",
		"mv /tmp/c /tmp/d",
	)
	out, err := st.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("List len = %d, want 3", len(out))
	}
	// Newest first: the last Append should be at index 0.
	if got, want := out[0].Command, "mv /tmp/c /tmp/d"; got != want {
		t.Fatalf("List[0].Command = %q, want %q", got, want)
	}
}

func TestStore_List_LimitRespected(t *testing.T) {
	st := newTestStoreWithEvents(t,
		"rm /a", "rm /b", "rm /c", "rm /d", "rm /e",
	)
	out, err := st.List(2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("List len = %d, want 2", len(out))
	}
}

func TestStore_Get(t *testing.T) {
	st := newTestStoreWithEvents(t, "rm /tmp/x")
	all, _ := st.List(0)
	if len(all) != 1 {
		t.Fatalf("setup: want 1 event, got %d", len(all))
	}
	got, err := st.Get(all[0].ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || got.Command != "rm /tmp/x" {
		t.Fatalf("Get returned wrong event: %+v", got)
	}
	miss, err := st.Get("evt_does_not_exist")
	if err != nil {
		t.Fatalf("Get missing: %v", err)
	}
	if miss != nil {
		t.Fatalf("Get missing returned event")
	}
}

func TestStore_Search_FTS5(t *testing.T) {
	st := newTestStoreWithEvents(t,
		"rm /tmp/a",
		"mv /tmp/foo /tmp/bar",
		"rm -rf /tmp/snapshot-dir",
		"truncate -s 0 /tmp/x",
	)
	out, err := st.Search("snapshot", 0)
	if err != nil {
		t.Fatalf("Search snapshot: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("Search snapshot len = %d, want 1; results=%+v", len(out), commands(out))
	}
	if !contains(out[0].Command, "snapshot-dir") {
		t.Fatalf("Search snapshot got %q, want match on /tmp/snapshot-dir", out[0].Command)
	}
}

func TestStore_Search_SubstringFallbackOnSpecialChars(t *testing.T) {
	st := newTestStoreWithEvents(t,
		"rm /var/log/*.log",
		"rm /tmp/a",
	)
	// `*.log` is FTS5-special — the substring fallback should kick in.
	out, err := st.Search("*.log", 0)
	if err != nil {
		t.Fatalf("Search fallback: %v", err)
	}
	if len(out) == 0 {
		t.Fatalf("Search fallback returned 0 events; expected the rm /var/log/*.log row")
	}
}

func TestStore_Purge(t *testing.T) {
	st := newTestStoreWithEvents(t, "rm /a", "rm /b", "rm /c")
	all, _ := st.List(0)
	if len(all) != 3 {
		t.Fatalf("setup: want 3 events, got %d", len(all))
	}
	// Purge everything older than the timestamp of the most recent event.
	cutoff := all[0].Timestamp
	n, err := st.Purge(cutoff)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if n != 2 {
		t.Fatalf("Purge removed %d, want 2", n)
	}
	rest, _ := st.List(0)
	if len(rest) != 1 {
		t.Fatalf("after Purge len = %d, want 1", len(rest))
	}
}

func TestStore_Checkpoint(t *testing.T) {
	st := newTestStoreWithEvents(t, "rm /a")
	cp, err := st.Checkpoint("before-cleanup")
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if cp.Kind != KindCheckpoint || cp.Name != "before-cleanup" {
		t.Fatalf("Checkpoint event = %+v, want kind=checkpoint name=before-cleanup", cp)
	}
	got, err := st.CheckpointByName("before-cleanup")
	if err != nil {
		t.Fatalf("CheckpointByName: %v", err)
	}
	if got == nil || got.ID != cp.ID {
		t.Fatalf("CheckpointByName returned wrong event: %+v", got)
	}
	miss, err := st.CheckpointByName("does-not-exist")
	if err != nil {
		t.Fatalf("CheckpointByName missing: %v", err)
	}
	if miss != nil {
		t.Fatalf("CheckpointByName returned for non-existent name")
	}
}

func TestStore_Checkpoint_RejectsEmptyName(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "h.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if _, err := st.Checkpoint(""); err == nil {
		t.Fatalf("Checkpoint with empty name accepted")
	}
}

func TestStore_EventsSinceCheckpoint(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "h.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	// Pre-checkpoint event (must NOT show in EventsSinceCheckpoint).
	pre := &Event{
		ID: NewEventID(), Timestamp: time.Now().Add(-2 * time.Hour).UTC(),
		Kind: KindSnapshot, Command: "rm /pre",
	}
	if err := st.Append(pre); err != nil {
		t.Fatalf("Append pre: %v", err)
	}
	if err := st.Finalize(pre.ID, 0, 0); err != nil {
		t.Fatalf("Finalize pre: %v", err)
	}
	// Checkpoint.
	cp, err := st.Checkpoint("anchor")
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	// Post-checkpoint events.
	for i, c := range []string{"rm /post1", "rm /post2"} {
		ev := &Event{
			ID: NewEventID(), Timestamp: cp.Timestamp.Add(time.Duration(i+1) * time.Second).UTC(),
			Kind: KindSnapshot, Command: c,
		}
		if err := st.Append(ev); err != nil {
			t.Fatalf("Append post: %v", err)
		}
		if err := st.Finalize(ev.ID, 0, 0); err != nil {
			t.Fatalf("Finalize post: %v", err)
		}
	}
	got, err := st.EventsSinceCheckpoint(cp)
	if err != nil {
		t.Fatalf("EventsSinceCheckpoint: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("EventsSinceCheckpoint len = %d, want 2", len(got))
	}
	// Newest first ordering.
	if got[0].Command != "rm /post2" || got[1].Command != "rm /post1" {
		t.Fatalf("EventsSinceCheckpoint order wrong: %v", commands(got))
	}
}

func TestStore_SigningPopulatesColumns(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "h.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	sig, err := NewFileSigner(filepath.Join(dir, "history.key"))
	if err != nil {
		t.Fatalf("NewFileSigner: %v", err)
	}
	st.WithSigner(sig)
	ev := &Event{
		ID:        NewEventID(),
		Timestamp: time.Now().UTC(),
		Kind:      KindSnapshot,
		Command:   "rm /tmp/x",
	}
	if err := st.Append(ev); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := st.Finalize(ev.ID, 0, 0); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if ev.Signature == "" || ev.SignerID != LocalSignerID {
		t.Fatalf("event not signed in place: signer_id=%q signature_len=%d", ev.SignerID, len(ev.Signature))
	}
	out, _ := st.List(1)
	if len(out) != 1 || out[0].SignerID != LocalSignerID || out[0].Signature == "" {
		t.Fatalf("persisted event missing signature columns: %+v", out)
	}
}

// ---- helpers ----

func commands(es []*Event) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Command
	}
	return out
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
