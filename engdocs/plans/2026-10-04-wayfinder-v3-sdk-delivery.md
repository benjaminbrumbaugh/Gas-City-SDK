# SDK v3 outcome delivery plan

Work bead: `sdk-bcy.2`
Branch: `polecat/sdk-bcy.2`
Planning counter: 3 (refined)

## Target truth and evidence boundary

Target truth: an SDK-owned immutable delivery item exists for each actual
runtime launch or authoritative non-admission fact, and a City/plugin consumer
can retrieve the same item after restart, retry an ambiguous acknowledgement,
and receive a stable conflict for a different acknowledgement.

Required evidence layer: `internal/routingdecision` bbolt persistence and its
focused tests observe exact bytes, identities, restart replay, and CAS/ack
conflicts. Controller/service tests observe that launch/non-admission facts
become items. Huma/API and CLI tests observe typed projections and existing
local write authorization. Generated schema/spec tests observe wire parity.

Useful but insufficient proxies: a read-time outcome projection, a passing
v2/v3 outcome listing test, a successful HTTP response without reopening the
ledger, or a generated schema that does not exercise the real handler. None of
these proves immutable source bytes or replay after an ambiguous commit.

Tempting false completion: treating `ProducerExecutionOutcome` projection as
the delivery record, synthesizing a timestamp/status during a read, deleting
an item after an unverified ack, or allowing a caller-supplied outcome to
rewrite a stored v2/v3 record. If this plan succeeds while the bug remains,
the likely cause is that source facts are still only read-time projections or
that the ack path is not tested across close/reopen and conflicting payloads.

## Full plan, tasks, and subtasks

1. Confirm current contracts and boundaries.
   - Verify `origin/main`, current routing store buckets/schema, launch receipt
     authority, non-admission transitions, API write-auth helpers, CLI routing
     projection, generated-spec workflow, and legacy v2 behavior.
   - Search history and sibling City/plugin sources for an existing delivery
     contract; port only proven compatible pieces.
2. Freeze the smallest SDK-owned delivery contract.
   - Add a versioned typed delivery item containing stable item identity,
     causal fact identity, schema generation, exact outcome bytes/digest, and
     durable source/evidence time; distinguish pending and acknowledged state.
   - Define bounded pending-page and acknowledgement request/result types.
     Acknowledgement must bind item identity and exact payload digest/bytes;
     replay is idempotent, conflicting identity or bytes is refused.
   - Preserve v2 and v3 schemas; never relabel an existing outcome or invent
     terminal status from admission, closure, or a read clock.
3. Implement durable storage in `internal/routingdecision`.
   - Add a dedicated bucket/index and migration-safe initialization without a
     second database or general event framework.
   - Record immutable items transactionally and idempotently; canonicalize
     bytes once at write time, clone on read, and retain exact bytes/digests
     across close/reopen.
   - Add bounded keyset pending reads and ack CAS/idempotency with explicit
     errors for missing, already-acked, and conflicting acknowledgements.
   - Add unit tests first for launch/non-admission creation, duplicate source
     replay, exact bytes, reopen replay, ambiguous commit/lost ack, conflict,
     bounds, corruption, and v2/v3 separation.
4. Wire controller-owned causal producers.
   - At the existing successful durable launch receipt boundary, emit one
     delivery item from the SDK-owned authority facts.
   - Emit a not-admitted item only from an authoritative refused/expired/revoked
     lifecycle fact; keep unknown outcomes out of terminal delivery.
   - Make emission idempotent for the same source fact and do not use mutable
     work metadata or read-time scans as authority.
5. Add typed API and CLI projections.
   - Extend the routing provider seam with bounded pending and ack operations.
   - Register read and mutation routes through Huma/city helpers; ack uses the
     existing city write grant + CSRF authorization and no loopback-only trust.
   - Add generated schemas/client updates from the normal generator; add CLI
     pending/ack commands that preserve typed bytes and stable replay output.
   - Keep the existing v2 outcomes and additive v3 outcomes unchanged.
6. Publish the contract and verification evidence.
   - Document the source/transport-ack boundary, exact-byte and unknown rules,
     endpoint/CLI contract, and authorization expectations in the appropriate
     SDK routing plan/reference surface.
   - Record test/gate results and remote SHA for the mayor/coordinator.

## Architectural changes

- `internal/routingdecision` remains the sole durable authority for delivery
  records and reuses the existing bbolt ledger.
