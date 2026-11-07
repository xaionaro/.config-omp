---
name: coding-go
description: Use when writing, reviewing, or modifying Go code (*.go, go.mod, go.sum files) or reviewing diffs/PRs containing Go changes — ensures code quality and adherence to the established Go conventions for naming, error handling, concurrency, types, logging, and code organization
---

# Coding: Go

Read and apply [Coding: Common](../coding-common/SKILL.md) alongside these Go-specific requirements.

## Idiomatic Go

- Write idiomatic Go (Effective Go, Go Proverbs).
- **Prefer existing implementations.** Use well-maintained third-party packages over reimplementing functionality. Search for established libraries before writing from scratch.
- `./internal` is a non-reusability smell.

## Strong Typing

- Named types for domain concepts (`type UserID uint64`, not bare `uint64`). Typed constants with iota. Use generics instead of `any`/`interface{}`.
- A set of named values goes on an integer kind. For a closed set or fixed flag set whose wire form is fixed, keep the integer backing and translate in the marshalers. An ordered quantity goes on `int` unless range exceeding `int32`, interop, or footprint over millions of instances forces a specific kind. Fractional values go on integer kinds when exactness is required, float kinds only where rounding error is acceptable. A fixed set of up to 64 independent combinable flags goes on `uint64`. A growing flag set, or one over 64 flags, goes on a struct of bools.
- Struct fields are exported by default. Only unexport a field when external mutation would violate an invariant maintained by the type's methods.

## File tree

- A declaration belongs in the non-exempt file of the group it serves or that most uses it, within its package; never alone in a file carrying no complete concept. When several files fit, or none does, keep the current file. Exempt: main.go, doc.go, test files, generated code.

## Naming

Multi-parameter functions: each parameter on its own line, closing `) returnType {` on separate line:

```go
func (n *NodeWithCustomData[C, T]) RemovePushTo(
	ctx context.Context,
	to Abstract,
) (_err error) {
```

## Error Handling

- Match explicitly suppressible errors with `errors.Is`/`errors.As`. If the function returns `error`, propagate unknown errors.
- Function signatures return `error`, never a concrete error type (`*ParseError`, `ErrFoo`). Use concrete types only for construction and `errors.As`/`errors.Is` matching.
- Custom error types implement `Unwrap() error`.
- Accumulate errors in `var errs []error`, return `errors.Join(errs...)`.
- Switch on error type:
  ```go
  switch {
  case err == nil:
  case errors.As(err, &ErrNotImplemented{}):
      logger.Warnf(ctx, "...")
  default:
      return fmt.Errorf("...: %w", err)
  }
  ```
- Deferred logging with named returns:
  ```go
  func (c *Codec) Close(ctx context.Context) (_err error) {
      logger.Tracef(ctx, "Close")
      defer func() { logger.Tracef(ctx, "/Close: %v", _err) }()
  ```
- Cleanup-on-error via defer:
  ```go
  defer func() {
      if _err != nil {
          _ = c.Close(ctx)
      }
  }()
  ```

## Concurrency

- `context.Context` is always the first parameter. Never stored in structs.
- Goroutines via `observability.Go(ctx, func(ctx context.Context) { ... })`, never raw `go`.
- **Prefer atomics over locks.** Use `sync/atomic` for single-word state (`atomic.Pointer[T]` for pointers). Prefer CAS (`CompareAndSwap` loop) for multi-step non-blocking transitions. Hold a mutex only when the mutation cannot be swapped atomically, guards an invariant atomics cannot express, or performs blocking work.
- Mutex unlock always via `defer`. Prefer small functions that lock/defer-unlock at the top, rather than locking inside complex functions with multiple code paths.
- Observable event sources include channels, `sync.Cond`, `ctx.Done()`, `fsnotify`, callbacks, and signals.

## Types & Generics

- Option pattern:
  ```go
  type Option interface { apply(*Config) }
  type Options []Option
  func (opts Options) config() Config { cfg := defaultConfig(); opts.apply(&cfg); return cfg }
  type optionCheckInterval time.Duration
  func (o optionCheckInterval) apply(c *Config) { c.CheckInterval = (time.Duration)(o) }
  ```

## Function Values

- Prefer named interfaces over anonymous functions/closures. They hide behavior, defeat method discovery, capture state implicitly, and do not serialize.
- Anonymous functions are OK only for `defer`/`go`/`observability.Go` bodies, one-off `sort.Slice` less funcs, and test closures. Anything reused, stored, or passed across packages uses a named type plus interface.

## Other patterns

- Never use `else if`. Always use `switch` if semantically there could be more then 2 options. Even if in practice you currently have 1-2 options, but semantically there could be more, it still should be a `switch`.

## Logging

- `github.com/facebookincubator/go-belt` via context: `logger.Debugf(ctx, "...")`.
- Logger is always derived from `context.Context`. Never store a logger in a struct.
- Structured fields: `belt.WithField(ctx, "key", value)`.
- Entry/exit tracing: `logger.Tracef(ctx, "MethodName")` / `logger.Tracef(ctx, "/MethodName")`.
- Do not reference stdin/stdout/stderr outside of the `main` package.
- **Level semantics** (use the right level for the situation):
  - `Trace` — method entry/exit, low-level flow tracing.
  - `Debug` — normal operational messages, state changes, request handling.
  - `Info` — rare, notable events only (startup, shutdown, config reload). Most messages should be `Debug`, not `Info`.
  - `Warn` — recoverable problems, degraded operation, unexpected-but-handled conditions.
  - `Error` — operation failed, needs attention but process continues.
  - `Fatal` — unrecoverable, process must exit.
- **Level consistency:** When adding log statements, scan how the same package uses levels for similar operations and match. If existing usage conflicts with the level definitions above, raise the inconsistency to the user after finishing the task.

## Testing

- `github.com/stretchr/testify` — `assert` for soft, `require` for fatal.

## Code Organization Principles

- Blank line between logical blocks within functions. Never double blank lines.
- Constants as `const` block at file top, not magic values inline.

## General discipline

- **Document every function or method—including function literals and interface method specifications—every type declaration and constant at any scope, and every package-level variable; exported and unexported items are both included.** Put a concise, clear comment immediately before each item; put a function literal's comment directly before its `func` keyword, reformatting the expression when needed. State semantics in one paragraph, add an empty comment line (`//`), then add an `Example:` paragraph with the most salient generic/common use; if none exists, give the primary/intended use. Exclude struct fields, non-method interface elements, local variables, parameters, and type parameters.

  ```go
  // <name> <concise, clear meaning or behavior>.
  //
  // Example: <most salient generic/common use>.
  ```

## Locality

- Use `:=` in `if`/`for`/`switch` init statements for the narrowest variable scope.

## Modules

- Local filesystem `replace` directives are forbidden in `go.mod` and must live in `go.work`. Every local `use` or `replace` path in `go.work` must be relative to that `go.work` file; absolute local filesystem paths are forbidden. Remote module-version replacements remain allowed.
- In Git repos, activate `hooks/install-pre-commit-go-mod.sh` to enforce this invariant against staged `go.mod` files and to keep local overrides in `go.work`.
