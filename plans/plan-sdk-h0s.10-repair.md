# sdk-h0s.10 repair plan

counter: 0

## Initial plan

### Objective

Repair the published `ResponseComposer` implementation on top of PR #72 so
that response bodies are preserved with one canonical normalization pass,
replays use the exact same commitment bytes, and tests remain valid after the
review fixture date. Remove the machine-local root planning artifact from the
published source branch. Keep the patch confined to
`internal/externalcoordination` plus this plan artifact; do not touch shared
API handlers, generated contracts, deployment, provider, account, pool, or
configuration paths.

### Tasks and subtasks

1. Reproduce each review finding at the owning layer.
   - Run the focused composition tests and capture the expiry failure.
   - Add a red regression for a long failed/expired summary that proves the
     composer and handoff use identical response bytes.
   - Inspect existing handoff normalization and commitment ownership before
     changing code.
2. Make the narrow production repair.
   - Establish one canonical response normalization boundary for composition.
   - Ensure the handoff path accepts already-normalized responses without
     independently changing their commitment bytes.
   - Preserve request, attempt, correlation, origin, and response fences.
3. Make evidence durable and time-safe.
   - Replace fixed past dates in new/affected tests with a deterministic
     future expiry derived from the test clock.
   - Keep crash-between-handoff-and-settlement, replay/idempotency, terminal
     outcome, no-origin, and stale-attempt tests at their owning layer.
4. Clean the PR artifact boundary.
   - Remove the machine-local root `plan-sdk-h0s.10.md` from the source diff;
     retain this scoped plan under `plans/`.
5. Verify and publish.
   - Run focused tests, race tests, formatting, vet/lint where available, and
     the documented fast shard.
   - Commit one cohesive source repair, push the repair branch, and report
     exact SHA/provenance, commands, and evidence limits to the bead owner.

### Architectural changes

No new authorization plane, queue, or provider is introduced. The existing
`ResponseHandoff` remains the durable handoff boundary and `Service` remains
the request settlement boundary. The composition module owns cross-boundary
ordering; response normalization and commitment identity must have one clear
owner so a retry cannot silently change bytes.

### Test plan

- RED: reproduce the current review failures before edits.
- Unit/coordination: long terminal summary remains byte-identical across
  composer enqueue and exact replay.
- Coordination: injected settlement failure leaves one durable handoff and a
  retry settles the same request.
- Coordination: refused, failed, and expired outcomes preserve truthful state.
- Coordination: no-origin recording, missing-handoff recovery, and stale
  attempt refusal remain covered.
- Boundary: focused `internal/externalcoordination` tests, race tests, vet,
  changed-file formatting/lint, then the documented fast shard.

### Support structures

Use the fresh git worktree from `origin/pr-72` and the existing MemStore,
conditional writer, handoff, and service seams. No new interface is needed.
Use explicit test times whose request expiry is in the future relative to the
test's injected claim time; do not use wall-clock sleeps or polling.

### Documentation

Keep the plan under `plans/` and document source-delivery versus rollout and
final acceptance in the handoff. Do not edit architecture docs because the
repair does not change the settled model.

### Execution order

Inspect and reproduce -> add failing regression -> implement smallest repair
-> update time-safe fixtures and remove root artifact -> focused verification
-> broader required verification -> commit/push -> bead handoff.

### Stability strategy

Preserve the exact PR base and existing source scope. Avoid force-push,
deploy/restart/config changes, current-chat registration, protected cleanup,
or changes outside the owning package. Use deterministic store state and
exact request/attempt/correlation identities.

### Blocker avoidance

Do not use the shared worktree. Do not infer rollout from a source branch or
open PR. If the host lacks ICU headers or another pre-existing dependency,
record the exact blocked command and retain narrower evidence rather than
masking the error. Do not mutate an absent owner session or repair unknown
external state.

### Candidate parallel work

