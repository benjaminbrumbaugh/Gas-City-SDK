# sdk-9h2 plan

counter: 0

## Objective

Resolve the refinery-reported baseline failure for
`TestSyncConfiguredDoltPortFilesWarnsOnRigPortFileRewrite` without weakening
the Dolt port-file warning contract or masking the host toolchain failure.
Confirm whether `cmd/gc/json_runtime_schema_test.go` still needs the
CWD-independent schema resource fix; current `origin/main` already contains
that fix and its spaced-CWD regression coverage. If the reported failure is
not reproducible at the owning layer, ship a concise baseline diagnosis with
no production-code change.

## Full plan, tasks, and subtasks

1. Establish evidence.
   - Verify the assigned bead, branch/worktree metadata, and clean
     `origin/main` base.
   - Compare the current schema validator and spaced-CWD test with the known
     `sdk-dsi` fix and confirm whether the change is already on the base.
   - Reproduce the warning test with the available fast toolchain and run the
     default command to distinguish product failure from environment failure.
2. Decide and implement the smallest safe outcome.
   - Preserve the existing `gc://schemas/...` resource identity and warning
     assertions; do not alter expectations merely to make a gate green.
   - Make a source change only if a controlled comparison proves the fix is
     absent or the warning contract regressed on this base.
   - Otherwise retain no production delta and record the no-change decision in
     this plan and the bead handoff note.
3. Verify owning evidence.
   - Run the JSON schema spaced-CWD contract and Dolt warning contract with
     `CGO_ENABLED=0`, which observes cmd/gc behavior without the unavailable
     ICU CGo dependency.
   - Run the default focused command to document the build-layer
     `unicode/regex.h` blocker if it remains.
   - Review the final diff for accidental test weakening, unrelated changes,
     and branch/worktree provenance.
4. Hand off.
   - Append the completed-subtask evidence to the temporary
     `agent-execution.log`.
   - Commit the required plan/diagnostic artifact if it is the only
     task-owned change, push `polecat/sdk-9h2`, preserve the implementation
     bead as open, and reassign it to the refinery.

## Architectural changes

No architectural or production-code change is planned. The owning boundaries
remain the cmd/gc test contract for in-memory JSON Schema resources and the
cmd/gc lifecycle writer for operator-visible port-file drift warnings. Do not
move this diagnosis into API, beads, or generic schema infrastructure, and do
not add an abstraction for a test-only resource identifier.

## Test plan and evidence discipline

Target truth: a checkout path containing spaces must not cause the JSON schema
validator to perform a filesystem lookup for an in-memory schema, and a stale
rig port file must be rewritten while emitting `WARN`, the rig label, and both
old/new ports. The required evidence layer is the owning `cmd/gc` package test
process, run with the host-compatible CGo setting; the default command also
observes the build/toolchain layer and is not product-behavior evidence when
compilation stops at `unicode/regex.h`.

Useful but insufficient proxies are source-text comparison alone, a schema
URL unit assertion without the real spaced-CWD command, or a warning-string
test that does not verify the rewrite. The tempting false completion is to
remove the warning assertions, change the expected output, or install a
filesystem fallback that hides the relative-resource bug. If this plan fully
succeeds, the original bug could remain if another validator constructs a
relative resource, if another lifecycle caller drops the warning writer, or
if a future base no longer contains the known fix; final source comparison and
both focused owning tests constrain those gaps.

## Support structures

Use the existing cmd/gc tests, `git diff`/history comparison, and the temporary
`agent-execution.log`. No new production seam, fake, fixture, dependency, or
live Dolt mutation is needed. Keep all work in the bead-scoped worktree and
leave unrelated polecat-home files untouched.

## Docs

No user-facing documentation or API/schema output changes are expected. This
plan records the baseline diagnosis, evidence layers, and explicit no-change
decision required for a pre-existing/environment failure.

## Execution order and stability strategy

Read assignment -> compare history/base -> focused RED/evidence -> decide
no-change or make the narrow source fix -> focused compatible tests -> default
build diagnosis -> diff/provenance review -> commit/push/refinery handoff.
Use `CGO_ENABLED=0` only to isolate cmd/gc behavior; report it as a narrower
behavioral proof, not as proof that the default host toolchain is healthy.
Do not rewrite or reset unrelated branches, and do not claim a product fix
from a compile failure or a source comparison alone.

## Blocker avoidance and parallel candidates

Independent read-only work can be parallelized: history/source comparison,
focused test commands, and repository-gate/config inspection. The
implementation decision and final diff review remain sequential. If the
default build still fails on the missing ICU header, record it as an
environment-layer blocker and preserve the passing compatible test evidence;
do not change Dolt code or test expectations to compensate.

## Pass 1 critique (top-to-bottom)

- The objective distinguishes the reported warning failure from the already
  merged schema-path fix, but it must explicitly prove both current-base
  files before deciding no source change.
- The task list follows TDD evidence discipline, though this is a baseline
  diagnosis and a source RED result may be impossible if the base already
  contains the repair.
