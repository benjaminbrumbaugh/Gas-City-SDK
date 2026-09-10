# sdk-gez supervisor rollback-plist/readiness baseline

counter: 2

## Objective

Repair or precisely characterize the host/integration harness baseline for the
pre-existing supervisor failures assigned by `sdk-gez`. Keep product changes
out of scope unless evidence shows the harness contract itself is broken in
this branch. Leave a reproducible record on the bead and preserve the
upstream-alignment boundary.

## Plan, tasks, and subtasks

1. Load the assignment and establish a clean, per-bead worktree.
   - Preserve the pre-existing dirty polecat home.
   - Record managed-worktree provenance on the bead.
   - Identify the exact launchd/service registrations and rollback paths.
2. Reproduce the reported failures with targeted tests and capture layer-aware
   evidence.
   - Separate supervisor readiness failure from test/application behavior.
   - Inspect live process/service state and generated plist locations without
     broad or destructive cleanup.
   - Compare the harness expectations with the current supervisor adapter.
3. Repair the smallest correct harness boundary if the failure is repairable.
   - Prefer an existing script or historical fix over a new mechanism.
   - Keep launchd-specific behavior in launchd/integration harness code.
   - Do not weaken readiness assertions or replace real supervisor evidence
     with a semantic proxy.
4. Verify and hand off.
   - Run targeted tests, then the documented relevant shard(s), vet, and any
     required generated checks.
   - Record failures that remain host-only or require unavailable authority.
   - Commit and hand the branch to the refinery; never close the work bead.

## Architectural changes

Expected: none to generic SDK orchestration. If required, changes are confined
to the supervisor/launchd integration harness boundary and must preserve live
state discovery, rollback safety, and readiness as an observable contract.

## Test plan

- Target the four reported integration tests individually first.
- Add or update a harness test only when the harness contract changes or an
  existing expectation is independently wrong; record that reason.
- Run the documented integration shard(s), plus `go vet ./...` and the fast
  unit baseline when code changes.
- Evidence discipline: a targeted test proves the asserted harness/application
  layer only; service inspection proves registration/process state only; a
  passing semantic preview does not prove launchd readiness or screen behavior.

## Support structures and docs

- Use a temporary diagnostic capture under `/var/tmp` and the existing
  supervisor/test scripts where available; do not add persistent status files.
- Keep this plan as the decision record. Update the bead with concise evidence
  and any host-only limitation.
- Touch no architecture or user docs unless the supported harness contract is
  changed.

## Execution order

1. Verify branch/worktree and assignment metadata.
2. Inspect history and harness entry points.
3. Reproduce targeted failures.
4. Repair or document the host baseline.
5. Re-run evidence and quality gates.
6. Commit, push, reassign to refinery, and drain.

## Stability and blocker avoidance

- Never use bare `tmux kill-server`, broad recursive deletion, or launchd
  mutation against an unverified service target.
- Query live process/service state instead of creating PID/lock/status files.
- Treat missing rollback artifacts as a failed recovery invariant, not as
  permission to overwrite an unrelated registration.
- If repair needs external credentials or host-only authority, escalate with
  the exact command, target, and evidence; continue with safe read-only checks.

## Candidate parallel work

- Independently inspect supervisor registration/rollback code and the targeted
  integration-test setup after the initial reproduction.
- Independently search history for prior rollback-plist/readiness fixes.
- Keep edits serialized at the final harness boundary so evidence and cleanup
  remain coherent.

## Planning pass 0: initial critique

- Objective: clear and bounded, but “repair” must not imply that a host-only
  mutation is authorized or safe.
- Tasks: the sequence covers reproduction, boundary identification, repair,
  and handoff; it must explicitly compare the branch against upstream before
  editing shared behavior.
- Architecture: correctly isolates launchd, but should require proof that the
  failing test reaches the intended service before interpreting assertions.
- Tests: names the four tests and distinguishes evidence layers; it should
  include a no-code conclusion path where the bead is updated without a
  product patch.
