# Convoy lifecycle callback fan-out v1

Status: implemented at `internal/convoyfanout`.

This is the Gas City-owned admission boundary for lifecycle callbacks. It turns
one validated `convoycallback.Event` into one durable delivery record per
authorized logical recipient, before any transport side effect. It applies the
delivery policy in `engdocs/plans/convoy-callback-delivery-policy-v1.md`; it is
not a transport, and it is not a delivery.

## What admission decides

`SelectRecipients` is the pure decision and returns both the authorized set and
an auditable reason code for every candidate it refused.

| Candidate | Admitted when | Kind |
| --- | --- | --- |
| Launching actor | The caller marks the captured origin `Trusted` and names a principal, and the origin's scope and lifecycle interests cover the event | `launch_origin` |
| Subscription | The durable record is active, carries a registration fence with a non-zero generation, and its scope and interests cover the event | `subscription` |
| Configured default | `DefaultRecipientPolicy` calls for it and an External Coordination target is configured at admission time | `configured_default` |

Trust is a boolean the caller supplies, never an inference from an origin
string. A scope authorizes only what it declares: every non-empty dimension
(`city`, `convoy_id`, `work_refs`) must match the admitting city and the event,
and a scope with no dimension authorizes nothing.

Rejection reasons are `launch_origin_untrusted`, `subscription_not_active`,
`subscription_unfenced`, `work_scope_mismatch`,
`lifecycle_interest_mismatch`, `duplicate_logical_recipient`, and
`default_target_unconfigured`. When nothing is authorized, the admission is
returned together with `ErrNoAuthorizedRecipient` so an empty fan-out cannot
read as a successful one.

## Recipient identity and deduplication

A recipient is a logical authorization principal, so identities are namespaced:
an explicit recipient is `principal:<principal>` and the configured default is
`default:<target-id>`. Two candidates deduplicate only when they name the same
logical recipient — the fenced subscription evidence wins over the launching
actor for a shared principal. Route data never deduplicates anything, and the
configured default stays a distinct recipient with its own idempotency key even
when its opaque route data resembles another recipient's.

`DefaultWhenNoExplicitRecipient` (the default) admits the configured target
only when no explicit recipient is authorized; `DefaultAlwaysDistinct` admits it
alongside them. Either way, selecting the configured target happens here, at
admission — it is never failover after a submission has started.

## Identity, correlation, and idempotency

The idempotency key is the SHA-256 digest of a canonical length-prefixed tuple:

```text
"gc-callback/v1", event_id, logical_recipient_id
```

Length-prefixing means no delimiter in an opaque identity can forge a field
boundary. Attempt number, target identity, route data, response time, and
mutable configuration are excluded, so retries and reconciliation reuse one key.
The key is re-derived on every read: a durable record whose key does not derive
from its own recipient is rejected as corrupt.

Correlation is the event's own `correlation_id`, propagated unchanged to every
recipient of that event. The fan-out is therefore correlatable as one unit, and
correlation is deterministic because it is immutable event data rather than
something admission invents.

## Target fence

`TargetFence` pins the sole configured External Coordination target by opaque
target identity plus configuration revision. Admission canonicalizes the target
identity before the fence is persisted, so the same configured target never
reads as a stale fence on surrounding whitespace alone. The fence is persisted
with each record and compared verbatim on every re-admission. A changed or missing fence fails closed with
`ErrStaleTargetFence`; the existing record is never redirected to a target that
was never authorized for that event. A new target may only be used by a new
admission with its own fence.

An immutable event field that disagrees with an existing record for the same
event ID fails with `ErrEventIdentityConflict` rather than overwriting history.

## Durability and convergence

Each admitted recipient becomes one bead labelled
`gc:convoy-callback-delivery`, carrying the record as JSON plus event,
recipient, key, and state metadata. Admission is idempotent per
(event, logical recipient): re-admitting returns the existing records unchanged,
including after a process restart.

Concurrent admissions of the same pair converge on read. The record that sorts
first in the store's canonical `(created_at, id)` order is canonical; every
duplicate is closed and marked `superseded_by`, so a recipient can never be
submitted to twice.

## Ownership and evidence boundary

Admission is not delivery. Fan-out writes `queued` and nothing else, and the
durable admission record carries no submission receipt, no `ReceivedAt`, and no
outcome. Reads deliberately tolerate a state the delivery-outcome boundary has
advanced past `queued`, because that boundary owns submit, reconcile, response,
and terminal outcome semantics and extends these records in place.

Fan-out admits `notification` intent only. An intervention or execution request
needs its own authorization, scope, correlation, and outcome contract, and never
inherits notification authorization from a shared event or route.

Route identity is opaque throughout: it is never authority, never target
selection, and never a URL. URL syntax, redirect rejection, and credential
handling belong to the External Coordination transport boundary; credentials,
callback URLs, and prompt bodies never enter these durable records.

Passing tests here prove recipient admission, key stability, fence behavior,
replay, and convergence. They do not prove that a harness observed a
notification, that a route is truthful, or that any work executed.
