---
name: writing-plans
description: Use when you have a spec or requirements for a multi-step task, before touching code
---

# Writing Plans

## Overview

Write comprehensive implementation plans assuming the engineer has zero context for our codebase and questionable taste. Document everything they need to know: which files to touch for each task, code, testing, docs they might need to check, how to test it. Give them the whole plan as bite-sized tasks. DRY. YAGNI. TDD. Frequent commits.

Assume they are a skilled developer, but know almost nothing about our toolset or problem domain. Assume they don't know good test design very well.

**Announce at start:** "I'm using the writing-plans skill to create the implementation plan."

**Context:** Follow the [Git policy](../../eci-binding-contract.md#git), including its SSOT checkout and branch rules. ECI also follows its [shared-tree rules](../explore-critique-implement/references/fast-path.md#shared-tree-and-eci-priority).

**Save plans to:** the destination recommended by the tool-hook flow that redirects `docs/plans` artifacts.
- Do not choose or hardcode a path inside the working repo, including `docs/plans/` or `docs/superpowers/plans/`.
- If the write tool or hook returns a redirected path, active plan/checklist path, or destination instruction, use that destination.
- User preferences for plan location override this default only when the user explicitly gives a path.

## Scope Check

If the spec covers multiple independent subsystems, it should have been broken into sub-project specs during brainstorming. If it wasn't, suggest breaking this into separate plans — one per subsystem. Each plan should produce working, testable software on its own.

## File Structure

Before defining tasks, map out which files will be created or modified and what each one is responsible for. This is where decomposition decisions get locked in.

- Design units with clear boundaries and well-defined interfaces. Each file should have one clear responsibility.
- You reason best about code you can hold in context at once, and your edits are more reliable when files are focused. Prefer smaller, focused files over large ones that do too much.
- Files that change together should live together. Split by responsibility, not by technical layer.
- In existing codebases, follow established patterns. If the codebase uses large files, don't unilaterally restructure - but if a file you're modifying has grown unwieldy, including a split in the plan is reasonable.

This structure informs the task decomposition. Each task should produce self-contained changes that make sense independently.

## Bite-Sized Task Granularity

**Each step is one action (2-5 minutes):**
- "Write the failing test" - step
- "Implement the minimal code to make the test pass" - step
- "Confirm the build, run the static checks, and commit" - step

Order tasks in test-driven-development's [Batch Order](../test-driven-development/SKILL.md#batch-order): the first task writes every test, later tasks implement, and only the final task runs the full tests.

## Plan Document Header

**Every plan MUST start with this header:**

```markdown
# [Feature Name] Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `subagent-driven-development` (recommended) or `executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** [One sentence describing what this builds]

**Architecture:** [2-3 sentences about approach]

**Tech Stack:** [Key technologies/libraries]

---
```

## Task Structure

````markdown
### Task 1: Failing tests

**Files:**
- Test: `tests/exact/path/to/test.py`

- [ ] **Step 1: Write the failing tests for every later task**

```python
def test_specific_behavior():
    result = function(input)
    assert result == expected
```

- [ ] **Step 2: Confirm the build, run static checks, and commit**

```bash
git add tests/path/test.py
git commit -m "test: specify specific feature" -m "Test Plan: <build check>: <observed result, naming each failure only later tasks' code removes>; <static check>: <observed result>; tests run in the final task"
```

### Task N: [Component Name]

**Files:**
- Create: `exact/path/to/file.py`
- Modify: `exact/path/to/existing.py:123-145`

- [ ] **Step 1: Write minimal implementation**

```python
def function(input):
    return expected
```

- [ ] **Step 2: Confirm the build, run static checks, and commit**

```bash
git add src/path/file.py
git commit -m "feat: add specific feature" -m "Test Plan: <build check>: <observed result>; <static check>: <observed result>; tests run in the final task"
```

### Final Task: Run the tests

- [ ] **Step 1: Run each new test without its code to verify it fails**

Run it in a scratch export of the commit before Task 1, with the new tests copied in (testing-discipline, Test validation):
Run: `mkdir -p "$HOME/tmp" && d=$(mktemp -d "$HOME/tmp/red.XXXX") && git archive <base> | tar -x -C "$d" && cp -r tests "$d"/ && (cd "$d" && pytest tests/path/test.py::test_name -v)`
Expected: FAIL on its assertion (stub a missing name in "$d" so the test fails rather than errors)

- [ ] **Step 2: Run the tests to verify they pass**

Run: `pytest tests/path/test.py -v`
Expected: PASS

- [ ] **Step 3: Fix every failure together, rerun until all pass, then commit with the Test Plan**
````

## No Placeholders

Every step must contain the actual content an engineer needs. These are **plan failures** — never write them:
- "TBD", "TODO", "implement later", "fill in details"
- "Add appropriate error handling" / "add validation" / "handle edge cases"
- "Write tests for the above" (without actual test code)
- "Similar to Task N" (repeat the code — the engineer may be reading tasks out of order)
- Steps that describe what to do without showing how (code blocks required for code steps)
- References to types, functions, or methods not defined in any task

## Remember
- Exact file paths always
- Complete code in every step — if a step changes code, show the code
- Exact commands with expected output
- DRY, YAGNI, TDD, frequent commits

## Self-Review

After writing the complete plan, look at the spec with fresh eyes and check the plan against it. This is a checklist you run yourself — not a subagent dispatch.

**1. Spec coverage:** Skim each section/requirement in the spec. Can you point to a task that implements it? List any gaps.

**2. Placeholder scan:** Search your plan for red flags — any of the patterns from the "No Placeholders" section above. Fix them.

**3. Type consistency:** Do the types, method signatures, and property names you used in later tasks match what you defined in earlier tasks? A function called `clearLayers()` in Task 3 but `clearFullLayers()` in Task 7 is a bug.

If you find issues, fix them inline. No need to re-review — just fix and move on. If you find a spec requirement with no task, add the task.

## Execution Handoff

After saving the plan, offer execution choice:

**"Plan complete and saved to `<path>`. Two execution options:**

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?"**

**If Subagent-Driven chosen:**
- **REQUIRED SUB-SKILL:** Use `subagent-driven-development`
- Fresh subagent per task + two-stage review

**If Inline Execution chosen:**
- **REQUIRED SUB-SKILL:** Use `executing-plans`
- Batch execution with checkpoints for review
