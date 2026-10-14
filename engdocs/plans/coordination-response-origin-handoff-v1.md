# External coordination response-to-origin handoff contract v1

`counter=0`

Status: design and ownership contract for the additive response-to-origin
handoff. This document is the scoped deliverable for `sdk-h0s.1`; it does not
claim that the runtime, a provider, or the hosted integration is ready.

## 1. Planning pass zero: full plan

### Target truth

The target behavior is a crash- and retry-safe handoff of one authenticated
external-coordination response to the correct requesting origin. The handoff
must preserve the request identity, attempt, correlation, destination, and
authorization fence; it must distinguish a coordinator response from delivery
to the origin; and it must be independently recoverable after a process or
provider failure.

The contract is intentionally additive. Existing external-coordination request
delivery, response recording, extmsg publication, and transcript storage remain
their own boundaries. This file freezes the seams that the subsequent recovery
(`sdk-h0s.3`) and return (`sdk-h0s.4`) work must implement.

### Tasks and subtasks

1. Establish the vocabulary and state machines.
   - Separate request delivery state from response outcome and origin-handoff
     state.
   - Define the four explicit response outcomes: `answered`, `refused`,
     `failed`, and `expired`.
   - Define what `follow_up_required` does and does not mean.
2. Freeze the existing wire and persistence fields.
   - Keep `coordination_request_id` and `coordination_attempt` as the
     canonical bridge metadata names.
   - Preserve request `result_destination` and opaque `route_identity` as
     additive inputs; do not infer a destination from source attribution.
   - Keep response authentication and adapter-registration checks at the API
     boundary.
3. Assign one owner to each transition and side effect.
   - Keep coordination queue/claim/response recording in the coordination
     service.
   - Put durable handoff state, claims, recovery, and idempotency in the
     return-handoff owner.
   - Use the existing extmsg/session authorization and transcript boundaries
     for the actual origin publication and evidence.
4. Specify retry, recovery, retention, and security fences.
   - Bound attempts by configured retry policy and request expiry.
   - Reconcile ambiguous delivery before any possible resend.
   - Scrub ephemeral content without deleting the identity needed for replay
     protection and audit.
5. Define evidence and an implementation fence.
   - Name tests by the layer they observe and what they cannot prove.
   - Map the follow-on beads to owned files and interfaces.
   - Record unresolved choices that should remain implementation decisions.

### Architectural changes and ownership boundaries

This bead changes no production source. It publishes the protocol and
ownership boundary for later additive changes:

| Boundary | Owns | Must not own |
| --- | --- | --- |
| External-coordination service | Request records, queue/claim, adapter delivery receipt, response authentication input, expiry and request retry | Origin publication or the claim that an origin received a response |
| API response handler | Typed wire decoding and configured adapter/generation/instance/bearer validation | Business state transitions, route selection, or origin delivery |
| Return-handoff owner (`sdk-h0s.4`) | Durable handoff record, conditional claim/recovery, destination fence, idempotency, handoff state | Request delivery policy, arbitrary callbacks, or credential storage |
| Extmsg/session boundary | Configured session/conversation resolution, binding authorization, outbound publication | Deciding whether a coordinator outcome is complete |
| Transcript service | Append and query of the outbound/inbound evidence record | Treating transcript presence as proof that a recipient acted |

The follow-on implementation may choose the smallest package boundary that
fits the existing code. It must not add a second generic dispatcher or make
the CLI/API own domain state.

### Test and evidence plan

The target truth is origin handoff correctness under duplicate delivery,
crash, restart, expiration, and ambiguous provider receipts. Evidence must be
named by its observed layer:

- Contract/package tests prove field names, state-transition guards,
  conditional ownership, replay identity, retention, and legacy normalization.
  They do not prove that a real provider or origin session received or acted
  on a message.
- Adapter tests prove canonical metadata, request/attempt/correlation fences,
  and receipt interpretation. They do not prove live network behavior.
