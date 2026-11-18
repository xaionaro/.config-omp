# Implementer Subagent Prompt Template

Use this template when dispatching an implementer subagent.

`agent_type` maps to `agent` (`task`, the general-purpose omp agent); `reasoning_effort` has no `task` equivalent and is dropped; `message` maps to `task`.

```
task(
  agent="task",
  task="""
Role label: task-n-implementer

<task>
Implement Task N: [task name] [Ref 10].
Paste the full task text here; do not make the agent read the plan file.
</task>

<task-description>
[FULL TEXT of task from plan, its [Ref N] markers renumbered from 11]
</task-description>

<context>
[Scene-setting: where this fits, dependencies, architectural context]
Work from: [directory]
</context>

<stop-hook-boundary>
Follow [Ref 1].
</stop-hook-boundary>

<clarify-first>
Follow [Ref 2].
</clarify-first>

<execute>
Follow [Ref 3], under [Ref 4] and [Ref 5].
</execute>

<code-organization>
Follow [Ref 6].
</code-organization>

<escalate>
Follow [Ref 7].
</escalate>

<self-review>
Follow [Ref 8].
</self-review>

<report>
Report each item in [Ref 9].
</report>

References
1. ~/.omp/agent/skills/dispatching-parallel-agents/SKILL.md ### 2. Create Focused Agent Tasks: "[its Stop-hook handling item, quoted when you fill this]"
2. ~/.omp/agent/skills/subagent-driven-development/implementer-prompt.md References, entry 2 (`<clarify-first>`): "Before editing, ask about unclear requirements, acceptance criteria, approach, dependencies, or assumptions." / "Pause when uncertain. Do not guess."
3. ~/.omp/agent/skills/subagent-driven-development/implementer-prompt.md References, entry 3 (`<execute>`): "1. Do exactly what the task specifies, writing any test before the code it tests." / "2. Confirm the build as `skill://test-driven-development` Batch Order defines, then run the static checks. Run tests only as Batch Order allows or when the task says to." / "3. Commit your work with a Test Plan listing those checks and their observed results." / "4. Self-review before reporting."
4. ~/.omp/agent/skills/test-driven-development/SKILL.md ## Batch Order: "[its build-confirmation and test-run sentences and its `subagent-driven-development` row, quoted when you fill this]"
5. ~/.omp/agent/eci-binding-contract.md ## Git: "[its `Test Plan` item, quoted when you fill this]"
6. ~/.omp/agent/skills/subagent-driven-development/implementer-prompt.md References, entry 6 (`<code-organization>`): "- Follow the plan's file structure." / "- Keep each file focused with a clear interface." / "- If a new file grows beyond the plan's intent, stop and report DONE_WITH_CONCERNS." / "- If an existing file is large or tangled, work carefully and report the concern." / "- Follow existing patterns. Improve touched code only within task scope."
7. ~/.omp/agent/skills/subagent-driven-development/implementer-prompt.md References, entry 7 (`<escalate>`): "Report NEEDS_CONTEXT, or DONE_WITH_CONCERNS, when the task needs unplanned architecture choices, unclear surrounding code, uncertain correctness, unplanned restructuring, or broad exploration without progress, naming what you tried and the context or decision you need." / "Report BLOCKED only for stuck work proven under the Stuck work rule in `~/.omp/agent/RULES.md`, with that proof and the help you need."
8. ~/.omp/agent/skills/subagent-driven-development/implementer-prompt.md References, entry 8 (`<self-review>`): "Check completeness, edge cases, names, maintainability, YAGNI, existing patterns, and tests." / "Fix issues before reporting."
9. ~/.omp/agent/skills/subagent-driven-development/implementer-prompt.md References, entry 9 (`<report>`): "- **Status:** DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT" / "- What you implemented, or attempted if blocked" / "- Checks and tests you ran, and results" / "- Files changed" / "- Self-review findings" / "- Issues or concerns"
10. [plan path] ### Task N: "[the task's heading line]"
[The plan's entries for the task text's markers, renumbered from 11, then your own entries]
"""
)
```

After spawn, print the roster entry: `task-n-implementer: <runtime name> [worker]`.
