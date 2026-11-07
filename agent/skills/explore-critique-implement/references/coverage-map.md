## Workflow coverage map

This map is an audit index, not an admission inventory. Source versions, hashes, records, and coverage-map entries are audit metadata, never ordinary-work admission prerequisites. The role-table routing responsibilities remain required workflow guidance.

| Concern | Destination | Verification |
| --- | --- | --- |
| activation, lineage, and worker routing | ECI router | routing test |
| published task ownership, active-outcome count, report routing/drain, and task-local review dispatch | task-orchestrator + router/coordinator/runtime/review-policy/Fast pointers | independent policy review |
| concurrent Fast owner, shared-tree priority, evidence feedback, retained-hunk review, closure | fast-path + owning role pointers | routing checks + independent policy scenarios/review |
| coordinator lifecycle and teardown | coordinator runtime + ECI coordinator | routing test |
| direct-user pause behavior | pause-all-work | routing test |
| style guidance | coding-style-admission | review + formatter/linter where applicable |
| last-resort escalation | ECI coordinator + blocker-resolution-protocol | routing test |
| exploration and design choice | ECI explore + critique | independent review |
| bounded implementation and proof | ECI implement | target-appropriate checks |
| independent review and acceptance | ECI review + coordinator runtime + review policy | fresh A/B/C per iteration; final E2E pair and timing per ECI router |
| workflow-policy pressure cases | policy-pressure-tests | routing test |
| cross-skill red flags | owning module + ECI router | focused review |
