## Summary

- Fixture for an audit correction with an old commit.

## Verification

- Fixture-level verification entry.

## Requirements

- Fixture covers audit freshness after HEAD movement.

## Root Cause

- Fixture is not a bug fix.

## Claim Inventory

- Fixture claims are local to the test.

## Pre-Mortem

- Most likely flaw is accepting old-only commit evidence.

## Adversarial Critique

- Objection considered: old commits do not prove a current-turn correction.

## Rule-Compliance Self-Audit

Violation: old commit
Rule: AGENTS.md
Correction:
  commit: __OLD_COMMIT__

## Gaps

- No production stop proof is implied.
