# Convoy callback delivery policy v1

Counter: 0

## Scope and target truth

Define the Gas City-owned policy for mechanically delivering one lifecycle
notification to each authorized recipient of a launched convoy. The policy
must make fan-out durable, replay-safe, and explicit about what was accepted,
what is unknown, and what outcome was recorded. It must preserve the one
configured External Coordination target; a recipient's intended harness or
conversation is opaque `route_identity` data and never becomes a second target
or a callback URL.

The target truth is a policy that later implementation work can apply after a
crash, retry, config change, or duplicate event without sending an unintended
second notification or silently treating transport acceptance as execution.
This document does not implement persistence, subscription CRUD, lifecycle
event production, or a transport adapter.

## Tasks and subtasks

1. Define the recipient set and fan-out snapshot.
   - Include the captured launching actor only when its origin is trustworthy
     and still authorized for the event.
   - Include active, owner- and generation-fenced subscriptions whose scope
     and lifecycle interests match the event.
   - Include the configured External Coordination target as one distinct
     default recipient when delivery-time fallback is eligible.
   - Deduplicate only the same logical recipient; the configured default is a
     distinct recipient even if its opaque route data resembles another route.
2. Define durable delivery identity and replay behavior.
   - Persist an immutable event identity and a recipient identity snapshot.
   - Derive one recipient-specific idempotency key from the event identity and
     recipient identity; reuse it for every retry and reconciliation.
   - Persist timestamps and outcomes instead of deriving them from logs or
     transport responses on replay.
3. Define the delivery state machine and fallback rules.
   - Separate queued, submitted, uncertain, reconciled, responded, failed,
     and outcome-recorded states.
   - Treat notification delivery and intervention/execution requests as
     different intent classes with different authorization.
   - Reconcile every uncertain submission against the same target and key
     before considering any fallback.
4. Define target and transport security fences.
   - Resolve the default target at delivery time, persist its config fence, and
     fail closed on stale target changes before submission.
   - Reject arbitrary callback URLs, unsafe URL forms, credential logging, and
     cross-origin redirects.
5. Add an implementation/test matrix for the owning packages without changing
   existing convoy event payloads in this policy bead.

## Architectural changes

Add a policy-level contract, owned by the callback delivery boundary, with
these conceptual records:

- `DeliveryEvent`: immutable lifecycle event identity, convoy/task identity,
  launch-origin identity, event kind, occurrence time, and correlation data.
- `RecipientSnapshot`: logical recipient identity, authorization snapshot,
  scope, opaque `route_identity`, and whether it is the configured default.
- `DeliveryRecord`: event/recipient key, target fence, recipient-specific
  idempotency key, state, attempt timestamps, persisted `ReceivedAt`, and one
  explicit outcome.

The contract owns policy and validation only. Subscription storage owns
owner/generation authorization. Lifecycle producers own event identity.
External Coordination owns the one configured target and its authenticated
adapter registration. A transport implementation owns how an opaque route is
used after authorization; it cannot replace the target or add a second
recipient implicitly.

## Test plan and evidence boundaries

The policy tests prove deterministic recipient selection, stable key
derivation, state-transition legality, timestamp persistence rules, target
fences, fallback ordering, and URL/credential/redirect rejection at the policy
boundary. They do not prove that an actual harness receives a notification,
that an opaque route is truthful, or that a recipient performs work.

Required failure edges include duplicate lifecycle events, duplicate
recipients, revoked or stale subscriptions, changed target configuration,
transport timeout after bytes were sent, malformed receipts, definitive
rejection before acceptance, and replay after process restart. A passing
policy test is not evidence of live delivery; one protocol-composition test
must later cover the real adapter boundary.

## Support structures

Use a table-driven state transition matrix and a single idempotency-key
derivation function. Keep target resolution, subscription authorization, and
transport reconciliation as separate seams. Use injected time and deterministic
event/recipient fixtures. Do not introduce a generic callback interface or a
provider-specific route parser until there are two real implementations.

## Documentation

