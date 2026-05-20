// Package stream classifies output bytes into one of aish's four pipe
// types so the AI side can operate on structured data without per-tool
// rewrites.
//
// v0.1-1 scope (sub-issue #10): probe up to 512 bytes and return
// KindText, KindJSON, KindNDJSON, or KindTable. Detection is
// best-effort heuristic — false positives are worse than false
// negatives, so ambiguous input falls back to KindText.
package stream

import (
	"bytes"
	"encoding/json"
)

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

// probeLimit caps how many bytes Detect examines. Past this point the
// classification is undefined; the cap keeps detection O(1) regardless
// of payload size.
const probeLimit = 512

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
// Probe order matters: JSON is checked before NDJSON so that a
// single-line JSON object classifies as KindJSON, not KindNDJSON. Table
// is checked before falling through to text so that genuine
// tab-separated data is not misclassified as opaque bytes.
func Detect(b []byte) Kind {
	if len(b) == 0 {
		return KindText
	}
	probe := b
	if len(probe) > probeLimit {
		probe = probe[:probeLimit]
	}

	if isJSON(probe) {
		return KindJSON
	}
	if isNDJSON(probe) {
		return KindNDJSON
	}
	if isTable(probe) {
		return KindTable
	}
	return KindText
}

// isJSON reports whether probe (after leading-whitespace trim) starts
// with '{' or '[' AND the whole probe parses as a single JSON value.
// Leading whitespace is tolerated because real producers (jq, curl,
// hand-edited config) often emit it; trailing whitespace is tolerated
// because json.Valid accepts it.
func isJSON(probe []byte) bool {
	trimmed := bytes.TrimLeft(probe, " \t\r\n")
	if len(trimmed) == 0 {
		return false
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return false
	}
	return json.Valid(probe)
}

// isNDJSON reports whether probe contains ≥1 newline AND every
// non-empty line is itself a valid JSON value. Blank lines are
// tolerated (a `\n\n` separator is common in human-edited streams).
func isNDJSON(probe []byte) bool {
	if !bytes.ContainsRune(probe, '\n') {
		return false
	}
	lines := bytes.Split(probe, []byte{'\n'})
	// If the probe doesn't end on a newline, the final element may be a
	// partial line truncated by the probe cap. Drop it to avoid a false
	// negative on otherwise-valid NDJSON whose tail exceeds 512 bytes.
	if len(probe) > 0 && probe[len(probe)-1] != '\n' {
		lines = lines[:len(lines)-1]
	}
	sawJSONLine := false
	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		if !json.Valid(trimmed) {
			return false
		}
		sawJSONLine = true
	}
	return sawJSONLine
}

// isTable reports whether probe looks like tab-separated tabular data:
// ≥2 non-empty lines, each with ≥2 tab-separated columns, and a
// consistent column count across lines. A single tab-separated line is
// not a table (per the plan §"T3" — one row of headers without data
// rows is indistinguishable from a column-named log line).
func isTable(probe []byte) bool {
	lines := bytes.Split(probe, []byte{'\n'})
	// Drop a trailing partial line (no terminating newline) for the same
	// reason as isNDJSON: the probe cap may have truncated mid-row.
	if len(probe) > 0 && probe[len(probe)-1] != '\n' {
		lines = lines[:len(lines)-1]
	}
	nonEmptyRows := 0
	expectedCols := -1
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		cols := bytes.Count(line, []byte{'\t'}) + 1
		if cols < 2 {
			return false
		}
		if expectedCols == -1 {
			expectedCols = cols
		} else if cols != expectedCols {
			return false
		}
		nonEmptyRows++
	}
	return nonEmptyRows >= 2
}
