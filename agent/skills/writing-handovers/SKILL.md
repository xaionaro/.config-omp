---
name: writing-handovers
description: Use when ending a session with unfinished work, handing off to another agent or human, writing a handoff/handover/baton-pass note, or saving session state to disk before context fills.
---

# Writing Handovers

A handover is a self-contained markdown file. A fresh agent with the file plus repo access alone must resume the work — no "see above", no "the conversation we had", no implicit context.

## Handover vs `/compact`

| Axis | `/compact` | Handover |
|------|-----------|----------|
| Output destination | Live context (replaces history) | File on disk |
| Continuation actor | Same session, same agent | Different session, agent, or human |
| Persistence | Lost on `/clear`; bound to originating session | Survives indefinitely |
| Skills carried forward | Auto-reattached after summarization | Successor must reload skills |
| Use when | Mid-session, near full | Wrapping session with unfinished work |

## Required sections

Every section is mandatory. Empty section: write `N/A — <one-line reason>`.

| # | Section | Must contain |
|---|---------|--------------|
| 1 | Goal | One sentence. Concrete end-state, not "continue work". |
| 2 | Status | Bullets: done / in-progress / not started. SHAs, file paths, line numbers. |
| 3 | Decisions | Each: rule + Why + How to apply. |
| 4 | Tried & ruled out | Approach + why rejected + evidence (cmd output, file:line, link). |
| 5 | Evidence pointers | Absolute paths, URLs, log paths, commit SHAs, test names. No "earlier in conversation". |
| 6 | Open questions | Each: question + what answer unblocks + who/where to ask. |
| 7 | Next action | Exactly one concrete step + verifiable success criterion. Not "investigate X". |
| 8 | Environment | Cwd, branch, dirty-tree state, running PIDs, env vars, tmux panes, ports. |
| 9 | Gotchas | Non-obvious traps (flaky test, prod-only behavior, root-only path). |

## Output rules

| Rule | Value |
|------|-------|
| Default path | `/tmp/handover-<topic-slug>-<YYYYMMDD-HHMM>.md` |
| User-specified path | Use verbatim. |
| Format | Pure markdown; absolute paths only; no relative refs. |
| Self-containment test | Fresh agent + file + repo access alone must resume. |

## Anti-patterns

| Anti-pattern | Fix |
|--------------|-----|
| Narrative ("I started by reading…, then tried…") | Bullet facts under Status / Tried & ruled out. |
| Vague next action ("continue debugging") | Concrete step + observable success criterion. |
| Missing Open questions | List even speculative; absent = "I assumed everything settled" — usually wrong. |
| Relative refs ("the file we changed", "see above") | Absolute paths and SHAs. |
| Skipping Tried & ruled out | Successor repeats dead ends — defeats the document. |
| Embedding large logs inline | Link to `/tmp/<log>.txt`; keep handover scannable. |
| Editorializing ("this was tricky") | Delete. |
| Decisions missing Why | Add — without why, successor cannot judge edge cases. |
