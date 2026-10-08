# Plan: sdk-h0s.10 crash-recoverable response-body handoff

bead: sdk-h0s.10
base: origin/main
worktree: /Users/benjaminbrumbaugh/Documents/Gas City/Gas-City/.gc/worktrees/Gas-City-SDK/work/sdk-h0s.10
counter: 3

## Pass 0 — full plan

### Full plan, tasks, and subtasks

1. Establish the published contract and current repository boundary.
   - Fetch and inspect the exact canonical bytes at Hermes-Extensions commit
     `512f7653307cbf35c4172ab64f585eba2f5a6184`, especially the accepted
     trusted-local resumption decision in `docs/plans/mayor-callback-completion.md`.
   - Read repository instructions, `TESTING.md`, and the existing
     `internal/externalcoordination` service/handoff implementation.
   - Record the current response bytes and settlement ordering before editing.
   - Preserve the scope boundary: no shared API/root/server, generated
     contract, deployment, provider/account, config, live-model, wisp, or
     authorization-plane changes.
2. Map the proven consumer and exact ownership boundary.
   - Trace `Service.RecordResponse` and `handoff.go` from request admission
     through response-body persistence and completion acknowledgement.
   - Identify the smallest dedicated response-composition module that can
     consume already-authorized request/origin/attempt/correlation/fence data.
   - Confirm which layer owns transport of a human-origin answer and keep
     decision composition in the Governor plugin, not this module.
3. Add failing target-layer tests first.
   - Test body-before-settlement ordering with injected failure between body
     handoff and request settlement.
   - Test deterministic recovery/replay after an interrupted write, including
     lost acknowledgement and HTTP idempotent replay.
   - Test exact request/attempt/correlation/origin/fence matching and refusal
     of stale origin writes after successor replacement.
   - Test honest failure, expiry, and refusal outcomes without inferring an
     answer from a hash or creating a new queue.
4. Implement the smallest typed composition/handoff slice.
   - Reuse the existing durable handoff store and existing service contracts.
   - Persist the full meaningful body before any irrecoverable completion or
     HTTP acknowledgement.
   - Make replay/restart reconciliation idempotent and deterministic.
   - Keep stale-origin protection and all existing authorization checks intact.
5. Verify the real owning layer and hand off.
   - Run focused package/integration tests that exercise real stores/handlers,
     then the configured affected-test command and proportionate quality gates.
   - Review exact diff, provenance, and evidence limitations; preserve foreign
     files and task-owned temporary artifacts only.
   - Commit, push, verify the remote SHA, record branch/commit/test evidence,
     and route the open work bead to the refinery without closing it.

### Architectural changes

- Add a small response-composition/handoff boundary under
  `internal/externalcoordination` (or the existing proven package boundary)
  owned by this task.
- The module consumes already-stored authorized request/origin/attempt fences;
  it does not create a new identity or authorization plane.
- Existing handoff persistence remains the durable substrate; no new queue,
  generic remediation engine, callback endpoint, or provider-specific behavior.
- Existing shared API/root/server and generated-wire contracts remain outside
  this task; integration is serially owned by sdk-h0s.6.

### Test plan and evidence boundaries

- Target truth: a meaningful response body survives an inter-write crash and
  can be returned/recovered exactly once for the authorized current origin,
  while stale origins cannot mutate a successor.
- Required evidence layer: real externalcoordination stores and handlers,
  including fault injection at the handoff/settlement boundary and replay
  paths. This proves durable response handoff semantics, not live provider
  delivery, model completion, deployment, or final product acceptance.
- Useful but insufficient proxies: pure serializer tests, hash comparisons,
  fake stores, or a successful compile. They do not prove body durability,
  ordering, or stale fencing.
- Tempting false completion: treating a persisted commitment/hash as the
  answer, returning a body from memory after restart, loosening origin checks,
  or passing a fake-handler test while the real store loses the body.
- Residual-risk question: if this plan is green while the bug remains, the
  likely gap is an untested real HTTP/restart path or an alternate settlement
  caller that still acknowledges before body persistence.

### Support structures

- `plan-sdk-h0s.10.md` records the refined plan and evidence boundaries.
- `agent-execution.log` records completed subtasks with timestamps and
  overall progress.
