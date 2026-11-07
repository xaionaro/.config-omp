---
name: incident-investigation
description: Use when investigating a software incident, writing a postmortem, extracting incident lessons, or coordinating incident followups; ordinary isolated debugging alone does not require this skill.
---

# Incident investigation

Reconstruct the incident, challenge the explanation independently, and carry every material finding to an explicit disposition with evidence. Seek bounded completeness within declared scope and available evidence; never promise every possible learning.

## Ownership and records

Use the existing incident/work record. Store incident-specific findings there; link existing action ownership, state and proof rather than copying mutable fields. Use [context-ledger](../context-ledger/SKILL.md) when maintaining a ledger.

The coordinator owns incident scope, reconciliation, convergence and reporting. Investigators supply evidence; scoped implementers own changes; independent reviewers assess claims and proof; authorized service/risk owners accept residual exposure. Under an active ECI/ATE root, reuse its agents, records and acceptance authority. This skill adds incident responsibilities, not another lifecycle. Outside such a root, the coordinator owns the incident procedure and final reporting, subject to independent challenge.

Operate autonomously within authority supplied by the request, standing project rules and existing ownership. Dispatch capable agents, make routine implementation decisions, execute scoped repairs and validate results without repeated human permission. Retain responsibility for unresolved findings; continue ready authorized work until the requested outcome is verified or only concrete dependencies remain after feasible internal recovery. This default expands no external or destructive authority.

Keep the existing incident record current after material evidence, interventions and decisions; retain changed hypotheses as history where relevant. Use [report-writing](references/report-writing.md) to produce a readable report scaled to the incident. Report polish or signoff must not delay authorized containment or repair; sending/publishing uses existing authority.

## Procedure

1. **Bound and stabilize.** Record user impact, incident boundary, recovery state, evidence availability, requested completion and mutation authority. Preserve transient evidence alongside authorized containment. Necessary restoration need not wait for exhaustive diagnosis. Record interventions so their effects remain distinguishable.
2. **Dispatch actual independent work.** With authorized agent tools available, launch evidence/timeline and causal/response investigation concurrently where independent. Combine those investigation responsibilities for small incidents, but obtain a separate independent challenge before accepting the investigation. Reuse qualified active agents/reviewers only when their actual assignment covers incident evidence, findings and closure claims. A roster, simulated critic or self-review is insufficient. Missing runtime does not prevent read-only investigation. If agents are unavailable, finish safe evidence work and leave independent review pending; do not self-accept.
3. **Give a common source packet.** Include scope, raw evidence links, time/collection limits, changes and interventions, permitted actions, a bounded question and expected evidence. Ask about trigger/mechanism, contributing conditions, successful and failed defenses, detection, response, recovery, communication and prior incidents/followups. These lenses are questions, not instructions to invent findings. Keep investigators' independent conclusions before reconciliation.
4. **Reconstruct and test explanations.** Use [debugging-discipline](../debugging-discipline/SKILL.md) for uncertain causal claims and scoped defect repairs. Distinguish observations, recollections, hypotheses and missing evidence. Preserve raw timestamps alongside normalized time and uncertainty. Retain dissent and competing or interacting causes. Ask why an action made sense with information then available; “human error” is not a mechanism. A rollback followed by recovery does not isolate simultaneous mitigations.
5. **Reconcile every material finding.** Include agent/critic discoveries, evidence gaps and failed previous followups. Link related findings to shared actions where appropriate. Use the register below, adapting columns to the existing record. A finding must have an action or explicit disposition; a lost question is not a disposition.
6. **Act within authority.** Execute authorized work through its existing engineering workflow. Prefer eliminating the failure, making correct actions easy, detecting early, then documentation/training when stronger controls do not fit. Record concrete requests beyond authority with needed acceptance and next trigger. Keep coordinator responsibility until an implementer acknowledges ownership. Invoking this skill grants no external messaging, ticket creation, scheduling or production-change authority. Preserve required controls; unrelated security findings remain visible followups and do not block authorized reliability improvement solely because of severity.

   Advance every ready authorized action; an unavailable resource blocks only dependent work. Use a faithful available environment or repair/route the dependency through its existing owner while independent work continues. Before a consequential intervention, identify its narrow target, measurable expected effect, regression/abort signal and feasible recovery route. Prefer a bounded reversible step; if reversal is unsafe or unavailable, use the action's already-authorized established recovery route. On adverse signals, stop expansion, restore only the owned intervention through the authorized route, verify restoration, preserve failed-attempt evidence, revise and continue. Protect unrelated work/state.
