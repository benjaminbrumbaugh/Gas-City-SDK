# sdk-6xj lease reconciliation plan

counter: 0

## Pass 1: full plan

Goal: repair the rejected controller-owned lease reconciliation so exact live
runtime owners stay leased in the city and eligible rig stores, while
abandoned local claims reopen only after native grace. Preserve provider and
session fencing boundaries; keep the dashboard observational.

Tasks/subtasks:

1. Read `TESTING.md`, the rejected implementation/history, native beads lease
   APIs, runtime/session identity, and reconciliation observation/API schema.
2. Reproduce and classify the refinery findings: typed trace snapshot schema,
   credential-provider timeout, and p95 NFR regression.
3. Write failing focused tests for exact identity renewal; draining,
   replacement-token, provider-fenced, missing-owner, and wrong-assignee
   cases; active/suspended/unregistered store selection; local-replica grace;
   unreadable-store fail-closed behavior; abandoned reopen; and diagnostics.
4. Implement the narrowest fix at existing boundaries, then run focused,
   affected, fast-sharded, vet, and any API schema checks required by the diff.
5. Review staged paths, commit only the cohesive lease change on
   `polecat/sdk-6xj`, push/verify the remote ref, and hand the bead to the
   refinery without closing it.

Architectural changes: reconciliation orchestration remains in `cmd/gc`;
lease mutation remains in the beads/store boundary; exact live identity stays
in session/runtime boundaries; observations remain factual and typed.

Test plan: RED at the smallest owning layer, GREEN with a minimal change,
refactor duplicate assertions, measure focused/affected timing, then verify
the cross-store integration. Store tests prove mutation semantics;
controller tests prove identity/store eligibility; integration proves wiring;
diagnostics prove reports only, not production liveness.

Support structures: use existing native lease ports, injected clocks/fakes if
already available, the existing observation counters, and no new abstraction
without two real implementations. Maintain a temporary execution log and
remove temporary artifacts before handoff.

Docs: update only contract comments/API schema when behavior truly changes;
do not touch product dashboard code. Execution order is inspect/reproduce,
tests, implementation, focused gates, broader gates, review, handoff.

Stability/blockers: bound each reconciliation tick; isolate store errors;
never treat unreadable as empty; never use `--any-replica`; never inflate
timeouts or weaken assertions. Preserve unrelated worktrees and staged work.
If the exact branch cannot be used safely, stop before commit and escalate.

Parallel candidates: independent read-only audits of native lease primitives,
rejection schema, and focused coverage are useful; do not parallel-edit this
branch.

## Pass 1 critique

The plan is aligned with acceptance and ownership but must identify the exact
schema mismatch and timeout owner rather than changing them blindly. It must
separate deterministic correctness tests from NFR measurement and explicitly
prove no mutation on unreadable stores.

Proxy audit: target truth is exact live-owner renewal and grace-limited local
reclaim across eligible stores. Required layers are native lease tests,
controller identity/store tests, and one cross-store integration. In-memory
tests and dashboard snapshots are useful but insufficient. False completion
would be changing trace schema or timeout thresholds while a rig store is
still skipped or a recycled identity is renewed.

## Pass 1 critical evaluation

Require the existing typed result contract, injected time for grace tests,
production store-selection source, and a separate p95 diagnostic. Keep
unreadable store errors explicit and assert zero mutation calls.

## Pass 1 roll-up

Add those requirements to the implementation/test steps, while treating
branch/worktree identity as a fail-closed handoff invariant.

No-change decisions: no dashboard filtering, no `--any-replica`, no broad
retry or timeout increase, no direct-owner heartbeat unless native boundaries
require it, and no unrelated staged-file changes.

## Pass 2: refined plan

counter: 1

Inspect the current branch's rejected commit and run exact focused tests with
cache disabled. Use table-driven identity cases and real registration state to
select city plus active/non-suspended rigs. Use injected clock/store-open
errors to prove native grace and zero mutation on partial failure. Verify
diagnostic last-success data plus per-store renew/reclaim/error counts through
the typed observation boundary. Keep a real active-rig and abandoned-claim
smoke if available, clearly labeling its observed layer.

## Pass 2 critique

The refined plan avoids false completion but must avoid brittle elapsed-time
tests and must not treat a focused timeout as proof of this change's behavior.
It needs a final staged-path audit and a clear record when environment gates
cannot run.

## Pass 2 critical evaluation

Use fake time for behavior, one separately labeled performance run for NFR,
and record environment failures without suppressing them. The final branch
metadata must match `polecat/sdk-6xj` and the pushed commit.

## Pass 2 roll-up

Correctness, performance, and environment evidence stay separate; no gate is
made green by retries or weakened expectations.

No-change decisions: no speculative interface, no schema loosening, no
dashboard work, no destructive removal of the other worktree.

## Pass 3: final plan

counter: 2

Read the implementation and tests, reproduce the rejection, add RED tests,
implement the minimal boundary fix, run focused/affected/fast/vet/API gates,
inspect the exact diff, commit/push/verify, update and reassign the bead, and
drain. If host ICU or another unrelated dependency prevents a gate, capture
the evidence and report it; do not claim the product is verified.

## Pass 3 critique

The plan is complete. Environmental ICU absence, credential-provider hangs,
and branch/worktree state are material risks; the existing clean exact branch
must be used and the dirty stale checkout must remain untouched.

## Pass 3 critical evaluation

Those risks are manageable without destructive actions. Use existing branch
history as evidence, keep edits in the clean dedicated worktree, and escalate
only if exact branch handoff cannot be preserved.

## Pass 3 roll-up

Proceed with the evidence matrix and fail closed on branch mismatch or unsafe
store behavior. Retain this plan as the durable review record and remove only
the temporary execution log before handoff.

No-change decisions: no reset/checkout of another worktree, no unrelated
files, no test expectation changes without an independently wrong contract,
and no sign-off from proxy evidence alone.

counter: 3
