#!/usr/bin/env bash

set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
POLICY="$ROOT/../skills/references/workflow-runtime/review-policy.md"

grep -Fq '## Design-versus-implementation boundary' "$POLICY"
grep -Fq 'blast radius and repair nature' "$POLICY"
grep -Fq 'substantial scale-up would amplify' "$POLICY"
grep -Fq 'semantic/model/contract errors' "$POLICY"
grep -Fq 'contained blast radius' "$POLICY"
grep -Fq 'may label it REJECT when its evidence supports that label' "$POLICY"
grep -Fq 'a fresh Step 2 critic decides ECI finding treatment' "$POLICY"
grep -Fq 'Within ECI, a fresh Step 2 critic alone decides final impact' "$POLICY"
grep -Fq 'never a design REJECT after that decision' "$POLICY"
grep -Fq 'resolves or records every such finding' "$POLICY"
grep -Fq 'data/side-effect boundary is design-level' "$POLICY"
grep -Fq 'mechanical call-site correction' "$POLICY"

printf '%s\n' 'design-boundary policy assertions: PASS'
