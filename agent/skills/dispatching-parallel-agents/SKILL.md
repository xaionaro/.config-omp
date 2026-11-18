---
name: dispatching-parallel-agents
description: Use when facing 2+ independent tasks that can be worked on without shared state or sequential dependencies
---

# Dispatching Parallel Agents

## Authorization

Use this skill only when the user explicitly requested subagents, delegation, or parallel agent work.

## Overview

You delegate tasks to specialized agents with isolated context. By precisely crafting their instructions and context, you ensure they stay focused and succeed at their task. They should never inherit your session's context or history — you construct exactly what they need. This also preserves your own context for coordination work.

When you have multiple unrelated failures (different test files, different subsystems, different bugs), investigating them sequentially wastes time. Each investigation is independent and can happen in parallel.

**Core principle:** Dispatch one agent per independent problem domain. Let them work concurrently.

## When to Use

```dot
digraph when_to_use {
    "Multiple failures?" [shape=diamond];
    "Are they independent?" [shape=diamond];
    "Single agent investigates all" [shape=box];
    "One agent per problem domain" [shape=box];
    "Can they work in parallel?" [shape=diamond];
    "Sequential agents" [shape=box];
    "Parallel dispatch" [shape=box];

    "Multiple failures?" -> "Are they independent?" [label="yes"];
    "Are they independent?" -> "Single agent investigates all" [label="no - related"];
    "Are they independent?" -> "Can they work in parallel?" [label="yes"];
    "Can they work in parallel?" -> "Parallel dispatch" [label="yes"];
    "Can they work in parallel?" -> "Sequential agents" [label="no - shared state"];
}
```

**Use when:**
- 3+ test files failing with different root causes
- Multiple subsystems broken independently
- Each problem can be understood without context from others
- No shared state between investigations

**Don't use when:**
- Failures are related (fix one might fix others)
- Need to understand full system state
- Agents would interfere with each other

## The Pattern

### 1. Identify Independent Domains

Group failures by what's broken:
- File A tests: Tool approval flow
- File B tests: Batch completion behavior
- File C tests: Abort functionality

Each domain is independent - fixing tool approval doesn't affect abort tests.

### 2. Create Focused Agent Tasks

Each agent gets:
- **Specific scope:** One test file or subsystem
- **Clear goal:** Make these tests pass
- **Constraints:** Don't change other code
- **Role label:** A stable label in the first prompt line
- **Stop-hook handling:** Follow any Stop-hook prompt in that session, including required proof/checklist files. Clear stuck work within assigned scope. Report to the orchestrator only when recovery needs out-of-scope changes, unrelated user work, credentials, or approval. Include in that report the proof the Stuck work rule in `~/.omp/agent/RULES.md` requires for any stuck claim.
- **Expected output:** Summary of what you found and fixed
- **Sources:** Mark each constraint with `[Ref N]` and end with a References list, under the Sourced constraints rule in `~/.omp/agent/RULES.md`. For your own decision, such as a scope limit, the entry also quotes the item above that grants it.

### 3. Dispatch in Parallel

`agent_type="worker"` maps to `agent="task"`; `reasoning_effort` has no `task` equivalent and is dropped; `message` maps to `task`.