No safe parallel writer is needed: production and test edits share the same
normalization boundary. Independent read-only review can inspect the final
diff and exact-byte evidence after implementation.

### Proxy audit

- Target truth: a response body and its request/origin fences survive an
  interrupted settlement and deterministic replay without duplicate logical
  handoffs or altered commitment bytes.
- Required evidence layer: real `Service` + `ResponseHandoff` + conditional
  store composition tests, including injected inter-write failure.
- Cheaper but insufficient proxies: a pure hash test, a unit test of string
  sanitization alone, or a passing handoff-only test.
- Tempting false completion: merely making existing tests pass, or accepting
  a different summary on replay because the hash still verifies.
- If this plan succeeds but the bug remains: a second normalization path or
  a real store's serialization could still rewrite bytes; the exact replay
  test and focused real store boundary must therefore remain.

## Planning pass 1

### Full plan/tasks/subtasks

Keep the initial objective and task order. Add the specific review-driven
checks: reproduce the fixed-date expiry under the current date, isolate the
double-sanitization commitment mismatch with a 128-byte boundary case, and
verify the source diff contains no root plan artifact or unrelated files.

### Critique

The initial plan correctly limits scope and identifies the two blockers, but it
does not yet state whether normalization should be moved, shared, or skipped
at the handoff boundary. It also needs an explicit check that successful
answered bodies are not altered and that terminal summaries retain their
truthful state. The broad fast shard may be unavailable for environmental
reasons, so focused evidence must be clearly separated from that limitation.

### Critical evaluation of the critique

The critique is valid: a vague normalization fix could move the bug rather
than remove the duplicate owner. The safest design is to normalize once in
the composer before both handoff and settlement, while the existing handoff
normalizer must remain correct for its public direct callers. The composition
module therefore needs a deliberate already-normalized handoff entry or a
shared canonical helper, not an undocumented bypass. The test matrix must
include answered and terminal responses, plus exact bytes.

### Roll-up: apply evaluation to critique

Treat normalization ownership as an explicit design decision before coding.
Prefer a small shared canonical normalization helper if it preserves direct
`ResponseHandoff.Enqueue` behavior; otherwise make the composition boundary
pass a typed normalized value through a private helper. Do not weaken direct
handoff validation. Add an answered-body identity assertion and retain
terminal outcome assertions.

### Roll-up: apply revised critique to plan/tasks/subtasks

Update task 2 to require one canonical helper or a private normalized enqueue
path with validation still enforced once. Update task 3 to assert the exact
stored summary and commitment remain stable for answered and terminal cases.
Update verification to distinguish focused composition truth from full-shard
environmental coverage.

### No-change decisions

No changes to the authorization boundary, request settlement API, durable
identity keys, or provider delivery state machine. No new abstraction is
justified because the existing handoff and service seams are sufficient.

counter: 1

## Planning pass 2

### Full plan/tasks/subtasks

Implement the revised plan: reproduce first, add a failing exact-byte terminal
summary test and future-expiry fixture, then repair normalization ownership,
remove the root plan artifact, and verify source-only scope and all required
boundaries.

### Critique

The revised plan still risks overfitting to the reported 128-byte example.
The actual contract is byte preservation for all normalized response fields,
including received time, response ID, retention, and summary. It also must
ensure a completed request cannot create or accept a new handoff. The plan
should explicitly inspect idempotency for both interrupted and already
completed requests.

### Critical evaluation of the critique

This critique improves coverage without requiring a Cartesian test matrix.
The existing tests already cover completed-without-handoff and stale attempt;
the new regression should compare the durable response and commitment rather
than duplicate every state. A single long terminal summary is the escaped
equivalence class, while the existing answered interruption test covers the
cross-boundary ordering.

### Roll-up: apply evaluation to critique

Add a focused assertion that all response fields used by commitment are
unchanged after composition and exact replay. Keep one test per distinct
obligation, not repeated permutations. Verify completed replay returns the
original handoff ID and refuses missing durable handoff as already specified.