- Extmsg/session tests prove configured destination resolution, authorization,
  publication, and transcript append. They do not prove coordinator state was
  recorded correctly.
- API tests prove typed request/response projection and authentication at the
  configured adapter-registration boundary. They do not prove origin
  publication.
- A provider-backed integration test must exercise a durable store, a
  recording adapter, restart/recovery, and a real origin publication path. A
  successful HTTP response, a semantic preview, a queue fixture, or a
  transcript row alone is insufficient evidence of end-to-end delivery.

### Support structures

The durable identity tuple is:

`(coordination_request_id, coordination_attempt, correlation_id,
result_destination)`

The return-handoff implementation should derive one stable idempotency key
from that tuple using an unambiguous length-prefixed encoding. Retry count,
wall-clock time, route-map iteration order, response body, credentials, and
provider-specific URLs are not inputs to that key. The same key is passed to
the outbound provider through the existing `IdempotencyKey` field and is
retained in the handoff record.

The handoff record needs an owner/lease, claim timestamp, state, attempt
identity, correlation, destination identity, provider receipt (when any),
response commitment, and sanitized error class. The exact bead metadata names
and schema are implementation-owned by `sdk-h0s.3`/`sdk-h0s.4`; this contract
does not add speculative public JSON fields.

### Documentation and integration plan

This contributor-facing plan/contract is the normative handoff artifact.
Follow-on implementation beads should link back to this path in their notes
and tests. The retained callback contracts remain separate: this contract
describes returning a response to its origin, not convoy callback fan-out.
No OpenAPI or generated dashboard artifact changes are required for this
contract-only bead. If later implementation adds a public wire field, that
implementation must run the API generation/synchronization gates itself.

### Execution order

1. Publish and review this contract on a fresh task branch.
2. Implement bounded recovery and conditional claims in `sdk-h0s.3`.
3. Implement durable response-to-origin handoff in `sdk-h0s.4`, consuming this
   contract and the recovery primitives.
4. Integrate production callback call sites in `sdk-h0s.5` only after the
   return path has independent evidence.
5. Run the coordinator integration and final review beads.

### Stability and blocker avoidance

- Use only the clean task-owned worktree and the supported `gc bd` wrapper for
  city-prefixed bead reads. Do not repair or reinitialize Dolt, inspect
  credentials, or broaden into database/profile work.
- Do not import or rewrite the preserved `sdk-a7n`, `sdk-2m9`, or `sdk-njx`
  branches. Their code and human fences are evidence only.
- Keep `coordination_request_id`/`coordination_attempt` canonical. A legacy
  decoder may read old records, but new writes must not emit old spellings.
- Do not make an ambiguous provider receipt look like success. Reconcile
  first; a possible remote acceptance must not be blindly resent.
- Do not change route, claim, session, config, cleanup, refinery, or human
  fence state as part of this contract publication.

### Candidate parallel work

The CLI work (`sdk-h0s.2`) is independent and must stay in its command files.
The recovery and return work are intentionally sequential because they share
the service and handoff state boundaries. Callback production wiring
(`sdk-h0s.5`) must wait for the return contract and recovery behavior. The
integration and review beads can independently audit this document against
the current main source and retained branches without importing them.

### Proxy audit

- Target truth: the correct origin receives at most one logical response for a
  given request attempt, and the system converges after duplicate, crash, and
  ambiguous-receipt events without crossing an owner or destination fence.
- Required evidence layer: durable coordination state plus the configured
  extmsg/session provider boundary, exercised through restart/recovery.
- Cheaper useful-but-insufficient proxies: JSON round-trips, unit state
  machines, a successful API response, a transcript row, or a semantic
  preview. These are valuable layer-local evidence only.
- Tempting false-completion substitution: treating the response hash,
  `RecordResponse`, provider `accepted`, or transcript append as proof of
  origin delivery. The contract explicitly rejects each substitution.
