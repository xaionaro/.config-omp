---
name: coding-common
description: Use when writing, modifying, or reviewing code in any language, including code diffs and pull requests.
---

# Coding: Common

Apply these requirements alongside the language-specific coding skill. Language-specific rules govern language syntax, APIs, and explicit policy differences. Apply type-system requirements where the language supports them.

## Strict rules

### Design and APIs

- Choose the correct, clean solution even when it is harder than the simple, practical, or convenient one.
- **Read before using.** Before first use of any package or library, read its API surface in documentation or source. Do not guess signatures, defaults, or behavior.
- Prefer established, adequate implementations over reimplementing functionality. Search for them before writing from scratch. Follow the language-specific standard-library and third-party dependency policy.
- Prefer reusable domain primitives over one-off code.
- Use named types for domain concepts instead of bare primitive types.
- Choose underlying types by the domain's value space and operations, never by minimal byte width alone.
- Group related behavior through types and interfaces. Encode domain relationships in the type system instead of scattering them across unrelated functions.
- Define each logic rule or constant once and reference its authoritative source.
- Return values must be orthogonal. If a caller can derive one result from another without losing information, return only the authoritative result. Add another result only for independent state or ambiguity the authoritative result cannot encode.
- Make illegal states unrepresentable.

### Organization and names

- Keep a semantic group's members together: a domain type, its constructors, methods, tightly coupled helpers, and types sharing those helpers. Split by sub-concern when each resulting file carries a complete concept; use cohesion, not line count.
- Never use abstract file, directory, or function names such as `helper`, `utils`, `misc`, `wrapper`, or `adapter`. Each name must specifically and unambiguously explain its content. Every word has a cost.
- Keep functions small while keeping semantically self-sufficient thoughts whole.
- Remove logic, not lines. Keep code readable.
- Keep code flat. Use early returns, `continue`, and guard clauses. Refactor blocks nested three or more levels deep.
- Use multi-way branching when the domain semantically admits more than two alternatives, even if only one or two are currently implemented. Follow the language-specific branching policy.

### Errors and concurrency

- Never blanket-ignore errors. Suppress only failures explicitly matched by type or value. Propagate unknown failures through the available error channel; never swallow them.
- Fallbacks belong at external boundaries. Fix failures in code we own or return them. Add fallbacks only for explicit failure modes of external systems, dependencies, hardware, or environments.
- Validate inputs with strong expectations. When there is no error channel, use an assertion or invariant.
- Handle exactly the cases you expect and return errors for everything else. Broad comparisons must not silently accept unconsidered cases.
- Narrow what a mutex guards to the fields one invariant requires. Split long-lived state from mutating code so retries need not hold a global lock. This narrows runtime lock scope; the audience rule below narrows data ownership.
- Subscribe to observable events instead of sleeping or polling to wait for them. Reserve timeouts for genuinely opaque waits, such as network round trips, hardware responses, or uninstrumentable third-party processes, and use them as an upper bound on an event wait, not its replacement.
- Values crossing RPC, configuration, snapshot, replay, or audit boundaries must be serializable data, not closures.

### Documentation and maintenance

- Code must explain itself through names, types, control flow, or comments. Make the rationale for approaches, protocols, components, workarounds, and dependencies discoverable.
- Where the language or project requires declaration documentation, explain semantics concisely and give the most salient common use, or the primary intended use if no common use exists. Follow the language-specific documentation coverage, placement, and format.
- Comments explain rationale, mechanics, or next steps for future readers. Never include session task numbers, agent IDs, root-cause prefixes, review-process notes, or AI attribution. Reference real external tracker URLs or IDs when relevant.
- When replacing an approach, add a comment explaining why the replacement was chosen.
- Treat ugly workarounds as a design smell and find the elegant approach.
- Fix generators rather than editing generated files.
- Use canonical sources for licenses, ignore templates, and configuration schemas instead of generating them from memory.
- Satisfy all applicable linters.

### Design complexity

