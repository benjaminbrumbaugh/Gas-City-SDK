# sdk-h0s.5 plan

Counter: 3

## Scope and evidence discipline

Target truth: authoritative convoy lifecycle transitions must mechanically
deliver supported creation/closure callbacks to the authorized launch-origin
subscriber, exactly once per logical recipient, across restart/replay, while
rejecting unknown or stale origin owner/generation. Missing origin must retain
legacy behavior, and convoy closure must never claim rebuild/deploy readiness.

Required evidence layer: production lifecycle call sites plus the existing
convoycallback/convoyfanout admission and delivery seam, with focused tests
that exercise typed event-to-recipient routing, idempotency, replay, origin
authorization, and compatibility. A production call-site regression is
mandatory; library-only tests are insufficient.

Useful but insufficient proxies: callback-library unit tests, a successful
queue/admission result, a recorded delivery outcome, convoy.notify output,
PR/branch state, or a semantic preview. These observe only a lower seam or
control plane, not production lifecycle wiring and not receipt by the
configured recipient.

Tempting false completion: treating callback admission or an accepted queue as
delivery, adding readiness/deployment events without an owner, or broadening
the route into arbitrary URLs/authorities. If this plan succeeds but the
original bug remains, the likely gap is a production transition that bypasses
the new seam, a restart/replay race that duplicates or drops a callback, or a
stale/opaque route that is accepted after authorization state changes.

## Pass 1 — initial full plan (counter 0 -> 1)

### Tasks and subtasks

1. Load context and inspect current bytes.
   - Read the assigned bead, coordination bead, repository instructions,
     testing policy, and callback/launch-origin package boundaries.
   - Search history and current production call sites for launch-origin,
     convoy lifecycle, convoycallback, convoyfanout, and related event names.
   - Preserve unrelated work and identify the smallest owned surface.
2. Establish a failing target-layer regression first.
   - Add a production-path test using existing recording collaborators or the
     smallest existing lifecycle harness.
   - Cover explicit supported creation and closure events, subscriber
     admission, recipient-idempotent delivery, missing origin, and stale or
     unknown owner/generation denial.
3. Implement the narrow adapter.
   - Capture/use the existing launch-origin subscription data.
   - Translate authoritative lifecycle transitions into typed callback delivery
     outcomes through the agreed callback/fanout seam.
   - Keep route identity opaque, retain the configured default-target fence,
     and reject unknown/stale registrations before delivery.
   - Do not add general remediation, arbitrary endpoints, deployment logic, or
     readiness claims.
4. Verify behavior and boundaries.
   - Run focused package tests, race tests when applicable, affected tests,
     fast baseline, and vet/pre-commit gates proportionate to the diff.
   - Inspect the diff for role/provider leakage, duplicate delivery, swallowed
     errors, and test expectations changed only for the changed contract.
5. Commit, push, and hand off to the refinery without closing the work bead.

### Architectural changes

Additive production wiring from convoy lifecycle ownership to the existing
authorized callback admission/delivery boundary. No new public protocol,
callback endpoint, role, or readiness model. Any helper remains near the
owning package and uses existing typed destination/registration contracts.

### Test plan and support structures

Use an existing deterministic lifecycle/recording seam. Add only the support
fixture needed to observe production transition -> admission -> delivery
outcome and replay identity. The test proves SDK production wiring and the
callback boundary; it does not prove live external receipt or recipient-side
application acknowledgement.

### Docs, execution order, and stability strategy

No documentation change unless the implementation exposes a changed contract;
if so, update the owning contract source rather than generated projections.
Order: context/history -> RED regression -> GREEN adapter -> focused/race
tests -> affected/full gates -> review -> commit/push/handoff. Avoid live
authorization, deployment, service restart, credentials, or production
requests. Keep changes additive and easy to rebase onto upstream.

### Blocker avoidance and candidate parallel work

Do not mutate held beads, foreign worktrees, or unrelated historical candidates.
Independent read-only history archaeology and package-boundary inspection can
be parallelized, but one writer remains responsible for the task worktree.
If the production owner or agreed delivery seam is absent, record the exact
boundary and escalate rather than inventing a second mechanism.

### Pass 1 critique

