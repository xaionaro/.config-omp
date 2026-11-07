This opt-in test starts the installed ECI app-server and feeds it deterministic Responses SSE from a local HTTP server. No paid model endpoint or auth file is used. It copies the authoritative hooks, launchers, and registration into a disposable isolated HOME, retaining the existing trust entries for those identical commands. The normal launcher/publisher builds fixture native artifacts from the copied sources.

The model requests an ordinary shell command and a protected ECI control mutation. Assertions require the actual registered Bash PreToolUse hook to complete, the ordinary command to produce its expected output, and the control mutation to be blocked with ECI_CONTROL_OWNER_REQUIRED. Failed hook events fail the test.

Run from the source checkout, using owned-command to retire the whole process family:

```sh
ECI_E2E=1 ECI_E2E_EVIDENCE="$HOME/tmp/eci-hook-e2e-$(date -u +%Y%m%dT%H%M%SZ)" \
  "$HOME/.omp/agent/eci/bin/owned-command" -deadline 90s \
  -receipt "$HOME/tmp/eci-hook-e2e-receipt-$(date -u +%Y%m%dT%H%M%SZ).json" -- \
  go -C hooks/tests/eci-hook-e2e-go test -v -count=1
```

`ECI_E2E_BINARY` selects an installed ECI runtime (default `/usr/bin/eci`). Use a fresh evidence directory for each run. Evidence contains event JSONL, stderr, and timing/environment/revision metadata. The fixture directory can be removed after evidence review. The route hash covers the registration and launcher/publisher files; the Git revision identifies the checkout baseline and does not claim the tree was clean.

To falsify the assertions, add `ECI_E2E_MUTATE_HOOK_FAILURE=1` to the same command with a fresh evidence directory. This changes only the disposable fixture Bash hook to exit 1; the run must fail with a real hook failure event. Ordinary `go test` skips the runtime test unless explicitly opted in.
