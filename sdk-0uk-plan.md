# sdk-0uk plan

counter: 0

## Objective

Make refinery rejection/reopen state dispatchable when a departed worktree
claim leaves `gc.work_dir` without a validated `gc.worktree_repo`. Preserve
branch, work commit, target, rejection metadata, and retryability; do not
touch external Wayfinder worktrees.

## Full plan, tasks, and subtasks

1. Establish evidence.
   - Locate the current rejection/reopen writer and all metadata mutation
     paths for `work_dir`, `worktree_repo`, branch, and rejection fields.
   - Read the pool-demand path and existing tests; identify the smallest owning
     boundary and reproduce the skip before production changes.
2. Implement the smallest safe transition.
   - Prefer clearing stale `work_dir`/legacy mirror when the branch claim is
     canonical and no validated repository identity exists.
   - If the writer owns a validated repo identity, publish the complete
     worktree evidence atomically instead; never synthesize an identity.
   - Keep pool demand and hook claim single-writer/idempotent.
3. Add evidence artifacts.
   - Add a rejection/reopen writer regression preserving required metadata.
   - Add a pool-trigger boundary regression proving the bead is dispatchable,
     with no duplicate writer and no unsafe worktree claim.
   - Use disposable `t.TempDir`/memory store fixtures only; do not inspect or
     mutate the live Wayfinder worktree.
4. Verify and hand off.
   - Run focused RED/GREEN tests, affected package tests, `go vet ./...`,
     `git diff --check`, and relevant repository gates.
   - Review the diff against `origin/main`, commit cohesively, push the
     `polecat/sdk-0uk` branch, preserve bead metadata, and hand off to
     refinery without closing the implementation bead.

## Architectural changes

Keep the fix at the existing beads/rejection or pool boundary. Do not add a
new abstraction. A worktree claim is valid only when its repository identity
and ownership evidence are coherent; an unvalidated departed path must not
become a launch target. Pool realization remains the sole creator of demand,
and rejection/reopen remains the sole transition writer.

## Test plan and evidence discipline

Target truth: an open rejected/reopened pool bead with stale departed
worktree metadata is considered dispatchable and can produce exactly one
safe demand; required evidence is the pool-trigger boundary plus the direct
writer transition. Useful but insufficient proxies are metadata-only unit
tests and `worktreeSpecForBead` tests. The tempting false completion is to
teach the reader to ignore malformed state while leaving the writer able to
recreate it. If this plan succeeds, the bug could remain if another rejection
writer, an atomic update race, or a legacy metadata mirror bypasses the fixed
seam, so tests must cover writer identity, preservation, and one-demand
realization.

## Support structures

Use existing beads metadata constants, store seams, pool-demand helpers, and
worktree validation. Add no production fixture or live-environment harness.
Keep `agent-execution.log` temporary and record each completed subtask.

## Docs

No user-facing documentation change is expected. Update comments only where
they define the corrected ownership boundary. The plan itself records the
decision and evidence for this bug.

## Execution order and stability strategy

Trace -> RED regression -> writer fix -> pool-trigger regression -> focused
tests -> affected checks/vet/diff review -> commit/push/handoff. Preserve all
unrelated staged work until ownership is understood; do not reset or mutate
the external worktree. If the source seam differs from the staged attempt,
split or discard only task-owned edits after a read-only comparison.

## Blocker avoidance and parallel candidates

Independent read-only searches can be parallelized: rejection writers,
pool-demand call graph, and history/fixtures. Test and implementation stay
sequential because the RED result defines the narrowest fix. If a second
agent is available, request an independent review of the writer seam and
single-writer/idempotency proof; no external coordination is required.

## Pass 1 critique (top-to-bottom)

- Objective is specific about the malformed shape and preserved fields, but
  must name the exact writer after code archaeology.
- The plan correctly prioritizes source transition over reader tolerance, but
  atomicity and retry semantics need explicit assertions in tests.
- Architecture keeps provider assumptions out of generic code and avoids a
  new interface; the pool boundary ownership needs confirmation from code.
- Evidence distinguishes direct state proof from pool behavior, but must verify
  no duplicate demand/session and no path mutation.
- Execution is safe around pre-existing staged edits, though the final diff
  review must prove which edits belong to this bead.

## Pass 1 critique evaluation

The strongest unresolved risk is treating `work_dir` as harmless without
proving the writer clears both canonical and legacy forms. The revised plan
must record the writer function, its store update ordering, and exact
preservation assertions before implementation.

## Roll-up: revised critique applied

Add a source inventory and a direct before/after metadata table to the focused
test. Treat any reader-only workaround as insufficient unless the writer is
already safe and the test proves that fact.

## Roll-up: revised critique applied to tasks

Task 1 now requires the exact writer and update ordering. Task 3 requires
canonical/legacy clear behavior, required-field preservation, and one-demand
proof. Task 4 requires staged-diff ownership review.

## Explicit no-change decisions

- No external Wayfinder worktree inspection or mutation.
- No new provider/interface abstraction.
- No role-specific logic or hardcoded role name.
- No broad pool reader relaxation without source-transition proof.
- No documentation or generated API changes unless archaeology proves they
  are directly affected.

## Pass 2 critique (top-to-bottom)

- The objective now has the correct source-first bias and preservation scope.
- The implementation alternatives are appropriately conditional on validated
  identity; “canonical branch” must be defined by existing metadata helpers,
  not inferred from path text.
- The test plan covers direct transition and demand realization, but must
  avoid asserting internal call counts that do not represent the public pool
  contract; use stable bead/session outcomes and a recording writer only where
  duplicate creation is the risk.
- The staged changes may be a prior attempt rather than task-owned work; the
  final implementation must avoid layering a second policy over them.
- The verification plan is proportional, with broader checks after focused
  proof.

## Pass 2 critique evaluation

Use existing canonical branch/worktree metadata semantics and the smallest
recording seam available. Do not claim duplicate prevention from a pure
metadata reader test. First determine whether the staged patch is the same
fix, a partial fix, or unrelated prior work.

## Roll-up: revised critique applied

Task 1 adds history comparison and staged ownership audit. Task 3 uses the
actual pool-trigger function and asserts one request/claim plus unchanged
preserved metadata. Task 4 explicitly removes or folds duplicate policy.

## Pass 3 critique (top-to-bottom)

- The plan is now source-, boundary-, and preservation-oriented.
- It remains maintainable because it uses existing seams and does not widen
  production behavior for malformed metadata.
- The only acceptable reader behavior is the one implied by the corrected
  writer contract; malformed legacy data must not be silently blessed.
- The no-change list prevents accidental live-system or role-specific work.
- The execution order supports TDD and makes failure cause legible.

## Pass 3 critique evaluation

No material gap remains before archaeology. After three passes, re-check that
the exact writer, test layer, and metadata fields were not lost during
refinement; if code shows a different boundary, update this plan before
editing production files.

## Final roll-up and approval state

The plan is approved for autonomous execution under the assigned
`mol-polecat-work` workflow. The next action is read-only source archaeology,
followed by a failing focused regression before production edits.

counter: 3