- If this plan fully succeeds but the original bug remains, the likely gap is
  an untested crash between provider acceptance and durable handoff state, a
  destination resolver that returns a valid but wrong session, or a recovery
  path that reuses an old attempt without reconciling the provider. The
  implementation and integration evidence must cover those seams.

## 2. Planning pass one: critique of the plan

### Critique, top to bottom

1. Target truth is correctly narrower than “the coordinator answered”, but it
   needs a stronger statement that `answered` is a response outcome while
   origin delivery is a separate fact. Otherwise an implementation could
   still use a response state as a handoff completion flag.
2. The task list names the important areas, but it does not yet enumerate the
   exact current wire fields and current delivery states. A producer/consumer
   handoff needs those names frozen to avoid parallel beads inventing aliases.
3. The ownership table is useful and keeps domain logic out of API/CLI, but
   the return-handoff owner needs an explicit rule for the one durable owner
   of each transition. It should also identify the transcript as evidence,
   not a second state machine.
4. The evidence plan distinguishes layers, but it needs explicit negative
   assertions: no premature `completed`, no cross-conversation publication,
   no replay conflict accepted, and no unbounded retry. Those are the likely
   regressions at this boundary.
5. The idempotency tuple is a good support structure, but its handling of
   response identity and route-map validation is under-specified. The contract
   must say what happens when the same idempotency key carries a different
   response or destination.
6. The execution order and blocker rules are appropriate. The “no public
   fields” fence should distinguish the fields already present on the current
   request/response wire from new fields that would require API generation.
7. The proxy audit correctly calls out weak evidence, but the target should
   mention retention and credential exclusion because a delivery path that
   leaks a bearer or ephemeral prompt is not correct even if it reaches the
   origin.

## 3. Planning pass one: critical evaluation of that critique

The critique is actionable rather than a request to expand the feature. The
largest risk is semantic ambiguity between three notions of success: the
coordinator recorded an outcome, the provider accepted a publication, and the
origin actually received/acknowledged it. The revised plan must name all three
and prohibit collapsing them. The second risk is parallel implementation drift
around fields and legacy spellings; an explicit current-field inventory and
compatibility rule addresses it without importing held code. The third risk is
over-specifying an unimplemented storage schema; exact internal names should
remain follow-on ownership, while invariants and state transitions are frozen
here.

## 4. Planning pass one: roll-up applied to the critique

The contract will add a current wire inventory, separate state/outcome tables,
an explicit single-owner transition rule, conflict and cross-destination
guards, bounded-retry/retention negative assertions, and a compatibility
section. It will keep public schema unchanged in this bead and describe only
the invariants future implementation must satisfy.

## 5. Planning pass one: revised plan and subtasks

1. Freeze current request, response, receipt, record, and bridge metadata
   fields exactly as they exist on `origin/main`.
2. Define three separate facts:
   - delivery lifecycle of the request;
   - explicit coordinator response outcome;
   - origin-handoff state and any origin receipt.
3. Define one transition owner and conditional claim/lease rules for every
   handoff state.
4. Define destination and route-identity semantics, including rejection of
   conflicting replay inputs and source-session inference.
5. Define retry classes, ambiguity reconciliation, retention, and security
   invariants.
6. Define legacy read compatibility without old-name writes or held-branch
   imports.
7. Define layer-specific and integration evidence with negative assertions.

## 6. Planning pass one: explicit no-change decisions

- No production Go, test, API schema, generated type, CLI, or configuration
  file changes in this bead.
- No new public wire field is introduced here; existing fields are documented
  and bounded for consumers.
- No callback fan-out behavior is redefined.
- No held branch is merged, cherry-picked, copied, or cleaned up.
- No response hash, transcript row, provider acceptance, or HTTP success is
  treated as origin delivery.

`counter=1`

## 7. Planning pass two: critique of the revised plan

### Critique, top to bottom

1. The revised target has the right facts but does not yet state the allowed
   response outcome vocabulary. A free-form state string could reintroduce
   “completed” ambiguity.
