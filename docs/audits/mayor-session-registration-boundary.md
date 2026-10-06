---
title: Mayor Session Registration Boundary Audit
description: Source and provider-free test evidence for caller, subscription, and convoy callback authority.
---

## Status

This is a bounded source proof for `sdk-h0s.8`, not a registration
implementation or feature-acceptance certificate. The durable subscription and
callback-admission boundaries are owner- and fence-safe once they receive
trusted authorization evidence. The SDK currently has no proven public seam
that binds a self-service caller to the `Owner` value used by subscription
mutations. The dependent self-registration consumer must remain held until that
missing surface is provided or an existing authoritative equivalent is accepted.

## Provenance and scope

- Canonical plan: Hermes-Extensions `origin/main` at PR68 merge
  `0be40f37dc204c1ca56f2ea3cc7c059e8df2ef11`,
  `docs/plans/mayor-callback-completion.md`.
- SDK proof base: `faefda46ea6ab3563cc3f9ad9ee3b02e1824c54d`.
- Task branch: `proof/mayor-session-authority`.
- Evidence tree: `/Users/benjaminbrumbaugh/Documents/Gas City/Gas-City-SDK/temp/mayor-session-authority-proof`.
- New test: `internal/convoyfanout/callback_registration_boundary_test.go`.

The work observes source-layer authorization and admission with an in-memory
bead store. It does not perform live registration, provider calls, external
conversation discovery, deployment, service restart, model execution, or
runtime/profile inspection.

## Target truth and evidence layers

| Target truth | Evidence observed | What it proves | What it does not prove |
| --- | --- | --- | --- |
| A subscription mutation is owner- and generation-fenced. | `convoy.SubscriptionService.Get`, `List`, `Renew`, `Update`, `Revoke`, and `Unregister` in `internal/convoy/callback_subscriptions.go`; existing package tests plus the new boundary test. | Exact owner equality, required registration fence, stale-generation rejection, and conditional-write behavior. | That the caller-supplied owner came from an authenticated session. |
| Only an in-scope subscription enters callback admission. | `convoyfanout.SubscriptionViews` and `SelectRecipients` in `internal/convoyfanout/`; the new test crosses durable subscription state into pure admission. | Active state, registration fence, work scope, lifecycle interest, deterministic logical recipient, and rejection reasons. | External transport delivery, receipt, or current authorization at a later submission attempt. |
| Route data cannot become authority by itself. | `launchorigin.Capture`/`Normalize` in `internal/launchorigin/`; `LaunchOriginAuthorization.Trusted` and route handling in `internal/convoyfanout/fanout.go`; existing and new provider-free tests. | Ambient route values are normalized opaque data, and untrusted launch-origin evidence is refused. | Authenticity of `GC_ALIAS`, `GC_AGENT`, `BEADS_ACTOR`, `GC_SESSION_NAME`, or any route string. |
| A city mutation can be request-authenticated. | `citywriteauth.Grant`, `Expect`, and `Verifier.Verify` in `internal/citywriteauth/citywriteauth.go`; the existing grant E2E tests. | Signature, audience, city/tenant, exact request digest, time window, epoch, and replay checks. | A per-session principal, subscription owner, or caller-session relationship. |
| External delivery uses the configured target boundary. | `convoyfanout.AdmissionService.Admit` and `internal/externalcoordination/transport_adapter.go`. | Admission persists a queued record with a target fence before transport; the adapter addresses the configured conversation. | That queued state was delivered, that a provider acknowledged it, or that a route identity authorized the target. |

These layers are intentionally separate. A passing semantic admission test is
not a screen capture or live receipt, and a valid city-write grant is not a
session identity.

## Existing boundary

### Durable subscription authority

`convoy.SubscriptionService` is the strongest existing self-registration
boundary found in the SDK:

- `CreateSubscriptionInput.Owner` is required and is persisted in the durable
  record with registration ID, generation, work scope, opaque route identity,
  and lifecycle interests.
- `Get` and `List` compare the supplied owner with the stored owner. A route
  identity is explicitly not an alternate credential.
- `Renew`, `Update`, `Revoke`, and `Unregister` require the supplied owner plus
  the current `RegistrationFence`. A different owner returns
  `convoy.ErrUnauthorized`; a missing fence returns `convoy.ErrFenceRequired`;
  a different registration ID or generation returns `convoy.ErrStaleFence`.
- Fenced writes use the store's conditional writer. A store that cannot provide
  the required compare-and-swap behavior fails closed with
  `convoy.ErrFencingUnavailable`.

This is durable authorization enforcement after an owner has been selected. It
is not a caller-authentication boundary because the owner is a string argument
to the service methods, not an identity derived from `context.Context`, an
authenticated request, or a trusted session handle.

### Callback admission and notification

`convoyfanout.SubscriptionViews` projects durable records into the evidence
consumed by `SelectRecipients`. Admission derives a logical recipient from the
stored owner, checks active state, registration fence, work scope, and lifecycle
interest, and carries route identity as opaque data. It refuses an untrusted
launch origin and does not infer trust from `Event.LaunchOrigin`.

`AdmissionService.Admit` persists one `StateQueued` delivery record per
authorized logical recipient before any transport side effect. Its record is an
authorization snapshot, not a delivery receipt. `TargetFence` identifies the
configured external coordination target; route identity never selects that
target. Native subscription admission and external transport submission are
therefore different boundaries.

