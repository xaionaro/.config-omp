---
name: using-superpowers
description: Use when starting a conversation or when skill routing is unclear - establishes how to find and apply available skills
---

<SUBAGENT-STOP>
If you were dispatched as a subagent to execute a specific task, skip this skill.
</SUBAGENT-STOP>

## Core Rule

Load named skills and clearly matching skills before acting. Do not load marginal skills just to check. Higher-priority omp instructions control skill usage.

## Instruction Priority

Follow active omp priority. Apply these skills only when they do not conflict with higher-priority system/developer instructions or explicit user instructions.

If AGENTS.md or GEMINI.md says "don't use TDD" and a skill says "always use TDD," follow the user's instructions. The user is in control.

## Skill Access

Use the skills listed by the current omp environment. If a needed local skill is not listed but exists under `~/.omp/agent/skills`, read its `SKILL.md` and follow it. Load only the files needed for the current task.

# Using Skills

## The Rule

**Load relevant or requested skills before acting.** If a loaded skill turns out not to fit, state that briefly and continue with the correct workflow.

```dot
digraph skill_flow {
    "User message received" [shape=doublecircle];
    "About to EnterPlanMode?" [shape=doublecircle];
    "Already brainstormed?" [shape=diamond];
    "Invoke brainstorming skill" [shape=box];
    "Does a listed skill clearly apply?" [shape=diamond];
    "Invoke skill loader" [shape=box];
    "Announce: 'Using [skill] to [purpose]'" [shape=box];
    "Has checklist?" [shape=diamond];
    "Create todo per item" [shape=box];
    "Follow skill exactly" [shape=box];
    "Respond (including clarifications)" [shape=doublecircle];

    "About to EnterPlanMode?" -> "Already brainstormed?";
    "Already brainstormed?" -> "Invoke brainstorming skill" [label="no"];
    "Already brainstormed?" -> "Does a listed skill clearly apply?" [label="yes"];
    "Invoke brainstorming skill" -> "Does a listed skill clearly apply?";

    "User message received" -> "Does a listed skill clearly apply?";
    "Does a listed skill clearly apply?" -> "Invoke skill loader" [label="yes"];
    "Does a listed skill clearly apply?" -> "Respond (including clarifications)" [label="no"];
    "Invoke skill loader" -> "Announce: 'Using [skill] to [purpose]'";
    "Announce: 'Using [skill] to [purpose]'" -> "Has checklist?";
    "Has checklist?" -> "Create todo per item" [label="yes"];
    "Has checklist?" -> "Follow skill exactly" [label="no"];
    "Create todo per item" -> "Follow skill exactly";
}
```

## Red Flags

These thoughts mean STOP—you're rationalizing:

| Thought | Reality |
|---------|---------|
| "This is just a simple question" | Questions are tasks. Check for skills. |
| "I need more context first" | Skill check comes BEFORE clarifying questions. |
| "Let me explore the codebase first" | Skills tell you HOW to explore. Check first. |
| "I can check git/files quickly" | Files lack conversation context. Check for skills. |
| "Let me gather information first" | Skills tell you HOW to gather information. |
| "This doesn't need a formal skill" | If a skill exists, use it. |
| "I remember this skill" | Skills evolve. Read current version. |
| "This doesn't count as a task" | Action = task. Check for skills. |
| "The skill is overkill" | Simple things become complex. Use it. |
| "I'll just do this one thing first" | Check BEFORE doing anything. |
| "This feels productive" | Undisciplined action wastes time. Skills prevent this. |
| "I know what that means" | Knowing the concept ≠ using the skill. Invoke it. |

## Skill Priority

When multiple skills could apply, use this order:

1. **Process skills first** (brainstorming, debugging) - these determine HOW to approach the task
2. **Implementation skills second** (frontend-design, mcp-builder) - these guide execution

"Let's build X" → brainstorming first, then implementation skills.
"Fix this bug" → debugging first, then domain-specific skills.

## Skill Types

**Rigid** (TDD, debugging): Follow exactly. Don't adapt away discipline.

**Flexible** (patterns): Adapt principles to context.

The skill itself tells you which.

## User Instructions

Instructions say WHAT, not HOW. "Add X" or "Fix Y" doesn't mean skip workflows.
