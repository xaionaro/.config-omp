# Spec Document Reviewer Prompt Template

Use this template when dispatching a spec document reviewer subagent.

**Purpose:** Verify the spec is complete, consistent, and ready for implementation planning.

**Dispatch after:** Spec document is written to docs/specs/

`agent_type` maps to `agent` (`scout`, the read-only omp agent); `reasoning_effort` has no `task` equivalent and is dropped; `message` maps to `task`.

```
task(
  agent="scout",
  task="""
Role label: spec-document-reviewer

<task>
Review [SPEC_FILE_PATH] for planning readiness [Ref 1].
</task>

<stop-hook-boundary>
Follow [Ref 2].
</stop-hook-boundary>

<check>
Check each row in [Ref 3].
</check>

<calibration>
Follow [Ref 4].
</calibration>

<report>
Report in the form [Ref 5] gives.
</report>

References
1. ~/.omp/agent/skills/brainstorming/spec-document-reviewer-prompt.md: "**Purpose:** Verify the spec is complete, consistent, and ready for implementation planning."
2. ~/.omp/agent/skills/brainstorming/spec-document-reviewer-prompt.md References, entry 2 (`<stop-hook-boundary>`): "Follow any Stop-hook prompt in this session, including required proof/checklist files." / "Fix blockers within your assigned scope. Report to the orchestrator only when recovery needs out-of-scope changes, unrelated user work, credentials, or approval."
3. ~/.omp/agent/skills/brainstorming/spec-document-reviewer-prompt.md References, entry 3 (`<check>`): "| Category | Look For |" / "| Completeness | TODOs, placeholders, "TBD", incomplete sections |" / "| Consistency | Internal contradictions, conflicting requirements |" / "| Clarity | Requirements ambiguous enough to cause wrong implementation |" / "| Scope | Focused enough for one plan, not multiple independent subsystems |" / "| YAGNI | Unrequested features, over-engineering |"
4. ~/.omp/agent/skills/brainstorming/spec-document-reviewer-prompt.md References, entry 4 (`<calibration>`): "Only flag issues that would break implementation planning." / "Approve unless gaps would produce a flawed plan." / "Treat wording, style, and uneven section detail as advisory."
5. ~/.omp/agent/skills/brainstorming/spec-document-reviewer-prompt.md References, entry 5 (`<report>`): "## Spec Review" / "**Status:** Approved | Issues Found" / "**Issues (if any):**" / "- [Section X]: [specific issue] - [why it matters for planning]" / "**Recommendations (advisory, do not block approval):**" / "- [suggestions for improvement]"
[Your entries, numbered from 6]
"""
)
```

After spawn, print the roster entry: `spec-document-reviewer: <runtime name> [explorer]`.

**Reviewer returns:** Status, Issues (if any), Recommendations
