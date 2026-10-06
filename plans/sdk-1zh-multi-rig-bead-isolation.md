# sdk-1zh recovery plan

counter: 0

## 1. Full plan, tasks, and subtasks

### Objective

Resume the existing `sdk-1zh` implementation for multi-rig bead isolation,
rebase it onto current `origin/main`, resolve the known conflict without
losing upstream changes, and hand the verified `polecat/sdk-1zh` branch back
to Refinery.

### Tasks

1. Load and bound the existing work.
   - Confirm the assigned bead, rejection reason, recorded worktree, branch,
     fork provenance, and current diff.
   - Read `TESTING.md` and the relevant integration helper/test context.
   - Preserve unrelated worktree changes and avoid the polecat home worktree.
2. Reconcile the rejected branch.
   - Fetch `origin/main` and inspect the conflict against current upstream.
   - Rebase `polecat/sdk-1zh` onto `origin/main`.
   - Resolve only the integration-helper conflict, retaining both the current
     upstream parser contract and the explicit create-marker behavior needed
     for multi-rig isolation.
3. Verify the product behavior at the correct evidence layer.
   - Run the focused parser/unit test and the affected integration test(s).
   - Run the configured affected-test gate and any required Go formatting,
     vet, or build checks that apply to the changed package.
   - Review the final diff and confirm no role/config/API boundary is leaked.
4. Submit through the formula contract.
   - Commit plan/log and any residual implementation/test corrections with a
     scoped message.
   - Run the self-review guard only after the tree is clean and checks pass.
   - Push, verify remote HEAD identity, record producer metadata, reassign to
     Refinery, wake/nudge it, and drain the session.

### Architectural changes

No production architecture changes are expected. The change belongs at the
integration diagnostic/parser boundary: `extractBeadID` must prefer an
explicit command-created bead marker over incidental ID-shaped text from
temporary paths, while retaining generic fallbacks for commands without that
marker. Multi-rig isolation remains owned by the integration harness and
beads/provider boundaries; no generic SDK role or storage behavior is added.

### Test plan and evidence discipline

Target truth: a multi-rig integration command creates a bead in the intended
rig, returns the correct rig-prefixed ID, and subsequent lookup does not cross
rig stores.

Required evidence layer: the integration test harness plus its focused parser
test, followed by the configured affected integration shard. These observe
the command-output parsing and real multi-rig isolation path, respectively.

Useful but insufficient proxies: a parser-only unit test, a passing build,
or seeing a correctly shaped ID in fixture text. None proves the actual
cross-rig store boundary remains isolated.

Tempting false completion: accepting the first `gc-*` token in diagnostic
output, or changing the expected prefix without exercising create-and-lookup
across rigs. If this plan succeeds incorrectly, the original bug could remain
if a temporary directory token still wins parsing in the real integration
output; the end-to-end multi-rig test is therefore mandatory.

Test expectations may change only if the current upstream command output
contract changed. The existing expected behavior (correct rig-prefixed bead
and isolation) is the product contract and must remain unchanged.

### Support structures

- Existing branch/worktree metadata and rejection reason are the recovery
  record.
- This plan records decisions and evidence boundaries.
- `agent-execution.log` records completed subtasks for handoff/recovery.
- Git rebase/conflict state is inspected before any edit; no destructive
  checkout or reset is allowed.

### Documentation

No user documentation change is indicated. The integration helper comment and
focused regression test should explain the parser boundary clearly enough for
future maintainers.

### Execution order

Load context -> inspect current branch/diff -> read testing guidance -> fetch
and rebase -> resolve conflict -> run focused tests -> run affected gate ->
self-review -> commit/push -> verify remote -> hand off to Refinery.

### Stability and blocker avoidance

- Reuse the recorded `work/sdk-1zh` worktree and `polecat/sdk-1zh` branch.
- Never edit the reusable polecat home or shared rig checkout.
- Never run `go clean -cache`; use the host shared cache.
- If Dolt becomes slow or unavailable, collect the prescribed diagnostics
  before escalation; do not restart it blindly.
- If the rebase or tests remain blocked after focused investigation, report
  the exact durable blocker to Witness and leave the branch recoverable.

### Candidate parallel work

Independent read-only work can run in parallel: inspect the prior commit/diff,
read testing guidance, and query formula/bead metadata. Conflict resolution,
formatting, and validation remain sequential because they share the branch.

## 2. Critique of every major section