- Existing test fixtures, stores, and fault-injection seams only; introduce a
  new fixture helper only when it observes the same target layer.
- Bounded logs may live under `/var/tmp`; never relocate Go cache or scratch to
  `/tmp` and never run `go clean -cache`.

### Documentation

- No user-facing documentation change is expected.
- Add concise source comments only where the body-before-settlement or exact
  origin/fence contract would otherwise be ambiguous.
- Durable bead notes must name exact source/test commits and state that source
  delivery is not rollout or final feature acceptance.

### Execution order

Published-plan inspection -> repository/consumer archaeology -> reproduce
current bytes -> failing target-layer tests -> minimal implementation ->
focused/affected tests -> quality review/gates -> commit/push/refinery handoff.

### Stability strategy and blocker avoidance

- Use a fresh branch from fetched `origin/main`; do not edit the home
  worktree or shared rig checkout.
- Preserve foreign untracked files and claims.
- Do not deploy, install, restart services, reload configuration, change
  providers/accounts/profiles, perform live model calls, mutate wisps, force
  push, or alter protected cleanup/fences.
- If an external dependency is unavailable, record the exact boundary and
  escalate rather than substituting a weaker proxy.
- If Dolt is slow or unavailable, collect the prescribed non-fatal diagnostics
  before escalation; do not restart it.

### Candidate subagent-parallel work

- Read-only history and canonical-plan archaeology could be parallelized.
- Current store/handler tracing and test-first design should remain one-writer
  work in this task worktree to avoid boundary drift.

### Proxy audit

- Target truth: crash-safe, exact-origin durable response handoff and honest
  recovery semantics.
- Required evidence layer: real stores/handlers with injected inter-write
  failure, replay, lost-acknowledgement, and successor-origin scenarios.
- Cheaper useful-but-insufficient proxies: in-memory unit tests, body hashes,
  and static ordering inspection.
- Tempting false completion: a green test that only checks a commitment, or a
  fake transport that never exercises restart/replay.
- If the plan succeeds yet the original bug remains, an alternate caller may
  still settle the request before invoking the composed handoff; caller
  inventory and real handler tests must therefore be part of verification.

## Pass 0 critique, top to bottom

- The plan correctly starts with exact published bytes and current code, but
  it should explicitly distinguish canonical-plan provenance from current
  branch behavior so the plan cannot turn an external decision into assumed
  implementation.
- The architecture section names the expected package but should defer the
  exact module filename until consumer tracing proves the boundary.
- The test plan is appropriately target-layered, but it must name the actual
  fault-injection seam after inspection and avoid promising HTTP integration
  if this repository only owns the durable handoff API.
- Support/documentation/handoff sections are complete; execution logging
  should record evidence-layer results, not just command names.
- Parallel work is possible only read-only; one writer remains safer.

## Pass 0 critical evaluation of the critique

- The critique is sound: it avoids inventing a module boundary and prevents
  canonical documentation from being mistaken for current behavior.
- It should also call out the requirement to reproduce current bytes before
  writing expectations, because a test can otherwise encode a desired body
  rather than the published contract.
- It should require a negative stale-origin test against the real successor
  state, not merely an isolated token comparison.

## Pass 0 roll-up: revised critique

Add explicit byte reproduction and source-cited consumer mapping before the
red test. Make the architecture provisional until the existing handoff seam
is identified. Require negative fencing against successor state and label
all evidence by observed layer. Keep the exact external-plan commit as
provenance only, not as an implementation oracle.

## Pass 0 roll-up: revised plan/tasks

Insert a byte-reproduction/source-map subtask between archaeology and tests;
make the target module name conditional on the proven existing boundary; and
require one test that exercises stale origin against an actual successor
record/fence.

## Pass 0 no-change decisions

- No new identity/authentication prerequisite under the accepted trusted-local
  decision.
- No changes to shared API/root/server or generated contracts.
- No answer inference from hashes, in-memory state, or absent bodies.
- No new queue or provider-specific transport implementation.
- No weakening of existing write/transport authorization or expiry policy.

## Pass 1 — refined plan

### Plan refinement

1. Fetch/read exact canonical bytes and current source before deciding the
   filename or public shape.
