package cache

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// stubBinary is the path to the compiled stub-plugin, populated by
// TestMain and consumed by every test in this file. Concurrent reads
// after TestMain finishes are safe; we only read after the m.Run()
// barrier.
var stubBinary string

// TestMain compiles testdata/stub-plugin into a tempdir once for the
// whole file's tests. The stub speaks the JSON-RPC NDJSON protocol on
// stdin/stdout — enough surface to exercise Start/Infer/Close end-to-end
// without dragging in the real aish-inference-cloud plugin (which would
// pull network, secrets, and rate-limited APIs into the unit suite).
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "cache-stub-plugin-*")
	if err != nil {
		panic("cache: TestMain: mkdir: " + err.Error())
	}
	defer os.RemoveAll(tmp)

	binName := "stub-plugin"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	stubBinary = filepath.Join(tmp, binName)

	// Find the testdata source. The test binary's working directory is
	// the package directory under the Go test harness convention.
	srcDir, err := filepath.Abs("testdata/stub-plugin")
	if err != nil {
		panic("cache: TestMain: abs: " + err.Error())
	}
	build := exec.Command("go", "build", "-o", stubBinary, srcDir)
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("cache: TestMain: build stub-plugin: " + err.Error())
	}

	os.Exit(m.Run())
}

func TestPluginStartAndInfer(t *testing.T) {
	// Use a discardable stderr — the exec package's child-goroutine
	// copy races with any in-test read on a *bytes.Buffer, and the
	// stderr content is not what this test is asserting on.
	plugin, err := Start(PluginConfig{BinaryPath: stubBinary, Stderr: io.Discard})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := plugin.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, conf, err := plugin.Infer(ctx, "hello world", "darwin")
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if want := "echo hello world"; got != want {
		t.Errorf("invocation = %q, want %q", got, want)
	}
	if conf <= 0 || conf > 1 {
		t.Errorf("confidence = %v, want in (0,1]", conf)
	}
}

func TestPluginInferIsConcurrent(t *testing.T) {
	// Issue multiple Infer calls in parallel against one plugin client.
	// The demultiplexer must route each response to the correct
	// per-request channel — a bug here would cross-wire results.
	plugin, err := Start(PluginConfig{BinaryPath: stubBinary})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer plugin.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const n = 8
	intents := make([]string, n)
	for i := range intents {
		intents[i] = "intent-" + string(rune('a'+i))
	}

	var wg sync.WaitGroup
	results := make([]string, n)
	errs := make([]error, n)
	for i, intent := range intents {
		wg.Add(1)
		go func(idx int, in string) {
			defer wg.Done()
			got, _, err := plugin.Infer(ctx, in, "darwin")
			results[idx] = got
			errs[idx] = err
		}(i, intent)
	}
	wg.Wait()
	for i, intent := range intents {
		if errs[i] != nil {
			t.Errorf("Infer[%d]: %v", i, errs[i])
			continue
		}
		want := "echo " + intent
		if results[i] != want {
			t.Errorf("Infer[%d] = %q, want %q (cross-wired?)", i, results[i], want)
		}
	}
}

func TestPluginCtxCancel(t *testing.T) {
	plugin, err := Start(PluginConfig{BinaryPath: stubBinary})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer plugin.Close()

	// Cancel before calling Infer — the call should return ctx.Err()
	// before the stub has a chance to respond.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = plugin.Infer(ctx, "some intent", "darwin")
	if err == nil {
		t.Fatal("Infer: expected error, got nil")
	}
	if ctx.Err() == nil {
		t.Fatalf("ctx.Err() = nil; want canceled. Infer returned: %v", err)
	}
}

func TestPluginBinaryNotFound(t *testing.T) {
	_, err := Start(PluginConfig{BinaryPath: "/nonexistent/aish-inference-please-do-not-exist-12345"})
	if err == nil {
		t.Fatal("Start: expected error for missing binary, got nil")
	}
}

func TestPluginPATHLookupMissing(t *testing.T) {
	// Empty BinaryPath + a PATH that contains nothing useful.
	t.Setenv("PATH", t.TempDir())
	_, err := Start(PluginConfig{})
	if err == nil {
		t.Fatal("Start: expected error for empty PATH, got nil")
	}
}

func TestPluginCloseIdempotent(t *testing.T) {
	plugin, err := Start(PluginConfig{BinaryPath: stubBinary})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := plugin.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := plugin.Close(); err != nil {
		t.Errorf("second Close (should be no-op): %v", err)
	}
}

func TestPluginEmbedRoundTrip(t *testing.T) {
	plugin, err := Start(PluginConfig{BinaryPath: stubBinary, Stderr: io.Discard})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := plugin.Embed(ctx, "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	want := stubEmbedHelper("hello world")
	if len(got) != len(want) {
		t.Fatalf("vector len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("vector[%d] = %v, want %v", i, got[i], want[i])
			break
		}
	}
}

func TestPluginEmbedIsConcurrent(t *testing.T) {
	plugin, err := Start(PluginConfig{BinaryPath: stubBinary, Stderr: io.Discard})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const n = 8
	intents := make([]string, n)
	for i := range intents {
		intents[i] = "embed-intent-" + string(rune('a'+i))
	}
	var wg sync.WaitGroup
	results := make([][]float32, n)
	errs := make([]error, n)
	for i, intent := range intents {
		wg.Add(1)
		go func(idx int, in string) {
			defer wg.Done()
			v, err := plugin.Embed(ctx, in)
			results[idx] = v
			errs[idx] = err
		}(i, intent)
	}
	wg.Wait()
	// Each result must match the deterministic stub vector for its
	// own intent — proves the demultiplexer didn't cross-wire.
	for i, intent := range intents {
		if errs[i] != nil {
			t.Errorf("Embed[%d]: %v", i, errs[i])
			continue
		}
		want := stubEmbedHelper(intent)
		if len(results[i]) != len(want) {
			t.Errorf("Embed[%d] vector len = %d, want %d", i, len(results[i]), len(want))
			continue
		}
		for j := range want {
			if results[i][j] != want[j] {
				t.Errorf("Embed[%d] vector[%d] = %v, want %v (cross-wired?)", i, j, results[i][j], want[j])
				break
			}
		}
	}
}

func TestPluginEmbedAfterClose(t *testing.T) {
	plugin, err := Start(PluginConfig{BinaryPath: stubBinary, Stderr: io.Discard})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := plugin.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := plugin.Embed(ctx, "intent"); err == nil {
		t.Error("Embed after Close: expected error, got nil")
	}
}

func TestPluginInferAfterClose(t *testing.T) {
	plugin, err := Start(PluginConfig{BinaryPath: stubBinary})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := plugin.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := plugin.Infer(ctx, "intent", "darwin"); err == nil {
		t.Error("Infer after Close: expected error, got nil")
	}
}
