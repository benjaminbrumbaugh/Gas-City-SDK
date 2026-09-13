# sdk-8xp repair plan

initial_counter: 0
counter: 3

## 1. Full plan, tasks, and subtasks

Goal: repair the existing `gc hook --claim` store-affinity implementation so
the branch passes repository boundary guards while preserving the requested
cross-store exclusion, diagnostic visibility, and empty-route semantics.

Tasks:

1. Load the rejection context and establish the exact failing surfaces.
   - Read `TESTING.md`, the current branch diff, and the boundary tests.
   - Identify whether the new metadata key and environment variable are
     legitimate public surfaces or should remain invocation-local.
2. Write or refine focused tests first.
   - Cover cross-store exclusion and diagnostic output at the hook layer.
   - Cover absent versus empty `gc.routed_to` semantics.
   - Keep same-store and existing assigned/class-route behavior as controls.
3. Implement the smallest repair.
   - Keep candidate store provenance in `cmd/gc` hook orchestration.
   - Register only durable metadata keys; avoid adding a new ambient env
     contract unless the runtime boundary genuinely requires it.
   - Preserve public ready JSON and generic bead types.
4. Verify and hand off.
   - Run focused tests, affected tests, vet, and the documented baseline as
     available; distinguish target-layer evidence from proxy evidence.
   - Commit to `polecat/sdk-8xp`, push and verify the remote tip, record
     producer metadata, reassign the open work bead to the refinery, and drain.

## 2. Architectural changes

The affinity guard remains at the CLI hook read-to-write boundary. The
claiming rig store is the only permitted mutation destination for ordinary
routed pool work; a candidate from another physical store is skipped before
claim and produces an explicit diagnostic. City-scoped and explicitly proven
class/assigned routes retain their existing semantics. Empty `gc.routed_to`
is treated exactly like an absent key: unrouted, with `gc.run_target` as the
only existing workflow fallback.

The repair must not leak physical store identity into the canonical bead wire
model or `gc ready` output. The metadata-key registry may be extended only for
metadata that is intentionally durable. Invocation-only source information
must stay in local typed state rather than an undocumented process environment
variable.

## 3. Test plan and evidence discipline

Target truth: a rig-scoped claim cannot mutate or serve a candidate from a
different physical store, and operators can see why it was skipped.

Required evidence: `cmd/gc` tests that exercise source-store selection,
claim-destination selection, mutation/non-mutation, returned work, and the
diagnostic. Route-state tests must compare absent and empty metadata through
the actual hook decision path. Boundary tests must prove metadata and env
surfaces remain intentionally declared.

Useful but insufficient proxies: route helper unit tests, bead-ID-prefix
comparisons, and tests that only observe no returned bead. They do not prove
the source and mutation stores differ. A false completion would be possible if
tests bypass the federated reader, infer residency from an ID, or assert a
diagnostic without checking claim destination; review will reject that result.

## 4. Support structures

- Reuse current in-process store fakes and hook claim seams.
- Keep the temporary `agent-execution.log` out of the commit.
- Use `git diff origin/main...HEAD` to bound the touched surface.
- No parallel implementation subtask: the source, claim, and test seams are
  tightly coupled and the branch is a rejected continuation.

## 5. Documentation

No user-facing documentation or dashboard changes are expected. Update only
the nearest CLI fixture/help if the final diagnostic is an established
operator-facing contract; do not expand the public ready schema.

## 6. Execution order

Read rejection and test guidance -> record baseline -> add/refine failing
focused tests -> repair implementation and boundary declarations -> format ->
run focused/affected/broader checks -> review diff and provenance -> commit,
push, verify, reassign to refinery, and drain.

## 7. Stability and blocker avoidance

Use the shared normal Go cache; never clear it or redirect it to `/tmp`. Avoid
live Dolt mutation and server restarts. Keep the implementation bead open for
refinery review. If a check fails, determine whether the failure is introduced
by this branch before changing expectations.

## 8. Pass-0 critique, top-to-bottom

The plan is appropriately narrow, but “ambient environment variable” could
be required by a subprocess boundary rather than accidental. The code review
must trace all readers before removing or renaming it. The route-state claim
must be tested through candidate discovery, not only a helper. Handoff must
preserve the authoritative branch and worktree metadata despite the rejected
continuation.