The initial plan correctly centers production lifecycle wiring and names the
main false-completion substitutions. It is underspecified about the exact
authoritative transitions, how restart/replay identity is represented, and
where launch-origin ownership/generation is validated. It also risks letting a
large integration harness hide a smaller owning proof and does not explicitly
require a negative readiness assertion for closure.

### Critical evaluation of the Pass 1 critique

The critique is valid: this task's acceptance hinges on exact transition and
authorization semantics, not merely broad callback activity. However, it must
not force speculative abstractions or a live provider test. The plan should
discover existing typed contracts first, then test only the supported event
set and retain a narrow production-path proof. A closure readiness-negative
assertion is necessary because a false readiness claim is a distinct safety
failure, not a variant of callback delivery.

### Pass 1 roll-up and no-change decisions

Refine the plan to identify the existing lifecycle owner and callback seam
before writing tests; require explicit creation/closure event cases, replay
identity, owner/generation authorization, and a closure-without-readiness
assertion. Keep the additive architecture, existing subscription library,
opaque route identity, missing-origin compatibility, no live operations, and
one-writer rule unchanged.

## Pass 2 — revised full plan (counter 1 -> 2)

### Tasks and subtasks

1. Inventory exact contracts and ownership.
   - Locate the authoritative convoy transition functions/types and existing
     launch-origin capture/subscription, convoycallback, and convoyfanout APIs.
   - Confirm the configured default-target authorization fence and the owner /
     generation fields used to invalidate stale subscriptions.
   - Search git history for prior proven implementations before inventing code.
2. RED at the production call site.
   - Trigger supported creation and closure transitions through the real
     lifecycle owner with recording admission/delivery collaborators.
   - Assert recipient-idempotent delivery and stable correlation across
     restart/replay; assert unknown/stale owner/generation rejection.
   - Assert absent launch origin preserves the old no-callback path and
     closure cannot emit rebuild/deploy readiness.
3. GREEN with the smallest owned adapter.
   - Feed only explicitly supported lifecycle events into the existing
     callback/fanout admission and delivery seam.
   - Consume launch-origin registrations without persisting credentials,
     callback URLs, or private provider state.
   - Preserve default-target authorization and opaque route identity; return
     recorded outcomes without claiming external receipt.
4. Evidence review.
   - Run focused tests and races for the owning packages, then the configured
     affected test command or documented fast shard.
   - Run `go vet ./...` and the active pre-commit hook as feasible; report any
     host prerequisite limitation precisely.
   - Audit that tests observe production lifecycle wiring rather than only the
     helper library, and that no expectation was weakened.
5. Handoff.
   - Commit only task-owned files, push `polecat/sdk-h0s.5`, record outcome and
     target metadata, assign to refinery, and drain.

### Architectural changes

One narrow event-to-recipient delivery path at the convoy lifecycle owner,
delegating admission, authorization, idempotency, and fanout to existing
libraries. No readiness/deployment event path is added without evidence from
its actual owner.

### Test plan and support structures

The production-path harness records callback attempts and outcomes and can
replay the same lifecycle event. It proves event selection, destination
authorization, and idempotency at the SDK seam. It does not prove live provider
transport, recipient receipt, or recipient-side application state; those remain
explicitly out of scope.

### Docs, execution order, and stability strategy

Update docs only if a source contract changes; otherwise preserve current docs.
Use deterministic IDs/clocks or existing fake seams, no sleeps or open-coded
polling. Keep errors contextual and typed. Work in the bead worktree only and
do not mutate live coordination state.

### Blocker avoidance and candidate parallel work

Parallel candidates are read-only history comparison and independent test
review; production edits stay serial. If exact transition ownership is split,
add one coordination test at that boundary instead of duplicating lower-layer
matrices. Escalate after two or three failed evidence attempts or when the
existing seam cannot express the required authorization contract.

### Pass 2 critique

This revision is materially better because it names the event set, replay,
authorization, readiness-negative, and evidence limits. It still needs an
explicit diff-surface inventory before implementation, a check that the
default-target fence remains enforced for every event, and a guard against
accidentally turning a recorded outcome into a receipt assertion. It should
also state how missing origin is tested at the production owner rather than
only by a helper call.

### Critical evaluation of the Pass 2 critique

