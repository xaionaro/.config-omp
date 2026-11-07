#!/usr/bin/env bash
# PreToolUse hook: coordinator role metadata is non-blocking for edits.

set -euo pipefail

# A role alone does not identify a wrong edit target.  This hook also cannot
# spawn, bind, or verify an implementer task, so it must not claim to dispatch
# one.  The edit validators and active ECI gate own concrete target, marker,
# control-state, and ledger protections.
exit 0