### Neighboring authority surfaces

The existing surfaces checked during this proof do not fill the caller-session
gap:

- `launchorigin.Capture` reads ambient actor-route keys in precedence order and
  guarantees only that a non-empty value is valid opaque callback data. Its
  package documentation explicitly leaves trust to the caller.
- `citywriteauth.Verifier` authenticates a signed, single-use, request-bound
  city mutation. `Grant` has city/tenant and request claims but no session-owner
  claim. CSRF/read-only and city-write admission authorize the request boundary,
  not a particular callback subscription principal.
- `internal/session.ResolveSessionID` and
  `internal/api.resolveSessionTargetIDWithContext` resolve a caller-provided
  selector to a session target. Resolution is addressing, not proof that the
  caller owns the selected session.
- `internal/extmsg.authorizeMutation` authorizes controller calls or adapter
  calls whose provider/account matches a conversation. `extmsg.Caller` has no
  session-caller kind, and the controller path is a trusted infrastructure
  caller rather than self-service session proof.
- `externalcoordination.TransportAdapter` publishes to a configured provider,
  account, and conversation. It owns transport delivery semantics, not convoy
  subscription ownership.

## Minimal operation/result shape

The existing source supports this bounded, non-public proof contract without
adding fields or abstractions:

| Operation | Existing input authority | Existing result/evidence |
| --- | --- | --- |
| Create | `Owner`, registration ID/generation, work scope, route identity, interests | `convoy.SubscriptionRecord` in `SubscriptionActive` state |
| Inspect/list | Exact owner string | Owner-filtered `SubscriptionRecord` values |
| Renew/update/revoke/unregister | Exact owner plus current `convoy.RegistrationFence` | Next generation or explicit terminal state; stale/foreign calls fail closed |
| Admit notification | Durable `SubscriptionRecord` projection plus event/city scope | `convoyfanout.RecipientSnapshot` and auditable rejection reasons |
| Queue delivery | Authorized recipient snapshot plus configured `TargetFence` | `StateQueued` delivery record; no delivery claim |

The missing consumed value is an immutable, trusted caller/session identity that
the self-service entry point can bind to `Owner`. The operation must not accept
an arbitrary `--owner`, `--source-agent`, session ID, route string, or ambient
environment variable as that proof. The result must preserve the owner,
convoy scope, current registration fence, and exact notification correlation;
route identity remains opaque.

## Provider-free probes

`internal/convoyfanout/callback_registration_boundary_test.go` exercises the
real `SubscriptionService` → `SubscriptionViews` → `SelectRecipients` path with
`beads.NewMemStore()`:

- exact `session-a` owner lookup succeeds;
- `session-b` and `opaque-route-a` cannot read the registration;
- a comma-joined owner is rejected as ambiguous;
- the current owner and registration fence renew the record;
- the prior generation is rejected as `convoy.ErrStaleFence`;
- an in-scope `convoy.closed` event admits only
  `principal:session-a`, preserves opaque route data, and refuses ambient
  launch-origin evidence;
- a cross-convoy event yields no recipient and includes
  `RejectWorkScopeMismatch`.

The existing neighboring tests independently cover malformed durable records,
foreign-owner list isolation, conditional-write failure, terminal-state fences,
route/principal separation, target-fence replay, and queued-versus-delivered
state. Together they prove the enforcement and admission layers, not the
missing caller identity binding.

## Trust limits and blocker

The strongest honest trust model is trusted-local, same-user operation: a
trusted infrastructure caller may resolve the current session and pass its
durable owner to the subscription service. This proof does not claim isolation
from a hostile process running as the same OS user. It also does not treat
ambient `GC_ALIAS`, `GC_AGENT`, `BEADS_ACTOR`, `GC_SESSION_NAME`, or caller
route text as authentication.

No current SDK API/runtime seam inspected here proves that a self-service caller
is the owner it supplies to `SubscriptionService`. Consequently, the precise
blocker is:

> The self-registration entry point needs an existing authoritative
> caller-session binding (or an explicitly accepted equivalent) that derives the
> immutable owner identity and convoy scope before invoking subscription
> mutation. The current service and fan-out contracts enforce the supplied
> values but cannot establish them.

Mayor `gc-0s4t4` owns acceptance of this proof and the next action. Until that
boundary is accepted, the `session-registration` consumer remains held. No
authentication provider, consent state machine, callback service, or production
registration API is introduced by this task.

## Verification record

The focused test passes with the repository's no-CGO test mode:

```text
CGO_ENABLED=0 go test ./internal/convoyfanout
ok   github.com/gastownhall/gascity/internal/convoyfanout  0.434s
```

The default focused command was also attempted. It is blocked by the host's
missing C++ ICU header (`unicode/regex.h`) while compiling
`github.com/dolthub/go-icu-regex`; no dependency installation or environment
mutation was authorized. The no-CGO result observes the Go subscription/fan-out
layer only and does not upgrade this artifact to live or provider acceptance.

The documentation gate also passes:

```text
CGO_ENABLED=0 make check-docs
ok   github.com/gastownhall/gascity/test/docsync  14.347s
```

The broader `CGO_ENABLED=0 make test-fast-parallel` attempt was interrupted
after its core package job remained idle for approximately 38 minutes on the
shared host. It produced no failure output and is not counted as passing
evidence; the focused package, neighboring package sweep, vet, and docs gate
are the bounded checks for this source-only task.
