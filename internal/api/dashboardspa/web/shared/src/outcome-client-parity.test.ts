import assert from 'node:assert/strict';
import test from 'node:test';

import type { OutcomeRecord } from './generated/gc-supervisor-client/types.gen.js';
import { zOutcomeRecord } from './generated/gc-supervisor-client/zod.gen.js';

const exactWorkOutcome: OutcomeRecord = {
  actual_config_digest: `sha256:${'a'.repeat(64)}`,
  actual_target_id: 'target-a',
  admission_receipt_id: 'controller-admit:decision-a:2',
  correlation_id: 'work-a',
  coverage: 'available',
  disposition: 'shipped',
  execution_id: 'execution-a',
  failure_class: 'none',
  observed_at_unix: 2_000,
  outcome_id: `outcome_${'d'.repeat(64)}`,
  provenance: 'authoritative_routing_decision_exact_work',
  recommendation_id: `routing/v2:${'c'.repeat(64)}`,
  requested_config_digest: `sha256:${'a'.repeat(64)}`,
  requested_target_id: 'target-a',
  routing_decision_id: 'decision-a',
  schema_version: 'routing/outcome/v2',
  session_id: 'session-a',
  status: 'succeeded',
  work_id: 'work-a',
};

test('generated TypeScript and Zod clients share canonical exact-work provenance', () => {
  assert.equal(zOutcomeRecord.parse(exactWorkOutcome).provenance, exactWorkOutcome.provenance);
  assert.equal(
    zOutcomeRecord.safeParse({
      ...exactWorkOutcome,
      provenance: 'authoritative_routing_decision+exact_work_bead_metadata',
    }).success,
    false,
  );
});

test('generated Zod outcome rejects unsafe URL identifiers', () => {
  for (const field of [
    'correlation_id',
    'routing_decision_id',
    'work_id',
    'admission_receipt_id',
    'session_id',
    'execution_id',
    'requested_target_id',
    'actual_target_id',
  ] satisfies Array<keyof OutcomeRecord>) {
    assert.equal(
      zOutcomeRecord.safeParse({
        ...exactWorkOutcome,
        [field]: 'https://example.invalid/secret',
      }).success,
      false,
      `expected ${field} to reject an unsafe URL`,
    );
  }
});

test('generated Zod outcome enforces succeeded evidence IDs', () => {
  for (const field of [
    'admission_receipt_id',
    'session_id',
    'execution_id',
  ] satisfies Array<keyof OutcomeRecord>) {
    assert.equal(
      zOutcomeRecord.safeParse({ ...exactWorkOutcome, [field]: null }).success,
      false,
      `expected succeeded outcome to require ${field}`,
    );
  }
});

test('generated Zod outcome enforces null actuals when not admitted', () => {
  const notAdmittedOutcome: OutcomeRecord = {
    ...exactWorkOutcome,
    actual_config_digest: null,
    actual_target_id: null,
    admission_receipt_id: null,
    disposition: 'not_admitted',
    execution_id: null,
    failure_class: 'unknown',
    session_id: null,
    status: 'failed',
  };
  assert.equal(zOutcomeRecord.safeParse(notAdmittedOutcome).success, true);
  for (const field of ['actual_target_id', 'actual_config_digest'] satisfies Array<
    keyof OutcomeRecord
  >) {
    assert.equal(
      zOutcomeRecord.safeParse({ ...notAdmittedOutcome, [field]: exactWorkOutcome[field] }).success,
      false,
      `expected not-admitted outcome to require null ${field}`,
    );
  }
});
