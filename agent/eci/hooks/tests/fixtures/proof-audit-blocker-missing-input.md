## Summary

- Fixture for an audit blocker missing input.

## Verification

- Fixture-level verification entry.

## Requirements

- Fixture covers blocker field validation.

## Root Cause

- Fixture is not a bug fix.

## Claim Inventory

- Fixture claims are local to the test.

## Pre-Mortem

- Most likely flaw is accepting incomplete blockers.

## Adversarial Critique

- Objection considered: blockers need enough data for recovery.

## Rule-Compliance Self-Audit

Violation: missing input
Rule: AGENTS.md
Correction:
  blocker:
  command: bash hooks/tests/run.sh

## Gaps

- No production stop proof is implied.