Those additions improve reviewability without expanding scope. The default
target must be asserted for creation and closure, and missing-origin behavior
must traverse the real owner. The evidence distinction is already sound but
should be repeated in the final handoff. No new harness or abstraction is
justified until the current seam inventory proves it necessary.

### Pass 2 roll-up and no-change decisions

Add a pre-implementation diff-surface inventory and event-by-event target
fence assertions. Require the missing-origin case to use the production owner.
Keep the no-new-harness, no-new-interface, no-readiness-event, no-live-action,
and no-foreign-cleanup decisions unchanged.

## Pass 3 — final full plan (counter 2 -> 3)

### Tasks and subtasks

1. Read the current production bytes and map ownership before editing:
   lifecycle transitions, launch-origin capture/subscription, callback
   admission, fanout/delivery, authorization registration, and existing tests.
2. Search history for an existing proven event-to-recipient implementation;
   port only the smallest compatible slice if found.
3. Write and run RED tests at the real lifecycle call site for:
   - supported creation callback;
   - supported closure callback;
   - recipient-idempotent restart/replay;
   - unknown owner/generation denial;
   - stale owner/generation denial;
   - missing-origin legacy compatibility;
   - default-target authorization on each supported event; and
   - closure never asserting rebuild/deploy readiness.
4. Implement the smallest typed adapter using existing launch-origin and
   convoycallback/convoyfanout seams. Keep route identity opaque, avoid
   credentials/URLs/private provider state, and preserve recorded outcome vs
   external receipt semantics.
5. Run focused owner tests, race tests if applicable, affected tests, fast
   baseline, vet, and pre-commit. Review changed expectations by contract
   reason, inspect source-boundary and error handling, then commit.
6. Push and hand off to refinery using the required metadata and drain; do not
   close the implementation bead.

### Architectural changes

Only an additive production connection from authoritative convoy lifecycle
transitions to the existing authorized callback delivery boundary. Creation
and closure are the supported event set; readiness/deployment remains owned by
its real producer and is not inferred from closure. No new primitive, endpoint,
role, provider-specific dependency, or general remediation engine.

### Test plan and support structures

Use existing recording/fake collaborators and a focused production lifecycle
harness. The test layer proves SDK transition-to-delivery wiring, admission,
authorization, replay idempotency, and compatibility. It cannot prove live
Hermes/provider receipt or recipient application acknowledgement; the final
report must say so explicitly.

### Docs, execution order, and stability strategy

No docs or generated artifacts unless source contract changes. No sleeps,
polling, deployment, install, service restart, credential/config mutation, or
live callback request. Preserve all foreign files and historical fences.
Execution is strictly RED -> GREEN -> focused/effected gates -> review ->
commit -> push/refinery handoff.

### Blocker avoidance and candidate parallel work

Read-only archaeology and review can be parallelized; implementation and
worktree writes remain single-owner. Stop and escalate only for a genuine
missing contract, unavailable dependency, or credential/external requirement;
continue safe local work otherwise. If no production call site can be proved,
do not claim completion from helper tests.

### Pass 3 critique

The final plan is complete and bounded. The only remaining risk is that the
actual repository may use different names or a merged predecessor than the
bead description implies; implementation must therefore let current code and
history override guessed paths. The plan also must preserve any foreign source
changes encountered in the dedicated worktree and record test-layer limits.

### Critical evaluation of the Pass 3 critique

This is the correct final caution. It does not alter the contract; it ensures
the work is evidence-led and avoids duplicate mechanisms. The branch/worktree
and bead metadata are already recorded, so the remaining controls are current
byte inspection, minimal diff, and explicit verification limits.

### Pass 3 roll-up, no-change decisions, and lost-information check

Apply the caution as execution rules: inspect first, use history deliberately,
and do not implement guessed package names. Retain all scope and safety
boundaries from earlier passes. No-change decisions: no new callback protocol,
no arbitrary route evaluation, no readiness inference, no credentials/private
state, no live authorization or deployment action, no mutation of held work,
no weakening of tests, and no implementation-bead close.

Lost-information check after three passes: the final plan retains the target
truth, required production evidence layer, insufficient proxies, failure
survival analysis, exact supported events, authorization/replay/compatibility
cases, architecture boundary, test and docs gates, stability constraints,
parallelization limits, escalation path, branch handoff, and explicit
provider-receipt limitation.
