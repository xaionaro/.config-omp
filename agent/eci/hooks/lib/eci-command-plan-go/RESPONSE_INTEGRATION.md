# Planner response integration seam

`NormalizePlannerResponse` is the first bounded Go-owned migration seam for
the PreToolUse adapter. The shell callback can pass the planner's exit status
and captured stdout to it before dispatching the result.

Only a process-status/JSON pair that agrees is authoritative:

- status 0 with `allow` and no diagnostic;
- status 2 with `deny` and a concrete diagnostic;
- status 3 with `defer` and no diagnostic.

Empty, malformed, unexpected, or mismatched output is a transparent fallback.
The caller continues through its existing effect-aware routes; planner health
does not become a command permission boundary.

This slice intentionally does not replace the shell callback. Integration
should be added after the existing adapter has a parity test that compares
this normalizer with its current response handling.
