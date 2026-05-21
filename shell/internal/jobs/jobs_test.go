package jobs

import (
	"strings"
	"testing"
)

// TestJobStatusString gates the bash-compatible status words used by
// `jobs`. If these strings drift the live smoke (the brief calls for
// `[1]+  Stopped sleep 30` etc.) breaks.
func TestJobStatusString(t *testing.T) {
	cases := map[JobStatus]string{
		StatusRunning: "Running",
		StatusStopped: "Stopped",
		StatusDone:    "Done",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("JobStatus(%d).String() = %q, want %q", int(s), got, want)
		}
	}
}

// TestJobTableAddAssignsMonotonicIDs gates the basic insertion contract:
// IDs are 1-based and increase per Add; current is the new job, prev
// is the old current.
func TestJobTableAddAssignsMonotonicIDs(t *testing.T) {
	jt := NewJobTable()
	j1 := jt.Add(100, 100, "sleep 30", StatusRunning)
	if j1.ID != 1 {
		t.Errorf("first Add ID = %d, want 1", j1.ID)
	}
	j2 := jt.Add(101, 101, "yes | head", StatusRunning)
	if j2.ID != 2 {
		t.Errorf("second Add ID = %d, want 2", j2.ID)
	}
	if jt.Current() != 2 {
		t.Errorf("Current = %d, want 2 (most-recent)", jt.Current())
	}
}

// TestJobTableFindBySpec gates the `%n` / `%+` / `%-` lookup grammar
// the `fg` and `bg` built-ins parse.
func TestJobTableFindBySpec(t *testing.T) {
	jt := NewJobTable()
	jt.Add(100, 100, "sleep 30", StatusRunning)   // %1
	jt.Add(101, 101, "yes | head", StatusStopped) // %2 (now current)

	for _, tc := range []struct {
		spec string
		want int
	}{
		{"%1", 1},
		{"%2", 2},
		{"%+", 2},
		{"%-", 1},
		{"", 2},  // default to current
		{"%", 2}, // bare %
		{"1", 1}, // bare-number for ergonomic callers
	} {
		j, ok := jt.Find(tc.spec)
		if !ok {
			t.Errorf("Find(%q) = not found", tc.spec)
			continue
		}
		if j.ID != tc.want {
			t.Errorf("Find(%q) = id %d, want %d", tc.spec, j.ID, tc.want)
		}
	}

	if _, ok := jt.Find("%99"); ok {
		t.Errorf("Find(%%99): unexpectedly found")
	}
	if _, ok := jt.Find("garbage"); ok {
		t.Errorf("Find(garbage): unexpectedly found")
	}
}

// TestJobTableSetStatusEnqueuesNoticeForBackground gates the prompt-
// notification contract: a background job transitioning to Done or
// Stopped emits a Notice; a foreground job does NOT (the REPL sees
// the change via Wait return directly).
func TestJobTableSetStatusEnqueuesNoticeForBackground(t *testing.T) {
	jt := NewJobTable()
	bg := jt.Add(100, 100, "sleep 30", StatusRunning)
	jt.SetStatus(bg.ID, StatusDone, 0)
	notices := jt.PendingNotices()
	if len(notices) != 1 {
		t.Fatalf("notices = %v, want exactly one", notices)
	}
	if notices[0].ID != bg.ID || notices[0].Status != StatusDone {
		t.Errorf("notice = %+v, want Done on id %d", notices[0], bg.ID)
	}
}

func TestJobTableSetStatusSkipsNoticeForForeground(t *testing.T) {
	jt := NewJobTable()
	fg := jt.AddForeground(200, 200, "sleep 1")
	jt.SetStatus(fg.ID, StatusDone, 0)
	notices := jt.PendingNotices()
	if len(notices) != 0 {
		t.Errorf("notices = %v, want none (foreground job)", notices)
	}
}

// TestJobTableReapResetsWhenEmpty matches bash: once the table empties
// the next assigned ID drops back to 1. This keeps `%1` meaningful for
// users who background a single job at a time.
func TestJobTableReapResetsWhenEmpty(t *testing.T) {
	jt := NewJobTable()
	j := jt.Add(100, 100, "sleep 30", StatusRunning)
	jt.SetStatus(j.ID, StatusDone, 0)
	removed := jt.Reap()
	if len(removed) != 1 || removed[0] != j.ID {
		t.Errorf("Reap returned %v, want [%d]", removed, j.ID)
	}
	next := jt.Add(200, 200, "sleep 60", StatusRunning)
	if next.ID != 1 {
		t.Errorf("post-reset Add ID = %d, want 1", next.ID)
	}
}