2. The field inventory requirement is clear, but compatibility needs concrete
   legacy names and a rule for partial or conflicting old records. Historical
   data must be readable without making guesses or requiring credentials.
3. The owner rule needs to distinguish a delivery receipt from a response
   commitment and from an origin acknowledgment. Each can be durable without
   authorizing the next state.
4. Destination semantics must say what absent, malformed, or unauthorized
   destinations do. A fallback to `source_session_id` would recreate the old
   adapter shortcut and could cross an owner boundary.
5. Retry/retention needs deterministic retry timing, terminal classifications,
   and a clear rule for scrubbing route data. “Durable” must not mean “store
   credentials forever”.
6. The evidence plan should tie each negative assertion to a test layer and
   state what an integration fixture must observe after a restart.
7. The no-change decisions are safe, but the implementation fence should
   explicitly say which follow-on bead owns any public API projection and
   generated artifacts.

## 8. Planning pass two: critical evaluation of that critique

This critique identifies contract ambiguities that can cause incompatible
implementations, so they must be resolved in the document rather than left to
code review. The outcome vocabulary should be small and explicit, while
delivery and handoff states may remain richer because they describe different
layers. Legacy compatibility should be a read-only decoder with quarantine for
ambiguous records, not a migration hidden inside new delivery code. Retention
must preserve replay fences while scrubbing sensitive content. The integration
test requirement should require observable duplicate suppression and correct
destination, not only a persisted row.

## 9. Planning pass two: roll-up applied to the critique

The final contract will include the exact current field inventory, explicit
state matrices, outcome vocabulary, legacy aliases and quarantine behavior,
typed destination rules, deterministic bounded retry, content-scrub rules,
per-layer negative evidence, and the follow-on ownership of any generated API
projection.

## 10. Planning pass two: revised plan and subtasks

1. Inventory current source contracts and mark which fields are wire, durable,
   bridge metadata, or in-memory authentication inputs.
2. Define response outcomes (`answered`, `refused`, `failed`, `expired`) and
   keep them separate from request delivery and handoff states.
3. Define explicit destination/route identity and the stable idempotency
   derivation, including replay conflict behavior.
4. Define conditional ownership, lease recovery, ambiguity reconciliation,
   bounded retry, deterministic `RetryAfter`, and terminal classes.
5. Define ephemeral/durable retention and never-persist rules.
6. Define canonical-vs-legacy read/write behavior and quarantine.
7. Define implementation ownership and evidence gates, including restart
   integration evidence.

## 11. Planning pass two: explicit no-change decisions

- The response `state` string remains an outcome field for the current API
  projection; this bead does not rename it or add an enum to generated API
  types.
- The current request `result_destination` remains a string on the existing
  wire; typed semantics are an internal consumer contract for the follow-on
  return implementation.
- `route_identity` remains opaque to generic infrastructure; bounds and
  rejection rules here do not authorize arbitrary URLs or commands.
- Existing `internal/extmsg` authorization and transcript APIs are reused;
  this bead does not create a parallel outbound transport.
- No live provider, credential, or hosted-service readiness claim is made.

`counter=2`

## 12. Planning pass three: critique of the second revision

### Critique, top to bottom

1. The plan now protects the three success facts, but it should call out that
   `cancelled` is a request-lifecycle operation and is not an answer outcome.
   Likewise, `follow_up_required` must be a modifier, not a fourth outcome.
2. The destination rules need an explicit authorization result for a valid
   but wrong conversation/session, not just malformed input. Cross-owner and
   cross-conversation mistakes are security failures.
3. The retry section should say that attempt identity never rewinds and that
   an accepted/unknown remote publication cannot be replaced by a new target
   or blindly resent.
4. Legacy handling must acknowledge historical `hca_*` metadata and the
   current `coordination_*` spellings, and must state that new writes emit
   only canonical names.
5. The evidence plan should separate an origin provider’s “delivered” receipt
   from an origin application acknowledgment. If the configured provider has
   no acknowledgment, the handoff must remain at the strongest truthful
   provider state.
