# sdk-ehn callback-subscription plan

counter: 3

## Pass 0 — full plan and execution order

### Plan, tasks, and subtasks

1. Define the `internal/convoy` callback-subscription boundary.
   - Model one recipient route per durable subscription.
   - Require an owner, registration ID, positive generation, non-empty
     authorized work scope, route identity, and lifecycle interests.
   - Define active, revoked, and unregistered states plus typed errors.
2. Implement bead-backed CRUD.
   - Persist a versioned structured record and indexed metadata.
   - Create and owner-scoped list/get reads.
   - Require an exact owner, registration ID, and generation fence on renew,
     update, revoke, and unregister.
   - Use conditional bead writes for every fenced mutation.
3. Add adversarial contract tests first, then make them pass.
   - Cover malformed and comma-joined identities, missing fences, replay and
     stale generations, cross-owner reads/mutations, route-as-authorization,
     terminal revocation/unregistration, and durable round trips.
4. Verify the owning package, affected shard, formatting, vet, and repository
   status without staging unrelated worktree changes.

### Architectural changes

Add one cohesive service in `internal/convoy` because callback subscriptions
authorize and describe delivery for convoy work. It depends only on the
existing `beads.Store` persistence boundary and its conditional-writer
capability. Route identity is opaque data and is never used as an authorization
principal. No provider, harness, role, credential, or callback URL is modeled.

### Test plan and evidence boundaries

The target truth is that a durable subscription can be observed and changed
only by its owner using its current registration/generation fence, and that
malformed or ambiguous identity input cannot create an authorization alias.
The owning evidence layer is the `internal/convoy` service contract suite
against `beads.MemStore`; it proves domain validation, persistence projection,
owner checks, and CAS fencing. A later API or real-Dolt test would prove
transport/provider composition, not this service contract. Smaller validation
helpers and a raw store round trip are useful but insufficient proxies. The
tempting false completion is a passing in-memory test that omits conditional
writes or treats route possession as authorization. Even if this plan succeeds,
the original bug could remain if a caller bypasses this service or if a future
wire handler authorizes by route; the API/fan-out work must therefore depend on
this boundary and use owner/fence inputs explicitly.

### Support structures, docs, and stability strategy

Use deterministic injected timestamps, deep-copy slices/maps at the boundary,
stable list ordering, and a versioned metadata envelope so restart and legacy
reads are diagnosable. Keep a short design record in this plan; no user-facing
docs or generated wire schema are needed because this is an internal domain
boundary. Preserve unrelated dirty files and do not modify the reference draft
worktree.

### Blocker avoidance and parallel candidates

No subagent is needed for this small cohesive boundary. Independent review of
the test matrix or a later API integration can be parallelized by the parent
graph after this service lands. If conditional writes are unavailable, fail
closed and report the typed store capability error rather than falling back to
an unfenced update.

## Pass 0 critique

The plan names the storage and authorization boundary and distinguishes target
truth from proxy evidence. It may be over-specific about package placement and
states without first checking sibling work. It does not yet settle exact method
signatures, metadata keys, or the relationship between revoke and unregister.

## Pass 0 critique evaluation

The unresolved details are implementation decisions that can be made after the
contract tests are written, but the plan must explicitly require an idempotency
policy and clarify that terminal records remain durably listable. Package
placement is justified by convoy ownership but should remain a small new file so
upstream alignment is easy.

## Pass 0 roll-up: revised critique

Add explicit idempotent replay rules, terminal-state retention, and a no-op
policy for same-owner same-fence terminal retries only where it cannot mask a
stale actor. Test method signatures through the exported service rather than
testing private metadata helpers alone.

## Pass 0 roll-up: revised plan

The implementation must define exact operation semantics before production code:
renew/update advance generation; revoke records a durable terminal state;
unregister records a distinct durable terminal state and closes the bead; all
terminal records remain owner-listable; stale and cross-owner requests fail.
Tests must pin those decisions and verify no unconditional mutation occurs.

