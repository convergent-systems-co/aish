// Package telemetry implements the v0.1-5 measurement layer for aish:
// per-session counters (commands, cache hits/misses, inference calls),
// inference cost tracking in USD (read from the plugin-owned
// `~/.aish/cost-log.jsonl`), opt-in consent, and the `aish stats`
// local dashboard.
//
// The package is privacy-first: any data that would leave the user's
// machine is gated on `opt_in_aggregate` in `~/.aish/telemetry.toml`,
// which defaults to false. Own-machine data ("opt_in_local") defaults
// to true and is what powers `aish stats`.
//
// Wiring: telemetry.Recorder implements shell.Interceptor (Before is a
// no-op, After records). It registers on the Shell's interceptor slice
// after history; per the Interceptor contract the After dispatch order
// is reverse-registration, so telemetry's After sees the post-history
// state. See `shell/internal/shell/interceptor.go` for the contract.
package telemetry

import (
	"crypto/rand"
	"fmt"
	"time"
)

// Counters carries the per-session integer counters that drive both
// `aish stats` (local) and the (deferred) aggregate dashboard payload.
//
// All fields are cumulative since session start. Values are recorded
// in the SessionRow at Recorder.Close and persisted to
// `~/.aish/sessions/<id>.json`.
type Counters struct {
	// Commands is the total number of pipelines that completed (i.e.
	// the number of times Recorder.After fired). A pipeline counts
	// regardless of exit code; FailedCommands tracks the subset.
	Commands int64 `json:"commands"`
	// CacheHits is the delta of cache.Store.Stats.Hits between session
	// open and session close (or last After call). Zero when the shell
	// ran without an open cache.
	CacheHits int64 `json:"cache_hits"`
	// CacheMisses is the delta of cache.Store.Stats.Misses. Zero when
	// the shell ran without an open cache. Note that misses include
	// both "plugin handled" and "no plugin available" cases; the
	// shell-side dispatch in shell.go distinguishes between them but
	// the cache counters do not.
	CacheMisses int64 `json:"cache_misses"`
	// InferenceCalls is the count of plugin Infer round-trips during
	// the session. v0.1-5 approximates this as
	// max(0, CacheMisses - cacheMissesWithoutPlugin). When the plugin
	// is running the whole session, InferenceCalls == CacheMisses.
	// When the plugin never started, InferenceCalls is 0.
	InferenceCalls int64 `json:"inference_calls"`
	// FailedCommands is the count of pipelines with a non-zero exit
	// code. Distinguishes "I tried things" from "I succeeded."
	FailedCommands int64 `json:"failed_commands"`
	// WallTimeMs is the sum of pipeline durations in milliseconds.
	// Sums child-process wall-time only; not REPL idle time.
	WallTimeMs int64 `json:"wall_time_ms"`
}

// CostByModel is the per-model cost rollup read from
// `~/.aish/cost-log.jsonl` for the session window. Used by both
// `aish stats` (renders the total as a footer) and the aggregate
// payload (records per-model so the team dashboard can answer
// "which model is the most expensive across users?").
type CostByModel struct {
	// Model is the upstream model identifier as emitted by the plugin
	// (e.g. "claude-3-5-sonnet-20241022"). The plugin owns the
	// canonical form; telemetry just aggregates.
	Model string `json:"model"`
	// Calls is the count of cost-log rows for this model in the
	// session window.
	Calls int64 `json:"calls"`
	// TokensIn / TokensOut are the summed prompt / completion tokens
	// across the session's calls. Zero when no row had a token count.
	TokensIn  int64 `json:"tokens_in"`
	TokensOut int64 `json:"tokens_out"`
	// USD is the summed cost across the session's calls to this
	// model. Float64 — sufficient precision for ~15 significant
	// digits, more than enough for sub-penny granularity.
	//
	// Drachma equivalent is NOT emitted. The Convergent Systems
	// Drachma:USD exchange rate is unpinned in GOALS.md; emitting a
	// guessed value would violate Common.md P2 (no fabrication).
	// TODO(v0.3-economy): pin Drachma:USD and add a Drachma field.
	USD float64 `json:"usd"`
}

// SessionCosts is the per-session aggregate of cost-log entries
// observed during the session window.
type SessionCosts struct {
	// TotalUSD is the sum across all models. Convenient for the
	// `aish stats` dashboard row.
	TotalUSD float64 `json:"total_usd"`
	// TotalCalls is the count of cost-log rows in the window.
	// Equivalent to sum(Calls) across PerModel.
	TotalCalls int64 `json:"total_calls"`
	// PerModel breaks the totals down by model identifier. Sorted by
	// USD descending so a downstream renderer doesn't need to sort.
	PerModel []CostByModel `json:"per_model"`
}

// SessionRow is the on-disk shape persisted to
// `~/.aish/sessions/<id>.json` on Recorder.Close. Also the payload
// shape that would be POSTed to the aggregate dashboard (#44) — when
// the transport lands in v0.2, the same struct serializes either way.
//
// SCHEMA STABILITY: field tags below are stable. Renaming any of them
// is a BREAKING change per Common.md §6. Add new fields, don't rename
// existing ones.
//
// PRIVACY: this struct MUST NOT carry command lines, paths, env vars,
// API keys, or any other user content. Counters and timing only.
type SessionRow struct {
	// ID is the session identifier minted at Recorder.New via
	// crypto/rand (RFC 4122 v4 layout). Used as the filename stem.
	ID string `json:"id"`
	// StartedAt is the RFC3339Nano UTC timestamp of session start.
	StartedAt string `json:"started_at"`
	// EndedAt is the RFC3339Nano UTC timestamp of session end (Close).
	EndedAt string `json:"ended_at"`
	// Counters carries the integer measurements.
	Counters Counters `json:"counters"`
	// Costs carries the USD + per-model rollup for the session.
	Costs SessionCosts `json:"costs"`
	// SchemaVersion identifies the SessionRow shape. Bumped only on
	// BREAKING changes per Common.md §6.
	SchemaVersion int `json:"schema_version"`
}

// CurrentSchemaVersion is the SchemaVersion value written by v0.1-5.
// Future migrations check against this; v0.1-5 readers reject rows
// with a higher value (forward-incompat) but tolerate equal-or-lower.
const CurrentSchemaVersion = 1

// NewSessionID generates an RFC 4122 v4 UUID string from crypto/rand.
// The implementation mirrors `reliab.newReqID` in
// plugins/cloud/internal/reliab/reliab.go so the two ID spaces look
// the same to a downstream observer.
//
// Returns an error only if the OS's random source fails — extremely
// rare; callers should treat it as a session-open failure.
func NewSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("telemetry: NewSessionID: %w", err)
	}
	// Version 4 (random) and variant (RFC 4122).
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16],
	), nil
}

// FormatChronon renders t as RFC3339Nano in UTC. Mirrors the plugin's
// cost-log chronon format so the cost reader can do exact-string
// time-range filtering when it needs to.
func FormatChronon(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
