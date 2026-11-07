---
name: coding-python
description: Use when writing, reviewing, or modifying Python code (*.py, pyproject.toml, setup.cfg files) or reviewing diffs/PRs containing Python changes — ensures code quality and adherence to established Python conventions for naming, error handling, concurrency, typing, logging, and code organization
---

# Coding: Python

Read and apply [Coding: Common](../coding-common/SKILL.md) alongside these Python-specific requirements. Preserve the Python policies below where they differ.

## Idiomatic Python

- Write idiomatic Python (PEP 8, PEP 20 — The Zen of Python).

## Type Hints

- Type-annotate all function signatures and class attributes. Use `typing` / `collections.abc` for complex types.
- Named types for domain concepts: `UserID = NewType('UserID', int)`, not bare `int`. Use `Literal`, `TypeAlias`, `TypeVar` where they add clarity.
- Use `@dataclass` or `NamedTuple` for structured data — not plain dicts or tuples for domain objects.

## File tree

- One class per module when the class is substantial. Small related classes can share a module.
- Use `__init__.py` to define the public API of a package. Re-export only what external consumers need.

## Naming

- `snake_case` for functions, methods, variables, modules. `PascalCase` for classes. `UPPER_SNAKE` for module-level constants.
- Private by convention: single `_prefix` for internal. No double `__` mangling unless avoiding name collision in inheritance.
- Multi-parameter functions: each parameter on its own line when >3 params or line exceeds limit:
  ```python
  def remove_push_to(
      self,
      target: Abstract,
      *,
      force: bool = False,
  ) -> None:
  ```

## Error Handling

- Custom exceptions inherit from a project-specific base, not bare `Exception`.
- Use exception chaining: `raise NewError("...") from original_err`.
- Context managers (`with`) for all resource cleanup — files, connections, locks.
- Catch specific exceptions, never bare `except:` or `except Exception:` without re-raise.
- Cleanup-on-error pattern:
  ```python
  resource = acquire()
  try:
      use(resource)
  except SomeError:
      resource.rollback()
      raise
  ```

## Concurrency

- `asyncio` for I/O-bound concurrency. `concurrent.futures` for CPU-bound parallelism.
- Never mix blocking I/O into async code — use `asyncio.to_thread()` or `run_in_executor()`.
- Async context managers (`async with`) for async resource lifecycle.
- Observable event sources include `asyncio.Event`, `asyncio.Condition`, `Future`, `Queue`, `threading.Event`, signals, callbacks, and `inotify`.

## Other patterns

- Prefer `match`/`case` (3.10+) over `if`/`elif` chains when semantically there could be more than 2 options.
- Use `Enum` for finite sets of related constants, not string or int literals.
- Prefer composition over inheritance. Use protocols (`typing.Protocol`) for structural subtyping.

## Logging

- `logging` stdlib module. Get logger per module: `logger = logging.getLogger(__name__)`.
- Structured logging: pass extra data via `extra=` or structured formatters, not string interpolation in the message.
- Do not use `print()` for operational output outside of CLI entry points.
- **Level semantics** (use the right level for the situation):
  - `DEBUG` — detailed diagnostic, state changes, request handling.
  - `INFO` — rare, notable events only (startup, shutdown, config reload). Most messages should be `DEBUG`, not `INFO`.
  - `WARNING` — recoverable problems, degraded operation, unexpected-but-handled conditions.
  - `ERROR` — operation failed, needs attention but process continues.
  - `CRITICAL` — unrecoverable, process must exit.
- **Level consistency:** When adding log statements, scan how the same module uses levels for similar operations and match. If existing usage conflicts with the definitions above, raise the inconsistency to the user after finishing the task.

## Testing

- `pytest` as test runner. `assert` statements directly — no `self.assertEqual`.
- Use fixtures for setup/teardown. Parametrize for variant coverage.

## Code Organization

- Blank line between logical blocks within functions. Two blank lines between top-level definitions.
- Constants at module top, not magic values inline.
- Imports: stdlib → third-party → local, separated by blank lines. Absolute imports preferred.

## General discipline

- After every change, reduce code in related pieces while preserving readability and semantics.
- **Comments explain how or what's next**: Only `how-it-works` explanations and TODOs. Apply Coding: Common's authoring-process exclusions and external tracker rule.

## Locality and lifetime

- **Release early.** Use `with` blocks scoped tightly — don't hold files/connections open longer than needed.

## Dependencies

- Pin dependencies in `pyproject.toml` or `requirements.txt`. Never install packages without recording them.
- Prefer stdlib solutions over third-party when the stdlib option is adequate.