## 9. Critical evaluation of the critique

The critique correctly identifies the main risk: a boundary guard fix could
silently become a behavior change if it drops required subprocess context.
The right decision is evidence-driven: inspect the env reader/writer pair and
retain it only if a typed local path cannot cross the boundary. The tests must
observe the real layer and existing failure guards, not just satisfy strings.

## 10. Roll-up: apply evaluation to critique

Before implementation, inventory every use of the new key/env and run the
rejection tests. If the env is test-only or redundant, remove it and its
golden expectation; if production code requires it, declare and test the
contract at its owner boundary. Keep the affinity regression unchanged unless
the product contract is independently wrong.

## 11. Roll-up: apply revised critique to plan

The implementation task is limited to the smallest proven boundary repair.
The focused tests remain target-layer evidence, and boundary-test updates are
allowed only when they reflect the intentional current surface. Existing
behavior controls and the branch handoff contract remain mandatory.

## 12. No-change decisions

- Do not close `sdk-8xp`; the refinery owns closure.
- Do not modify Wayfinder, the shared rig checkout, dashboard code, or generic
  beads APIs.
- Do not weaken boundary tests or hide diagnostics to get a green baseline.
- Do not infer store identity from bead IDs or change public ready JSON.

## 13. Pass 1 (counter 1)

Plan review, top-to-bottom: the goal and task list are bounded to the rejected
boundary repair; the architecture section keeps provenance at the hook seam;
the evidence section requires both source and destination observation; support
and execution sections preserve the existing fakes and sharded gates; docs
and stability sections correctly avoid unrelated surfaces; no-change decisions
protect the implementation bead and upstream boundary.

Critique: the plan should explicitly require the guard to run before every
claim mutation, including existing-assignment and eligible-pool paths, and
should verify that rejected candidates cannot be returned as work.

Critical evaluation: this is a real gap in the initial plan wording, not a
contract change. Add the pre-mutation and returned-work assertions to the
focused test task; keep unannotated legacy candidates under the existing
single-store behavior unless current code proves otherwise.

Roll-up: the implementation and test plan now names the claim mutation seam,
the returned result, and the diagnostic as a single target-layer evidence
bundle. No other package needs to own store affinity.

No-change decision: do not add a generic store interface or alter beads
provider APIs for this one CLI boundary.

## 14. Pass 2 (counter 2)

Plan review, top-to-bottom: the revised task list covers both production and
boundary fixtures; the architectural placement remains local; the evidence
criteria distinguish physical provenance from IDs and route labels; support,
docs, execution, and stability remain proportionate; the no-change list still
prevents scope drift.

Critique: the environment and metadata guard surfaces can fail independently
of the functional tests. The plan should require explicit registry/golden
checks and should call out host-toolchain failures without weakening guards.

Critical evaluation: this is necessary because boundary tests observe
repository-wide contracts rather than only hook behavior. The checks belong
in preflight and final verification, while ICU or unrelated lint failures
must be reported as environmental/baseline evidence rather than repaired in
this branch.

Roll-up: retain the metadata declaration and environment baseline only where
the current implementation owns those surfaces; run focused tests, fast
baseline, vet, and the documented process shards; classify failures by layer.

No-change decision: do not change expected output merely to make a test pass;
only declare a surface when it is intentionally part of the current contract.

## 15. Pass 3 (counter 3)

Plan review, top-to-bottom: the goal, tasks, architecture, evidence, support,
docs, execution, and stability sections now cover the complete repair and
handoff; the three-pass refinement preserves the source/destination proof;
the no-change section keeps the boundary clean.

Critique: the remaining operational risk is handoff after rebase: the branch
must retain the per-bead name, remote tip must be verified, and the stale
rejection reason must not be carried into refinery review.

Critical evaluation: this risk is independent of code correctness and is
resolved by the final metadata/push gate. Force-with-lease is appropriate only
after confirming the remote branch still points at the known pre-rebase tip.

Roll-up: final execution includes clean status, branch-name and worktree
metadata verification, remote SHA verification, stale-rejection cleanup, and
open reassignment to the refinery. The implementation bead remains unclosed.

No-change decision: do not touch the unrelated unreferenced branch observed
by the branch audit; only the authoritative `polecat/sdk-8xp` branch is in
scope.