6. The file needs an explicit unresolved-scope fence so later implementers do
   not silently decide registration-fence storage, provider reconciliation, or
   API generation in the wrong bead.

## 13. Planning pass three: critical evaluation of that critique

These are final semantic gaps, not requests for more implementation. They
protect the boundary from two common errors: making lifecycle vocabulary look
like an answer, and making a provider receipt look like an application-level
acknowledgment. The compatibility spelling rule is necessary because old HCA
records exist in history while current main has already standardized the
external-coordination names. The unresolved-scope fence lets follow-on beads
make concrete choices without changing this contract’s stable invariants.

## 14. Planning pass three: roll-up applied to the critique

The normative section below will explicitly distinguish `cancelled`, answer
outcomes, provider delivery, and origin acknowledgment; reject cross-owner or
cross-conversation destinations; preserve monotonic attempt identity; reconcile
possible remote acceptance; accept legacy spellings only for read compatibility;
and record provider delivery as the terminal evidence available when no origin
acknowledgment protocol exists. It will finish with unresolved fences and a
lost-information check.

## 15. Planning pass three: revised plan and subtasks

1. Publish the normative wire, outcome, destination, state, retry, retention,
   security, and compatibility contract below.
2. Map each rule to its owning follow-on bead and evidence layer.
3. Verify that no production source or held branch was touched.
4. Run document whitespace and relevant repository checks, commit only this
   contract, push the per-bead branch, and hand it to the refinery.

## 16. Planning pass three: explicit no-change decisions

- No provider-specific acknowledgment is invented where the current provider
  only reports acceptance/queue/delivery.
- No automatic fallback destination is invented when `result_destination` is
  absent or invalid.
- No old HCA spelling is emitted, even when a legacy record is read.
- No public API schema or generated artifact is changed by this publication.
- No cleanup/refinery/human fence is changed.

`counter=3`

## 17. Lost-information check after three passes

The first plan’s target truth, ownership table, evidence-layer distinctions,
parallel-work split, and blocker fences remain present. The revisions added
the current field inventory, explicit outcomes, legacy handling, destination
authorization, monotonic attempts, retention/security constraints, and
provider-versus-origin evidence distinctions. No requirement was dropped; the
only intentionally deferred material is implementation-specific storage and
provider registration detail.

# Normative contract

## 18. Scope and success facts

The response-to-origin handoff is a separate operation after a coordinator
response has been authenticated and recorded. It has three independently
observable facts:

1. **Coordinator outcome recorded:** a valid response was accepted for the
   request attempt and its response commitment is durable.
2. **Origin provider result:** the configured outbound provider accepted,
   queued, or reported delivery of the publication.
3. **Origin acknowledgment:** the origin application explicitly acknowledged
   receipt, if and only if the selected provider/protocol supplies such an
   acknowledgment.

Fact 1 never implies fact 2 or 3. Fact 2 never implies fact 3. A response
commitment, transcript row, HTTP success, or provider `accepted` result is not
origin acknowledgment.

The contract target is convergence to the strongest truthful fact available,
with no duplicate logical publication for one request attempt, no
cross-owner/cross-conversation delivery, no sensitive-content leak, and no
unbounded retry. A provider with no application acknowledgment may terminate
at its documented delivery receipt; it must not claim an application
acknowledgment it cannot observe.

## 19. Current wire and durable fields

The following inventory reflects `origin/main` and is frozen for consumers of
this contract. It documents existing fields; it does not add them in this
bead.

### Request JSON

The external-coordination request contains:

`request_id`, `attempt`, `target`, optional `city`, `work_ref`, `repository`,
`rig`, `reason`, `delivery_mode`, `session_mode`, `prompt`,
`content_retention`, `allowed_tools`, `correlation_id`, `idempotency_key`,
`expires_at`, `result_destination`, `route_identity`, and `created_at`.

