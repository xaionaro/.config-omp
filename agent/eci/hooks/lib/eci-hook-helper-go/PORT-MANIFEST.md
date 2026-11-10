# eci-hook-helper port manifest

Go replacement for the live Python hook helpers. Published as a third member of
the planner pair (`eci-command-plan`, `eci-safe-import`, `eci-hook-helper`) via
`bin/eci-runtime-sync`, receipt row `helper_sha256`.

| Former Python | Go mode |
| --- | --- |
| `hooks/lib/bounded_hook_input.py stdin` | `eci-hook-helper stdin` |
| `... hook-transcript-first-record <root>` | `eci-hook-helper hook-transcript-first-record <root>` |
| `... hook-transcript-thread-spawn-metadata <root>` | `eci-hook-helper hook-transcript-thread-spawn-metadata <root>` |
| `hooks/security-reminder.py` | `eci-hook-helper security-reminder` |
| `hooks/lib/eci-environment-command.sh` python recognizer (shell wrapper removed in C2r1; no live caller) | `eci-hook-helper environment-command <command>` |
| `hooks/lib/reviewer-filter.sh` python filter | `eci-hook-helper reviewer-filter <static-corpus> <extra-text> <threshold>` |

Not ported (no live caller): `first-record`, `transcript-first-record`.

Native-only mode (no Python predecessor): `edit-gate <bash> <dispatcher>` runs
`hooks/pretooluse-edit-dispatch.sh` once per target of the omp edit adapter's
request, through the `hooks/eci-edit-gate.sh` launcher.

The `coding-skill <describe|reduce|check>` mode implements the optional coding-skill
bridge policy; that bridge invokes the shared helper directly.

Known divergences from the Python implementation:
- NaN/Infinity JSON literals are rejected (Python accepted them).
- A lone surrogate in `transcript_path` exits 2 (Python exited 1).
- A lone surrogate in a metadata parent id is emitted as U+FFFD.
- `security-reminder` on non-object JSON or numeric `edits` returns 0 (Python crashed with 1).
- Non-string `tool_input` values use compact JSON rather than Python repr.
- A non-list state file is treated as empty.
- `hook-transcript-thread-spawn-metadata` reads the first record in chunks of at most 4096 bytes. A first record whose newline does not appear within the first 65537 bytes is rejected (Python parsed the truncated 65536-byte prefix and succeeded).
- `reviewer-filter` rejects JSON nested deeper than 52000 containers; CPython 3.14's own limit is nondeterministic between about 52100 and 52160.
- `reviewer-filter` stdin decodes invalid UTF-8 bytes as lone surrogates (surrogateescape), as Python does.
- `filter_violations` fails closed: when the helper is unavailable, exits nonzero, or prints nothing, it returns `{"verdict":"fail","violations":[]}` (Python fallback returned the unfiltered result).
