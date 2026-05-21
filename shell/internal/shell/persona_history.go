package shell

import (
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/history"
	"github.com/convergent-systems-co/aish/shell/internal/parser"
	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

// personaHistoryInterceptor records the active persona against the
// in-flight history event after the history interceptor has already
// appended it. The pair is written to a JSONL sidecar
// (~/.aish/persona-events.jsonl) so history.Event itself stays
// untouched — the FU plan forbids extending history.Append in this
// branch.
//
// Order matters: this interceptor MUST be registered AFTER
// history.History so its Before sees the event history just appended.
// openPersona is called last in NewWithOptions, which guarantees the
// order without additional coordination.
type personaHistoryInterceptor struct {
	meta    *persona.MetaStore
	store   *history.Store
	persona func() string // closure that reads s.activePersona live
}

// Before queries the latest event in the history store and records
// the active persona against its ID. A nil store / meta / persona
// closure is a no-op — the destructive command still runs, the
// attribution row is best-effort.
//
// We deliberately do not gate on parser-level "is destructive?" —
// history.Before is the authority on event creation. If history
// emitted an event, we attribute. If history skipped (non-destructive
// command, ignored path, …), List(1) still returns the previous
// event; we detect this by remembering the most-recent-seen event ID
// and only Recording when the latest ID changes.
func (p *personaHistoryInterceptor) Before(pl *parser.Pipeline, line string) error {
	if p == nil || p.store == nil || p.meta == nil {
		return nil
	}
	events, err := p.store.List(1)
	if err != nil || len(events) == 0 {
		return nil
	}
	latest := events[0]
	if latest == nil || latest.ID == "" {
		return nil
	}
	// Skip if we've already attributed this ID (no new destructive
	// event since the last call).
	if _, already := p.meta.Lookup(latest.ID); already {
		return nil
	}
	name := ""
	if p.persona != nil {
		name = p.persona()
	}
	ts := latest.Timestamp.UTC().Format(time.RFC3339)
	_ = p.meta.Record(latest.ID, name, ts)
	return nil
}

// After is a no-op for persona attribution. The event-to-persona map
// is fixed at command start; exit codes do not change the attribution.
func (p *personaHistoryInterceptor) After(_ *parser.Pipeline, _ string, _ int, _ time.Duration) {
}
