---
name: finishing-a-development-branch
description: Use when implementation is complete and you need to finish, integrate, or preserve development work
---

# Finishing a Development Branch

## Overview

Finish development work using the authorized integration or preservation action.

**Core principle:** Verify tests → honor the requested action → preserve authoritative and unrelated work.

**Announce at start:** "I'm using the finishing-a-development-branch skill to complete this work."

## The Process

### Step 1: Verify Tests

**Before finishing, verify tests pass:**

```bash
# Run project's test suite
npm test / cargo test / pytest / go test ./...
```

**If tests fail:**
```
Tests failing (<N> failures). Must fix before completing:

[Show failures]

Cannot proceed with merge/PR until tests pass.
```

Stop. Don't proceed to Step 2.

**If tests pass:** Continue to Step 2.

### Step 2: Identify the Authoritative Checkout

Follow the [Git policy](../../eci-binding-contract.md#git) to identify the SSOT repository, checkout, and branch from the assignment and context. Do not infer a base branch from its name. Never delete the SSOT branch or remove its checkout, including when it is a linked worktree.

### Step 3: Finish the Work

- Default to preserving the checkout and branch and reporting their state. If the user requested an applicable finishing action, perform it without an extra permission question.
- For an authorized local merge, establish distinct source and destination branches from context; never merge a branch into itself. Preserve the SSOT branch and checkout. Verify tests on the merged result.
- Push only on explicit user request. Create a pull request when requested, using the identified source and destination and a concise summary and Test Plan.
- Do not automatically pull, delete branches, or remove worktrees after integration. Preserve unrelated user data.
- Discard only an owned, authorized disposable experiment. Identify its exact affected branch, checkout, commits, and uncommitted changes, then require explicit confirmation before disposal. Never discard the SSOT branch or checkout.

## Common Mistakes

**Skipping test verification**
- **Problem:** Merge broken code, create failing PR
- **Fix:** Verify tests before finishing.

**Automatic cleanup**
- **Problem:** Removing authoritative or unrelated work after integration.
- **Fix:** Preserve it; discard only confirmed, owned, authorized disposable experiments.

## Red Flags

**Never:**
- Proceed with failing tests
- Merge without verifying tests on result
- Delete work without confirmation
- Force-push without explicit request

**Always:**
- Verify tests before finishing
- Preserve the SSOT branch and checkout
- Confirm before discarding an owned, authorized disposable experiment

## Integration

**Called by:**
- **subagent-driven-development** (Step 7) - After all tasks complete
- **executing-plans** (Step 5) - After all batches complete
