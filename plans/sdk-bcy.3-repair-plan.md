# sdk-bcy.3 Repair Plan

counter: 3

## 1. Full plan / tasks / subtasks

Objective: repair the current-base delivery branch so each execution-launch
receipt produces an immutable delivery item whose source ID and projected
session/execution evidence refer to the same launch, and so persisted v3
`OutcomeID` values are verified against the canonical projection bytes.

Tasks:

1. Preserve the existing PR #68 and prior branch/ref lineage; establish a
   compliant per-bead repair branch from the latest `origin/main` without
   rewriting or force-pushing existing refs.
2. Add RED tests before implementation:
   - two launches for one decision must not let launch A carry launch B's
     session/execution evidence;
   - a v3 payload with a shape-valid but recomputed-invalid `OutcomeID` must be
     rejected;
   - uppercase payload/outcome digests must be rejected when the producers'
     contract requires lowercase canonical hex.
3. Implement the smallest routingdecision-only repair:
   - project launch delivery from the exact receipt being recorded, not a
     mutable "latest launch" query;
   - centralize canonical v3 outcome-ID derivation and recompute it during
     delivery validation;
   - enforce lowercase canonical digest spelling at the delivery boundary.
4. Run focused routingdecision tests, race coverage, affected tests, vet, and
   the repository's required API/dashboard gates. Preserve generated artifacts
   only when their source/gate requires them.
5. Commit, push, verify the remote head, update durable bead metadata, and
   hand the branch to the refinery. Do not close the implementation bead.

## 2. Architectural changes

Ownership remains in `internal/routingdecision`; no new registry, outbox, event
framework, crypto authority, or provider dependency is introduced. The
delivery projection boundary will receive one authoritative launch receipt,
which is already durably committed before projection begins. The generic
projection helper remains reusable for a supplied receipt slice, but the
launch-delivery caller will not re-select mutable state from all launches.

The v3 outcome ID is a content address over the canonical wire projection with
its `OutcomeID` field blanked, matching the producer's existing construction.
Validation will apply the same function rather than only checking prefix/shape.
Digest validation will share the existing lowercase `validDigest` invariant.
The repair must preserve legacy v2 delivery behavior, idempotent replay, causal
receipt persistence when projection fails, and the existing acknowledgement
contract.

## 3. Test plan and evidence discipline

Target truth: durable delivery item `source_id`, session ID, execution ID,
observed time, outcome ID, and payload bytes must all describe the exact
execution-launch receipt that caused the item. A shape-valid delivery is not
enough evidence; the target layer is the routingdecision store and its typed
wire validation, not merely a fake HTTP client or a passing unit helper.

Tests:

- Red then green store-level regression covering two distinct launch receipts
  for one decision and asserting each pending item maps to its own receipt.
- Red then green validator regression for a mutated v3 projection whose
  `OutcomeID` retains the right shape but not the canonical digest.
- Red then green canonical lowercase digest regression.
- Existing launch replay/projection-failure, ack, pending-page, legacy, API,
  and CLI tests remain unchanged unless the contract is independently wrong.
- Run focused package tests first, then race tests for routingdecision, then
  affected/full project gates. Passing tests prove the observed layers only;
  they do not prove a live external provider or product dashboard.

Proxy audit for this pass:

- Required evidence layer: same-package store/validator tests plus race tests;
  API/CLI tests are boundary confirmation only.
- Useful but insufficient proxies: direct projector tests, static diff review,
  or a single-launch happy path.
- False completion to avoid: accepting a recomputed-invalid ID, or asserting
  only that one pending item exists while source/outcome fields are swapped.
- Residual bug after apparent success: concurrent launches could still be
  selected by a helper outside the tested call site, or historical persisted
  items could bypass validation; inspect every producer and stored-item path.

## 4. Support structures

- `plans/sdk-bcy.3-repair-plan.md` records decisions and planning refinements.
- `agent-execution.log` records completed formula subtasks with timestamp and
  overall percentage.
