# omp Tool Mapping

Skills may name tools from other harnesses. When you encounter these in a skill, use the omp equivalent:

| Skill references | omp equivalent |
|-----------------|----------------|
| Legacy subagent dispatch | `task` (choose the most specific `agent`) |
| Legacy parallel dispatches | Multiple `task` entries in one `tasks[]` batch |
| Legacy subagent result | Auto-delivered results; `wait` only when blocked |
| Legacy fire-and-forget dispatch | `proc://<id>/kill` to cancel a running job |
| `update_plan` (task tracking) | No dedicated tool; keep the plan in visible notes |
| `Skill` tool (invoke a skill) | `read skill://<name>` |
| `Read`, `Write`, `Edit` (files) | `read`, `write`, `edit` |
| `Bash` (run commands) | `bash` |
| `Grep`, `Glob` | `grep`, `glob` |

## Named agent dispatch

Named agent types (for example `superpowers:code-reviewer`) are not an omp registry.
Use `task` with an omp agent selector (`scout`, `reviewer`, `security-reviewer`, `task`, `sonic`).
When a skill names a prompt file (for example `code-reviewer.md` or `code-quality-reviewer-prompt.md`):

1. Read the prompt file.
2. Fill its placeholders (`{BASE_SHA}`, `{WHAT_WAS_IMPLEMENTED}`, etc.).
3. Pass the filled content as the `task` body, framed as a task with explicit instructions.

Use task-delegation framing ("Your task is...") rather than persona framing ("You are..."), wrap instructions in XML tags, and end with an explicit execution directive.

## Environment Detection

Branch and PR workflows detect their environment with read-only git commands before proceeding:

```bash
GIT_DIR=$(cd "$(git rev-parse --git-dir)" 2>/dev/null && pwd -P)
GIT_COMMON=$(cd "$(git rev-parse --git-common-dir)" 2>/dev/null && pwd -P)
BRANCH=$(git branch --show-current)
```

- `GIT_DIR != GIT_COMMON` → already in a linked worktree
- `BRANCH` empty → detached HEAD (cannot branch/push/PR from sandbox)

See `finishing-a-development-branch` Step 1 for how that skill uses these signals.