- Support/docs: temporary captures and no status files are appropriate; the
  plan itself must not become a substitute for bead notes.
- Execution/stability: safe, but the repair step needs a rollback plan and a
  prohibition on mutating registered services whose rollback target is absent.
- Parallel work: useful and bounded; no parallel edits to the same harness.

## Critical evaluation of the critique

The critique correctly identifies the main risk: treating a launchd repair as a
normal code change could destroy unrelated host state. It also catches two
missing gates: upstream comparison and a no-code evidence path. The test-layer
concern is material because these failures may be blocked before the test body.
The rollback concern should be made operational by requiring a dry-run or
explicit ownership proof before any mutation. No section needs broader scope.

## Roll-up: revised critique

The plan is sound after adding three explicit decisions: first prove the
failure layer, compare relevant code/history before editing, and treat missing
rollback files as a fail-closed condition. The no-code path is a valid result
for this assignment if the host cannot be safely repaired from the repository.

## Roll-up: revised plan decisions

- Add an upstream/history comparison before any edit.
- Add a fail-closed service-ownership gate before mutation.
- Treat a reproducible host-only diagnosis plus bead evidence as a complete
  implementation outcome when no repository-owned repair is justified.

## No-change decisions

- No generic orchestration, role, worker, or event abstractions will be added.
- No readiness assertion will be relaxed.
- No persistent host status file, PID file, or lock file will be introduced.
- No user-facing documentation change is planned unless the supported contract
  is demonstrably changed.

## Planning pass 1: full-plan refinement

### Critique, top to bottom

- Objective: correctly prioritizes diagnosis and safe scope, but should name
  the four reported test names so the reproduction target cannot drift.
- Tasks: provisioning is already complete; the live next step is targeted
  reproduction, followed by service-state inspection and history comparison.
  The repair task should distinguish repository-owned fixture setup from
  machine-wide launchd state.
- Architecture: the boundary is correctly narrow. It should explicitly avoid
  editing `cmd/gc` behavior merely because the test invokes `gc init`.
- Tests: targeted tests are sufficient to locate the failure layer, but the
  planned shard should only be run after the baseline is repaired or recorded.
  A passing test that skips readiness would be false completion.
- Support/docs: the plan file and bead notes are complementary. The temporary
  capture must not be committed.
- Execution: upstream comparison needs exact paths and history search terms;
  final handoff must include whether the bead is a code fix or host repair.
- Stability: fail-closed mutation guidance is right, but missing rollback
  plists require an explicit “do not install/overwrite” gate.
- Parallel work: history and test-entry inspection can proceed independently
  after the baseline command is captured, but shell mutation remains serial.

### Critical evaluation of the critique

These refinements reduce ambiguity without expanding scope. Naming all tests
protects against completing only the first failure. Separating fixture setup
from machine-wide state is essential because launchd registrations are outside
the repository and may belong to other cities. The shard sequencing prevents a
long noisy run from hiding the first failing layer. The “do not overwrite” gate
is stronger than a generic rollback warning and is necessary when the rollback
file is itself missing.

### Roll-up: apply evaluation to critique

The investigation will begin with the exact four tests and capture their raw
exit/status evidence. It will then inspect the test harness and launchd
adapter/history, proving ownership before considering any mutation. If no
repository-owned repair exists, the complete result is a host-baseline finding
with reproducible evidence, not a weakened test or speculative code patch.

### Roll-up: apply revised critique to plan/tasks/subtasks

1. Reproduce `TestPersonalWorkFormulaCompileAndRun`,
   `TestE2E_WorkspaceDefaults`, `TestE2E_MultiAgent_PoolAndFixed`, and
   `TestGastown_PipelineHumanToWorker` individually, recording the first
   failing layer and command exit status.
2. Inspect launchd registration and rollback paths plus the repository test
   harness; compare the relevant files and history to `upstream/main`.
3. Apply a repair only if the target is repository-owned and its rollback and
   service ownership are proven. Missing rollback files are a hard stop for
   overwrite/install operations.