- The architecture section correctly prevents leakage into generic layers;
  it should name the test/process and lifecycle writer as separate owners.
- The evidence section states what each test observes and what CGo failure
  cannot prove, but must avoid treating `CGO_ENABLED=0` as equivalent to the
  default quality gate.
- The handoff must preserve the implementation bead open and record the
  precise no-change/environment result for refinery verification.

## Pass 1 critique evaluation

The plan needs a source-to-base comparison table in the execution record:
schema resource identity, spaced-CWD setup, and warning writer plumbing. The
focused commands must be named separately as behavioral pass versus build
environment refusal, and the final artifact must not imply a production fix
when the base already has one.

## Roll-up: revised critique applied

Task 1 now requires direct comparison of both files and known fix history.
Task 3 explicitly separates compatible behavioral evidence from default
toolchain evidence. Task 4 records the no-source outcome and keeps refinery
ownership of the open implementation bead.

## Roll-up: revised critique applied to tasks/subtasks

Add a final diff audit that verifies the only task-owned artifact is the plan
or a narrowly justified schema test change, and that no warning expectation,
production lifecycle path, or unrelated polecat-home change was touched.

## Explicit no-change decisions

- Do not modify Dolt lifecycle production code when the warning test passes in
  the compatible test environment.
- Do not weaken or delete `TestSyncConfiguredDoltPortFilesWarnsOnRigPortFileRewrite`.
- Do not duplicate the already-present `gc://schemas/...` fix or spaced-CWD
  regression test.
- Do not add an ICU workaround, vendored header, or dependency change in this
  bead; that belongs to the separate toolchain baseline owner.
- Do not touch the shared rig checkout, another polecat branch, or live Dolt
  state.

## Pass 2 critique (top-to-bottom)

- The plan now identifies the exact source claims to compare and the separate
  behavioral/build evidence layers.
- The no-change path is appropriately preferred only after the owning tests
  pass; the plan still permits a source fix if a fresh comparison finds drift.
- The architecture boundary is clear and avoids speculative interfaces or
  generic test helpers.
- The test plan guards against false completion by retaining both schema and
  warning outcomes, while accurately labeling the CGo-disabled limitation.
- The final handoff accounts for the formula rule that refinery, not the
  polecat, closes the implementation bead.

## Pass 2 critique evaluation

No material architecture gap remains. The remaining risk is accidental
shipping of an artifact that claims to fix behavior already present on the
base. The final source diff, commit message, bead note, and `gc.work_outcome`
must all say whether this is a no-source baseline record or a narrowly proven
test correction.

## Roll-up: revised critique applied

Strengthen the final review task with a three-way check: current base versus
known fixed commit, focused compatible test result, and default build result.
Use that check to choose `no-op` only when the branch contains no source
delta; if the required plan artifact is committed, describe it as diagnostic
evidence rather than product remediation.

## Roll-up: revised critique applied to tasks/subtasks

Task 4 now requires the handoff summary to name the three-way evidence result,
the environment blocker, and the preserved implementation contract. No
additional test or production change is justified by the current evidence.

## Pass 3 critique (top-to-bottom)

- The objective is bounded to the reported baseline and refuses an unrelated
  Dolt or ICU change.
- The tasks preserve the smallest owning proofs and make the no-source case
  explicit without skipping verification.
- The architecture boundary and no-change list prevent test infrastructure or
  provider assumptions from leaking into production code.
- The evidence section distinguishes semantic behavior, source comparison,
  and compilation environment, so a passing substitute cannot be overstated.
- The handoff is actionable: commit only task-owned evidence, push the
  per-bead branch, and leave closure to refinery.

## Pass 3 critique evaluation

After three passes, no material gap remains. Before editing any source, verify
that the exact schema resource identity, spaced-CWD regression setup, warning
writer plumbing, and default ICU failure have not been lost or conflated.

## Final roll-up and approval state

The plan is approved for autonomous execution under `mol-polecat-work`. The
next action is the final read-only three-way evidence check, followed by the
documented no-source handoff unless that check finds a real regression.

## Verification record

- `CGO_ENABLED=0 go test ./cmd/gc -run '^(TestCmdWorktreeEnsureDryRunJSONContract|TestSyncConfiguredDoltPortFilesWarnsOnRigPortFileRewrite)$' -count=1`: PASS.
- `CGO_ENABLED=0 go vet ./...`: PASS.
- `EXTRA_TEST_ENV='CGO_ENABLED=0' make test-fast-parallel`: cmd/gc shards PASS; unit-core reached terminal failure only on pre-existing `sdk-edl` formula legacy-`bead_id` drift and resource-census drift tracked by `sdk-75c`/`sdk-qdm`.
- Default focused `go test` and `go vet` stop before package tests at the host toolchain error `unicode/regex.h` missing; this is the separate `sdk-dqa` environment failure, not Dolt warning behavior.
- `origin/main` and known fixed commit `edda4e007` have identical `cmd/gc/json_runtime_schema_test.go` and `cmd/gc/cmd_worktree_test.go` content; no source change is justified by this bead.

counter: 3
