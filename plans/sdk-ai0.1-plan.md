# sdk-ai0.1 candidate-review repair hardening plan

counter: 0

## Planning pass 0

### 1. Full plan, tasks, and subtasks

1. Recover the rejected implementation branch onto `origin/main`.
   - Resolve the formula conflict by retaining convoy source binding and the
     repair-token/current-worktree safeguards.
   - Record the new fork point only after the branch is a clean descendant.
2. Audit the candidate-review repair path against the acceptance criteria.
   - Inspect the order, formula, worker, wrapper, tests, and embedded pack
     bytes together; use history to identify intended behavior.
   - Map every mutation and trust boundary: source bead, path, symlink, Git
     worktree, branch, staging, push, gate execution, claims, locks, retries.
3. Implement missing fail-closed protections test-first.
   - Add focused tests for control characters, symlink escapes, eligibility
     before mutation, unique CAS claims, writer locking, trusted worktree and
     source branch binding, declared-path staging, fast-forward-only publish,
     mandatory operator gates, and one bounded stale-repair attempt.
   - Keep human/external holds and real reset/provider state outside the test
     mutation surface.
4. Keep fork-owned behavior isolated.
   - Update only candidate-review example sources, tests, and the corresponding
     gascity-packs asset bytes or pin metadata required by the acceptance gate.
   - Avoid generic SDK role logic, commercial policy, or unrelated pack code.
5. Verify exact source and active-pack bytes independently, then run focused
   and repository quality gates.
6. Commit a cohesive fix, push `polecat/sdk-ai0.1`, record evidence, and hand
   the bead to the refinery without closing the implementation bead.

### 2. Architectural changes

- The candidate-review order remains the policy selector; Go remains a
  mechanical transport/mutation worker and does not decide actionability.
- The formula supplies an explicit convoy-derived source identity and a unique
  order claim token; the worker validates both before mutation.
- The worker owns fail-closed repository boundaries: canonical paths, symlink
  rejection, trusted current formula worktree, declared source branch, a
  repository-wide writer lock, exact staging, normal fast-forward publication,
  and bounded retry evidence.
- Operator-owned configured gates remain mandatory inputs; the worker must not
  silently substitute or skip them.

### 3. Test plan and evidence discipline

- Target truth: only a selected mechanical hold may be repaired exactly once,
  safely, and resubmitted; all other holds and real provider/reset state remain
  untouched.
- Required evidence layer: focused worker/order tests exercising actual shell,
  Git, filesystem, and beads boundaries; exact-byte comparison of source and
  active pack assets; then affected/full Go gates and vet.
- Useful but insufficient proxies: pure path helpers, unit-only eligibility
  tests, a successful semantic preview, or a passing test fixture that does not
  execute the target worker against a real temporary Git repository.
- False-completion substitution to avoid: treating updated expectations or a
  clean formula preview as proof that actual candidate repair is safe.
- If the plan succeeds but the bug remains, the likely gap is an untested
  external mutation edge (real symlink/path, stale CAS claimant, concurrent
  writer, non-fast-forward push, or active-pack byte mismatch), so each gets a
  direct harness assertion.

### 4. Support structures, docs, and execution order

- Support: temporary repositories/worktrees, isolated beads store fixtures,
  fake gate commands, and deterministic claim/hold fixtures only.
- Docs: update the candidate-review example/formula comments only if behavior
  changes; no generic architecture docs unless the current contract is wrong.
- Order: rebase/conflict resolution -> inspect existing diff/history -> write
  failing tests -> implement -> focused tests -> exact-byte review -> broader
  gates -> commit/push/handoff.

### 5. Stability strategy and blocker avoidance

- Preserve the existing branch and rejection context; never rebuild from a
  parallel mechanism.
- Keep real provider/reset state out of tests; use temporary repositories and
  synthetic mechanical holds.
- Use documented sharded test targets, never `go clean -cache`, and avoid
  shared-fleet contention by running focused checks first.
- If an external pin or remote permission remains unavailable, capture the
  exact evidence and escalate rather than weakening the boundary.

### 6. Candidate subagent-parallel work

- Independent read-only archaeology of prior candidate-review commits and
  active pack bytes.
- Independent review of worker/formula boundary coverage.
- Independent focused test execution after implementation.

### Critique of pass 0

- The plan must distinguish source code correctness from active-pack
  installation and exact-byte identity; these are separate evidence layers.
- The task may include existing corrected commits, so avoid duplicating or
  rewriting proven work unnecessarily.
- “Full gates” can be noisy in this shared fleet; record whether failures are
  target failures or infrastructure contention without masking target failures.
- A formula conflict is a source-boundary risk and must be resolved before any
  implementation conclusions are trusted.

### Critical evaluation of the critique

- The critique correctly catches byte-installation and test-layer gaps, but it
  must also require checking branch ancestry and producer commit identity so a
  later handoff cannot silently ship a different patch.
- It should explicitly reject changing tests merely to fit current behavior.
- It should require a review of cleanup and untracked generated files so no
  foreign paths enter the staged patch.

### Roll-up: apply evaluation to critique

- Add branch ancestry/producer identity checks to the verification gate.
- Require every expectation change to be justified by a changed contract or an
  independently wrong test.
- Include staged-path and generated-artifact cleanup in self-review.

### Roll-up: apply revised critique to plan

- Add a final diff review against `origin/main`, explicit producer-commit
  tracking, and a staged-path audit before push.
- Record why each test expectation changed, if any.
- Treat exact active-pack bytes and source bytes as independently reviewable
  outputs, not as one artifact.

