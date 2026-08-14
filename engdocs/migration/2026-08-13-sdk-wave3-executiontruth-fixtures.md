---
title: Execution-truth shadow-replay fixtures
description: Deterministic redacted evidence corpus for SDK shadow replay tests.
---

## Scope

`internal/testfixtures/executiontruth` contains a small, embedded corpus for
shadow-replay tests. It is an evidence artifact at the fixture/serialization
boundary. It is not a provider capture, a live event source, a lifecycle
projector, a receiver, a router, or a source of mutation authority.

The corpus uses the existing `pkg/eventexport.Envelope` type for every event.
Each scenario is validated with `eventexport.ValidateBatch`, so the existing
schema version, opaque-reference checks, execution-fact shape, and topology
rules remain the wire authority. The fixture adds only local metadata:

- `provenance`: `authoritative`, `observed`, `derived`, or `unknown`;
- `freshness`: `fresh`, `stale`, or `unknown`; and
- `observed_at`, which is omitted for unknown freshness and fixed for the
  deterministic scenarios.

Provenance and freshness are intentionally separate. A derived fact can be
fresh, and an authoritative fact can be stale in a replay window. Neither
metadata field is emitted on the event-export wire.

## Cases and invariants

The corpus includes:

- a fresh authoritative graph with work association, root topology, dependency
  topology, and started/completed lifecycle facts;
- an authoritative step with unknown native topology (`nil` dependency
  pointer);
- a stale derived completion with known dependency topology; and
- an unknown-provenance, unknown-freshness step.

The JSON representation preserves the native topology tri-state:

- omitted `depends_on_step_ids` means unknown;
- `"depends_on_step_ids": []` means a known root; and
- a non-empty sorted array means known dependencies.

`Load` decodes the embedded file on every call and validates it, so replay
consumers receive independent values. Fixed timestamps, opaque references, and
strict sequence ordering make repeated loads and JSON round trips deterministic.
The fixture rejects free-form title/formula content and does not contain event
payloads, messages, transcript paths, provider session IDs, raw records,
credentials, or provider transcript text.

## Evidence limits

Passing the fixture tests proves only that a safe, typed scenario can cross the
fixture serialization boundary without losing omission, topology,
provenance, or freshness distinctions. It observes neither the graph/work
store nor a provider runtime. It does not prove event delivery, screen state,
transcript correctness, a downstream shadow comparator, or any routing or
admission decision. Consumers must retain unknown and stale as non-negative
evidence states; a missing field is not proof that execution did not happen.
