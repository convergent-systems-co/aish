package theme

import (
	"fmt"
	"testing"
	"time"

	proto "github.com/convergent-systems-co/aish/libs/proto/theme"
)

// makeFullRegistry returns a registry pre-loaded with N synthetic
// compiled themes plus the bundled set. Used to simulate the
// post-sync state when many theme-atoms.com brands are loaded.
func makeFullRegistry(b *testing.B, n int) *Registry {
	b.Helper()
	r := NewRegistry()
	for i := 0; i < n; i++ {
		t, err := Compile(proto.Brand{
			Name: fmt.Sprintf("synthetic-%04d", i),
			Type: "shell",
			Palette: proto.Palette{
				"primary": "#88c0d0",
				"accent":  "#a3be8c",
			},
			Roles: proto.Roles{
				"prompt":  "$palette.primary",
				"accent":  "$palette.accent",
				"primary": "$palette.primary",
			},
			Glyphs: proto.Glyphs{
				Static: map[string]string{"prompt_char": "❯"},
			},
			Prompt: proto.PromptConfig{
				Segments:   []string{"cwd", "prompt"},
				Separators: "minimal",
			},
		})
		if err != nil {
			b.Fatalf("compile synthetic theme: %v", err)
		}
		r.Add(t)
	}
	return r
}

// BenchmarkSetActive measures the cost of `Registry.SetActive(name)`
// across a registry holding 100 themes. The design says this is a
// single-pointer swap under a mutex; the benchmark proves the design.
func BenchmarkSetActive(b *testing.B) {
	r := makeFullRegistry(b, 100)
	names := r.List() // sorted; deterministic
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.SetActive(names[i%len(names)])
	}
}

// BenchmarkColorPrompt measures the cost of `ColorPrompt(s)` over a
// compiled theme. GOALS demands sub-microsecond per-character render;
// pre-compiled ANSI escapes + string concat is what makes that achievable.
func BenchmarkColorPrompt(b *testing.B) {
	tm, _ := Compile(proto.Brand{
		Name:    "bench",
		Palette: proto.Palette{"primary": "#88c0d0"},
		Roles:   proto.Roles{"prompt": "$palette.primary"},
	})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tm.ColorPrompt("x")
	}
}

// TestSetActive_PerformanceBudget — #81 hard gate.
//
// Target: 50ms ceiling for a SetActive call. The design budget is
// orders of magnitude tighter (single pointer swap), but we don't want
// to chase nanoseconds on a CI box of unknown beef. 50ms is the loose
// outer envelope GOALS promises; <100µs is the design's actual claim,
// asserted as the secondary gate.
func TestSetActive_PerformanceBudget(t *testing.T) {
	r := makeFullRegistry_T(t, 100)
	names := r.List()

	const trials = 1000
	start := time.Now()
	for i := 0; i < trials; i++ {
		if err := r.SetActive(names[i%len(names)]); err != nil {
			t.Fatalf("SetActive: %v", err)
		}
	}
	elapsed := time.Since(start)
	perOp := elapsed / trials
	if perOp > 50*time.Millisecond {
		t.Fatalf("SetActive per-op = %v, want < 50ms (GOALS sub-50ms theme switch)", perOp)
	}
	if perOp > 100*time.Microsecond {
		t.Logf("SetActive per-op = %v — over the 100µs soft target but within 50ms hard target", perOp)
	}
}

// TestColorPrompt_PerformanceBudget — #81 hard gate.
//
// Target: <1µs per ColorPrompt call. GOALS calls for sub-microsecond
// per-character render; this is the unit op that the prompt walks for
// every visible character.
//
// We measure 100,000 ops in a tight loop and divide; that smooths over
// scheduler jitter on slow CI boxes while still flagging a regression
// that broke the design (e.g. an accidental fmt.Sprintf inside the
// render path).
func TestColorPrompt_PerformanceBudget(t *testing.T) {
	tm, _ := Compile(proto.Brand{
		Name:    "perf",
		Palette: proto.Palette{"primary": "#88c0d0"},
		Roles:   proto.Roles{"prompt": "$palette.primary"},
	})
	const ops = 100_000
	start := time.Now()
	for i := 0; i < ops; i++ {
		_ = tm.ColorPrompt("x")
	}
	elapsed := time.Since(start)
	perOp := elapsed / ops
	if perOp > time.Microsecond {
		t.Fatalf("ColorPrompt per-op = %v, want < 1µs (GOALS sub-microsecond per-char render)", perOp)
	}
}

// makeFullRegistry_T is the t.T cousin of makeFullRegistry — same
// shape, different fatal handler. The Go test framework refuses to
// share a *testing.B handle inside a *testing.T context.
func makeFullRegistry_T(t *testing.T, n int) *Registry {
	t.Helper()
	r := NewRegistry()
	for i := 0; i < n; i++ {
		tm, err := Compile(proto.Brand{
			Name: fmt.Sprintf("synthetic-%04d", i),
			Type: "shell",
			Palette: proto.Palette{
				"primary": "#88c0d0",
				"accent":  "#a3be8c",
			},
			Roles: proto.Roles{
				"prompt":  "$palette.primary",
				"accent":  "$palette.accent",
				"primary": "$palette.primary",
			},
			Glyphs: proto.Glyphs{
				Static: map[string]string{"prompt_char": "❯"},
			},
			Prompt: proto.PromptConfig{
				Segments:   []string{"cwd", "prompt"},
				Separators: "minimal",
			},
		})
		if err != nil {
			t.Fatalf("compile synthetic theme: %v", err)
		}
		r.Add(tm)
	}
	return r
}