- Existing PR #68, `polecat/sdk-bcy.3-currentbase`, and prior candidate refs
  remain preserved; the repair branch is additive and current-base only.
- No subagent tool is exposed in this session. Candidate parallel review work
  is documented for the independent exact-byte reviewer: inspect source/ID
  canonicalization and inspect branch/ref/remote integrity separately.

## 5. Documentation

Update comments only where they currently claim latest-launch selection or
under-specify canonical ID validation. Do not alter product/dashboard docs or
restate the historical PR narrative in source. If the public schema changes,
regenerate through the existing source/gate; this repair should not change the
wire shape.

## 6. Execution order

1. Complete planning passes and verify no existing worktree is accidentally
   modified.
2. Reuse the current-base source worktree, record it in bead metadata, and
   create the compliant repair branch while preserving old refs.
3. Add and run the RED regressions; record the expected failures.
4. Implement the minimal fix and run focused GREEN tests.
5. Review the diff and boundary invariants; run race, vet, affected tests, and
   required dashboard/API gates.
6. Commit, push, verify exact remote SHA, set metadata, reassign to refinery,
   and drain.

## 7. Stability and blocker avoidance

- Never force-push, delete/overwrite existing PR branches, mutate deployment
  state, or touch shared rig-root source.
- Do not run `go clean -cache`; use the shared on-disk cache and documented
  sharded targets. Keep temporary build/cache paths off `/tmp`.
- If the exact branch-name handoff gate conflicts with a preserved local ref,
  preserve that ref under an explicit archival name and document the immutable
  commit, or escalate before any destructive ref mutation.
- If tests expose a pre-existing unrelated failure, record it and continue
  only when the affected gate is independently clean; otherwise escalate with
  evidence.

## 8. Candidate subagent-parallel work

If parallel agents become available, assign one read-only reviewer to prove the
receipt/source association across all launch-delivery call sites and another to
audit canonical v3 ID/digest validation and exact-byte replay. Neither may
modify files or refs; their findings are inputs to the same branch-local tests
and implementation.

## Planning pass 1: critique

1. Scope: the plan correctly isolates the rejected high-severity finding and
   the two secondary identity findings, but branch handling needs an explicit
   proof because the recorded branch suffix is not the strict per-bead shape.
2. Architecture: passing a one-element receipt slice is minimal, but a helper
   that silently trusts an unpersisted receipt could weaken authority; the
   implementation must retain durable-commit ordering and validate receipt
   identity where needed.
3. Tests: the two-launch test must observe the real store path, not just call
   the projector with a hand-built slice. It also needs deterministic
   timestamps/identity fields so a false pass cannot hide source swapping.
4. Validation: lowercase digest enforcement should use the existing shared
   predicate; v3 outcome-ID validation must compare canonical bytes, not just
   recompute a second representation with `OutcomeID` still populated.
5. Operations: no branch deletion is allowed merely to satisfy a formula
   gate; any ref preservation decision must be recorded before mutation.

## Planning pass 1: critique of critique

The critique identifies the highest-risk boundaries. It should also require a
test that mutates a canonical v3 field while leaving the original ID and a test
that changes only hex case, because those distinguish recomputation from shape
validation. It should clarify that `ProjectProducerExecutionOutcome` may keep
its multi-launch behavior for callers that intentionally request a latest
projection; only the authoritative launch-delivery producer must bind one
receipt. Finally, the plan must explicitly inspect all `RecordExecutionLaunch`
callers before changing code so no producer is missed.

## Planning pass 1: roll-up

Add an all-caller inventory, deterministic mutation tests, and an explicit
receipt-persistence check to the implementation and verification steps. Treat
branch preservation as an invariant with evidence, not a convenience.

## Planning pass 1: applied changes

- Add caller inventory to implementation review.
- Add source/output mutation and uppercase digest cases to tests.
- Require the store-level test to prove launch receipt persistence before
  validating the pending delivery projection.