### Roll-up: apply revised critique to plan/tasks/subtasks

Add to task 1 a before/after commitment inspection using the durable record.
Add to task 3 exact-field assertions for response ID, state, summary,
retention, and received time where applicable. Treat existing completed
replay tests as retained owning evidence.

### No-change decisions

Do not add a new queue, replay table, hash-based reconstruction, or identity
field. Do not alter request expiration semantics; only make test fixtures
future-relative and deterministic.

### Proxy audit

- Target truth: recovery returns the same durable logical response to the
  authorized origin identity after a settlement interruption.
- Required layer: composition over real in-memory durable store plus the
  conditional settlement writer, with exact durable record inspection.
- Useful but insufficient: comparing only returned Go structs, only counting
  handoff beads, or only checking the final request state.
- False completion to avoid: a replay that returns success while storing a
  different sanitized body, or a test that passes only before expiry.
- Residual risk: a file/Dolt-backed store could serialize differently; this
  patch does not claim provider/store conformance beyond the exercised store
  boundary and must say so in handoff evidence.

counter: 2

## Planning pass 3

### Full plan/tasks/subtasks

Use the exact PR head as the base, reproduce both review failures, add only
the smallest red tests, implement a single canonical response normalization
route without weakening direct handoff validation, remove the machine-local
root plan, run focused/race/vet/lint/fast-shard evidence, and publish a
reviewable repair commit with explicit evidence limits.

### Critique

The plan is complete, but the phrase “without weakening direct handoff
validation” must be checked against the existing API shape. A helper that
accepts normalized data must remain private or prove that normalization is
idempotent. The implementation must not silently default a missing received
time in the composer when the stored request lacks a creation time.

### Critical evaluation of the critique

The current composer already rejects an absent received time when the request
creation time is absent, while direct handoff currently defaults to wall time.
Changing that unrelated behavior would expand scope. The repair should share
only the canonicalized value produced by the composer and leave direct
handoff defaults untouched, with a private method or helper that makes the
boundary explicit.

### Roll-up: apply evaluation to critique

Implement the narrowest private handoff enqueue path that takes the composer’s
validated response and does not normalize it a second time, or refactor both
paths to a shared helper with identical documented semantics. Preserve the
composer’s strict received-time rule and direct handoff’s established public
behavior unless the exact regression requires otherwise.

### Roll-up: apply revised critique to plan/tasks/subtasks

Before editing, inspect call sites of `normalizeHandoffResponse` and
`ResponseHandoff.Enqueue`. After editing, test both the composition path and
the direct handoff path. Review the final diff for accidental generated,
deployment, or root-handler changes.

### No-change decisions

No rollout, install, restart, configuration reload, account/profile change,
live model call, current-chat registration, protected cleanup, force push, or
identity/database repair. The plan is approved for execution by the user’s
explicit instruction to execute the claimed work immediately; no separate
interactive approval pause is needed.

### Proxy audit

- Target truth: exact-origin, exact-attempt response durability and honest
  settlement across the inter-write failure boundary.
- Required evidence: focused composition tests with the real package stores and
  conditional writer, plus direct handoff regression and source-diff review.
- Insufficient proxies: compile success, a green test with expired fixtures,
  or a semantic preview of normalized values without durable commitment
  comparison.
- False completion: shipping a source branch while claiming rollout or final
  acceptance; the handoff will explicitly distinguish those layers.
- Remaining bug path if all planned work succeeds: an untested production
  store/provider serialization or downstream `.6` integration could still
  violate delivery; those remain outside this source-only repair’s proof.

counter: 3

## Lost-information check after three passes

Retained: exact review blockers, source-only boundary, one-writer worktree,
canonical identity/fence requirements, crash/replay evidence, time-safe
fixtures, direct-handoff compatibility, and the distinction between source
delivery and rollout/final acceptance. No material requirement was dropped.