- The delivery record is a source-layer object, not an event bus message, City
  outbox, provider result, ranking decision, or terminal-status synthesizer.
- Controller/service code calls narrow store methods at existing lifecycle
  boundaries; API and CLI depend on typed service/provider projections only.
- Local acknowledgement is a transport acknowledgement over an SDK-authored
  immutable item. It cannot mutate the causal outcome or grant execution.
- No v4, no ranking logic, no new daemon/database, no cryptographic trust/key
  change, no cross-session migration, and no live provider/deployment work.

## Test plan

- RED/GREEN unit tests in `internal/routingdecision` for the durable contract,
  especially exact bytes and restart/ambiguous-ack behavior.
- Controller/service tests for launch and authoritative non-admission source
  facts, duplicate emission, and unknown preservation.
- API handler/client tests for bounded pagination, strict typed fields,
  authorization, idempotent ack, and conflicting ack refusal.
- CLI tests for JSON byte-preserving output, pending cursors, ack replay, and
  v2/v3 non-relabeling.
- Regenerate and run OpenAPI/schema/client synchronization tests.
- Run focused package tests, affected tests, `go vet ./...`, documented fast
  suite, and required API/spec/dashboard/doc gates as applicable.

## Support structures and docs

- Reuse existing bbolt helpers, keyset cursors, idempotency records, error
  classification, Huma registration, generated client, and city write auth.
- Add no helper abstraction until the store and service each have a concrete
  use; keep codec/validation at the persistence/wire edges.
- Update the existing Wayfinder SDK execution/delivery contract plan rather
  than creating a second competing architecture document.
- Maintain `agent-execution.log` as a temporary task artifact; append after
  each completed subtask.

## Execution order and stability strategy

Plan review → base preflight → focused RED tests → store implementation →
controller wiring → typed API/client/CLI → docs/generated artifacts → focused
verification → affected/full gates → vet/spec/docs checks → commit/push and
refinery handoff.

Keep all edits in the bead worktree. Preserve foreign changes in the polecat
home worktree. Use injected clocks and `t.TempDir`; never sleep/poll for
completion. Keep pagination and reads bounded, reject malformed/conflicting
state, and use atomic bbolt transactions. Do not change live city settings,
provider processes, keys, or sibling repositories.

## Blocker avoidance and candidate parallel work

- If the baseline fails, deduplicate by exact test function before filing or
  fixing unrelated failures.
- If generated artifacts or pre-commit are unavailable, diagnose the existing
  generator/hook and escalate with evidence; do not hand-edit generated output
  as a substitute.
- Candidate parallel review after the contract is frozen: one read-only exact
  byte/storage review and one API/schema/CLI boundary review. They are
  independent evidence reviews, not separate implementations. This session
  remains the single owner of source edits and final handoff.

## Proxy-domain audit

Target truth is durable source delivery and acknowledgement replay. Required
evidence is bbolt bytes after reopen plus service/API/CLI boundary tests. A
projection-only test, generated schema, or successful first request is cheaper
but insufficient. The false-completion substitution is calling a live
projection/outbox write “delivery.” If all planned checks pass while the bug
remains, inspect whether the source fact was actually persisted before the
transport call and whether conflicting bytes were rejected after restart.

## No-change decisions

- No City or Wayfinder plugin source changes: this bead owns SDK delivery
  authority and projections only.
- No new database, event framework, trust/key/pin, scheduler, provider, or
  role-specific mechanism.
- No changes to existing signed schema-1/schema-2 ingest or v2/v3 outcome
  wire contracts except additive delivery surfaces.

## Planning refinement pass 1 — counter 1

### Critique of the initial plan

The target and evidence boundary are correct, but the initial plan leaves the
most important contract under-specified: “exact outcome bytes” could be
mistaken for a re-marshaled typed outcome. It also does not clearly separate
an immutable source item from the mutable fact that a transport consumer has
acknowledged it. A generic event-like record would risk becoming a second
event framework, and a read-time projection could still fabricate v2/v3
status. The producer wiring list is too broad until one source identity is
defined for each fact.

### Critical evaluation of that critique

The critique is valid. The store must own an append-only delivery item and a
separate acknowledgement record. The item should carry the exact serialized
outcome bytes plus a digest and a schema discriminator; the ack should bind
the item ID and digest without rewriting or deleting the item. A stable source
identity plus outcome identity must derive the item ID, so retries cannot make
duplicates. Validation must decode the declared v2/v3 payload and reject a
schema mismatch before storage. This is still a focused durable-store seam,
not a general event bus.

