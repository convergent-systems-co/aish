package telemetry

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadSessionCosts_MissingFile(t *testing.T) {
	t.Parallel()
	costs, err := ReadSessionCosts(filepath.Join(t.TempDir(), "nonexistent.jsonl"), time.Now())
	if err != nil {
		t.Fatalf("ReadSessionCosts on missing file: %v", err)
	}
	if costs.TotalUSD != 0 || costs.TotalCalls != 0 || len(costs.PerModel) != 0 {
		t.Errorf("missing file produced non-zero costs: %+v", costs)
	}
}

func TestReadSessionCosts_FiltersByChronon(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, "cost-log.jsonl")
	body := "" +
		`{"chronon":"2026-05-20T10:00:00Z","model":"claude-sonnet","tokens_in":100,"tokens_out":50,"usd":0.01,"req_id":"a"}` + "\n" +
		`{"chronon":"2026-05-20T11:00:00Z","model":"claude-sonnet","tokens_in":200,"tokens_out":100,"usd":0.02,"req_id":"b"}` + "\n" +
		`{"chronon":"2026-05-20T12:00:00Z","model":"claude-haiku","tokens_in":50,"tokens_out":25,"usd":0.001,"req_id":"c"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Session started at 10:30 — only rows b and c should count.
	since := time.Date(2026, 5, 20, 10, 30, 0, 0, time.UTC)
	costs, err := ReadSessionCosts(path, since)
	if err != nil {
		t.Fatalf("ReadSessionCosts: %v", err)
	}
	if costs.TotalCalls != 2 {
		t.Errorf("TotalCalls = %d, want 2", costs.TotalCalls)
	}
	if got, want := costs.TotalUSD, 0.021; absDelta(got, want) > 1e-9 {
		t.Errorf("TotalUSD = %v, want %v", got, want)
	}
	if len(costs.PerModel) != 2 {
		t.Errorf("PerModel len = %d, want 2", len(costs.PerModel))
	}
	// Sorted USD descending: claude-sonnet first (0.02), then haiku.
	if costs.PerModel[0].Model != "claude-sonnet" {
		t.Errorf("PerModel[0] = %s, want claude-sonnet", costs.PerModel[0].Model)
	}
}

func TestReadSessionCosts_SkipsMalformedLines(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, "cost-log.jsonl")
	body := "" +
		`{"chronon":"2026-05-20T10:00:00Z","model":"claude-sonnet","tokens_in":1,"tokens_out":1,"usd":0.01}` + "\n" +
		`{this is not json` + "\n" +
		`{"chronon":"not a timestamp","model":"x","usd":0.5}` + "\n" +
		`{"chronon":"2026-05-20T11:00:00Z","model":"","usd":0.5}` + "\n" +
		`{"chronon":"2026-05-20T11:00:00Z","model":"claude-haiku","usd":0.002}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	since := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	costs, err := ReadSessionCosts(path, since)
	if err != nil {
		t.Fatalf("ReadSessionCosts: %v", err)
	}
	// First good row (0.01) and last good row (0.002) — total 2 calls.
	if costs.TotalCalls != 2 {
		t.Errorf("TotalCalls = %d, want 2 (3 malformed lines skipped)", costs.TotalCalls)
	}
}

func TestReadSessionCosts_ExactSinceBoundaryIsIncluded(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, "cost-log.jsonl")
	// Row chronon == session start. Must be included (>= since).
	body := `{"chronon":"2026-05-20T10:00:00.000000000Z","model":"x","usd":0.05}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	since := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	costs, err := ReadSessionCosts(path, since)
	if err != nil {
		t.Fatalf("ReadSessionCosts: %v", err)
	}
	if costs.TotalCalls != 1 {
		t.Errorf("boundary row not included: TotalCalls=%d, want 1", costs.TotalCalls)
	}
}

func TestReadSessionCosts_EmptyFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, "cost-log.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	costs, err := ReadSessionCosts(path, time.Now())
	if err != nil {
		t.Fatalf("ReadSessionCosts: %v", err)
	}
	if costs.TotalCalls != 0 {
		t.Errorf("empty file produced calls: %+v", costs)
	}
}

func TestReadSessionCosts_AccumulatesPerModelTokens(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, "cost-log.jsonl")
	body := "" +
		`{"chronon":"2026-05-20T10:00:00Z","model":"m1","tokens_in":100,"tokens_out":50,"usd":0.01}` + "\n" +
		`{"chronon":"2026-05-20T10:01:00Z","model":"m1","tokens_in":200,"tokens_out":100,"usd":0.02}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	costs, err := ReadSessionCosts(path, time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ReadSessionCosts: %v", err)
	}
	if len(costs.PerModel) != 1 {
		t.Fatalf("PerModel len = %d, want 1", len(costs.PerModel))
	}
	m := costs.PerModel[0]
	if m.Calls != 2 || m.TokensIn != 300 || m.TokensOut != 150 {
		t.Errorf("PerModel[0] = %+v, want Calls=2 TokensIn=300 TokensOut=150", m)
	}
}

func absDelta(a, b float64) float64 {
	if a < b {
		return b - a
	}
	return a - b
}