2. Reproduce current handoff bytes and settlement order with the existing
   store/handler path; identify the first irreversible acknowledgement.
3. Add red tests at the discovered seam for body-before-settlement,
   interrupted replay, idempotent HTTP retry, lost acknowledgement, and
   stale successor fencing.
4. Implement only the typed response composition/handoff slice needed by the
   proven consumer; preserve existing auth/expiry/refusal behavior.
5. Run focused tests and the rig affected-test command, then proportionate
   vet/format/pre-commit gates; record exact evidence and hand off.

### Critique

This refinement is narrower and keeps the module boundary evidence-led. It
still needs an explicit check that response transport does not accidentally
grant work authority, and it should test empty/failed/expired outcomes rather
than only successful body delivery.

### Critical evaluation of critique

Those additions are necessary because a response body is data transport, not
authorization. Failure/refusal/expiry are part of the honest contract and
must not be collapsed into a successful empty answer. The plan should also
retain exact request/attempt/correlation binding in both positive and
negative cases.

### Roll-up: apply evaluation to critique

Add a transport-without-authority assertion and table-driven failure/refusal/
expiry cases. Keep request/attempt/correlation/origin/fence fields explicit in
test fixtures so a test cannot accidentally pass by matching only an origin
string.

### Roll-up: apply revised critique to plan/tasks

The red-test phase now includes successful, empty, failed, refused, expired,
replayed, and stale-origin outcomes, each bound to the full persisted identity
tuple. Implementation remains one small module and must not choose or compose
the user's decision.

### No-change decisions

- Transport may carry the human-origin body but may not authorize work.
- Governor/plugin composition remains outside this task.
- No fallback target or inferred answer is introduced.

### Proxy audit

The target remains durable exact-origin handoff. A green serializer or hash
test is insufficient; required evidence still reaches the real store/handler
settlement boundary. If the original bug remains after green tests, the most
likely gap is an untested alternate caller or a restart path that bypasses the
new composition module.

## Pass 2 — final refinement

### Plan refinement

- Use the canonical external plan only to confirm the accepted trusted-local
  boundary and exact provenance; derive behavior from this branch's bytes.
- Keep one writer for production and tests, with read-only archaeology only
  if it cannot alter task files.
- Require evidence of body persistence before settlement, deterministic
  recovery without duplicate execution, and refusal of stale successors.
- Publish source with exact SHA/test commands and explicitly separate source
  delivery from rollout and final acceptance.

### Critique

The final plan is complete but should explicitly check that the implementation
does not create a new broad abstraction merely to host one consumer, and that
the test fixture's failure injection is removable/non-invasive to production.

### Critical evaluation of critique

This is an important YAGNI and boundary check: the smallest proven consumer
should own the composition, and fault injection must be test-only or injected
through an existing seam. A general recovery framework would violate scope.

### Roll-up: apply evaluation to critique

Add an explicit review gate for abstraction size and test-only fault injection;
reject any new queue, generic recovery engine, or provider coupling that is
not required by the proven consumer.

### Roll-up: apply revised critique to plan/tasks

Before self-review, inspect the diff for one-consumer abstractions, production
failure hooks, and leaked provider/auth assumptions. Keep only the narrow
module and evidence needed for the exact handoff contract.

### No-change decisions

- Do not reopen the resolved caller-session identity decision.
- Do not touch unrelated held beads, native convoys, or integration owner
  sdk-h0s.6's shared API/root consumption.
- Do not claim feature acceptance, deployment, live authorization, or model
  completion from source tests.

### Proxy audit

The required proof remains at the durable handoff/settlement layer. Cheap
proxies remain useful for localization only. False completion remains any
green result that proves a commitment without the full body, skips restart/
replay, or permits stale-origin mutation. Residual risk is recorded with the
exact untested caller/transport layer rather than hidden behind a passing
unit suite.

## Refinement outcome and approval state

After three passes, no required information was lost: canonical provenance,
scope boundaries, implementation ownership, failure semantics, evidence
layers, stability restrictions, test gates, and handoff requirements remain.
The plan is satisfied and ready for execution under the assigned polecat
workflow; no separate approval wait is introduced because the durable bead
assignment is the execution authority.