### Roll-up applied to the plan

Freeze these names and invariants before implementation:

- `DeliverySchemaVersion = "routing/delivery/v1"`.
- Immutable `DeliveryItem`: delivery ID, source kind/ID, outcome schema
  version, outcome ID, work/decision causal identities, exact `Payload []byte`,
  `PayloadSHA256`, and persisted evidence time. `[]byte` is a typed base64
  field on the JSON projection; it is not `json.RawMessage` or hand-built
  JSON.
- Separate durable `DeliveryAck`: delivery ID, payload digest, and one
  persisted acknowledgement time. A same-ID/same-digest retry replays the
  stored ack; a different digest is `ErrDeliveryAckConflict`.
- `RecordDelivery` is source-owned, idempotent by stable delivery ID, and
  refuses a same-ID/different-byte collision. `ListPendingDeliveries` is a
  bounded delivery-ID keyset read that excludes only items with durable acks.
- Source IDs are the successful launch receipt ID for v3 launch facts and the
  exact lifecycle transition receipt identity for v2 non-admission facts. No
  unknown outcome becomes a delivery item.
- v2 and v3 payloads are validated against their declared typed contracts;
  the stored schema value is compared to the payload’s `schema_version`.

### No-change decisions from pass 1

- Do not add a `DeliveryState` field to the immutable item; pending is the
  absence of an ack, which avoids mutable source records and read-time state.
- Do not accept caller-provided terminal status or observation time through
  API/CLI. Only controller-created source records can call `RecordDelivery`.
- Do not expose the bbolt ack bucket as an event stream or delete acknowledged
  source items during ordinary ack.

## Planning refinement pass 2 — counter 2

### Critique of pass 1

Pass 1 correctly freezes immutable bytes and a separate acknowledgement, but
it still leaves two integration hazards. First, an item must not be emitted
from every generic store transition: that would make the store infer domain
meaning without the authoritative work/launch context and could duplicate
v2 and v3 facts. Second, the API/CLI surface is not concrete enough to prove
that the consumer can drain bounded pending work without gaining authority to
create or rewrite outcomes. The plan also needs a precise rule for the
non-admission source identity and for which endpoint methods may write.

### Critical evaluation of that critique

Both hazards are real. The store should expose narrow immutable-record and
ack primitives, while controller/service code is responsible for constructing
typed source facts at existing authoritative boundaries. Generic `Transition`
must remain a lifecycle primitive, not a hidden delivery producer. A delivery
item can be created immediately after a committed launch receipt or committed
authoritative non-admission transition, with a source ID derived from that
durable fact. The read API needs only pending retrieval; the write API needs
only acknowledgement and must use the existing city write grant plus CSRF.
There is no need for a delivery-ingest endpoint, caller-supplied outcome, or
new idempotency header because the durable delivery ID and payload digest are
the idempotency key.

### Roll-up applied to the plan

Refine implementation and wire contracts as follows:

- Add store primitives with explicit names and narrow responsibility:
  `RecordDelivery(item)`, `ListPendingDeliveries(opts)`, and
  `AcknowledgeDelivery(ack)`. The first two are controller/service-facing;
  the third is exposed through the authenticated API provider. All methods
  use the existing store lock, schema initialization, bounded keyset cursor,
  and typed sentinel errors.
- Keep producer construction outside generic `Transition`. The launch
  producer runs only after `RecordExecutionLaunchAttempt` has committed and
  serializes the typed v3 producer outcome from that receipt. The
  non-admission producer runs only after the authoritative service/controller
  operation has committed a refused, expired, or revoked transition; it
  serializes a strict v2 `OutcomeRecord` whose status is `not_admitted` and
  whose source ID is a deterministic transition identity. Claimed,
  outcome-recorded, and unknown states do not create a terminal item.
- Define the transition identity from durable decision identity plus the
  committed store revision and terminal state (not wall-clock time). If the
  current store does not retain enough information to make that identity
  stable, add the smallest persisted transition receipt field rather than
  deriving it from a fresh read.
- Add typed provider methods for one page of pending delivery items and one
  acknowledgement result. Register `GET /routing/delivery/pending` with
  existing city read authorization and `POST /routing/delivery/ack` with
  existing city write authorization and CSRF. The ack body contains only
  delivery ID and payload digest; response reports the durable ack identity
  and whether it was a replay. Unknown IDs and digest conflicts are typed
  client errors.
