# Stop Recovery

Treat Stop as post-response continuation, not output control. A Stop result cannot itself guarantee that already-rendered output is hidden.

## Concrete Recovery Boundary

Stop does not deny completion based on ECI lifecycle state or ECI marker presence. Native goals provide continuation; the Supervisor owns ordinary teardown and goal synchronization.

Only a resolved direct scope mistake, cross-session target, or destructive/broad target warrants recovery before ordinary work can proceed. State the exact target, repair it or choose the narrow safe route, then continue.

## Advisory State

ATE phase, agent role, callback/transcript metadata, peer or sibling markers, historical proof, ledgers, reports, hashes, and file grammar are advisory. They do not select the callback owner and do not require a checklist, wait report, receipt, or workflow ceremony before ordinary completion.

An unchanged Stop callback is control metadata, not a new user request. Do not turn it into retries, polling, a final/status reply, or a blocker-resolution loop. A worker-owned dirty path is a normal coordinator handoff and returns normally.

## Actual External Blockers

If work is independently blocked by a genuinely unavailable user-owned input, resource, or decision, continue feasible internal work first and communicate the concrete need through the normal workflow. Any workflow-specific state file is optional coordination context; Stop neither requires it nor treats its exact shape or hash as admission evidence.
