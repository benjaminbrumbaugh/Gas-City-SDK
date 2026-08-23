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
