# Skill Authoring Best Practices

Compact omp-native guidance for authoring skill files. Use with
`writing-skills`, `test-driven-development`, `harness-tuning`, and
`verification-before-completion`.

## Hard Rules

| Topic | Rule |
| --- | --- |
| Purpose | Write reusable techniques, patterns, references, or workflows. Do not write session stories. |
| Frontmatter | Include `name` and `description`. Start descriptions with `Use when`. |
| Descriptions | Trigger-only: name symptoms, contexts, file types, tools, and user wording. Do not summarize the workflow. |
| Names | Use letters, numbers, and hyphens. Prefer verb-first or gerund names. |
| References | Refer to other skills by bare omp-local names: `test-driven-development`, `harness-tuning`. |
| Loading | Avoid forced file-load syntax and home-directory paths in cross-references. |
| Agents | Test by spawning subagents. Do not run agents through shell commands. |
| Stop-hook handling | Subagent prompts must include the Stop-hook handling line below. |
| Evidence | Verify behavior before claiming the skill works. |

## Description Pattern

Descriptions are for routing. The body is for instructions.

| Bad | Good |
| --- | --- |
| `description: Use when implementing features; writes tests, then code, then refactors` | `description: Use when implementing any feature or bugfix, before writing implementation code` |
| `description: Helps with prompt cleanup` | `description: Use when writing, reviewing, shortening, or fixing skill files, system prompts, or AGENTS.md` |
| `description: Use for flaky tests with setTimeout` | `description: Use when tests have race conditions, timing dependencies, or inconsistent pass/fail behavior` |

Checklist:
- Starts with `Use when`.
- Names the trigger, not the procedure.
- Uses words users actually say.
- Stays third-person and imperative-free.
- Keeps process details out of frontmatter.

## Structure

```text
skills/
  skill-name/
    SKILL.md
    reference-or-tool-if-needed.*
```

| Put in `SKILL.md` | Split into another file |
| --- | --- |
| Core principle | Long API/reference tables |
| Trigger guidance | Reusable scripts or templates |
| Required workflow | Large examples |
| Short examples | Generated or bulky assets |

Preferred `SKILL.md` order:
1. Frontmatter.
2. One-sentence core rule.
3. When to use and when not to use.
4. Required workflow or quick reference.
5. Examples only where they change behavior.
6. Common mistakes.
7. Verification checklist.

## Degree of Freedom

| Need | Format |
| --- | --- |
| Judgment-heavy task | Short principles and decision table |
| Preferred but flexible approach | Numbered steps or pseudocode |
| Fragile operation | Exact command or script with allowed parameters |

Use the narrowest instruction that prevents likely failure. Do not over-specify harmless choices.

## Cross-Skill References

Use bare names and requirement labels:

```markdown
**REQUIRED BACKGROUND:** Use test-driven-development.
**REQUIRED CHECK:** Use verification-before-completion before reporting done.
**STYLE RULE:** Apply harness-tuning to keep this skill concise.
```

Do not reference old skill roots, absolute home paths, or load directives. If a skill is optional, say when to load it.

## Testing Skills

Treat skill authoring as TDD for process documentation.

| Phase | Action |
| --- | --- |
| RED | Run pressure scenarios without the skill. Record exact failures and rationalizations. |
| GREEN | Add the smallest guidance that blocks those failures. |
| REFACTOR | Re-test, close new loopholes, remove redundant text. |

Spawn a subagent for subagent tests. The prompt must include:

```text
Follow any Stop-hook prompt in this session, including required proof/checklist files. Fix blockers within your assigned scope. Report only when recovery needs out-of-scope changes, unrelated user work, credentials, or approval.
```

Do not shell-wrap agents. Use the standard agent management path available in the session.

## Pressure Scenarios

| Skill type | Test for |
| --- | --- |
| Discipline | Compliance under time pressure, authority pressure, sunk cost, or fatigue. |
| Technique | Correct application on realistic variants. |
| Pattern | Recognition, non-use cases, and edge cases. |
| Reference | Retrieval speed, missing common use cases, and correct command/API use. |

Capture failures as specific edits. A rationalization table usually beats more prose.

## Concision Rules

Apply `harness-tuning`:
- One idea per sentence.
- Tables for 3+ parallel items.
- Delete prose that repeats a table.
- Keep one strong example, not many weak examples.
- Move long command help to scripts or reference files.
- Prefer examples that are complete and runnable.

## Verification

Before publishing or reporting success:
- Run the skill's pressure tests by spawning subagents.
- Run relevant repo checks.
- Run `git diff --check`.
- Re-read changed lines.
- Use `verification-before-completion` for final claims.

If editing this reference, also audit for legacy provider terms, shell-agent workflows, old skill roots, and forced-load syntax.
