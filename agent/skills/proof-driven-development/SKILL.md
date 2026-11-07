---
name: proof-driven-development
description: Use when logic-heavy implementation may benefit from formal specification, proofs, or an executable reference model.
---

# Proof-Driven Development

The full proof-through-Lean workflow is optional assurance work. Run it in parallel with the main effort, never as a prerequisite.

## Track separation

| Track or condition | Rule |
|---|---|
| Main effort | Continue design, implementation, ordinary tests, review, delivery, and completion independently. Never wait for Lean work. |
| Lean track | Use any valuable subset: specification, key proofs, executable model, or differential tests. Schedule it concurrently with the main effort. |
| No parallel capacity | Prioritize the main effort. Defer or skip Lean work instead of serializing it ahead of delivery. |
| Missing, incomplete, or failing Lean artifacts | Keep artifact and tooling status non-blocking. Never fail the main effort solely because a proof, model, or tool is unavailable or failing. Confirmed correctness or security defects still follow ordinary gates. |

Lean's optional status does not relax ordinary testing, review, security, or delivery requirements.

## Optional Lean track

These steps order only work performed inside the Lean track. They do not order or gate the main effort.

1. **Specify** — Write a formal spec in Lean 4: types, function signatures, preconditions, postconditions, and invariants.
2. **Prove** — Prove valuable properties. Use `lake build` to verify completed proofs.
3. **Model** — Build an executable Lean reference when it adds useful comparison coverage.
4. **Differential test** — When both implementations exist, run the same inputs through them and investigate divergence.
5. **Maintain** — Keep retained proofs in `proofs/` and update them when their modeled algorithm changes.

## Optional project structure

When retaining Lean artifacts:

```
project/
├── proofs/               # Lean 4 project
│   ├── lakefile.lean
│   ├── lean-toolchain
│   ├── Spec/             # Formal specifications (types, signatures)
│   ├── Proofs/           # Correctness proofs
│   └── DiffTest/         # Executable spec for differential testing
├── src/                  # Production code (any language)
└── tests/
    └── differential/     # Tests comparing Lean output vs production output
```

## Lean 4 setup

The optional Lean track uses `elan` + `lake`. Verify: `source ~/.elan/env && lean --version`.

Initialize a new proofs directory:
```bash
cd project && lake init proofs && mv proofs/ proofs-tmp/ && mkdir proofs && mv proofs-tmp/* proofs/ && rm -rf proofs-tmp/
```

## What to prove

When using the Lean track, focus on properties that matter:
- **Correctness**: output satisfies the specification for all valid inputs.
- **Invariants**: data structure invariants preserved across operations.
- **Edge cases**: boundary conditions, empty inputs, overflow, off-by-one.
- **Equivalence**: when refactoring, prove new implementation equivalent to old.

## Applying Lean results

When a Lean model exists:
- Map Lean types to target language types preserving semantics (not just syntax).
- Compare algorithm structure and account explicitly for optimizations that change it.
- Generate property-based tests from Lean theorems — each theorem becomes a test case generator.

## Differential testing

When an executable Lean model is available, use it as an additional oracle:
1. Generate test inputs (random, edge cases, from Lean theorem parameters).
2. Run inputs through Lean executable (`lake env lean --run`).
3. Run same inputs through target implementation.
4. Compare outputs. Any difference is a mismatch to investigate.

Do not delay the main effort to create or repair this oracle.

## When to use this skill

Good candidates include algorithms, data structures, protocols, state machines, parsers, serializers, business rules, validation logic, transformations, and high-risk refactors. Use the Lean track when its expected assurance value justifies the effort and parallel capacity exists.

## When to defer, skip, or limit

- Pure configuration files (YAML, JSON, TOML) with no logic.
- Glue code that only wires components together with no decisions.
- Unclear specifications — defer formalization while the main effort proceeds on clear requirements.
- Work where Lean would compete with or delay the main effort.
- Prefer a smaller subset when it provides enough value.

Skipping, deferring, stopping, or partially applying the Lean track never requires an exception.