- Require branch/ref evidence before any branch-name accommodation.

## Planning pass 1: no-change decisions

- Keep delivery schema and public API unchanged.
- Keep producer construction outside generic lifecycle transition.
- Do not add a new abstraction solely for this repair; one small canonical ID
  helper is justified because construction and validation already duplicate a
  protocol rule.

## Planning pass 2: critique

The revised plan is appropriately narrow. The remaining concern is whether a
two-launch scenario can be created through public store APIs without violating
the execution-incarnation fence; if not, the regression may need a same-package
transaction fixture that writes only already-validated receipts. That fixture
must still exercise `recordExecutionLaunchDeliveryTx`, and its truth claim must
be labeled as store projection coverage rather than concurrency scheduling
proof. Also verify that current `validDigest` already rejects uppercase before
editing the predicate.

## Planning pass 2: critique of critique

This correctly separates semantic projection proof from scheduler/race proof.
The final plan should use public APIs when possible, otherwise explain the
fixture's authority boundary and add a race test around the public launch path.
It should also avoid broad generated-file churn because the wire shape is
unchanged.

## Planning pass 2: roll-up

Use the strongest available public-store setup, supplement with a narrow
same-package fixture only if required, and label each test by observed layer.
Confirm existing lowercase behavior before making code changes. Keep generated
artifacts untouched unless a gate proves they are required.

## Planning pass 2: applied changes

- Test design now distinguishes direct projection proof, store persistence
  proof, and race/scheduling evidence.
- Validation work begins with source inspection of `validDigest` and all launch
  delivery callers.
- Generated API/dashboard output is explicitly out of scope unless derived
  gates require regeneration.

## Planning pass 2: no-change decisions

- No API schema or CLI contract changes.
- No change to latest-selection semantics for generic projector callers.
- No deployment, provider probe, or external service mutation.

## Planning pass 3: critique

The plan now covers source, tests, operation, and evidence boundaries. It must
still prevent a subtle regression where receipt A is passed to projection but
the decision audit timestamp is mutable and changes the serialized outcome on
retry. The implementation should preserve the existing exact-replay guard and
tests should assert payload bytes remain identical after lifecycle changes.
The plan should also require `go vet ./...` and the repository's pre-commit
hook before handoff, with no claim that focused tests prove full product
correctness.

## Planning pass 3: critique of critique

The final risks are valid and should be converted into explicit acceptance
criteria: receipt-specific identity, stable replay bytes, recomputed v3 IDs,
lowercase digests, no public shape changes, clean branch/ref handoff, and the
required quality gates. The existing replay test already covers much of the
byte stability; extend only if the new regression does not exercise it.

## Planning pass 3: roll-up

Add the acceptance criteria to self-review and preserve the existing replay
test unless a focused gap remains. Require the pre-commit hook configuration,
`go vet`, affected tests, and any mandated dashboard/API gate before push.

## Planning pass 3: applied changes

- Added exact acceptance criteria to the handoff checklist below.
- Added stable-replay verification to the test plan.
- Added pre-commit and vet verification to execution order.

## Planning pass 3: no-change decisions

- Do not rewrite PR #68 history or force-push any existing branch.
- Do not close the implementation bead; refinery owns merge/close.
- Do not claim independent review completion; handoff records the repaired
  source and test evidence for the required independent reviewer.

## Final acceptance checklist

- [ ] Current-base repair branch has only the intended additive commits.
- [ ] RED tests failed on the original implementation before the fix.
- [ ] Launch A cannot carry launch B's source/outcome/session evidence.
- [ ] v3 `OutcomeID` is recomputed from canonical projection bytes.
- [ ] Uppercase digest spellings are rejected.
- [ ] Existing exact replay and projection-failure behavior remains stable.
- [ ] Focused tests, race tests, affected/full gates, vet, and pre-commit pass.
- [ ] Remote branch SHA is verified; bead metadata and refinery handoff are
      updated without closing the work bead.