Document the following stable rules next to the implementation:

- `route_identity` is opaque data, not authority, target selection, or a URL.
- Notification means “deliver lifecycle information”; it never authorizes an
  intervention or execution request.
- `submitted` means accepted by the delivery boundary, not completed work.
- `uncertain` must be reconciled before retrying or falling back.
- `ReceivedAt` is the persisted server observation time of a valid response and
  is reused on exact replay.
- Credentials, callback URLs, prompt bodies, and redirect locations do not
  enter durable policy records or ordinary logs.

## Execution order

Write the policy contract tests/matrix first if implementation is needed →
observe the intended red cases → implement the smallest policy seam → test
restart and uncertain-delivery paths → review target and security boundaries →
run focused tests and vet → update downstream implementation beads with the
exact state and fence vocabulary.

## Stability and blocker avoidance

Do not touch the already-dirty unrelated files in this shared worktree. Do not
restore the historical `internal/hca` or external-coordination implementation
as a shortcut. Do not add provider, role, harness, conversation, credential,
or callback-URL fields to the durable event contract. Do not use a transport
HTTP success code as proof of execution. Do not retry an uncertain submission
against another route or target before reconciliation.

## Candidate parallel work

Subscription CRUD can implement owner/generation fences against this policy.
Lifecycle work can produce immutable event identities and launch-origin
snapshots. Fan-out work can materialize one delivery record per authorized
recipient. Route-outcome work can implement submit/reconcile/response
semantics. A separate security review can inspect URL, redirect, credential,
and route-identity handling without changing the policy vocabulary.

## Pass 1 — top-to-bottom critique

### Scope and target truth

The scope correctly separates policy from implementation, but “default
recipient” and “delivery-time fallback” could be read as permission to fail
over from one route to another. The policy needs an explicit distinction:
default resolution is an admission-time choice made before submission; it is
not failover after a submission becomes uncertain. The target truth also needs
to name the immutable event/recipient pair as the unit of exactly-once policy
convergence (with at-least-once transport as the honest external guarantee).

### Tasks and subtasks

The task list includes the required concepts but does not yet pin all state
transitions or terminal outcomes. In particular, `reconciled` is an observed
fact about an uncertain attempt, while `responded` is a recipient response;
neither alone means the complete lifecycle outcome has been persisted. The
plan should define a transition table and make all fallback eligibility
conditions explicit. It should also say that an intervention request cannot be
created by a notification delivery or by a callback response alone.

### Architectural changes

The three-record split is useful, but `RecipientSnapshot` must not be treated
as proof of current authorization. It is an audit snapshot; authorization is
rechecked at admission and again before submission. `TargetFence` should be a
first-class record containing the configured target identity and revision, not
an incidental field. The contract should require copy-on-write handling for
opaque route data so retries cannot mutate the original event.

### Test plan and evidence boundaries

The policy tests cannot prove URL behavior if URL handling lives in the
transport adapter. Keep policy tests for “no URL field / no route-derived
target” and put URL syntax, redirect, and credential-header behavior in the
transport security suite. Add tests proving exact replay reuses both the
recipient idempotency key and the first valid `ReceivedAt`, while a conflicting
response is rejected rather than overwriting history.

### Support structures

“Single idempotency-key derivation function” is correct but underspecified.
The formula must include a version marker, immutable event ID, and logical
recipient ID, and must not include mutable attempt number, current time, target
URL, or route contents. State and outcome should be typed values rather than
free-form strings. Injected time must be used for every persisted timestamp.

### Documentation

The documentation list covers the core security claims but should distinguish
logs from durable records and state that error summaries are sanitized. It
should also document that an absent or stale default target produces a durable
failed outcome, not an arbitrary callback attempt.

### Execution order, stability, and parallel work

The execution order is appropriate. It should explicitly require an
uncertain-submission RED test before implementing fallback, because a green
happy-path fan-out test can hide the most dangerous duplicate-delivery bug.
The stability rules should preserve existing HCA compatibility only through an
adapter owned by its existing boundary, rather than by importing old names
into the new callback policy. Parallel work must share the state-transition
table as the source of truth to prevent sibling implementations from inventing
incompatible meanings.

