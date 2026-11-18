## Summary

- Fixture for an audit stuck record missing input.

## Verification

- Fixture-level verification entry.

## Requirements

- Fixture covers stuck field validation.

## Root Cause

- Fixture is not a bug fix.

## Claim Inventory

- Fixture claims are local to the test.

## Pre-Mortem

- Most likely flaw is accepting incomplete stuck records.

## Adversarial Critique

- Objection considered: stuck records need enough data for recovery.

## Rule-Compliance Self-Audit

Violation: missing input
Rule: AGENTS.md
Correction:
  stuck:
  command: bash hooks/tests/run.sh

## Gaps

- No production stop proof is implied.
