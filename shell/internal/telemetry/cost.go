package telemetry

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"time"
)

// costLogRow mirrors the schema written by
// `plugins/cloud/internal/reliab.Cost.Record`. Field tags MUST match
// the plugin-side `costRow`. Renaming any of them is a BREAKING
// change per Common.md §6.
//
// We define this struct locally rather than importing from the plugin
// to keep the shell module free of plugin imports — the contract is
// the on-disk JSONL, not a Go type.
type costLogRow struct {
	Chronon   string  `json:"chronon"`
	Model     string  `json:"model"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	USD       float64 `json:"usd"`
	ReqID     string  `json:"req_id"`
}

// ReadSessionCosts reads the JSONL file at path and returns the
// aggregate of rows whose chronon is `>= since`. Missing file returns
// zero values without error — a session may run without any
// inference at all.
//
// Per the alternatives table in `.artifacts/plans/v0.1-5.md`, we
// trust the plugin's chronon stamping; we do NOT dedup on req_id
// within a session (duplicate rows are the plugin's bug, not ours).
// The chronon filter is strict `>=` so a row at exactly the session
// start is counted.
//
// Malformed lines (parse errors, missing fields) are SKIPPED, not
// fatal — the cost reader is a measurement subsystem, not the
// shell's hot path; a single garbage line MUST NOT cost the user
// their session row.
func ReadSessionCosts(path string, since time.Time) (SessionCosts, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return SessionCosts{}, nil
	}
	if err != nil {
		return SessionCosts{}, err
	}
	defer f.Close()

	sinceUTC := since.UTC()
	perModel := make(map[string]*CostByModel)
	var totalUSD float64
	var totalCalls int64

	sc := bufio.NewScanner(f)
	// cost-log lines are short (~150 bytes); the default scanner
	// buffer is 64 KB which is fine, but bump the cap to 256 KB to
	// tolerate an unexpectedly long row without truncating silently.
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 256*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var row costLogRow
		if err := json.Unmarshal(line, &row); err != nil {
			continue // skip malformed line
		}
		if row.Model == "" || row.Chronon == "" {
			continue // missing required field — skip
		}
		ts, err := time.Parse(time.RFC3339Nano, row.Chronon)
		if err != nil {
			continue // skip unparseable chronon
		}
		if ts.UTC().Before(sinceUTC) {
			continue // row precedes the session window
		}
		m, ok := perModel[row.Model]
		if !ok {
			m = &CostByModel{Model: row.Model}
			perModel[row.Model] = m
		}
		m.Calls++
		m.TokensIn += row.TokensIn
		m.TokensOut += row.TokensOut
		m.USD += row.USD
		totalUSD += row.USD
		totalCalls++
	}
	if err := sc.Err(); err != nil {
		return SessionCosts{}, err
	}

	out := SessionCosts{
		TotalUSD:   totalUSD,
		TotalCalls: totalCalls,
		PerModel:   make([]CostByModel, 0, len(perModel)),
	}
	for _, m := range perModel {
		out.PerModel = append(out.PerModel, *m)
	}
	// USD descending, then model name ascending for stable ordering.
	sort.Slice(out.PerModel, func(i, j int) bool {
		if out.PerModel[i].USD != out.PerModel[j].USD {
			return out.PerModel[i].USD > out.PerModel[j].USD
		}
		return out.PerModel[i].Model < out.PerModel[j].Model
	})
	return out, nil
}