## Pass 1 — critical evaluation of the critique

The critique improves precision without expanding this bead into a transport
implementation. The most important correction is to define “fallback” as
pre-submission default admission and to prohibit post-uncertainty failover.
Making `TargetFence` first-class is necessary because the configured target is
the authorization boundary, while `route_identity` is only opaque payload.

The critique’s proposal to reject a conflicting response is sound, but a
response that repeats the exact idempotency key must be accepted only when its
immutable correlation and outcome bytes match the persisted record. A replay
with a new `ReceivedAt` is not an exact replay and must not move the timestamp.

The policy should not require every consumer to duplicate URL checks. The
transport boundary owns URL parsing and redirect behavior; the policy boundary
owns the stronger invariant that no arbitrary URL can be selected by a route.
This preserves layering while still making the security obligation testable.

## Roll-up: applied evaluation

The next revision will add:

- a precise pre-submission-only default fallback rule;
- an explicit event/recipient convergence unit and at-least-once transport
  guarantee;
- a typed state/outcome matrix separating `reconciled`, `responded`, and
  `outcome_recorded`;
- admission-time and submission-time authorization/target-fence checks;
- a versioned key formula over immutable event and recipient identities only;
- persisted first-valid `ReceivedAt` replay behavior and conflict rejection;
- transport-owned URL/redirect tests paired with policy-owned route/target
  tests; and
- sanitized logging and stale-target failure behavior.

## Pass 1 — roll-up: revised plan, tasks, and subtasks

1. Define an immutable `DeliveryEvent` and `RecipientSnapshot`, and make the
   event/recipient pair the convergence unit. State explicitly that transport
   is at-least-once even when policy records converge exactly once.
2. Define `TargetFence` as the configured target identity plus revision. Resolve
   a missing default recipient before the first attempt, recheck authorization
   and the fence before each submission, and never use `route_identity` to
   select a target.
3. Define typed delivery states/outcomes and the allowed transitions, including
   uncertain submission and reconciliation before any retry or fallback.
4. Define the versioned idempotency formula and immutable timestamp/replay
   rules, including first-valid `ReceivedAt` preservation.
5. Assign URL parsing, redirect rejection, and credential handling to the
   transport security boundary; retain policy tests for the no-arbitrary-route
   and no-secret-persistence invariants.

## Pass 1 — explicit no-change decisions

- No implementation of persistence, subscriptions, fan-out, lifecycle events,
  or transport belongs in this design bead.
- No callback URL, credential, provider-specific target, role name, or private
  runtime identifier is added to the lifecycle contract.
- No fallback is attempted after an uncertain submission until reconciliation
  has a definitive result.
- No existing convoy event payload or legacy compatibility path is rewritten
  here; compatibility remains an explicit adapter concern.

Counter: 1

## Pass 2 — revised full plan, tasks, and subtasks

### Plan

The delivery policy will be a provider-neutral, durable state machine around
an immutable lifecycle event and a snapshot of each authorized recipient. It
will guarantee one policy record and one replay-stable key per event/recipient
pair, while stating honestly that an external transport can observe a request
more than once. The configured External Coordination target is the only
transport target; default selection happens at delivery admission, and
`route_identity` remains opaque route data.

### Tasks and subtasks

1. Establish the fan-out admission set.
   - Validate the launching actor's captured origin and subscription scope.
   - Select active subscriptions using owner/generation fences and immutable
     event-kind/work-scope matching.
   - Add the configured default as a distinct logical recipient only when no
     equivalent default record already exists and the target is configured at
     admission time.
   - Persist the admission snapshot before any network side effect.
2. Establish delivery identity and state transitions.
   - Persist event ID, recipient ID, authorization snapshot, target fence, and
     opaque route data by value.
   - Derive `gc-callback/v1/<event-id>/<recipient-id>` conceptually (or its
     canonical digest representation) without mutable attempt data.
   - Permit only table-defined transitions through `outcome_recorded` or
     `failed` terminal states.
