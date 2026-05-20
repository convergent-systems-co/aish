package integration

// Regression tests are pinned to historical defects in aish. Each test
// reproduces the original failure mode and is named for the issue / PR /
// commit that introduced the fix.
//
// Layout convention:
//
//	// TestRegression_<issueOrShortSha>_<oneline> verifies <bug>.
//	// Original: <link to issue/PR/commit>
//	// Symptom: <one-line description>
//	// Fix: <commit SHA>
//	func TestRegression_NNN_<slug>(t *testing.T) { ... }
//
// Adding a new regression test (workflow):
//   1. Reproduce the bug in this file. The test should FAIL against the
//      buggy commit.
//   2. Commit the failing test.
//   3. Land the fix. The test should PASS.
//   4. Commit the fix; the test stays as the seatbelt against re-introduction.
//
// **Do not delete a regression test** without surfacing the rationale in
// the PR description per `Code.md §11.5`. A removed regression test is a
// silent rollback of the bug it guarded against.

// No regressions yet — Wave 1 (v0.1-1) shipped clean per the tester's
// adversarial pass (PR #161). Subsequent fixes will populate this file.
