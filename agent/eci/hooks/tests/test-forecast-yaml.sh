#!/usr/bin/env bash
set -Eeuo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
node - "$root" <<'NODE'
const fs = require('fs');
const YAML = require('yaml');
const path = require('path');
const assert = require('assert');
const examples = path.join(process.argv[2], 'skills/context-ledger/examples');
const parse = file => YAML.parse(fs.readFileSync(path.join(examples, file), 'utf8'), {uniqueKeys:true, stringKeys:true, maxAliasCount:0});
const ledger = parse('project-understanding.yaml');
const forecasts = parse('forecasts.yaml');
assert.strictEqual(forecasts.schema, 'forecasts/v1');
const ref = ledger.records['work:receiver:clock'].data.forecast_ref;
const match = /^forecasts\.yaml#\/records\/([a-z0-9_:-]+)$/.exec(ref);
assert(match, 'forecast reference must be a stable record pointer');
const record = forecasts.records[match[1]];
assert(record, 'forecast reference must resolve');
assert.strictEqual(record.kind, 'lane');
assert.strictEqual(record.status, 'active');
assert.strictEqual(record.assessment, 'provisional');
for (const key of ['outcome', 'target', 'target_basis', 'original_baseline', 'baseline_evidence']) assert(record[key], key);
for (const key of ['prior', 'current', 'reason', 'evidence']) assert(record.recalibration[key], key);
assert.strictEqual(record.recalibration.current, record.target);
assert(record.base_case.endpoint && record.base_case.packages[0].basis);
const scenario = record.scenarios[0];
for (const key of ['name', 'trigger', 'affected_work', 'evidence', 'assumptions', 'added_duration', 'event_time', 'endpoint', 'basis', 'evidence_gap', 'next_action']) assert(scenario[key], key);
console.log('forecast YAML fixture and locator: PASS');
NODE
node - "$root" <<'NODE'
const fs = require('fs');
const assert = require('assert');
const text = fs.readFileSync(`${process.argv[2]}/skills/context-ledger/SKILL.md`, 'utf8');
assert(!text.includes('Initial forecast.Original baseline'), 'baseline must use its YAML key');
assert(!text.includes('Target basis: conditional'), 'conditional basis must use its YAML key');
assert(text.includes('forecast record’s `kind`'), 'forecast edits must check kind rather than ledger type');
assert(text.includes('`active` or `inactive`'), 'forecast status meanings must remain');
assert(text.includes('`complete` or `provisional`'), 'assessment meanings must remain');
console.log('forecast documentation contract: PASS');
NODE
# Exercise native selective edits on a disposable copy, including closure.
node - "$root" <<'NODE'
const fs = require('fs');
const path = require('path');
const os = require('os');
const assert = require('assert');
const YAML = require('yaml');
const fixture = fs.readFileSync(path.join(process.argv[2], 'skills/context-ledger/examples/forecasts.yaml'), 'utf8');
const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'forecast-lifecycle-'));
const file = path.join(dir, 'forecasts.yaml');
const parse = () => YAML.parse(fs.readFileSync(file, 'utf8'), {uniqueKeys:true, stringKeys:true, maxAliasCount:0});
const edit = (oldText, newText) => {
  const text = fs.readFileSync(file, 'utf8');
  assert.strictEqual(text.split(oldText).length, 2, 'edit must select exactly one subtree');
  fs.writeFileSync(file, text.replace(oldText, newText));
};
try {
  fs.writeFileSync(file, fixture);
  const locator = 'forecasts.yaml#/records/receiver-root';
  const key = locator.split('/').pop();
  const initial = parse().records[key];
  assert.strictEqual(initial.kind, 'root');
  assert.strictEqual(initial.recalibration.status, 'unchanged');
  const original = initial.original_baseline;
  const originalEvidence = initial.baseline_evidence;
  edit('    outcome: "Receiver evidence is recorded and its limits are visible."', '    outcome: "Receiver evidence and its limits are recorded for review."');
  edit('    target: "2000-01-01T02:00:00Z"', '    target: "2000-01-01T02:30:00Z"');
  edit('      status: "unchanged"\n      evidence: "Synthetic example: initial review estimate"', '      status: "changed"\n      prior: "2000-01-01T02:00:00Z"\n      current: "2000-01-01T02:30:00Z"\n      reason: "Additional review"\n      evidence: "Synthetic example: updated review estimate"');
  const changed = parse().records[key];
  assert.strictEqual(changed.original_baseline, original);
  assert.strictEqual(changed.baseline_evidence, originalEvidence);
  assert.notStrictEqual(changed.outcome, initial.outcome);
  assert.strictEqual(changed.recalibration.prior, original);
  assert.strictEqual(changed.recalibration.current, changed.target);
  assert.strictEqual(locator, 'forecasts.yaml#/records/' + key);
  edit('    status: "active"\n    assessment: "complete"\n    target: "2000-01-01T02:30:00Z"', '    status: "inactive"\n    assessment: "complete"');
  const closed = parse().records[key];
  assert.strictEqual(closed.status, 'inactive');
  assert(!Object.hasOwn(closed, 'target'));
  assert.strictEqual(closed.original_baseline, original);
  assert.deepStrictEqual(parse().records['receiver-clock'], YAML.parse(fixture).records['receiver-clock']);
  // Model the documented report projection and closure records as native YAML.
  const lane = parse().records['receiver-clock'];
  const projected = YAML.parse(YAML.stringify({outcome: lane.outcome, assessment: lane.assessment,
    recalibration: lane.recalibration, base_case: lane.base_case, scenarios: lane.scenarios}));
  assert.strictEqual(projected.assessment, 'provisional');
  assert.deepStrictEqual(projected.recalibration, lane.recalibration);
  assert.deepStrictEqual(projected.scenarios, lane.scenarios);
  assert.deepStrictEqual(projected.base_case, lane.base_case);
  const ledgerWork = YAML.parse('state: "completed"\ncompleted_at: "2000-01-01T02:15:00Z"\nforecast_ref: "' + locator + '"\n');
  const report = YAML.parse('state: "completed"\ncompleted_at: "2000-01-01T02:15:00Z"\nactive_forecast_deadline: null\n');
  assert.strictEqual(ledgerWork.forecast_ref, locator);
  assert.strictEqual(ledgerWork.completed_at, report.completed_at);
  assert.strictEqual(report.active_forecast_deadline, null);
  assert.strictEqual(parse().records[key].status, 'inactive');
  console.log('forecast root lifecycle and stable locator: PASS');
} finally { fs.rmSync(dir, {recursive:true, force:true}); }
NODE