## Pass 0 no-change decisions

No API routes, config fields, event types, or new abstraction interfaces are
added. No route lookup or callback delivery is implemented in this bead.

## Pass 1 — contract shape and mutation semantics

### Plan, tasks, and subtasks

Use `SubscriptionService` and exported value types in `internal/convoy`:

- `SubscriptionRecord` carries ID, schema version, owner, registration ID,
  generation, `WorkScope`, one opaque `RouteIdentity`, lifecycle interests,
  state, and lifecycle timestamps/reason.
- `CreateSubscriptionInput` supplies those fields plus `Now`.
- `RegistrationFence` is the pair `(RegistrationID, Generation)`.
- `SubscriptionPatch` can change scope, route, or interests, but never owner
  or registration ID.
- `Create`, `Get`, and `List` are owner-scoped reads; `Renew`, `Update`,
  `Revoke`, and `Unregister` require owner plus the exact current fence.
- `Renew` and `Update` increment generation. `Revoke` and `Unregister` are
  terminal, preserve the record for audit/listing, and are idempotent only for
  the exact owner/fence and matching terminal operation.

Represent records as closed/open beads with a versioned JSON metadata payload
and indexed scalar metadata. A conditional write is required for every
mutation; an incapable store returns `beads.ErrConditionalWriteUnsupported`
wrapped in a package error. The service never authorizes using route identity.

### Architecture and compatibility decisions

One record represents one recipient route; multiple recipients mean multiple
records, avoiding comma-joined identity parsing and making fan-out ownership
auditable. `WorkScope` supports a convoy ID and optional work references; at
least one scope anchor is required. Route and identity strings are trimmed but
never split, and any comma is rejected. Unknown future lifecycle-interest
strings remain valid when non-empty, preserving harness neutrality.

### Evidence and adversarial tests

Write tests for each operation through the public service. Assert that a route
string cannot be passed as an owner, that owner mismatch does not reveal a
record, that omitted or stale fences perform zero writes, and that two service
instances race through the same CAS boundary. Test durable metadata decode by
loading the same bead through a fresh service. Keep raw store metadata checks
only as supplemental evidence of the persistence shape.

### Support and stability

Clone mutable input/output values, sort list results by creation time then ID,
and inject `Now` with a UTC normalization helper. Use a package-local label and
metadata keys so unrelated bead queries cannot accidentally treat subscriptions
as work. Keep records closed only for unregister; revoke remains an open bead
with a terminal subscription state so generic closed filtering does not erase
revocation audit data.

## Pass 1 critique

The contract shape now prevents the obvious identity alias and specifies CRUD,
but allowing unknown lifecycle interests could admit malformed values and the
terminal replay rule is complex. The plan also assumes bead revision CAS is
available in every intended production store without defining the error surface
precisely. Revoke remaining open may surprise consumers that query closed beads.

## Pass 1 critique evaluation

Lifecycle names are provider-owned extension data, so syntactic validation
(non-empty, trimmed, no comma) is the correct boundary; semantic allow-listing
would put harness judgment in Go. Terminal replay must require the same terminal
state and exact fence, while any different operation or fence fails closed.
The package error should wrap a stable `ErrFencingUnavailable`, and the record
state—not bead status—must be authoritative for lifecycle. Revoke as an open
durable row is intentional and should be tested explicitly.

## Pass 1 roll-up: revised critique

Add explicit state constants and terminal-operation replay tests. Ensure all
mutations reread the bead and compare owner/fence before constructing writes;
never rely on a caller-provided record. Add a conditional-capability test that
proves no fallback `Update` occurs.

## Pass 1 roll-up: revised plan

Define active/revoked/unregistered states, `ErrUnauthorized`,
`ErrFenceRequired`, `ErrStaleFence`, `ErrInvalidState`, and
`ErrFencingUnavailable`. Require exact owner matching for reads and mutations.
On CAS conflict, return a stale-fence error and do not retry with an
unconditional write. Close the bead only in `Unregister`, while `Revoke`
persists its terminal state and timestamp without closing.