3. Establish retry, reconciliation, and fallback.
   - Preserve the same key and attempt identity for retries of one delivery.
   - Mark a lost or malformed receipt `uncertain`, then reconcile the same
     target/key before any retry or default fallback.
   - Allow a fallback only after a definitive pre-submission rejection or
     definitive reconciliation result; never after an unresolved uncertainty.
4. Establish response and timestamp semantics.
   - Require correlation and recipient identity on responses.
   - Persist the first valid `ReceivedAt`; exact replays return it unchanged,
     while conflicting response bytes are rejected.
   - Keep notification delivery separate from intervention/execution intent.
5. Establish security and evidence ownership.
   - Keep target URL selection in the configured adapter boundary.
   - Reject unsafe URL forms and cross-origin redirects there, and keep
     credentials out of durable records and logs.
   - Add tests at the smallest owner and retain one real protocol composition
     test for eventual delivery wiring.

### Architectural changes

The policy boundary gains a typed transition/outcome vocabulary and a
first-class target fence. It does not gain a transport abstraction merely to
name an HTTP callback. Existing subscription and External Coordination
boundaries supply authorization and transport capabilities; fan-out composes
them through the policy record.

### Test plan and evidence boundaries

Add table-driven tests for recipient admission, distinct default delivery,
stable keys across attempts, target-fence races, uncertainty/reconciliation,
response replay, timestamp reuse, notification/intervention separation, and
sanitized diagnostics. These tests prove policy decisions only. Transport
tests prove URL and redirect rejection; one integration test proves the
configured adapter receives the typed request.

### Support structures, documentation, and execution order

Use injected clock and key fixtures, a single transition table, and a
redaction-aware diagnostic helper. Write RED tests for uncertainty before
implementing fallback, then implement the pure policy transition functions,
wire durable records, run focused tests/vet, and update dependent beads with
the exact vocabulary. Document the at-least-once transport guarantee and the
fact that `submitted` is not execution completion.

### Stability and blocker avoidance

Keep this design independent of the dirty worktree and historical HCA package.
Do not infer authorization from route possession, do not let target changes
silently redirect queued records, and do not recover an uncertain request by
creating a fresh key.

### Candidate parallel work

Subscription implementation owns authorization snapshots; lifecycle
implementation owns immutable events; fan-out owns admission records; route
outcome implementation owns submit/reconcile/response; transport security owns
URL and redirect checks. They must consume this transition table unchanged.

## Pass 2 — top-to-bottom critique

### Plan and tasks

The revised plan is more actionable, but the default-recipient sentence is
still ambiguous: “when no equivalent default record already exists” could
collapse an explicit route with the default and violate the requirement that
the default is a distinct recipient. The plan should define recipient identity
as logical identity, not route equality, and say exactly when multiple records
are expected.

The key example is readable but can expose delimiters or untrusted IDs if
implemented literally. It should specify canonical encoding and preferably a
fixed-length digest. The state transition task also needs a separate outcome
field so `failed` is not confused with a failure reason or with a transport
receipt.

### Architectural changes

The boundary assignment is sound, but target fence contents remain abstract.
The implementation needs a comparison rule covering target identity and
configuration revision, plus an explicit result for a stale fence. It should
also say whether authorization is checked before fan-out, before submission,
or both; a snapshot alone is not enough after revocation.

### Test plan and evidence boundaries

The tests cover the right risks but need a clear negative claim for fallback:
a test that sees a timeout and then a successful default callback would be a
false pass unless it proves reconciliation occurred first. The integration
test must assert the configured target and opaque route separately, not merely
that some HTTP server received bytes.

### Support structures, documentation, execution order

The proposed redaction helper is potentially too broad for a policy package;
logging ownership belongs to the layer that emits logs. Keep only a diagnostic
field allowlist in the policy contract. The execution order should include
restart replay and concurrent duplicate fan-out as explicit gates.

