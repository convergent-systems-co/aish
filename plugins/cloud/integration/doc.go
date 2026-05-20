// Package integration is the end-to-end test suite for the
// aish-inference-cloud binary.
//
// Scope. These tests build the plugin binary fresh in TestMain, then
// spawn it as a subprocess against httptest stubs that serve canned
// Anthropic SSE responses. Internals (rpc dispatcher, anthropic client,
// reliab) are exercised through the wire — this suite is the contract
// pin for the cmd/ wiring, not for the packages under internal/.
//
// What this suite proves:
//
//   - --version exits 0 with the expected prefix.
//   - Missing $ANTHROPIC_API_KEY produces a non-zero exit with a
//     diagnostic that names the env var but never echoes its value.
//   - Ping returns a Pong frame.
//   - A streaming Infer response surfaces N token frames + one terminal
//     Complete frame, all carrying the same Response.ID.
//   - 401 from upstream surfaces a proto.Error{Code: CodeAuthFailed}
//     with no API-key leak in the message.
//   - 429 from upstream surfaces a proto.Error{Code: CodeRateLimited}.
//   - An unknown method returns proto.Error{Code: CodeMethodNotFound}.
//   - Malformed NDJSON returns proto.Error{Code: CodeParseError} and
//     the REPL continues serving the next valid request.
//
// Running. From the plugin root:
//
//	cd plugins/cloud
//	go test -race -count=1 ./integration/...
//
// The suite is also run as part of `make -C plugins/cloud test` and the
// top-level `make ci` gate.
//
// Conventions. The synthetic API key used by every test (the
// fakeAPIKey constant in harness_test.go) MUST NEVER appear in
// production source. Per Common.md §4 it is a synthetic value
// with no upstream meaning; its only purpose is to feed
// ANTHROPIC_API_KEY in subprocess env and to assert it never
// leaks through any user-visible output path.
package integration
