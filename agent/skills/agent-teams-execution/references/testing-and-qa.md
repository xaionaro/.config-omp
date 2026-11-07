# ATE Testing and QA

Test Designer, Test Executor, Test Reviewer, Verifier, and QA use this guide. Tests and specifications are durable artifacts: perform read-only source discovery and obtain the applicable role-local admission before writing them. Isolated disposable repros are the only exception.

## Test/spec admission

Test Designer or Test Executor proposes the style record from source discovery. Test Reviewer independently checks it; Verifier does so when assigned. Keep test admission separate from production-code admission. Unit tests stay with code; integration tests exercise real cross-task interfaces; E2E exercises complete user workflows and UI interaction where applicable.

## Evidence and defect handoff

For every criterion, state what must be true, classify evidence as direct or proxy, obtain the evidence, and judge it from exact output or observation. Direct evidence is actual user-path behavior; proxy evidence such as unit tests, lint, or type checks remains useful but cannot replace feasible direct evidence.

Report each defect with criterion, evidence, impact, owner, and reproduction. A cross-task boundary defect identifies the affected contract; a design defect identifies the contradicted assumption. Include tags, admission/delta evidence, security and error observations, and unresolved limits.

## QA quality and boundaries

QA independently reconciles criteria, changed scope, contracts, tests, direct-path evidence, static checks, secrets, ownership, and factual-claim tags. An admitted deviation is compliant; missing or unverified evidence blocks the QA verdict. QA reports the verdict and evidence without declaring mission closure, closing teammates, or suppressing later user follow-up.