### Stability and parallel work

The stability rules correctly avoid old code. The parallel-work section needs a
compatibility handoff artifact so consumers cannot independently reinterpret
`reconciled` or `outcome_recorded`.

## Pass 2 — critical evaluation of the critique

The critique identifies real ambiguity rather than asking for speculative
abstraction. A recipient is a logical authorization principal, so route data
must never deduplicate it. A digest key is safer than a readable concatenation
because it has bounded length and cannot carry delimiters, while the durable
record can retain the component IDs separately for audit.

The stale target result should be terminal for that delivery admission, not an
automatic redirect to a newly configured target. A new target may be used only
by a new delivery admission with a new target fence; this avoids sending an
old lifecycle event to a target that was never authorized for it. Authorization
must be checked at both snapshot and submission boundaries.

The logging change belongs in implementation guidance, not a new policy
helper. The contract can require an allowlist of non-secret identifiers and
leave formatting to existing logging boundaries. Restart and concurrent
duplicate tests are necessary because durability, not in-memory behavior, is
the target truth.

## Pass 2 — roll-up: applied evaluation

Revise the final policy to make the following normative:

- recipient identity is logical and distinct from route equality;
- the idempotency key is a versioned digest over canonical event and recipient
  IDs only;
- `TargetFence` compares configured target identity and revision, with stale
  fences failing closed and never redirecting an existing record;
- authorization is checked during admission and immediately before submit;
- `reconciled` is a durable observation that must resolve to an accepted or
  rejected result before fallback, and `outcome_recorded` is the sole normal
  terminal success state;
- tests must prove reconciliation ordering, restart replay, and concurrent
  duplicate convergence; and
- diagnostics have an allowlist, with URL/redirect behavior remaining in the
  transport security owner.

## Pass 2 — explicit no-change decisions

- Do not deduplicate recipients by provider, conversation, URL, or opaque route
  data; only the same logical recipient ID may deduplicate.
- Do not treat a changed config target as a safe migration of an old queued
  delivery.
- Do not add a policy-owned logging implementation or a new transport
  interface.
- Do not allow an intervention/execution intent to inherit notification
  authorization or fallback behavior.

Counter: 2

## Pass 3 — final full plan, tasks, and subtasks

### Normative policy shape

1. Persist one immutable lifecycle event and one delivery record for each
   logical event/recipient pair before side effects.
2. Build recipients from the trusted launch origin and authorized active
   subscriptions. If no explicit recipient is authorized, resolve the one
   configured External Coordination target at delivery admission as a distinct
   default recipient. Route equality never merges logical recipients.
3. Persist a target fence and route snapshot. The fence is checked at
   admission and immediately before submission; the route is copied and never
   interpreted as a target, URL, or authority.
4. Derive a versioned, fixed-length idempotency key from canonical immutable
   event and logical-recipient IDs. Keep it unchanged across retries and
   reconciliation.
5. Use the typed state machine below, with injected server time for all
   timestamps and immutable first-valid `ReceivedAt`.
6. Treat notification and intervention/execution as separate intents. Only
   explicit, separately authorized intervention requests may request an action;
   lifecycle notification fan-out never implies execution.

### Tasks and subtasks

1. Make the state/outcome matrix executable at the policy owner.
2. Make default target resolution and stale-target fencing explicit at the
   External Coordination boundary.
3. Make callback/response correlation and replay timestamp behavior durable.
4. Make URL, redirect, credential, and route-data security checks testable at
   their respective owners.
5. Preserve the evidence claims and no-change decisions below in downstream
   implementation and acceptance beads.

### Architectural changes, test plan, support, documentation, execution,
stability, and parallel work