```python
agent_tool_abort = task(
    agent="task",
    task="""Role label: agent-tool-abort

Fix the failures in src/agents/agent-tool-abort.test.ts [Ref 1].

Scope: follow [Ref 2].

Stop-hook boundary: follow [Ref 3].

Return: follow [Ref 4].

References
1. User request (this session): "[the user's words asking for these fixes]"
2. Delegator decision, recorded here: "- Read only the abort-related test and implementation paths needed for this failure." / "- Keep changes focused on abort behavior and its tests." / "- Do not modify batch completion or tool approval race tests." Granted by ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "- **Specific scope:** One test file or subsystem" / "- **Constraints:** Don't change other code"
3. ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "- **Stop-hook handling:** Follow any Stop-hook prompt in that session, including required proof/checklist files. Clear stuck work within assigned scope. Report to the orchestrator only when recovery needs out-of-scope changes, unrelated user work, credentials, or approval. Include in that report the proof the Stuck work rule in `~/.omp/agent/RULES.md` requires for any stuck claim."
4. Delegator decision, recorded here: "Return: root cause, changed files, verification run, and remaining risk." Granted by ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "- **Expected output:** Summary of what you found and fixed"
""",
)

batch_completion = task(
    agent="task",
    task="""Role label: batch-completion

Fix the failures in src/agents/batch-completion-behavior.test.ts [Ref 1].

Scope: follow [Ref 2].

Stop-hook boundary: follow [Ref 3].

Return: follow [Ref 4].

References
1. User request (this session): "[the user's words asking for these fixes]"
2. Delegator decision, recorded here: "- Read only the batch completion test and implementation paths needed for this failure." / "- Keep changes focused on batch completion behavior." / "- Do not modify abort or tool approval race tests." Granted by ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "- **Specific scope:** One test file or subsystem" / "- **Constraints:** Don't change other code"
3. ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "- **Stop-hook handling:** Follow any Stop-hook prompt in that session, including required proof/checklist files. Clear stuck work within assigned scope. Report to the orchestrator only when recovery needs out-of-scope changes, unrelated user work, credentials, or approval. Include in that report the proof the Stuck work rule in `~/.omp/agent/RULES.md` requires for any stuck claim."
4. Delegator decision, recorded here: "Return: root cause, changed files, verification run, and remaining risk." Granted by ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "- **Expected output:** Summary of what you found and fixed"
""",
)

tool_approval_races = task(
    agent="task",
    task="""Role label: tool-approval-races

Fix the failures in src/agents/tool-approval-race-conditions.test.ts [Ref 1].

Scope: follow [Ref 2].

Stop-hook boundary: follow [Ref 3].

Return: follow [Ref 4].

References
1. User request (this session): "[the user's words asking for these fixes]"
2. Delegator decision, recorded here: "- Read only the tool approval race test and implementation paths needed for this failure." / "- Keep changes focused on approval timing behavior." / "- Do not modify abort or batch completion tests." Granted by ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "- **Specific scope:** One test file or subsystem" / "- **Constraints:** Don't change other code"
3. ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "- **Stop-hook handling:** Follow any Stop-hook prompt in that session, including required proof/checklist files. Clear stuck work within assigned scope. Report to the orchestrator only when recovery needs out-of-scope changes, unrelated user work, credentials, or approval. Include in that report the proof the Stuck work rule in `~/.omp/agent/RULES.md` requires for any stuck claim."
4. Delegator decision, recorded here: "Return: root cause, changed files, verification run, and remaining risk." Granted by ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "- **Expected output:** Summary of what you found and fixed"
""",
)

# Print the roster immediately after spawn using the returned runtime names:
# agent-tool-abort: <runtime name> [worker]
# batch-completion: <runtime name> [worker]
# tool-approval-races: <runtime name> [worker]
```

### 4. Review and Integrate

Park until the spawned agents' results auto-deliver (the Parking rule in `~/.omp/agent/RULES.md`); integrate only after delivery.

When agents return:
- Read each summary
- Review every changed line before accepting it
- Verify fixes don't conflict
- Run full test suite
- Integrate all changes only after review

## Agent Prompt Structure

Good agent prompts are:
1. **Focused** - One clear problem domain
2. **Self-contained** - All context needed to understand the problem
3. **Specific about output** - What should the agent return?
4. **Labeled** - First line is `Role label: <stable-role>`
5. **Bounded** - Includes the Stop-hook handling item above.