`result_destination` is an explicit return target. `route_identity` is opaque
correlation/provenance data. Neither is a command, URL, bearer, or permission
grant.

### Request durable projection

`RequestRecord` contains the request plus `id`, `state`, `attempt`,
`claimed_by`, `claimed_at`, `delivered_at`, and `error`; response commitment
and response-scrub state are internal durable fences. The request delivery
states currently in use are `accepted`, `queued`, `running`, `completed`,
`failed`, `expired`, and `cancelled`.

`completed` on this record means the coordinator accepted/recorded the
response-side request lifecycle as defined by the current service. It does not
mean that the origin received the response.

### Response JSON and authentication boundary

The response contains `request_id`, `attempt`, `correlation_id`, `response_id`,
`state`, optional `summary`, optional `content_retention`,
`follow_up_required`, and `received_at`.

The response API requires the configured adapter, adapter generation, adapter
instance, and bearer authentication at the request boundary. Those
credentials and authorization headers are in-memory authentication inputs;
they are not response payload, handoff metadata, or durable route identity.
The handler validates them and delegates domain transitions. It does not
select an origin or mark an origin handoff complete.

### Bridge metadata

New bridge publications use these canonical metadata keys:

| Key | Meaning |
| --- | --- |
| `coordination_request_id` | Stable request identity |
| `coordination_attempt` | Monotonic attempt identity for this request |
| `source_agent` | Requesting-agent provenance |
| `reason` | Configured coordination reason |
| `work_ref` | Optional work provenance |
| `correlation_id` | End-to-end correlation fence |
| `content_retention` | Content-retention mode |

Values are bounded at the transport boundary. New writes must not emit the
historical `hca_request_id` or `hca_attempt` spellings.

## 20. Explicit response outcomes

The response `state` is an outcome vocabulary, not the request delivery or
origin-handoff state machine. v1 recognizes exactly:

| Outcome | Meaning | Origin-handoff implication |
| --- | --- | --- |
| `answered` | The coordinator produced an answer or decision | Publish the answer subject to retention and destination authorization |
| `refused` | The coordinator explicitly declines or cannot authorize the requested work | Publish the refusal if a valid destination exists; do not imply work execution |
| `failed` | The coordinator attempted but could not produce a valid answer | Publish a sanitized failure outcome if configured; do not retry as an origin delivery |
| `expired` | No valid outcome was produced before `expires_at` | Publish only the sanitized expiration outcome if configured; never invent content |

`follow_up_required` is a modifier on any permitted outcome. It is not a
fourth outcome and does not authorize a new destination or an unbounded retry.
`cancelled` is a request-lifecycle operation; it is not a coordinator answer
outcome. A legacy record with another answer label must be normalized only by a
compatibility rule that preserves its meaning; unknown labels are refused or
quarantined rather than guessed.

Response summaries are optional and are retained only according to the
request/response retention contract. Failure and expiration details must be
sanitized and must never contain bearer credentials, authorization headers,
provider secrets, arbitrary callback URLs, or command text.

## 21. Destination and route identity

`result_destination` is the sole explicit return destination. It is an opaque
logical session selector consumed by the existing session/extmsg resolution
boundary. The resolver may map a configured logical identity to a concrete
session or conversation, but generic coordination code must not interpret the
value as a URL, shell command, provider name, arbitrary conversation ID, or
permission grant.

- If `result_destination` is absent, the response is still recorded and the
  handoff state is `not_requested`; no origin is synthesized.
- If it is malformed, expired, unregistered, or unauthorized, the handoff
  fails with a sanitized authorization/destination error. It must not fall
  back to `source_agent`, `source_session_id`, `target_id`, or the request’s
  delivery conversation.
- A valid selector resolving to the wrong owner or conversation is a security
  failure, not a successful delivery. The handoff must reject it before
  publication.

`source_agent` and the extmsg `source_session_id` metadata are provenance.
Historical adapter fallback behavior using `source_session_id` is not a
response destination and must not be revived.