## Pass 1 no-change decisions

Do not add a route resolver, callback dispatcher, HTTP endpoint, or event-bus
integration. Do not add an interface for a single service implementation.

## Pass 2 — implementation safety review

### Plan, tasks, and subtasks

Implement the public types and service in one source file plus a focused test
file. Validation is performed before any store call on create and before any
mutation write. The mutation helper loads the record by exact bead ID, checks
the owner, current state, and fence, builds the next record, and calls
`ConditionalWriter.UpdateIfMatch` with the observed revision. On success it
returns the projected next record; on a precondition failure it returns
`ErrStaleFence` (with the storage error wrapped for diagnostics).

Persist canonical JSON for the structured record and scalar keys for label
queries. Decode rejects wrong schema, missing required fields, duplicate
interests, comma-joined values, invalid timestamps, and unknown states. A
legacy or malformed row is an error, never an empty subscription. List returns
only records whose owner exactly matches the query, and owner mismatch is
reported as unauthorized for Get/mutations while List returns no rows for that
owner to avoid cross-owner enumeration.

### Architecture and boundary review

The service remains below API/fan-out layers and above beads. Its route field is
opaque and never interpreted or resolved. `WorkScope` is authorization data
owned by the eventual fan-out caller; this service records it and does not
decide whether a lifecycle event is permitted. Registration generation is a
monotonic concurrency fence, not a wall-clock lease or proof of route
possession.

### Test plan and proxy audit

RED tests cover invalid identities and scope, create/read/list owner isolation,
renew generation advancement, update generation advancement, stale concurrent
updates, missing fences for update/revoke/unregister, exact terminal replay,
cross-owner route possession, revoke versus unregister state, malformed stored
JSON, and conditional-writer unavailability. The tests observe the domain and
CAS layers; they do not claim to prove API authentication or a real Dolt
deployment. A passing test that only inspects returned structs would miss
durability, so each successful mutation rereads from the store and at least one
test seeds/loads persisted metadata directly.

## Pass 2 critique

The plan is implementable and has a strong adversarial matrix, but “decode
rejects duplicate interests” can conflict with harmless input normalization,
and returning no rows from List makes malformed owner input indistinguishable
from a normal empty result. It also needs to ensure errors never expose route
or credential-like data and that create idempotency/replay semantics are not
accidentally implied without an idempotency key.

## Pass 2 critique evaluation

Reject duplicate interests rather than silently deduplicating: explicit
configuration should not be rewritten and duplicates may signal a fan-out bug.
List must validate the owner first, then return an empty result for a valid
owner with no records; invalid/comma owners return `ErrInvalidInput`, while a
valid unknown owner also returns empty. No credentials or URLs are accepted as
fields at all, and errors name only operation/id, not route values. Create is
not idempotent without an explicit key; repeated calls create independent
registrations, each with its own registration ID and generation.

## Pass 2 roll-up: revised critique

Add strict duplicate rejection and owner-input validation to the implementation
checklist. Make the record’s route field a single string and reject commas in
all identity-like strings, while preserving exact bytes for arbitrary
non-comma opaque values (including significant surrounding whitespace).
Document that create has no implicit idempotency and that mutations are replay
safe only within the exact fenced terminal operation.

## Pass 2 roll-up: revised plan

Proceed with strict validation, byte-preserving serialization, exact-owner read
authorization, conditional CAS mutation, and deterministic list ordering. Keep
the public error taxonomy stable and avoid including user-controlled route data
in error messages. Treat malformed durable records as hard read errors.

## Pass 2 no-change decisions

No credential, callback URL, network client, or external-service policy enters
the record. No deduplication key is added speculatively. No tests are moved to
integration tiers because the owning risk is the internal store contract.

## Three-pass refinement check

Passes 0–2 retain the full implementation, architecture, evidence, security,
stability, and handoff requirements. The final execution order is RED tests,
GREEN service, refactor/format, focused verification, commit, and bead close.
