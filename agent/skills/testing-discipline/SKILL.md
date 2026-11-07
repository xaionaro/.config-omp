---
name: testing-discipline
description: Use when writing, running, or reviewing tests, or before reporting any task as done — ensures test coverage, falsification, dual-sided validation, and determinism
---

# Testing Discipline

- Test every modification before reporting done: unit checks plus E2E when a framework exists. Unit isolates; E2E proves user path.
- E2E = built artifact in target env, exercised through real UI/API, validated by output/screenshots/state. Anything less is not E2E; name it honestly.
- Iterate on the shortest faithful check first. If behavior reproduces without full E2E (unit/API/CLI/component), debug there; use E2E for final user-path proof.
- Treat tests as falsification attempts — they try to disprove your code works. Tests that cannot fail are worthless. Assert behavior and edge cases, not just happy path.
- **Dual-sided testing**: Every test must confirm both that good behavior IS happening AND that bad behavior is NOT happening. Testing only one side leaves the other unverified.
- **Test validation**: When adding a new test, break the code intentionally and confirm the test fails. A test that passes regardless of code correctness proves nothing.
- **A/B differential on every bug fix**: Run the test against the pre-fix code (e.g. `git show HEAD~1:<path>`, `git stash`, or revert) and confirm it FAILS. Then re-run against the fixed code and confirm it PASSES. "Test passes after my fix" alone is worthless — it proves the test runs, not that the fix changed anything. Show both outputs in the report. Skip only when the change is a literal text edit a human can verify by reading the diff.
- Infeasible tests → document why + provide alternative verification.
- Use provided logs/stacktraces as verification evidence. Add logging if insufficient.
- Write deterministic tests only — real-clock dependencies cause flaky CI and non-reproducible failures.
- Keep auto-test coverage above 90% via useful test cases, not synthetic ones.