4. Verify with the same tests and the documented shard, or record the exact
   host blocker and leave the bead with evidence for an authorized operator.

### No-change decisions for pass 1

- Do not change `cmd/gc` production behavior based solely on a launchd setup
  refusal.
- Do not delete or regenerate a registered plist without proving its owner,
  source, and recoverable backup location.
- Do not call a semantic test or config parse a readiness proof.

## Planning pass 2: final critical review

### Critique, top to bottom

- Objective: actionable and aligned with the assignment; “repair” remains
  conditional on ownership and recoverability.
- Tasks: exact test coverage, history comparison, and a no-code outcome are
  now explicit. The task list should also require checking for partial changes
  from the interrupted prior turn before edits.
- Architecture: the supervisor/launchd boundary is protected, and generic SDK
  paths are excluded. Any harness patch must have a focused test at the same
  boundary.
- Tests: the evidence-layer distinction is complete. The final run should
  state whether each failure is pre-test setup, test body, or post-test cleanup.
- Support/docs: sufficient. No new diagnostic artifact is justified until the
  existing scripts and logs are inspected.
- Execution: safe and ordered. The handoff must preserve the pre-existing dirty
  home and commit only changes in the managed bead worktree.
- Stability: covers destructive actions and status-file avoidance. A host
  repair that cannot be persisted in this repository must be reported rather
  than faked as a code fix.
- Parallel work: remains safe only for read-only inspection; make this
  restriction explicit.

### Critical evaluation of the critique

The final critique is consistent with the project’s evidence discipline and
does not invent a requirement for a product patch. The added interrupted-turn
check is important because the home worktree is known dirty. Distinguishing
setup/body/cleanup failures makes the final evidence useful to whoever owns the
host. Restricting parallel work to reads avoids races around service state.

### Roll-up: apply evaluation to critique

Before edits, record the managed worktree status and leave the dirty home
untouched. Use existing harness diagnostics first. Any change must be small,
owned by the harness boundary, and tested at that boundary. The final bead
note will state truth proven, layer observed, and what remains unproven.

### Roll-up: apply revised critique to plan/tasks/subtasks

- Add an interrupted-turn/status check to task 1.
- Add boundary-focused test coverage as a prerequisite for any harness patch.
- Classify every observed failure as setup, body, or cleanup evidence.
- Keep all parallel work read-only; serialize service mutations and git edits.

### No-change decisions for pass 2

- No additional abstraction or recovery service will be introduced.
- No test expectation changes unless the contract is independently proven
  wrong.
- No broad shard rerun will be treated as a repair when the first setup gate
  still fails.

## Plan approval and execution note

The three planning passes are complete. The plan is approved for execution by
the explicit instruction to run the claimed formula immediately; no additional
approval pause is required. Counter is 2 and all implementation edits remain
inside this bead worktree.

## Execution evidence

- Baseline shard: all six `rest-smoke` tests initially failed in `gc init`
  because isolated launchd registrations had no recoverable rollback plist.
- Root cause: the integration launchd shim returned success for registration
  probes, so production correctly entered its fail-closed missing-rollback
  path. The fixture had no explicit service-manager ownership contract.
- Repair: add the exact `GC_SUPERVISOR_SERVICE_MANAGER=none` opt-out to the
  integration and acceptance environments, with a production bare-child path
  and focused unit coverage. Keep the non-zero shims as a backstop.
- Passing evidence: focused `cmd/gc` tests, acceptance helper tests,
  integration environment contract test, and `make check-docs`.
- The intentional new environment read and source file were also banked in
  the checked `internal/testenv` vocabulary and resource-census ledgers; both
  focused baseline tests pass after regeneration.
- Remaining evidence: post-fix E2E setup no longer emitted the rollback-plist
  refusal. The pool and pipeline E2E assertions still fail downstream, and
  the Dolt-backed formula/graph waits block in `bd show` subprocess reads;
  those results do not prove or disprove supervisor readiness.