- Whenever you design or modify code, you MUST eliminate compensating complexity and patchwork by correcting the underlying design. First establish the domain model, invariants, and responsibilities from domain semantics and contracts, without assuming the existing structure is correct. Use that model to derive or validate a coherent design that solves the problem directly, then implement it and remove the code and mechanisms it makes unnecessary. A sounder design may be more general or flexible.
- Justify every increase. A reusable, implementation-agnostic domain boundary is a concrete reason for an abstraction. Other increases need a concrete reason a simpler design cannot satisfy.
- Decrease moving parts, state, and call depth when touching code.
- Simplify through understanding while keeping required behavior, contracts, and handled cases. Never amputate semantics to look simpler.
- When redesigning, choose the better behavior over existing behavior. Keep existing behavior only for a stated reason, such as a requirement, contract, or consumer need; that it exists is not a reason.

### Computational complexity

- Choose algorithms and data structures with optimal time and memory complexity for the use, given its input sizes, access patterns, and constraints. For example, replace repeated linear scans with an index or set lookup.

### Semantic integrity

A name is a contract. Read the name, predict the body, then read the body; any surprise is a violation.

- **Does only what it says.** A table resolver resolves a table; decisions about whether to resolve, retries, and analytics belong in the caller or name.
- **Does everything it says.** `ValidateAndSave` must validate and save. Split or rename if either can occur independently.
- **Interface implementation is a contract.** Always-erroring or no-op primary methods violate the claim to implement an interface. Implement the behavior or do not create the type yet.
- **No opposite behavior.** Disable must not return enabled; remove must not archive.
- **Return type matches name.** Get-user returns a user; is-valid returns a boolean; list-items returns a collection.
- **No smuggled decisions.** A do-X operation assumes X should happen; the caller decides whether it is needed.
- **No smuggled side effects.** Getters, properties, and predicates do not mutate state. Reveal necessary mutation in the name.
- **One concern per contract.** Keep policy, orchestration, I/O, persistence, observability, and domain logic behind their owning function, type, or package contract. Split mixed work unless one precise domain concept owns the combination.
- **One audience per data type.** An independently existing entity's descriptive metadata, shared specification, or configuration is separate from its owner's private runtime state, such as locks, channels, and handles. Compose by reference or embedding. A public lock still belongs with the state it guards. Combine audiences only when splitting would lose an invariant the combination maintains. For example, a job's ID, description, and schedule exist without a runner; the runner's lock and cancellation channel exist only while it runs.
- **Package scope.** Verify code belongs in the current package or binary. A standalone CLI package must not contain code requiring its daemon to be running.

### Semantic consistency

Same concept means the same name everywhere. The same name means the same thing everywhere. Related concepts use parallel structure.

- Use one name per concept: do not alternate stream, channel, feed, and pipe for the same thing.
- Use one concept per name: handle must not mean both processing a request and a resource reference.
- Use parallel verb pairs, such as start/stop capture instead of begin encoding/end encoding.
- Propagate renames through types, functions, variables, logs, errors, and comments.
- Keep sibling operations at a consistent abstraction level.
- Name domain concepts rather than implementations. A name should survive an implementation change.

### Locality, lifetime, and consistency

- Use the innermost scope that needs a variable. Minimize the distance between declaration and last use; move the declaration or restructure intervening code.
- Release resources as soon as they are no longer needed, rather than holding them for an entire function unnecessarily.
- Use package-level mutable state only when multiple functions genuinely share it.
- Do not retain references past the data's logical lifetime, such as closed connections, expired cache entries, or finished request contexts.
- Scan neighboring files and follow their patterns. Raise suboptimal patterns that need repair to the user after finishing the task.
- Fix obvious surrounding violations encountered while reading or modifying code, within the authorized task scope.

## Inspiration ideas

Evaluate designs against a 10–100× increase in the project's capabilities, features, or needs. Choose the best design for that growth up front. Consider these techniques when applicable:

### Design and APIs

- Separate data holders from data handlers.
- Use a state machine.

### Concurrency

- For shared state, consider optimistic transactions over immutable snapshots: read one version, prepare changes separately, and atomically publish the replacement only if the version is still current; retry safely or report conflicts.