```markdown
Role label: agent-tool-abort

Fix the 3 failing tests in src/agents/agent-tool-abort.test.ts [Ref 1]:

1. "should abort tool with partial output capture" - expects 'interrupted at' in message
2. "should handle mixed completed and aborted tools" - fast tool aborted instead of completed
3. "should properly track pendingToolCount" - expects 3 results but gets 0

These are timing/race condition issues. Your task: follow [Ref 2].

Timeouts: follow [Ref 3].

Stop-hook boundary: follow [Ref 4].

Return: follow [Ref 5].

References
1. User request (this session): "[the user's words asking for these fixes]"
2. Delegator decision, recorded here: "1. Read the test file and understand what each test verifies" / "2. Identify root cause - timing issues or actual bugs?" / "3. Fix by:" / "- Replacing arbitrary timeouts with event-based waiting" / "- Fixing bugs in abort implementation if found" / "- Adjusting test expectations if testing changed behavior" Granted by ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "- **Clear goal:** Make these tests pass"
3. Delegator decision, recorded here: "Do NOT just increase timeouts - find the real issue." Granted by ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "- **Constraints:** Don't change other code"
4. ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "- **Stop-hook handling:** Follow any Stop-hook prompt in that session, including required proof/checklist files. Clear stuck work within assigned scope. Report to the orchestrator only when recovery needs out-of-scope changes, unrelated user work, credentials, or approval. Include in that report the proof the Stuck work rule in `~/.omp/agent/RULES.md` requires for any stuck claim."
5. ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "- **Expected output:** Summary of what you found and fixed"
```

## Common Mistakes

**❌ Too broad:** "Fix all the tests" - agent gets lost
**✅ Specific:** "Fix agent-tool-abort.test.ts" - focused scope

**❌ No context:** "Fix the race condition" - agent doesn't know where
**✅ Context:** Paste the error messages and test names

**❌ No constraints:** Agent might refactor everything
**✅ Constraints:** "Do NOT change production code [Ref 2]" or "Fix tests only [Ref 2]", with entry 2 quoting your decision and the **Constraints:** item that grants it

**❌ Vague output:** "Fix it" - you don't know what changed
**✅ Specific:** "Return summary of root cause and changes"

## When NOT to Use

**Related failures:** Fixing one might fix others - investigate together first
**Need full context:** Understanding requires seeing entire system
**Exploratory debugging:** You don't know what's broken yet
**Shared state:** Agents would interfere (editing same files, using same resources)

## Real Example from Session

**Scenario:** 6 test failures across 3 files after major refactoring

**Failures:**
- agent-tool-abort.test.ts: 3 failures (timing issues)
- batch-completion-behavior.test.ts: 2 failures (tools not executing)
- tool-approval-race-conditions.test.ts: 1 failure (execution count = 0)

**Decision:** Independent domains - abort logic separate from batch completion separate from race conditions

**Dispatch:**
```
Use the three `task` calls from "Dispatch in Parallel" with role labels:
agent-tool-abort
batch-completion
tool-approval-races

roster:
agent-tool-abort: <runtime name> [worker]
batch-completion: <runtime name> [worker]
tool-approval-races: <runtime name> [worker]

wait:
wait
```

**Results:**
- Agent 1: Replaced timeouts with event-based waiting
- Agent 2: Fixed event structure bug (threadId in wrong place)
- Agent 3: Added wait for async tool execution to complete

**Integration:** All fixes independent, no conflicts, full suite green

**Time saved:** 3 problems solved in parallel vs sequentially

## Key Benefits

1. **Parallelization** - Multiple investigations happen simultaneously
2. **Focus** - Each agent has narrow scope, less context to track
3. **Independence** - Agents don't interfere with each other
4. **Speed** - 3 problems solved in time of 1

## Verification

After agents return:
1. **Review each summary** - Understand what changed
2. **Check for conflicts** - Did agents edit same code?
3. **Run full suite** - Verify all fixes work together
4. **Spot check** - Agents can make systematic errors

## Real-World Impact

From debugging session (2025-10-03):
- 6 failures across 3 files
- 3 agents dispatched in parallel
- All investigations completed concurrently
- All fixes integrated successfully
- Zero conflicts between agent changes