### No-change decisions

- Do not introduce a new generic SDK abstraction; this is pack-owned behavior.
- Do not add role-name logic or decision heuristics to Go.
- Do not replace the real worker with a mock-only proxy.
- Do not mutate real holds, provider state, or reset state for evidence.
- Do not close the implementation bead; refinery owns closure.

## Planning pass 1

counter: 1

### 1. Full plan, tasks, and subtasks

Execute the refined pass-0 plan with the branch-identity, exact-byte, and
staged-path checks treated as release gates. First finish the rebase, then
review the current diff before writing only genuinely missing tests.

### 2. Architectural changes

Keep the source/order/formula/worker boundaries intact. Any required pin or
asset change must live at the pack boundary and be independently attributable.

### 3. Test plan and proxy audit

Run tests at the layer they observe: shell/Git/filesystem harnesses for worker
mutation, formula/order tests for binding and retry, byte comparison for pack
activation, and Go tests/vet for compilation and package contracts. A passing
unit test alone cannot prove safe publication or active-pack installation.

### 4. Support structures, docs, and execution order

Use existing fixtures and scripts first. Add a harness only where it exercises
an otherwise unobservable boundary. Keep documentation changes adjacent to the
formula contract and avoid generated-doc edits by hand.

### 5. Stability and blocker avoidance

Rebase conflicts are resolved from current `origin/main` plus the rejected
branch's intent; do not blindly choose ours/theirs. Run focused tests before
shared broad gates. Preserve all evidence in the execution log and bead notes.

### 6. Candidate subagent-parallel work

Parallel review is useful only for read-only inspection and independent test
execution; implementation and branch mutation stay serialized in this
worktree.

### Critique of pass 1

- The refined plan still needs a concrete inventory of current modified files
  before deciding what is missing.
- It should explicitly verify the required operator gate list and stale attempt
  budget in both source and active bytes.
- It should define the no-op outcome if the corrected commits already cover the
  requested work.

### Critical evaluation of the critique

- These are execution-discovery requirements, not reasons to broaden scope.
- Inventory can be satisfied by current diff/history and source/asset hashes.
- A no-op is acceptable only if exact-byte review, focused tests, and the
  acceptance scenario are independently demonstrated.

### Roll-up: apply evaluation to critique

- Add a source/asset hash inventory and operator-gate/attempt-budget matrix.
- Permit no-op implementation only when all target evidence is already present
  and branch ancestry is clean; otherwise add the smallest missing correction.

### Roll-up: apply revised critique to plan

- Before implementation, record current diff, source hashes, active asset hashes,
  and the gate/budget matrix in the execution log or review notes.
- Use the acceptance scenario as the deciding evidence, not code volume.

### No-change decisions

- Do not broaden to unrelated candidate-review features.
- Do not weaken retries, path checks, or gate requirements to accommodate noisy
  infrastructure.
- Do not rewrite history merely to make the branch look smaller.

## Planning pass 2

counter: 2

### 1. Full plan, tasks, and subtasks

Proceed with the refined plan. Resolve the current formula conflict, inspect
the rebased implementation, fill only evidence gaps, run the synthetic stale
mechanical-hold replay once, verify untouched human/external holds and provider
state, run required gates, then submit through the refinery contract.

### 2. Architectural changes

No architectural expansion beyond the explicit candidate-review pack boundary;
the final patch must remain easy to drop or rebase independently of upstream.

### 3. Test plan and evidence discipline

The acceptance test must observe actual mutation and resubmission state. The
exact-byte review must compare both corrected source and the active pack bytes.
Focused tests prove local behavior; broad gates prove integration/build health;
neither alone proves the other layer.

### 4. Support structures, docs, and execution order

Use the temporary execution log for subtask evidence, keep any new fixture
minimal and deterministic, and run formatting/static checks before the final
diff audit.

### 5. Stability strategy and blocker avoidance

Stop and escalate only for a repeated external blocker after preserving
diagnostics. Do not push or reassign until branch shape, remote head, clean
tree, and producer identity all verify.

### 6. Candidate subagent-parallel work

After the implementation is stable, independent review of exact bytes and
focused tests may run in parallel, but final branch mutation and submission
remain serialized.

### Critique of pass 2

- The plan is complete only if the final report names what each evidence
  artifact proves and does not prove.
- It must preserve the user-required temporary execution log and append after
  every completed subtask.
- It should explicitly confirm no known cleanup remains before handoff.

### Critical evaluation of the critique

- These requirements improve handoff correctness without changing the design.
- The final report can remain concise while linking the evidence to layers.
- Cleanup must include worktree status, generated files, and stale rejection
  metadata, but not destructive cleanup of shared unrelated worktrees.

### Roll-up: apply evaluation to critique

- Add evidence-layer statements and a final cleanup checklist to the handoff.
- Treat the execution log and git/bead state as required submission artifacts.

### Roll-up: apply revised critique to plan

- Before push, verify the log includes each completed subtask, the tree is clean,
  only declared paths are staged, rejection metadata is resolved, and no
  unrelated shared worktree is touched.

### No-change decisions

- Do not run destructive global cleanup.
- Do not claim active-pack installation from source tests alone.
- Do not claim product correctness from semantic previews or changed tests.

## Post-pass information-loss check

The three passes preserve the original acceptance criteria, the requested
candidate-review boundary list, the branch/refinery contract, test-first work,
proxy audit, and cleanup requirements. Refinement narrowed implementation to
the smallest pack-owned evidence-backed delta; no requirement was dropped.

counter: 3
