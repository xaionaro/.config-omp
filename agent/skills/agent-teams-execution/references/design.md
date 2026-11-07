# ATE Design

Designer, Design Reviewer, and Fundamentals Design Reviewer (FDR) only. Read tagged research, original requirements, validated lineage, and applicable style-source facts before deciding architecture. Report evidence; do not implement the design.

## Design artifact

Record components, data/error flow, trust boundaries, file ownership without overlap, binary/service purpose, public interface contracts, dependency order, requirement traceability, applicable security design, and a shared-concerns register. State error modes, pre/postconditions, invariants, concurrency expectations, and test-design inputs. An unproven core mechanism gets one isolated real-input PoC; it is not production precedent.

Propose the scoped style record from source facts without self-admitting it. A design change must preserve the current artifact as input and make changed premises, contracts, ownership, security boundaries, or feasibility assumptions explicit.

## Independent review

Design Reviewer checks traceability, security, shared concerns, contracts, ownership/purpose fit, PoC evidence, and the style proposal. Report each material issue with evidence, severity, impact, and concrete direction. `REJECT` identifies a load-bearing premise, framing, or scope flaw; `CONDITIONAL` identifies a concrete original-scope issue; `NIT` is non-blocking.

## FDR scrutiny

FDR independently challenges premise, model, contract, trust, ownership, and feasibility assumptions. Its report identifies weak evidence, hidden consequences, and the conditions needed to support or reject the design. FDR owns no style decision and does not edit the artifact.

## Boundaries

Do not turn research evidence into an unstated architecture, silently expand ownership, or treat a disposable PoC as final implementation. Hand off the design artifact, review findings, and unresolved assumptions to the assigned recipient.
