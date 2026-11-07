# ATE Review

Design, execution, test, and verifier reviewers report only. Independently reread the target, requirements, design/contracts, and admitted scope evidence. Do not trust a producer or sibling claim, repair the target, or turn praise into evidence.

## Independent lenses

Critic A checks material style and quality. Critic B checks correctness, fidelity, security, interfaces, tests, proof, and ownership claims. Critic C checks coupling, architecture, maintainability, and final-state clarity. Critic C may return `reconstructed intention:` followed by 2–4 bullets and stop. For code, the E2E reviewer exercises the affected real path and cites output, state, or screenshots.

## Review quality

Review purpose before details, then requirements, hard contracts, root-cause rationale, claim scope, security, edge/error behavior, shared concerns, and evidence. Reject a submission without its critique log. Each finding states consequence, impact, precise location, supporting evidence, and concrete direction. Critical or Major findings need a file/symbol anchor; vague “refactor” or “clean up” is not evidence.

## Boundaries

Keep lenses independent before reading sibling findings. A reviewer may challenge a claim with code/spec/test evidence but does not edit, choose work disposition, or represent another reviewer’s result. Hand off the report with tags and remaining uncertainty.