The architecture remains a pure policy seam composed with existing lifecycle,
subscription, External Coordination, and transport boundaries. Tests use fake
time and deterministic IDs; real transport coverage remains one composition
proof. The transition table is the shared support artifact. Documentation must
state at-least-once transport, first-valid timestamp reuse, stale-fence failure,
and the absence of arbitrary URL selection. Execution starts with the
uncertain-receipt RED test and ends with focused tests/vet and a boundary audit.
Parallel implementers consume the same state/outcome vocabulary; no sibling
may add a provider-specific interpretation.

## Pass 3 — top-to-bottom critique

### Normative policy shape

The final plan now answers the requested behavior, but “trusted launch origin”
and “authorized active subscription” should name the exact checks without
making this bead own subscription implementation. The policy should require a
boolean admission decision plus a reason code, not infer trust from a string.

The key formula must define canonical encoding to avoid ambiguity. The state
machine needs a concrete table, and `ReceivedAt` must be tied to a valid
correlated response rather than an HTTP receipt. The target fence should say
that a stale target is a terminal failed outcome for the existing record.

### Tasks, architecture, and evidence

The tasks are complete but terse. The final artifact should include an explicit
table of allowed transitions and a compact security matrix mapping each threat
to its owning layer. Evidence should distinguish policy convergence from
actual recipient observation and should name the tempting false-completion
substitution: “HTTP accepted” or “queue admitted” is not “recipient responded.”

### Stability and parallel work

The stability strategy is correct. Add a lost-information check after the
third pass to verify that launch-origin capture, recipient authorization,
configured-target preservation, route opacity, idempotency, outcome states,
timestamps, and security rejection all remain represented after refinement.

## Pass 3 — critical evaluation of the critique

The critique asks for observable policy decisions, not hidden heuristics. A
reason code on admission/rejection improves auditability without encoding any
role judgment. A canonical length-prefixed encoding followed by SHA-256 makes
the key independent of delimiters, mutable fields, or route contents.

The final table should permit a notification to record a terminal delivered
outcome after a trustworthy submission receipt, while an intervention must
remain `submitted` until a correlated response is durably recorded. This keeps
notification useful without falsely requiring an execution result. An
uncertain notification still requires reconciliation because the recipient may
have observed it. A stale target cannot be repaired by redirecting the old
record; only a fresh admission may use the new fence.

The security matrix belongs in this design as ownership guidance, not as a
second implementation. This preserves the no-premature-abstraction rule and
keeps the policy package free of URL/network dependencies.

## Pass 3 — roll-up: final applied plan

### State and outcome matrix

| State | Meaning | Allowed next state | Required persisted data |
| --- | --- | --- | --- |
| `queued` | Authorized delivery admitted; no submission started | `submitted`, `uncertain`, `failed` | event ID, logical recipient ID, target fence, route snapshot, idempotency key |
| `submitted` | Configured target gave a trustworthy acceptance receipt | `responded`, `outcome_recorded`, `uncertain` | attempt identity, submitted time, receipt identity |
| `uncertain` | Submission may have been observed but no trustworthy receipt exists | `reconciled` only | uncertainty time, same attempt/key, sanitized failure class |
| `reconciled` | The same target/key was checked after uncertainty | `submitted`, `failed`, or `uncertain` | reconciliation time and definitive/unknown result |
| `responded` | A correlated, authorized recipient response was validated | `outcome_recorded` or `failed` | response identity, first valid `ReceivedAt`, response commitment |
| `failed` | Definitive rejection, expiry, revocation, or stale-fence failure | terminal; exact replay only | typed failure outcome and time |
| `outcome_recorded` | Final accepted notification or validated response is durable | terminal; exact replay only | typed outcome, immutable timestamps, first valid `ReceivedAt` when applicable |

`uncertain` never transitions directly to another recipient or target.
`reconciled` with an unknown result remains `uncertain`; fallback is eligible
only after definitive non-acceptance and only when no submission to that
fallback's logical recipient has begun. A `submitted` notification may record
`notification_delivered` as `outcome_recorded`; an intervention cannot do so
without its separate correlated response.

### Identity, key, and timestamp rules

The durable record stores the event ID, logical recipient ID, target fence,
route snapshot, and admission authorization reason. The idempotency key is the
SHA-256 digest of a canonical length-prefixed tuple:

```text
gc-callback/v1, event_id, logical_recipient_id
```

The version marker and tuple fields are encoded as lengths plus bytes before
hashing. Attempt number, target URL, route data, response time, and mutable
configuration are excluded. `ReceivedAt` is assigned by Gas City when the
first valid, correlated response is accepted; exact replays return the stored
value, and a different response commitment for the same key is rejected.

### Target-fence and fallback rules

At delivery admission, resolve the sole configured External Coordination target
and persist an opaque target identity plus configuration revision as the
`TargetFence`. Before every submission, require the current configured target
to match that fence and require the recipient authorization to remain valid.
A missing, changed, or stale fence records `failed/stale_target` without
redirecting the old record to a newly configured target.

“Delivery-time default fallback” means selecting the configured target at this
admission boundary when no authorized explicit recipient exists. It is not
failover to an arbitrary route and it is never a response to unresolved
uncertainty. A definitive pre-submission rejection may admit a distinct
default-recipient record with its own key only if policy explicitly allows the
default; a timeout or ambiguous receipt must reconcile first.

### Notification versus intervention

Lifecycle events are `notification` intent. They report convoy state and carry
opaque route data; they do not authorize a recipient to mutate work or spawn
execution. `intervention`/`execution` is a separate request intent requiring
its own authorization, scope, correlation, and outcome contract. It does not
inherit notification fallback or authorization merely because it shares an
event or route.

### Security ownership matrix

| Risk | Owning boundary | Required behavior |
| --- | --- | --- |
| Arbitrary callback URL | External Coordination/transport registration | No URL comes from `route_identity`; accept only validated HTTPS endpoints, with literal loopback HTTP reserved for local operation |
| Unsafe URL form | Transport registration | Reject non-HTTP(S), empty host, userinfo, query, fragment, and malformed forms |
| Cross-origin redirect | HTTP transport | Do not follow redirects; reject every 3xx response, including cross-origin redirects |
| Credential persistence/logging | Adapter/auth and diagnostics | Keep credentials in memory, never in durable records, request bodies, response bodies, or ordinary logs |
| Route possession as authority | Subscription/fan-out policy | Require owner/generation/scope authorization; route data is opaque and non-authoritative |
| Duplicate or lost submission | Delivery policy/transport | Stable per-recipient key; uncertain requires same-target reconciliation before retry/fallback |

### Evidence and acceptance gates

Policy tests prove only recipient admission, key stability, transition legality,
fence behavior, replay timestamps, and security ownership invariants. They do
not prove actual harness observation, truthful route interpretation, or work
execution. The real protocol test must assert both configured-target
preservation and opaque route forwarding. The tempting false-completion
substitutions are queue admission for delivery, HTTP acceptance for execution,
and a clean semantic preview for a real screen/recipient observation.

## Pass 3 — explicit no-change decisions

- No new role, provider, harness, conversation, or execution primitive.
- No arbitrary callback URL or redirect-following behavior in the policy.
- No target migration, failover, or fresh idempotency key after unresolved
  uncertainty.
- No overwrite of the first valid `ReceivedAt` or accepted outcome on replay.
- No use of route possession as authorization and no persistence of credentials.
- No changes to existing convoy payloads, HCA compatibility code, or unrelated
  dirty worktree files.

## Lost-information check after three passes

The refined plan still carries every requested and discovered obligation:

- one mechanical delivery record per authorized logical recipient;
- recipient-specific replay-stable idempotency;
- persisted first-valid `ReceivedAt` and explicit state/outcome vocabulary;
- delivery-time selection of the sole configured External Coordination target;
- opaque `route_identity` with no target-selection authority;
- notification/intervention separation;
- reconciliation before any fallback after uncertainty; and
- rejection of arbitrary/unsafe URLs, redirects, and credential leakage.

The plan intentionally does not claim live delivery, recipient behavior, or
execution success; those require downstream boundary evidence.

Counter: 3
