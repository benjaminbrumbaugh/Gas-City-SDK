# Migration Wave 2: native ready-CAS on the pinned Beads API

Counter: 3

## Full plan and tasks

1. Establish the baseline and owning boundary.
   - Confirm the worktree starts at `f1da14d2be291bb9097f73580293a77001aaa80c`.
   - Read the pinned Beads public storage and transaction contracts.
   - Run the focused package build/test to capture the compile failure.
2. Define the compatibility seam with RED tests first.
   - Preserve one atomic transaction for readiness, empty assignee, and row revision.
   - Cover success, stale revision, assigned bead, blocked bead, missing bead, and
     unsupported mutation shapes without falling back to read-then-write.
   - Keep the real native-Dolt integration tests as the cross-boundary evidence.
3. Implement the smallest native-store adapter.
   - Use only the pinned public `beadslib.Storage`/`Transaction` API.
   - Refuse unsupported operations and map backend precondition failures to the
     existing Gas City errors.
   - Do not change cutover logic, other repositories, or dependency replacements.
4. Verify and hand off.
   - Run focused tests, package build, `go vet` on the affected package, and the
     relevant integration tests where the real provider is available.
   - Inspect the diff, stage only scoped files, commit locally, and record exact
     evidence and canonical-repo status invariants.

## Architecture, support structures, and documentation

The adapter remains in `internal/beads`, at the native storage boundary. The
controller-facing `ReadyConditionalWriter` contract remains unchanged. The
existing error taxonomy and test helpers are reused. No API/OpenAPI/generated
surface or documentation page changes are expected; this plan is the review
artifact for the migration.

## Test and evidence plan

The unit evidence proves dispatch, validation, and error mapping against the
native storage test double; it does not prove database isolation. The integration
evidence proves the transaction behavior against real upstream native storage;
it does not prove every controller call site. The build and vet evidence prove
the pinned public API compiles and is statically sound; they do not prove the
runtime readiness contract by themselves.

## Execution order and stability strategy

Baseline → RED tests → GREEN adapter → focused unit tests → real integration
tests → build/vet → diff review → local commit. Tests must wait on explicit
transaction results, never sleeps or open-coded polling. If the real provider
is unavailable, report the skip as a limitation rather than treating the unit
test as equivalent evidence.

## Blocker avoidance and candidate parallel work

No parallel code edit is needed: the target is one file and its owning tests.
An independent read-only review can inspect the pinned Beads transaction
contract and history while implementation proceeds, but concurrent edits would
increase boundary risk. If the public API cannot express an atomic readiness
fence, stop and report that blocker rather than adding an ephemeral replace or
an unsafe fallback.

## Planning pass 1 — initial critique

The key risk is preserving the dependency/block projection, not merely replacing
missing symbols. A status-plus-assignee check alone would be a false completion.
The plan therefore requires a transaction-level readiness read and update, and
retains real-provider integration coverage. The unit test must not be mistaken
for database isolation evidence.

## Planning pass 2 — critique of the critique

The pinned API exposes `RunInTransaction`, `Transaction.GetIssue`,
`Transaction.IsBlocked`, and `Transaction.UpdateIssue`; these are enough to keep
all predicates and the mutation in one transaction. `UpdateIssueChecked` alone
does not express dependency readiness. Revision mismatch and semantic mismatch
must be returned before mutation, while transaction-commit conflicts must fail
closed. No dependency upgrade or local replace is justified.

## Planning pass 3 — roll-up and no-change decisions

Apply the revised critique: implement through the public transaction seam, add
tests for each distinct refusal edge, and keep the existing integration tests as
the real-boundary proof. No change to controller cutover, no other-repository
edit, no generated artifact, no new abstraction, and no dependency change.

After three passes, no material plan information was lost: scope, boundary,
evidence layers, failure edges, stability policy, and handoff constraints remain
explicit. The plan is approved for execution by the assigned pool-worker
instruction; no interactive approval pause is added.
