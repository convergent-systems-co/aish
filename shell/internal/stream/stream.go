// Package stream classifies output bytes into one of aish's four pipe
// types so the AI side can operate on structured data without per-tool
// rewrites.
//
// v0.1-1 scope (sub-issue #10): probe up to 512 bytes and return
// KindText, KindJSON, KindNDJSON, or KindTable. Detection is
// best-effort heuristic — false positives are worse than false
// negatives, so ambiguous input falls back to KindText.
package stream

// Kind is the classification of a stream's content.
type Kind int

const (
	// KindUnknown is the zero value. Detect never returns this; it exists
	// only so the zero value of Kind is distinguishable from a real result
	// in test failures.
	KindUnknown Kind = iota
	// KindText is the fallback — opaque bytes, no structure detected.
	KindText
	// KindJSON is a single JSON object or array spanning the probe.
	KindJSON
	// KindNDJSON is newline-delimited JSON: every non-empty line parses as
	// JSON on its own.
	KindNDJSON
	// KindTable is tab-separated, at least two columns and at least two
	// rows. Renders in the TUI; pipes downstream as NDJSON.
	KindTable
)

// String returns a stable lowercase name for the kind, suitable for
// logging and serialisation.
func (k Kind) String() string {
	switch k {
	case KindText:
		return "text"
	case KindJSON:
		return "json"
	case KindNDJSON:
		return "ndjson"
	case KindTable:
		return "table"
	default:
		return "unknown"
	}
}

// Detect probes up to the first 512 bytes of b and returns the detected
// Kind. Detect is total: every input returns a defined Kind (never
// KindUnknown). Empty input returns KindText.
//
// Implementation lives in the v0.1-1 coder T3 sub-task; this stub returns
// KindUnknown so the test file compiles and the tests fail at runtime.
func Detect(b []byte) Kind {
	return KindUnknown
}