7. **Challenge and converge.** Actually assign an independent critic to raw evidence, strongest alternatives, missing lenses, prior followup failures, register coverage and proposed proof. Builders cannot accept their own changes. Reconcile new material findings into the same register; revise affected claims/actions and obtain review of those revisions. Stop investigation iterations when no new material finding appears within the declared scope/evidence boundary and remaining questions have justified dispositions. State unresolved dissent and review limits. Missing evidence gets a discriminating question and resume trigger, not an endless audit or invented certainty. Use the active workflow's blocker route; [blocker-resolution-protocol](../blocker-resolution-protocol/SKILL.md) applies when its actual trigger is reached.

   Have the critic derive expected behavior from the maintained requirement/consumer contract, known-good behavior or independent measurement before relying on the producer's expectations. Independent agents agreeing with a shared mistaken model are not an independent oracle.
8. **Prove effects.** Apply [verification-before-completion](../verification-before-completion/SKILL.md) to claimed changes. Test the failing path and promised outcome, including recovery/detection/operator behavior when claimed. Run the cheapest faithful replay or drill now when authorized and capable of answering the acceptance question. Production failure injection requires explicit applicable authority and stop/abort conditions. Tickets, merges and unit tests alone do not prove broader effectiveness. Use an observation period only for a question requiring exposure/time; specify owner, required evidence and resume/decision condition. Keep unavailable effectiveness evidence pending; quiet time alone proves no fix.

   Establish the acceptance oracle before repair. Check the failing path and relevant healthy/transient/alternative controls; ensure checks reject plausible wrong fixes. Do not weaken the promised outcome to obtain green tests or require a second implementation for an obvious repair. Verify actual effects at the claimed boundary. For needed observation, name adverse signals and the authorized recovery/reinvestigation response; run the observation within this task when feasible and execute that response when a signal occurs. Interacting changes invalidate affected proof. Do not invent resident monitoring or scheduling authority.
9. **Close each state honestly and retain learning.** Report the three states below independently. Promote supported lessons into their maintained code, tests, runbooks or skills; identify the owner and verify the changed behavior. Revisit recurrence and control overhead where relevant. If all actions were requested, continue authorized executable work; an accepted-risk decision or a proposed ticket does not satisfy a promised repair. Disclose actual remaining boundaries and next triggers.

   Report the measured outcome, independent acceptance and exact remaining limits; reopen work when contrary evidence appears. Autonomous execution does not imply universal reliability.

## Finding register and closure

For each material finding record: **ID; evidence/confidence and consequence; action or disposition; priority/dependencies; accountable coordinator; accepted implementer or acceptance pending; acceptance/effectiveness proof; state and next trigger.** Link action fields already maintained elsewhere. Preserve rejected explanations and dissent when they affect decisions.

| State | Sufficient claim boundary |
| --- | --- |
| Operational recovery | Impact ended or is contained, with supporting observation and remaining exposure. This says nothing about followups. |
| Investigation disposition | Independent challenge completed; scoped evidence and alternatives reconciled; uncertainties and continuation triggers explicit. Otherwise review/evidence remains pending. |
| Followup completion | Each promised action has accepted ownership, execution and effect evidence, or an explicit authorized alternative disposition. Show verified fixes, accepted risk, declined work, deferred validation and pending ownership distinctly; never call them all fixed. |

## Shortcuts to reject

| Shortcut / red flag | Required correction |
| --- | --- |
| “No runtime, so no independent investigation is useful.” | Dispatch read-only evidence/challenge work; separate unavailable execution. |
| “The report is thorough; I can accept it.” | Obtain actual independent challenge or mark review pending. |
| “Done tickets / quiet week prove learning.” | Verify each promised effect; use time only for a stated exposure question. |
| “Another team owns it, so it is addressed.” | Record pending acceptance and retain coordinator responsibility. |
| “Deadline requires one root cause.” | Deliver the strongest supported assessment with uncertainty and continuation. |

Read [references/example-and-origins.md](references/example-and-origins.md) for a worked incident and the source adaptations. Use [references/behavioral-checks.md](references/behavioral-checks.md) when evaluating changes to this skill.
