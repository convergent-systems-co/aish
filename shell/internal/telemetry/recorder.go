package telemetry

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// CacheStatsReader is the narrow interface telemetry needs from the
// cache.Store. Kept here so the telemetry package never imports
// `shell/internal/cache` (a TL3-owned package) and the dependency
// graph stays one-way.
//
// `shell/internal/cache.Store` satisfies this interface by virtue of
// its existing `Stats() (cache.Stats, error)` method — the cache.Stats
// shape is unwrapped at the call site in shell.go.
type CacheStatsReader interface {
	StatsSnapshot() (hits, misses int64, err error)
}

// PluginPresence reports whether the inference plugin was running at
// session start. When false, InferenceCalls is recorded as zero even
// if CacheMisses > 0 (a miss without a plugin can't have made a
// network call).
//
// This is a function value rather than a bool so a caller (the Shell
// wiring in shell.go) can re-check at Recorder.New time without
// telemetry holding a reference to the plugin object.
type PluginPresence func() bool

// Recorder implements shell.Interceptor (Before is a no-op; After
// records) and owns the lifecycle of the per-session counters, the
// cost reader, and the session-row persistence.
//
// Construct via New, call Before/After per the Interceptor contract,
// and call Close once on shell shutdown.
type Recorder struct {
	mu sync.Mutex

	dotAishDir   string
	consent      Consent
	costLogPath  string
	cacheReader  CacheStatsReader
	pluginActive PluginPresence

	sessionID    string
	startedAt    time.Time
	counters     Counters
	cacheBaseHit int64
	cacheBaseMis int64

	closed bool
}

// Config bundles the inputs to New so the constructor signature stays
// short. All fields are optional except DotAishDir (without a home,
// telemetry can't persist anything).
type Config struct {
	// DotAishDir is the user's `~/.aish` directory. Required.
	DotAishDir string
	// CostLogPath overrides the default
	// `~/.aish/cost-log.jsonl` location. Empty means "use the
	// default under DotAishDir."
	CostLogPath string
	// CacheReader, when non-nil, lets the recorder observe cache
	// hit/miss deltas. nil = the shell ran without a cache; the
	// recorder still ticks Commands but cache counters stay at zero.
	CacheReader CacheStatsReader
	// PluginActive, when non-nil, gates the InferenceCalls counter.
	// nil = plugin status unknown; treat as inactive (InferenceCalls
	// stays zero — never over-report inference activity).
	PluginActive PluginPresence
	// Now overrides the clock. nil = time.Now. Tests use this to
	// pin the session start.
	Now func() time.Time
}

// New constructs a Recorder. Reads (and lazily-creates) the consent
// file. Always returns a valid Recorder, even on partial failure —
// the shell must keep running even when telemetry is degraded. An
// error is returned only when the session ID generation fails (which
// would mean the OS's random source is unusable; a session without
// an ID is meaningless).
func New(cfg Config) (*Recorder, error) {
	if cfg.DotAishDir == "" {
		return nil, errors.New("telemetry: New: empty DotAishDir")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	id, err := NewSessionID()
	if err != nil {
		return nil, err
	}
	r := &Recorder{
		dotAishDir:   cfg.DotAishDir,
		consent:      LoadConsent(cfg.DotAishDir),
		costLogPath:  cfg.CostLogPath,
		cacheReader:  cfg.CacheReader,
		pluginActive: cfg.PluginActive,
		sessionID:    id,
		startedAt:    cfg.Now().UTC(),
	}
	if r.costLogPath == "" {
		r.costLogPath = filepath.Join(cfg.DotAishDir, "cost-log.jsonl")
	}
	// Baseline the cache counters so After-deltas measure SESSION
	// activity, not cumulative-since-cache-install activity.
	if r.cacheReader != nil {
		if hits, misses, err := r.cacheReader.StatsSnapshot(); err == nil {
			r.cacheBaseHit = hits
			r.cacheBaseMis = misses
		}
	}
	return r, nil
}

// SessionID returns the session identifier minted at New. Exposed for
// the dashboard built-in and for tests.
func (r *Recorder) SessionID() string {
	if r == nil {
		return ""
	}
	return r.sessionID
}

// Consent returns the snapshot of the consent state read at New time.
// Subsequent edits to telemetry.toml are NOT picked up mid-session by
// design — consent for the in-flight session is fixed at session
// start.
func (r *Recorder) Consent() Consent {
	if r == nil {
		return DefaultConsent()
	}
	return r.consent
}

// CurrentCounters returns a copy of the in-flight counter state. The
// `stats` built-in uses this to render the in-progress session row
// alongside the historical rows from ListSessions.
func (r *Recorder) CurrentCounters() Counters {
	if r == nil {
		return Counters{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counters
}

// Before is the no-op half of the Interceptor contract. Telemetry
// never vetoes commands — it observes only.
func (r *Recorder) Before(_ *parser.Pipeline, _ string) error {
	return nil
}

// After records one completed pipeline. Mutates the counters under
// the mutex; tolerates a missing Before (per the Interceptor
// contract); tolerates nil cacheReader / nil pluginActive (degrades
// counters to zero rather than panicking).
func (r *Recorder) After(_ *parser.Pipeline, _ string, exitCode int, dur time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters.Commands++
	if exitCode != 0 {
		r.counters.FailedCommands++
	}
	if dur > 0 {
		r.counters.WallTimeMs += dur.Milliseconds()
	}

	if r.cacheReader == nil {
		return
	}
	hits, misses, err := r.cacheReader.StatsSnapshot()
	if err != nil {
		return // silent; cache stats failures shouldn't poison telemetry
	}
	// The deltas can only grow; clamp at zero in case of an unexpected
	// reset (e.g. user ran `cache clear` mid-session).
	if dh := hits - r.cacheBaseHit; dh > r.counters.CacheHits {
		r.counters.CacheHits = dh
	}
	if dm := misses - r.cacheBaseMis; dm > r.counters.CacheMisses {
		r.counters.CacheMisses = dm
	}
	if r.pluginActive != nil && r.pluginActive() {
		r.counters.InferenceCalls = r.counters.CacheMisses
	}
}

// Close finalizes the session: reads costs for the session window,
// writes the session row locally (when OptInLocal), and queues the
// payload (when OptInAggregate). Idempotent — second call is a no-op.
//
// Close MAY be called on a Recorder that never saw any commands —
// the resulting session row has all-zero counters, which is the
// honest record.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	counters := r.counters
	r.mu.Unlock()

	row := SessionRow{
		ID:            r.sessionID,
		StartedAt:     FormatChronon(r.startedAt),
		EndedAt:       FormatChronon(time.Now()),
		Counters:      counters,
		SchemaVersion: CurrentSchemaVersion,
	}
	costs, err := ReadSessionCosts(r.costLogPath, r.startedAt)
	if err == nil {
		row.Costs = costs
	}

	var firstErr error
	if r.consent.OptInLocal {
		if werr := WriteSessionRow(r.dotAishDir, row); werr != nil {
			firstErr = fmt.Errorf("telemetry: Close: local: %w", werr)
		}
	}
	if r.consent.OptInAggregate {
		if perr := WritePending(r.dotAishDir, row); perr != nil && firstErr == nil {
			firstErr = fmt.Errorf("telemetry: Close: pending: %w", perr)
		}
	}
	return firstErr
}
