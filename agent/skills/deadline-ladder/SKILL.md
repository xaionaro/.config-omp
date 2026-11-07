---
name: deadline-ladder
description: Use when deadlines, demos, incidents, launches, or expiring opportunities make the durable solution risky to finish in time
---

# Deadline Ladder

Run two tracks: the right fix and the quickest-to-build reliable fallback.

## Rule

Never bet the deadline on the clean solution alone.

Quality can wait. Reliability cannot.

Work wide before deep.

## Workflow

1. Define the deadline in concrete terms: time, required behavior, and failure cost.
2. Map independent workstreams and evidence to gather.
3. Start the durable solution. Keep it aligned with the real architecture.
4. In parallel, build the quickest-to-build reliable fallback.
5. When the fallback works, verify it. This becomes the floor.
6. Keep improving from that floor. Each step must still work.
7. Stop only when time runs out or the fallback converges with the durable solution.

## Parallelization Rule

If several components, hypotheses, tests, logs, or commands could matter, check them together. Design one run that returns one evidence bundle. Go sequential only when the next check depends on the previous result.

## Tracks

| Track | Job | Constraint |
| --- | --- | --- |
| Durable | Correct long-term shape | No corner-cutting hidden as design |
| Fallback | Working answer before the deadline | Implementation speed first; reliability non-negotiable |

## Floor Rule

Once the fallback passes verification, do not drop below it.

Every cleanup, replacement, or merge must preserve a working state. If an improvement breaks the floor, revert or repair before continuing.

## Fallback Rules

- Make the quickest-to-implement thing that satisfies the deadline and can be trusted.
- Cut scope, polish, architecture quality, and maintainability before reliability.
- Isolate hacks behind narrow interfaces, flags, scripts, or adapters.
- Name debt plainly in code, tickets, or handoff notes.
- Prefer reversible shortcuts.
- Do not disguise fallback code as durable architecture.

## Improvement Loop

```text
working fallback -> verify -> improve one slice -> verify -> repeat
```

The fallback is not a parking lot. It is the first rung of the durable solution.

## Red Flags

| Thought | Correction |
| --- | --- |
| "The hack works, so stop." | Improve until the deadline or convergence. |
| "We can clean it later." | Clean the next safe slice now. |
| "This shortcut is basically the architecture." | Label it fallback code. Do not launder debt. |

## Done

- The deadline behavior works and is verified.
- Remaining shortcuts are explicit.
- The next improvement is obvious.
- The current state is shippable.