`route_identity` is an opaque, bounded, immutable correlation map. The v1
consumer contract permits at most 16 entries, keys of at most 64 lowercase
`[a-z0-9._-]` characters, and values of at most 256 printable characters.
Values that look like credentials, bearer tokens, callback URLs, or commands
must be rejected or scrubbed. These are consumer invariants, not a claim that
the current main service already validates every bound. The return
implementation owns the validation at its boundary and must preserve the map
without using it as routing authority.

## 22. Handoff identity, idempotency, and ownership

There is one logical handoff record for:

`(coordination_request_id, coordination_attempt, correlation_id,
result_destination)`.

The implementation derives a stable key from an unambiguous length-prefixed
encoding of those fields and a version marker. It excludes retry count,
timestamps, map iteration order, response body, credentials, and provider
URLs. It passes that key as the existing outbound `IdempotencyKey` and stores
the key with the handoff record.

The same key with the same response identity is an exact replay and must be
idempotent. The same key with a different request attempt, correlation,
destination, response identity, or response commitment is a conflict and must
not overwrite or publish. Conflicts are durable sanitized errors for review.

One handoff owner performs each transition through a conditional write or
equivalent compare-and-swap fence. A transcript append, response commitment,
or provider receipt may be evidence consumed by that owner, but none may
independently transition the handoff.

## 23. State machines

### Request delivery

The current request states remain `queued`, `running`, `accepted`,
`completed`, `failed`, `expired`, and `cancelled` as applicable to the
existing service. Claiming a queued request sets the current attempt and
claim owner. An attempt never rewinds. A response may close the coordinator
request record after identity/correlation validation, but that closure is not
origin completion.

For recovery, a running claim whose lease has expired is first marked
ambiguous/abandoned in the recovery owner. The owner reconciles the same
configured target and idempotency identity before retry or fallback. It must
not blindly resend a request that may already have been accepted remotely.

### Response-to-origin handoff

The additive internal states are:

`not_requested` -> `queued` -> `claimed` -> `accepted` -> `delivered`

with `uncertain`, `failed`, and `expired` terminal or review states as
appropriate. `reconciled` may resolve an `uncertain` state only after the
configured provider confirms the original identity. An implementation may
name an application-acknowledged state, but it must keep it distinct from
provider `delivered`.

- `not_requested`: no explicit destination exists.
- `queued`: a destination-qualified handoff is durably pending.
- `claimed`: one owner holds the handoff lease.
- `accepted`: the outbound provider accepted or queued the idempotent
  publication; this is not application acknowledgment.
- `delivered`: the provider reported delivery under its own contract.
- `uncertain`: the outcome is ambiguous; reconcile before any resend.
- `reconciled`: the original publication identity was confirmed after an
  ambiguous result.
- `failed`: a permanent or authorization failure prevented publication.
- `expired`: the handoff could not obtain a valid result before its expiry.

No state named `completed` may be used to hide the difference between a
response commitment and origin delivery. If a future API exposes a completed
handoff, it must carry separate evidence for provider delivery and any origin
acknowledgment.

## 24. Retry, recovery, and retention

Retry is bounded by configured maximum attempts and `expires_at`. The
coordination attempt and handoff identity are monotonic and never reset during
recovery. `RetryAfter` is deterministic for a given attempt and failure class;
the implementation must not create a busy loop.

- Transient, rate-limited, or unavailable provider failures may requeue while
  the bound remains.
- Permanent authentication, authorization, target mismatch, invalid request,
  and not-found failures are terminal `failed` outcomes.
- Timeout or connection loss after submission is `uncertain`, not transient
  success. Reconcile the original idempotency key before any retry.
- An accepted or possibly accepted publication cannot be resent to a different
  destination or with a new logical identity.

Ephemeral content is scrubbed after the durable identity, state, timestamps,
commitment, and sanitized error class needed for replay/recovery are retained.
Durable retention may preserve the configured summary/body subject to policy,
but never persists bearer credentials, authorization headers, provider secrets,
arbitrary callback URLs, or command text. Scrubbing is itself idempotent and
must not erase the attempt/correlation fence.

