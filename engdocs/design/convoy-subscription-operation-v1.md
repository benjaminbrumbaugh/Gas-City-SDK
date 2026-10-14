# Convoy subscription operation contract v1

Status: published by `internal/convoysubscription` as
`convoy-subscription.v1`.

This is the early provider-neutral operation contract for a session's convoy
callback registration. It composes the durable owner- and generation-fenced
subscription service; it does not add a transport, authentication provider,
external registry, or root command/API route.

## Operations

| Operation | Input owner | Durable effect | Successful result |
| --- | --- | --- | --- |
| `subscribe` | one trusted-local selector | creates an active registration | `subscribed` with canonical owner and fence |
| `list` | exact canonical local session ID | read-only, includes terminal records | `listed` with owner records |
| `renew` | exact canonical local session ID plus current fence | advances generation and replaces lease expiry | `renewed` with the new fence |
| `retire` | exact canonical local session ID plus current fence | unregisters and closes the durable record | `retired` |

The schema version is `convoy-subscription.v1`. A successful result sets
`acknowledged: true` only after the durable read or mutation completes. Every
registration carries:

- an immutable owner identity;
- an opaque `conversation_ref`;
- an authorized convoy scope and lifecycle interests;
- a positive `registration_id`/`generation` fence;
- a lease expiry; and
- an explicit `default` designation when the configured external coordinator
  should treat this route as its fallback.

`default` is designation data, not a Gas City role. The external bridge owns
the configured coordinator route, compare-and-replace rule, renewal
semantics, and final designation acknowledgement. This package owns no
parallel designation registry.

## Trusted-local owner boundary

`subscribe` accepts `session_selector` as launch context. The platform
resolves it through the current city session store to exactly one open,
canonical session bead ID. Missing, ambiguous, foreign, non-session, or closed
identifiers are refused. The canonical ID is returned in the result and is the
only owner form accepted by subsequent `list`, `renew`, and `retire` calls;
replays must not re-resolve a recycled alias.

This resolution is trusted-local routing context, not authentication. A
same-user process can imitate ambient context. Existing city-write admission,
configured adapter binding, and any authenticated API boundary remain the
authority for untrusted callers. This contract does not create an
unauthenticated self-registration endpoint or promise hostile same-user
isolation.

External consumers use `owner.kind: "external_binding"` with an existing
configured binding reference. Shape validation does not prove that binding is
authorized; the existing adapter/controller boundary must do that before use.
External sessions do not need fabricated city session beads.

## Scope, fencing, and late registration

The operation service delegates owner checks, generation checks, and
compare-and-swap writes to the existing `SubscriptionService`. A route or
conversation reference is opaque data and never an authorization principal.
Cross-owner and stale-fence mutations fail without changing the record.

For convoy-scoped subscription, admission requires an authoritative
terminal-state lookup. If the convoy is already terminal, the operation
returns `status: "terminal_refused"`, `acknowledged: false`, and a
`refusal` naming the convoy; it also returns the typed terminal-state error to
the caller. If terminal-state authority is unavailable, admission fails closed
without creating a record. This contract therefore makes no indefinite
promise that a late registration will receive a future event.

## Evidence boundary

The package tests prove the contract schema, canonical-session resolution,
closed/ambiguous selector refusal, durable lease/default persistence, owner and
generation fencing, renewal/retirement, external-owner shape separation, and
explicit terminal refusal. They observe the in-process SDK/store layer. They
do not prove HTTP/API root registration, bridge transport delivery, external
conversation receipt, live coordinator designation, or deployment. Root/API
and generated-schema adoption remains the serial integration lane.
