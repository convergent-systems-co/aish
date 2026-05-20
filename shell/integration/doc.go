// Package integration exercises the aish binary end-to-end.
//
// Tests in this package do NOT import shell packages. They spawn the aish
// binary as a subprocess (built once per `go test` invocation in TestMain),
// feed it scripted stdin, capture stdout/stderr/exit-code, and assert on
// the observed output. This is intentional: integration tests verify the
// CONTRACT of the binary, not the internals.
//
// File layout:
//
//	harness.go        — TestMain, session, run, assertion helpers
//	basic_test.go     — external commands, args, flags
//	builtins_test.go  — cd, export
//	pipes_test.go     — multi-stage pipes (POSIX last-stage exit code)
//	expansion_test.go — $VAR / ${VAR} / $? expansion
//	exitcode_test.go  — exit-code propagation across commands
//	quoting_test.go   — single / double quoting
//	errors_test.go    — missing binary, syntax errors, empty input
//	flags_test.go     — --version / --help
//	regression_test.go — fixtures pinned to historical defects (one per bug)
//
// Running:
//
//	go test ./integration/...            # all integration tests
//	make test-integration                # same, via Makefile
//
// Adding a regression test:
//
//	When a defect is fixed in aish, add a test to regression_test.go that
//	reproduces the original failure. The test must FAIL against the buggy
//	commit and PASS against the fix. Include the commit/PR/issue link in
//	the test's docstring.
package integration