## 25. Legacy compatibility

Read compatibility recognizes historical HCA records where evidence shows the
old names:

| Historical | Canonical current |
| --- | --- |
| `gc:hca-request` | `gc:external-coordination-request` |
| `hca.*` durable metadata | `external_coordination.*` durable metadata |
| `hca_request_id` | `coordination_request_id` |
| `hca_attempt` | `coordination_attempt` |

New writes use canonical names only. A compatibility reader may map a legacy
record when the identity, attempt, correlation, target, and retention values
are internally consistent. Partial, conflicting, credential-bearing, or
otherwise ambiguous records are quarantined/refused for review; the reader
must not guess or ask for credentials. The reader does not import candidate
code from the preserved `sdk-a7n`, `sdk-2m9`, or `sdk-njx` branches.

Historical `source_session_id` remains provenance/adapter context, never an
implicit `result_destination`. Historical response labels such as `answered`
can be preserved when their identity fence is valid. A historical transport
`completed` marker is delivery/recording evidence, not an answer outcome and
must not be silently converted to `answered` or origin completion.

## 26. Security and cross-boundary fences

- Only the configured external-coordination adapter and registration
  generation/instance may authenticate a response. Authentication material
  exists only at that boundary.
- Every response and handoff validates request ID, attempt, correlation,
  configured target, and applicable registration/owner fence.
- Destination resolution and publication use the existing extmsg/session
  binding and authorization boundary at delivery time.
- A destination resolving to another owner, conversation, or authorization
  scope is rejected; it is never repaired by fallback.
- Durable records and logs contain no bearer, authorization header, secret,
  arbitrary callback URL, or executable command.
- Route identity is correlation data, not an authority to select a provider,
  session, conversation, or command.

## 27. Evidence gates and follow-on ownership

| Follow-on | Owned surface | Required evidence |
| --- | --- | --- |
| `sdk-h0s.2` | Existing `cmd/gc` extmsg command surface | CLI tests for typed requests and existing API use; no coordination state rewrite |
| `sdk-h0s.3` | Recovery/claim/dispatch service and focused API projection | Conditional claim, abandoned-running recovery, bounded retry, monotonic attempt, retention and ambiguous-receipt tests |
| `sdk-h0s.4` | Additive response-to-origin handoff owner and extmsg/session integration | Destination/owner fence, stable replay, crash/restart recovery, provider receipt distinction, no duplicate publication |
| `sdk-h0s.5` | Production callback call sites | Callback integration tests proving the caller uses response outcome and handoff evidence correctly |
| `sdk-h0s.6` | Cross-component integration | Durable store plus recording provider and restart test observing the correct origin |
| `sdk-h0s.7` | Review and boundary audit | Current-main diff audit, legacy compatibility review, security/retention review |

The retained held branches are read-only evidence. They are not implementation
inputs and must not be merged or cleaned up by this bead.

## 28. Unresolved scope fences

The following are deliberately left to the owning follow-on implementation:

- exact durable bead metadata names for the new handoff record;
- the registration-fence storage shape and lease duration;
- the provider-specific reconciliation query/receipt contract;
- whether a provider can expose an application acknowledgment beyond delivery;
- any new public API projection and its generated OpenAPI/TypeScript changes;
- the concrete package name for the additive handoff owner.

These choices must preserve this document’s identities, states, security,
retention, ownership, and evidence invariants. They must not be smuggled into
the CLI, generic SDK, arbitrary URL callback, or a held human-fenced branch.

## 29. Final no-change record

This contract publication intentionally changes no Go source, tests, OpenAPI,
generated dashboard types, CLI behavior, route/claim/session/config state,
cleanup/refinery state, or human fence. It does not claim live provider,
hosted-service, or origin readiness. The only deliverable is this tracked
contributor contract for the follow-on beads.
