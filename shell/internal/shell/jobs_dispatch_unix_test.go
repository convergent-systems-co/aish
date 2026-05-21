//go:build !windows

package shell

import (
	"bytes"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/jobs"
)

// killSignal returns SIGKILL on Unix. Tests use this to clean up
// child sleep processes deterministically.
func killSignal() syscall.Signal { return syscall.SIGKILL }

// TestBackgroundDispatchSpawnsAndReports gates the headline contract:
// `sleep 0.1 &` returns immediately, the JobTable lists it, and the
// stderr line matches bash's `[1] <pid>` format. Uses a real `sleep`
// child (per the task brief: NEVER signal the test process).
func TestBackgroundDispatchSpawnsAndReports(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep(1) not on PATH; skipping job-control smoke")
	}
	s := New()
	defer s.Close()
	var stdout, stderr bytes.Buffer
	if err := s.dispatch("sleep 0.1 &", strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(stderr.String(), "[1]") {
		t.Errorf("stderr = %q, want `[1] <pid>` line", stderr.String())
	}
	job, ok := s.jobTable.Find("%1")
	if !ok {
		t.Fatalf("JobTable: %%1 not found after `sleep &`")
	}
	if job.Status != jobs.StatusRunning {
		t.Errorf("Status = %v, want Running", job.Status)
	}
	if job.Pgid <= 0 {
		t.Errorf("Pgid = %d, want > 0", job.Pgid)
	}
	// Wait for the sleep child to finish naturally (it sleeps 0.1s).
	// The reaper goroutine will flip the JobTable to Done.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, ok := s.jobTable.Find("%1")
		if !ok || j.Status == jobs.StatusDone {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("background sleep did not complete within deadline")
}

// TestBackgroundDoneNoticeAppears verifies that after a background
// job finishes, a Done notice surfaces via drainJobNotices. This is
// the path the REPL uses to print `[1]+  Done  sleep 0.1` on the
// next prompt.
func TestBackgroundDoneNoticeAppears(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep(1) not on PATH; skipping job-control smoke")
	}
	s := New()
	defer s.Close()
	var stdout, stderr bytes.Buffer
	if err := s.dispatch("sleep 0.05 &", strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// Wait for reaper.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, ok := s.jobTable.Find("%1")
		if !ok || j.Status == jobs.StatusDone {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var noticeOut bytes.Buffer
	s.drainJobNotices(&noticeOut)
	out := noticeOut.String()
	if !strings.Contains(out, "[1]") || !strings.Contains(out, "Done") {
		t.Errorf("drainJobNotices output = %q, want `[1] ... Done` line", out)
	}
	// After drain, the table should be empty.
	if list := s.jobTable.List(); len(list) != 0 {
		t.Errorf("JobTable.List() after drain = %v, want empty", list)
	}
}

// TestJobsBuiltinListsBackgroundJobs covers the `jobs` built-in with
// a live job in the table.
func TestJobsBuiltinListsBackgroundJobs(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep(1) not on PATH; skipping job-control smoke")
	}
	s := New()
	defer s.Close()
	var dispOut, dispErr bytes.Buffer
	if err := s.dispatch("sleep 1 &", strings.NewReader(""), &dispOut, &dispErr); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	defer func() {
		// Kill the sleep so the test exits fast.
		if j, ok := s.jobTable.Find("%1"); ok {
			_ = jobs.SendSignal(j.Pgid, killSignal())
		}
	}()
	var stdout, stderr bytes.Buffer
	if rc := s.jobsBuiltin(nil, &stdout, &stderr); rc != 0 {
		t.Fatalf("jobsBuiltin rc = %d, want 0; stderr = %q", rc, stderr.String())
	}
	if !strings.Contains(stdout.String(), "sleep 1") {
		t.Errorf("jobs output = %q, want sleep 1 in listing", stdout.String())
	}
}

// TestBgBuiltinResumesStoppedJob exercises `bg` against a known-
// stopped synthetic job. We DON'T actually SIGSTOP a child process
// because that would be flaky on CI; instead we register a job
// manually with Status=Stopped, run bg, and assert SIGCONT was
// "attempted" (we expect ESRCH since the pgid is fake, which we
// suppress per builtin_bg_unix.go).
func TestBgBuiltinResumesStoppedJob(t *testing.T) {
	s := New()
	defer s.Close()
	// Use a deliberately-not-running pid so SIGCONT returns ESRCH;
	// the builtin should swallow ESRCH and proceed.
	s.jobTable.Add(9999999, 9999999, "fake stopped job", jobs.StatusStopped)
	var stdout, stderr bytes.Buffer
	rc := s.bgBuiltin([]string{"%1"}, &stdout, &stderr)
	if rc != 0 {
		t.Errorf("bgBuiltin rc = %d, want 0; stderr = %q", rc, stderr.String())
	}
	j, _ := s.jobTable.Find("%1")
	if j.Status != jobs.StatusRunning {
		t.Errorf("after bg: Status = %v, want Running", j.Status)
	}
	if !strings.Contains(stdout.String(), "fake stopped job") {
		t.Errorf("bg stdout = %q, want command echoed", stdout.String())
	}
}

// TestFgBuiltinMissingJob covers the error path when the user
// references a job that doesn't exist.
func TestFgBuiltinMissingJob(t *testing.T) {
	s := New()
	defer s.Close()
	var stdout, stderr bytes.Buffer
	rc := s.fgBuiltin([]string{"%99"}, strings.NewReader(""), &stdout, &stderr)
	if rc == 0 {
		t.Errorf("fgBuiltin rc = 0, want non-zero on missing job")
	}
	if !strings.Contains(stderr.String(), "no such job") {
		t.Errorf("stderr = %q, want `no such job` message", stderr.String())
	}
}
