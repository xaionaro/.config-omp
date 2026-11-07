# ATE Execution

Executor-only. Receive owned files, relevant design/contracts/shared concerns, validated lane/assignment, admitted style record, and test inputs. Reread targets before writing and keep one independent slice per lane.

## Owned slice and contracts

Implement the approved slice with unit tests. Preserve names, interfaces, binary/package purpose, error modes, invariants, and shared locations. Do not edit another owner’s files, hide a broken dependency, introduce an unapproved cross-cutting abstraction, or turn unrooted discovery into a change.

## Evidence and causal proof

Use matching implementation, testing, and debugging skills. For a bug, build failure, flake, or performance regression, provide the current repro, cause chain, alternative or falsifying prediction, `regression: yes|no|unknown`, and real failing-path evidence. Repair the mechanism rather than masking a symptom. Report a newly found design risk or code smell with its source, target, and question.

## Submission

Submit as `submitted`, never `complete`. Include actual scope, claim tags, admitted record/deltas/tool evidence, unit and relevant proof output, a critique log (3+ issues found/fixed), causal/regression evidence where applicable, and current diff/commit status. Do not claim user-visible behavior from a build or linter proxy alone.

## Boundaries

Durable writes wait for admission; an isolated disposable repro may precede them but is not production precedent. Keep valid debt/defer out of the diff except for the required searchable tracker comment. Hand off findings and evidence without deciding their disposition.
