# ECI command-gate mode Go port

This module is the production implementation of `bin/eci-command-gate-mode`.
The former Python file `bin/eci-command-gate-mode.py` was removed once its
differential-test oracle role ended; hooks must execute the compiled binary.

| Python contract | Go implementation | Compatibility proof |
| --- | --- | --- |
| `read_mode()` | `ConfigStore.ReadMode` | `config_test.go`, `main_test.go` get/malformed cases |
| `set_mode()` | `ConfigStore.SetMode` | `config_test.go`, `main_test.go` exact bytes/metadata |
| `parse_denial()` | `ParseDenial` | `telemetry_test.go` malformed, UTF-8, oversize, fallback cases |
| `_read_permissive_input()` | `readBoundedDenial` | `main_test.go` oversize and stdin-drain case |
| `_stream_enforcing_input()` | `copyEnforcingInput` | `main_test.go` byte-for-byte enforcing forwarding |
| `append_event()` and fixed rotation | `TelemetryStore.AppendEvent` | `telemetry_test.go` descriptor/race/rotation tests |
| `finalize()` | `finalizeDenial` | `main_test.go` permissive/enforcing/fallback cases |
| `main()` | `run` plus compiled `main` | `contract_test.go`, `main_test.go`, 48-case Python regression |

Compatibility invariants:

- `get` prints compact JSON with `mode` then `config_state`, plus one newline.
- Invalid command shapes and enum values print the published usage line and
  exit `2`.
- `set` persists exactly `permissive\n` or `enforcing\n`; failures use the
  published `eci-command-gate-mode: set failed:` prefix and exit `1`.
- `finalize` exits `0` in both modes. Enforcing forwards stdin unchanged;
  permissive stores one `eci-command-gate-event/v2` record (at most
  `MaxEventBytes`, JSON without HTML escaping) that adds `session_id`, `cwd`,
  `command` (from the `ECI_GATE_DENIAL_*` env the planner sets) and the denial
  `reason`. `cwd` is the callback's working directory, not a directory a
  later `cd` in the command would reach; `command` is the whole callback
  command, and the denied segment is identified inside `reason`. Likely
  credentials in `command` and `reason` (credential-named env assignments,
  `Authorization:`/`Bearer` values, credential-named `--flag` values) become
  `***`. Each field is cut to its JSON-encoded byte bound with a
  `…[truncated]` marker; it warns with the fixed
  `eci-command-gate-mode: telemetry unavailable` line when reduction/storage
  cannot be completed.
- The binary has no runtime interpreter dependency and is built for the
  provider architecture with `go build -trimpath -buildvcs=false`.

The omp copy of this module and the production binary are required to
remain hard-linked and are verified by the runtime synchronization receipt.
