---
name: testing-discipline
description: Use when writing, running, or reviewing tests, or before reporting any task as done — ensures test coverage, falsification, dual-sided validation, determinism, behavior-level assertions, and efficient suites
---

# Testing Discipline

- Test every modification before reporting done: unit checks plus E2E when a framework exists. Unit isolates; E2E proves user path.
- E2E = built artifact in target env, exercised through real UI/API, validated by output/screenshots/state. Anything less is not E2E; name it honestly.
- Iterate on the shortest faithful check first. If behavior reproduces without full E2E (unit/API/CLI/component), debug there; use E2E for final user-path proof.
- Treat tests as falsification attempts — they try to disprove your code works. Cover edge cases, not just the happy path.
- **Assert behavior once**: Assert each behavior once per test level, as its callers or users observe it; program output is behavior. Never assert source text, prose wording, or implementation details: a behavior-preserving rewrite must keep tests green. For instruction files (skills, prompts, docs), assert only structure: links and anchors resolve, imports exist, and routing tables link the intended modules.
- **Prune on touch**: When editing a test file, delete its tests that cannot fail, restate the implementation (wiring, echoed inputs), pin incidental defaults, or repeat another test's assertion.
- **Dual-sided testing**: Every test must confirm both that good behavior IS happening AND that bad behavior is NOT happening. Testing only one side leaves the other unverified.
- **Test validation**: When adding a new test, break the code intentionally and confirm the test fails. A test that passes regardless of code correctness proves nothing. Break it once by hand; never commit loops that mutate inputs only to re-prove the test's own checks.
- **A/B differential on every bug fix**: Run the test against the pre-fix code (e.g. `git show HEAD~1:<path>`, `git stash`, or revert) and confirm it FAILS. Then re-run against the fixed code and confirm it PASSES. "Test passes after my fix" alone is worthless — it proves the test runs, not that the fix changed anything. Show both outputs in the report. Skip only when the change is a literal text edit a human can verify by reading the diff.
- Infeasible tests → document why + provide alternative verification.
- Use provided logs/stacktraces as verification evidence. Add logging if insufficient.
- Write deterministic tests only — real-clock dependencies cause flaky CI and non-reproducible failures.
- **Efficient tests**: Spend only the time and resources the assertions need; avoid repeated builds or setup, a process fork per line or pattern, and quadratic string scans. A suite run over 60 s is a speed defect: profile where its time goes, in the tests and in the code under test. Remove each avoidable cost where it arises, or report it when it is outside your task. Treat an inefficiency in the code under test as a code defect: fix it in that code, never by trimming or speeding up its test. Skip the speed review when `test-speed-reviews.tsv` beside the suite's entry point has a row for the same command, unless a run on a comparable host and load takes over 25% longer than that row. When a review leaves a suite over 60 s, append a row, creating the file with the tab-separated header `reviewed_utc command revision environment elapsed_s remaining_costs`; `remaining_costs` names each remaining cost and why it stays.
- Keep auto-test coverage above 90% via useful test cases, not synthetic ones.
