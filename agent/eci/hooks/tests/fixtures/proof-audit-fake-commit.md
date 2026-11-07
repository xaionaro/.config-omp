## Summary

- Fixture for an audit correction with an unreachable commit.

## Verification

- Fixture-level verification entry.

## Requirements

- Fixture covers audit commit validation.

## Root Cause

- Fixture is not a bug fix.

## Claim Inventory

- Fixture claims are local to the test.

## Pre-Mortem

- Most likely flaw is accepting fake audit commits.

## Adversarial Critique

- Objection considered: commit markers must point to reachable commits.

## Rule-Compliance Self-Audit

Violation: fake commit
Rule: AGENTS.md
Correction:
  commit: ffffffffffffffffffffffffffffffffffffffff

## Gaps

- No production stop proof is implied.