- Add `gc routing delivery pending` and `gc routing delivery ack` as typed
  projections. JSON mode emits the generated page/result directly, preserving
  base64 payload bytes and cursors. Human mode may summarize IDs/digests but
  must never replace the JSON contract. Existing v2 and v3 commands/routes
  remain unchanged.

### No-change decisions from pass 2

- Do not make `internal/routingdecision.Store` an event publisher or add a
  callback/listener abstraction for delivery.
- Do not let API, CLI, City, plugin, or a transport consumer create delivery
  items; only SDK controller/service producers may do so.
- Do not add a separate daemon queue, background scheduler, retry loop, or
  delivery lease. Pending retrieval is bounded and replay-safe; consumer
  scheduling remains outside the SDK.
- Do not require a new authentication scheme or interpret an acknowledgement
  as proof of execution; source-layer authority remains in the SDK ledger.

## Planning refinement pass 3 — counter 3

### Critique of pass 2

Pass 2 makes the producer and wire boundaries concrete, but it risks
overpromising v2 non-admission delivery if the current lifecycle audit does
not preserve a deterministic committed revision for every terminal transition.
It also needs an explicit serialization rule: calling `json.Marshal` on a
typed outcome at the producer boundary is acceptable only if the bytes are
then persisted and never regenerated for delivery. Finally, generated API
artifacts and CLI tests are useful only after the persistence and producer
tests prove the source facts exist; otherwise a clean surface test can mask a
missing producer.

### Critical evaluation of that critique

The critique identifies the final failure modes. Before coding, inspect the
actual transition audit fields and use the existing durable revision or add a
minimal receipt identity. If a particular v2 terminal path cannot provide a
stable source fact without broadening the store, keep that path unknown and
record the limitation instead of fabricating a delivery row. Typed JSON is
the edge codec: marshal once in the controller producer, validate the
declared schema, compute the digest over those exact bytes, and persist the
bytes. Consumer reads return cloned persisted bytes. Verification must be
layered from store to producer to API/CLI, with each layer named for what it
does and does not prove.

### Roll-up applied to the plan

The final execution order and acceptance gates are:

1. Finish read-only inspection of transition/audit persistence and start the
   temporary execution log; document any terminal path that remains unknown.
2. Write failing focused store tests for immutable item identity, exact bytes,
   duplicate/conflicting records, reopen replay, bounded pending pages, ack
   idempotency/conflict, and corruption. Implement only the minimum bbolt
   bucket/index and typed methods needed to make them pass.
3. Write failing controller/service tests at the committed launch and
   authoritative non-admission boundaries. Implement producers that marshal
   once, persist the exact bytes, and retry by source identity. Prove that
   unknown/claimed/v2-v3 mismatches remain unknown or are refused.
4. Add failing provider/handler/client/CLI tests, then implement the typed
   pending/ack projections and generated artifacts. Verify auth at the
   existing city route boundary and verify that ack cannot create or mutate an
   outcome.
5. Run focused tests, affected/full fast tests, vet, generated-spec checks,
   and the repository pre-commit hook. Review the final diff for source-only
   scope, exact-byte retention, no fabricated timestamps/status, and no
   duplicate API contract. Remove this temporary execution log before the
   final commit, then push and hand off to the refinery.

### No-change decisions from pass 3

- Do not force a v2 delivery row for a terminal path lacking a persisted
  causal identity; preserve `unknown` and report the evidence gap.
- Do not canonicalize or pretty-print payloads after persistence, and do not
  regenerate a payload during pending reads or acknowledgement.
- Do not treat API schema generation, CLI output, or a successful first ack
  as evidence of source delivery; the store reopen and producer tests remain
  mandatory.
- No parallel implementation is warranted: the store, producer, and wire
  changes share the same exact-byte contract. A read-only review can happen
  after tests, but source ownership stays in this worktree.

## Planning roll-up check

After three passes, no required target truth was lost. The plan now names the
durable source layer, the producer commit boundaries, the exact-byte codec
boundary, the transport-only ack, typed API/CLI surfaces, authorization, and
the evidence each test layer provides. The only intentionally unknown case is
a terminal transition that cannot be tied to a persisted causal identity;
that case must not be relabeled as delivered.