// TestJobTableIsForegroundGuardsAgainstReaperRace gates the no-double-
// reap invariant: a foreground job MUST be filtered out before the
// SIGCHLD reaper calls wait*.
func TestJobTableIsForegroundGuardsAgainstReaperRace(t *testing.T) {
	jt := NewJobTable()
	jt.Add(100, 100, "sleep 30", StatusRunning) // bg
	jt.AddForeground(200, 200, "sleep 1")       // fg
	if jt.IsForeground(100) {
		t.Errorf("pid 100 (bg) reported as foreground")
	}
	if !jt.IsForeground(200) {
		t.Errorf("pid 200 (fg) NOT reported as foreground")
	}
	jt.ClearForeground(200)
	if jt.IsForeground(200) {
		t.Errorf("ClearForeground(200): still foreground")
	}
}

// TestJobTableListRendersBashStyle ensures the output format matches
// bash. Spot-check: current job carries `+`, previous `-`, others ` `.
func TestJobTableListRendersBashStyle(t *testing.T) {
	jt := NewJobTable()
	jt.Add(100, 100, "sleep 30", StatusRunning)
	jt.Add(101, 101, "yes | head -n5", StatusStopped)
	jt.Add(102, 102, "tail -f /var/log/system.log", StatusRunning)
	lines := jt.List()
	if len(lines) != 3 {
		t.Fatalf("List() = %d lines, want 3", len(lines))
	}
	// Most-recent is [3]+, second-most [2]-, oldest [1] (space)
	wantFlags := map[string]string{
		"[1]": " ",
		"[2]": "-",
		"[3]": "+",
	}
	for prefix, flag := range wantFlags {
		var found string
		for _, l := range lines {
			if strings.HasPrefix(l, prefix) {
				found = l
				break
			}
		}
		if found == "" {
			t.Errorf("no line starting with %q", prefix)
			continue
		}
		marker := found[len(prefix) : len(prefix)+1]
		if marker != flag {
			t.Errorf("line %q: flag = %q, want %q", found, marker, flag)
		}
	}
}

// TestJobTableNoticesAreNonBlocking gates the reaper-side contract:
// even when the buffer is "full" (we don't realistically hit 32, but
// we test the policy), enqueue MUST NOT block. Dropped notices are
// acceptable — the live-smoke contract is "next prompt sees the
// recent change" not "see every state change ever."
func TestJobTableNoticesAreNonBlocking(t *testing.T) {
	jt := NewJobTable()
	for i := 0; i < 100; i++ {
		j := jt.Add(1000+i, 1000+i, "noise", StatusRunning)
		jt.SetStatus(j.ID, StatusDone, 0)
	}
	// The drain should yield <= 32 (buffer cap) notices; the point of
	// the test is that the writes did not deadlock.
	got := jt.PendingNotices()
	if len(got) == 0 {
		t.Errorf("PendingNotices returned 0; want some buffered notices")
	}
	if len(got) > 32 {
		t.Errorf("PendingNotices returned %d; buffer should cap at 32", len(got))
	}
}

// TestJobTableFindByPid gates the reaper's attribution path: a Wait4
// result names the pid; we map back to a Job.
func TestJobTableFindByPid(t *testing.T) {
	jt := NewJobTable()
	jt.Add(100, 100, "sleep 30", StatusRunning)
	j, ok := jt.FindByPid(100)
	if !ok {
		t.Fatalf("FindByPid(100) = not found")
	}
	if j.LeaderPid != 100 {
		t.Errorf("LeaderPid = %d, want 100", j.LeaderPid)
	}
	if _, ok := jt.FindByPid(9999); ok {
		t.Errorf("FindByPid(9999): unexpectedly found")
	}
}

// TestJobTableSnapshotIndependentOfTable ensures the snapshot used by
// Shell.Close() is a deep copy — mutating the returned slice MUST NOT
// affect the live table.
func TestJobTableSnapshotIndependentOfTable(t *testing.T) {
	jt := NewJobTable()
	jt.Add(100, 100, "sleep 30", StatusRunning)
	snap := jt.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("Snapshot len = %d, want 1", len(snap))
	}
	snap[0].Status = StatusDone
	live, _ := jt.Find("%1")
	if live.Status != StatusRunning {
		t.Errorf("mutation of snapshot bled through to table: %v", live.Status)
	}
}
