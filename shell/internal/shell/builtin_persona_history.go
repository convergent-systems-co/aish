package shell

import (
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/history"
)

// recordPersonaUse writes a best-effort signed history event noting
// the persona switch. Per v0.3-fu-secrets §"#106 Strengthening pass":
// every persona switch is auditable. The event carries only the
// persona NAME — no secret material, and (per the persona engine's
// design) persona names are not secret.
//
// If the history engine is not wired (`s.history == nil`), the call
// is a no-op. If a Signer is attached, history.Store.Append signs
// the event in the standard v0.1-4 path; the strengthening test
// asserts a non-empty Signature on the persisted row.
func (s *Shell) recordPersonaUse(name string) {
	if s.history == nil {
		return
	}
	store := s.history.Store()
	if store == nil {
		return
	}
	ev := history.Event{
		ID:        history.NewEventID(),
		Timestamp: time.Now().UTC(),
		Kind:      history.Kind("persona.use"),
		Command:   "persona use " + name,
		Cwd:       s.cwd,
	}
	zero := 0
	ev.ExitCode = &zero
	_ = store.Append(&ev)
}