- Objective/tasks: correctly treats this as rejected-branch recovery, but the
  final handoff must explicitly verify branch shape and remote identity.
- Architecture: appropriately limits the change to the integration boundary;
  it must not infer that a passing parser test proves store isolation.
- Tests/evidence: distinguishes parser evidence from end-to-end isolation and
  preserves the existing product expectation.
- Support/docs: plan and execution log are useful recovery artifacts, but they
  must not hide unrelated pre-existing changes.
- Execution/stability: rebase conflict handling must use current upstream and
  preserve both sides, with no destructive commands.
- Parallel work: only read-only discovery is parallel-safe.

## 3. Critical evaluation of the critique

The critique catches the main risk: a superficially clean rebase could still
drop upstream behavior or validate only the parser. It does not change the
target architecture. The plan should make the conflict-resolution acceptance
criteria explicit and ensure the temporary execution log does not become an
untracked handoff failure.

## 4. Roll-up: revised critique decisions

- Add a conflict-resolution check against the post-rebase diff and current
  upstream helper behavior.
- Require the final clean-state check to account for the plan and log as
  intentional artifacts, committing them if the workflow requires them.
- Keep the multi-rig integration test as the decisive evidence artifact.

## 5. Roll-up: revised plan/tasks/subtasks

Add to reconciliation: after conflict resolution, inspect `git diff
origin/main...HEAD` and compare the helper's surrounding code with
`origin/main` before running tests. Add to submission: ensure plan/log status
is intentional, then run the formula guards from the recorded worktree.

## 6. Explicit no-change decisions

- No production SDK behavior or public contract changes.
- No change to multi-rig isolation expectations.
- No broad parser abstraction or new interface.
- No documentation page update.
- No test expectation weakening.

counter: 1

## Planning pass 2

### Critique

The revised plan is appropriately narrow and recovery-aware. The remaining
risk is operational: the branch is behind `origin/main`, and the plan must
ensure the current base is fetched before any rebase. It also needs an explicit
review that the helper still compiles under the current imports and that the
test name covers the actual failure mode.

### Critical evaluation of the critique

Those additions improve evidence without changing scope. The parser test alone
can miss a compile/import regression, while the integration test may be gated
by environment; a package compile/test check is a useful intermediate signal,
but not a substitute for the integration path.

### Roll-up: applied evaluation

The execution order now requires fresh fetch, conflict diff review, focused
compile/test, then end-to-end integration evidence. Any unavailable external
infrastructure must be recorded as a blocker rather than silently replaced by
the parser test.

### Roll-up: plan/tasks/subtasks

1. Fetch and record the exact `origin/main` base before rebasing.
2. Resolve only the known helper conflict and inspect imports, parser ordering,
   and test naming after rebase.
3. Run focused package tests/build and the multi-rig integration test or the
   configured affected gate; report infrastructure blockers distinctly.
4. Complete guarded self-review and refinery handoff.

### No-change decisions

Scope, architecture boundary, product expectation, and no-destructive-command
policy remain unchanged.

counter: 2

## Planning pass 3

### Critique

The plan now covers technical correctness and operational recovery. A final
gap would be accidentally handing off a branch whose metadata still points to
an old fork or rejection state, or leaving a stale local branch/worktree
identity after push.

### Critical evaluation of the critique

The formula's workspace and submit guards already define the authoritative
metadata behavior. The plan should rely on those guards rather than inventing
parallel bookkeeping, while explicitly checking their outcomes in the final
evidence summary.

### Roll-up: applied evaluation

Use the formula's existing worktree/branch/rebase metadata and self-review
guards. Do not close the implementation bead. Push only after the branch-shape,
clean-tree, test, and remote-head checks pass; then assign the open work bead to
Refinery with the producer commit recorded.

### Roll-up: final plan/tasks/subtasks

The final execution is: inspect -> fetch/rebase -> resolve and review -> run
focused and affected evidence -> record execution log -> guarded self-review ->
push/verify -> record metadata -> assign/wake Refinery -> drain.

### No-change decisions

No additional feature, abstraction, test weakening, role-specific behavior,
documentation page, or infrastructure restart is justified.

counter: 3

## Information-loss check after three passes

No material information was lost. The final plan still includes the original
acceptance truth, exact evidence layers, rejection-aware branch recovery,
architectural boundary, test sequence, support artifacts, stability rules,
parallel-safe discovery, and explicit no-change decisions.
