# Capture and prepare a changed assignment

Applicability follows [Update Points](SKILL.md#update-points). Run `"$HOME/.omp/agent/eci/bin/eci-active" requirement-assignment --request <JSON-file>` from the intended working directory with explicit `ECI_SESSION_ID` (or `ECI_THREAD_ID` when unset). It uses that session’s existing `project-understanding.yaml`; it does not send messages or update the status report.

Classify the input under [Project requirements](../../eci-binding-contract.md#project-requirements); reuse saved requirements when only corrective or operational work changes.

1. Apply the existing [affected-record and native-edit rules](SKILL.md#update-points). Represent the resulting changes as exact native text replacements; `edits: []` selects no-write reuse.
2. Put the request in a JSON file. Each `old` must occur exactly once when its edit is applied, in listed order. Include adequate surrounding text to distinguish the intended record. There is no separately authored requirement-message field.

```json
{
  "edits": [{"old": "exact current native YAML text", "new": "replacement native YAML text"}],
  "requirements": ["requirement:job:source:facet"],
  "outcome": "outcome:job:result",
  "target": "/root/worker",
  "instructions": "Job, parent chain, role, scope, expected work, checks and reporting route. Keep operational directions separate from canonical requirements.",
  "actor": {"id": "/root", "role": "Supervisor"},
  "reason": "Material user correction changes this assignment"
}
```

3. Invoke the installed command and capture its stdout only on exit zero:

```sh
"$HOME/.omp/agent/eci/bin/eci-active" requirement-assignment --request /absolute/path/request.json
```

4. Forward the exact returned `{target,message}` through native `write agent://<id>`. The prepared message contains the authoritative saved requirements followed by visibly separate operational instructions. Do not rewrite its requirement portion. Maintain the current status report in the same turn under [Update Points](SKILL.md#update-points); record actual delivery separately when material. Preparation is not delivery.

The operation applies exact native edits to a same-directory temporary file under the existing mutation lock, validates the affected IDs against the assigned outcome, then publishes and reads back the canonical snapshot. It projects every requirement and scope linked to that outcome, including unchanged source, acceptance, evidence and unknown domain fields. Other named record links are followed too; missing or unsupported chain content fails explicitly instead of disappearing. The ledger’s unrelated bytes, comments, order, multiline text and mode remain intact. Faithful reuse does not rewrite the snapshot.

The prepared payload remains internal until the existing canonical `ledger-append` succeeds. The mutation lock is released before that append takes its own lock. Successful stdout contains only `{target,message}`. Errors go to stderr with no assignment payload. Before publication, failure leaves the snapshot unchanged. A failure after publication can leave the snapshot saved without its paired log entry: inspect current records, reconcile the log, and retry with empty edits when faithful. A stale edit is not automatically replayed, and a failed delivery does not roll back capture.

This operation couples capture and assignment preparation on this adopted path. It does not inspect arbitrary direct native messages or discover omitted user meaning. The direct paths for urgent controls and independent progress remain governed by [Update Points](SKILL.md#update-points).
