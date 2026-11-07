## Workflow coverage map

This map is an audit index, not an admission inventory. Source versions, hashes, records, and coverage-map entries are audit metadata, never ordinary-work admission prerequisites. The role-table routing responsibilities remain required workflow guidance.

| Concern | Destination | Verification |
| --- | --- | --- |
| outer lifecycle, role routing, and lineage | ATE router + orchestration + coordinator runtime | routing test |
| nested ECI fast path and scoped ownership exception | ECI fast-path + ATE router/orchestration | routing checks + independent policy scenarios/review |
| direct-user pause behavior | pause-all-work | routing test |
| independent review and acceptance | coordinator runtime + review policy + ATE review | fresh A/B/C and required E2E |
| style guidance | coding-style-admission | review + formatter/linter where applicable |
| architecture, PoC, and design choice | ATE design | independent review |
| fact discovery | ATE research | source checks |
| owned implementation and debugging | ATE execution + debugging-discipline | target-appropriate checks |
| test design and QA | ATE testing-and-qa | required E2E and tests |
| workflow-policy pressure cases | policy-pressure-tests | routing test |
| cross-skill red flags | owning module + ATE router | focused review |
