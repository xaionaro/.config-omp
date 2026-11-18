## Summary

- Fixture for an audit stuck record missing command.

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

- Objection considered: stuck records need an executable recovery command.

## Rule-Compliance Self-Audit

Violation: missing command
Rule: AGENTS.md
Correction:
  stuck:
  input: missing review decision

## Gaps

- No production stop proof is implied.
