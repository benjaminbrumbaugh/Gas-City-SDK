# sdk-3no REST-full shard 2 diagnostic plan

counter: 3

## Scope and target truth

Determine whether the three reported failures reproduce on the exact current
`origin/main` baseline and whether they are also present on the candidate
revision named by the bead. Record evidence at the REST/full-integration
process boundary, distinguish product failures from harness/environment
failures, and avoid changing production behavior unless the evidence shows
this branch owns a defect.

## Initial full plan

1. Load assignment context and verify the worktree/branch contract.
2. Run the configured preflight or the smallest documented REST-full shard-2
   command on the clean baseline; capture exit status and failure details.
3. Inspect test helpers, recent history, and candidate/base revisions only as
   needed to establish provenance; do not infer root cause from one run.
4. If a branch-owned defect is demonstrated, add a smallest-owner test first,
   implement the narrow fix, and retain one real integration proof. If the
   failures are pre-existing, produce a concise diagnostic artifact and update
   the existing bead rather than filing a duplicate or weakening tests.
5. Re-run the focused evidence, then run required quality checks for any code
   or test changes. Review the diff and keep the branch clean.
6. Push the per-bead branch, record verification metadata, and hand the bead to
   the refinery without closing the implementation bead.

### Tasks and subtasks

- T1 context: confirm `sdk-3no`, `origin/main`, branch, worktree, and clean
  starting tree.
- T2 baseline: execute shard-2 evidence with bounded cleanup diagnostics;
  capture each test's status and the layer observed.
- T3 attribution: compare the exact baseline with the candidate and inspect
  `git diff`/history for path ownership; classify each failure separately.
- T4 implementation decision: no code change unless attribution is proven;
  otherwise add RED test, make narrow GREEN change, refactor only within scope.
- T5 verification: focused tests, applicable shard, `go vet`/build as needed,
  diff review, and clean-tree check.
- T6 handoff: commit with rationale, push and verify remote head, update bead
  metadata, reassign to refinery, drain.

### Architectural changes

Expected: none. This is an evidence/attribution assignment. Any production
change must remain behind the existing runtime/integration boundary and be
justified by a failure that differs between base and candidate. Do not move
REST-full assertions into a cheaper layer merely to make the shard pass.

### Test plan

- Primary proof: the actual `rest-full-2-of-8` integration target, with the
  exact failing test functions selected when possible.
- Provenance proof: same target on clean `origin/main` and candidate state,
  with separate process/environment cleanup observations.
- If code changes: RED focused owner test, GREEN focused test, affected tests,
  relevant shard, and required build/vet gates.
- A passing rerun alone is not sufficient evidence; retain failure status,
  revision, and cleanup state.

### Support structures

Use the existing integration harness and documented sharded runner. Add no
new abstraction or harness unless a missing diagnostic seam is the only way to
distinguish the reported boundary. Keep `agent-execution.log` temporary and
append one line after each completed subtask.

### Documentation and handoff

Update the work bead with the concise finding and evidence commands/results.
If a durable follow-up is needed, search for an existing symptom bead first;
create one only for a genuinely distinct root cause. Do not edit product or
embedded dashboard documentation for this server-side diagnostic.

### Execution order

T1 -> T2 -> T3 -> T4 -> T5 -> T6. T2 and T3 can use independent read-only
inspection where useful, but attribution must combine the same revisions and
the same target layer before any implementation decision.

### Stability and blocker avoidance

Use bounded commands and the repository's shard targets; do not add sleeps,
retries, or assertion weakening. Query live process/port state before blaming
the product. If Dolt is slow, collect the prescribed non-fatal diagnostics
before escalation. If the target is unavailable after safe checks, record the
exact environmental blocker and escalate to Witness rather than guessing.

### Candidate parallel work

Read-only history/test-helper inspection and runner-target discovery are
independent candidates. No parallel write or concurrent process run is planned
because shared tmux/Dolt resources could corrupt attribution.

## Proxy audit

- Target truth: whether the reported failures are reproducible and owned by the
  candidate revision, plus the concrete layer responsible when evidence allows.
- Required evidence layer: the real REST/full-integration process composition,
  with revision identity and cleanup/process state captured separately.
- Cheaper useful-but-insufficient proxies: unit tests, harness source review,
  a single rerun, or a semantic test preview. They can narrow hypotheses but
  cannot prove end-to-end provenance.
- Tempting false completion: changing expected values, adding waits/retries,
  running only a passing unit package, or calling a candidate-only failure
  proof of ownership without a clean baseline comparison.
- If the plan fully succeeds, the original bug could still remain if the
  environment masks it or if the observed process boundary does not expose the
  lower-level propagation/convergence cause; the handoff must say that limit.

## Planning pass 1 — counter 1

### 1. Full plan/tasks/subtasks

The initial sequence is retained: establish identity, reproduce on the clean
baseline, compare candidate/base provenance, decide whether implementation is
authorized by evidence, verify, then hand off. T2 is split into independent
observations for the two environment-report tests and the pipeline test, while
T3 remains the integration point for attribution.

### 2. Critique, top to bottom

- Scope is appropriately diagnostic, but the candidate revision is not yet
  identified by durable metadata and must not be guessed from `HEAD`.
- The plan says “configured preflight” without naming how to discover the
  command; runner documentation and Make targets must be inspected first.
- Test plan can overrun the shared environment if all eight shards are run;
  only shard 2 and exact functions are needed for this bead.
- No architecture change is expected, but adding a report file would itself
  become a tracked diagnostic surface and need justification.
- The current proxy audit correctly rejects a single passing rerun, but it
  should explicitly capture whether supervisor/Dolt/tmux cleanup is healthy.
- Handoff must carry evidence in bead notes because no code commit may be
  produced for a pure pre-existing finding.

### 3. Critical evaluation of that critique

The critique improves provenance and resource discipline without changing the
diagnostic objective. “Candidate” can be the currently claimed branch only if
the branch's base and HEAD are recorded; the bead's description names `sdk-zta`
as prior evidence, not necessarily a checkout to mutate. A same-branch baseline
run is sufficient to establish non-ownership when it is the exact `origin/main`
tree. Cleanup state is an observation, not a root-cause conclusion.

### 4. Roll-up applied to critique

Use `origin/main` as the authoritative baseline and record the claimed branch
SHA separately. Discover the exact shard command before running it. Capture
cleanup/process evidence as a separate column from test outcomes. Avoid adding
files solely to store output; bead notes and the temporary execution log are
enough unless reproducibility requires a fixture.

### 5. Revised tasks/subtasks

- T1.1 record branch/HEAD/base and clean tree.
- T2.1 discover the canonical shard-2 command from `TESTING.md`, Makefiles, and
  CI scripts.
- T2.2 run the exact failing functions on baseline where the runner supports it;
  otherwise run the canonical shard once, with bounded diagnostics.
- T3.1 compare status and failure signatures against the bead's candidate
  evidence; do not treat different symptoms as one cause.
- T3.2 inspect changed paths/history only after the observed result is recorded.
- T4-T6 remain conditional on attribution and follow the original plan.

### 6. Explicit no-change decisions

No production code, test expectation, test retry, dashboard path, or new
abstraction is justified by the plan alone. No duplicate bug bead is created
until the symptom search is complete.

### 7. Proxy audit

Target truth remains ownership/provenance at the real REST-full boundary. The
required evidence now includes revision SHAs and live cleanup/process status.
Unit tests and source review remain useful but insufficient. The main false
completion risk is declaring “pre-existing” from an unclean or stale checkout;
the exact baseline SHA and process diagnostics prevent that. A fully successful
diagnosis still cannot identify the underlying environment or convergence cause
without a lower-level owner proof.

## Planning pass 2 — counter 2

### 1. Full plan/tasks/subtasks

The plan now prioritizes command discovery and a bounded baseline reproduction.
If the canonical target starts its own supervisor/Dolt/tmux resources, run only
the required shard and collect cleanup output. Then compare the claimed branch
to `origin/main`; if the same failure occurs at the same function/signature on
both, treat this assignment as attribution/documentation, not a fix.

### 2. Critique, top to bottom

- Identity checks are strong, but `origin/main` may move during the run; its SHA
  must be captured immediately before baseline execution.
- The phrase “same function/signature” is too strict for a timeout whose emitted
  details vary; the invariant should be the same contract violation.
- Running tests on the claimed branch after baseline can be contaminated by
  processes from the baseline; cleanup must be verified between runs.
- Bead notes should state what evidence proves, what layer it observes, and what
  it does not prove, matching the project's evidence discipline.
- The done path must still push a commit, so a pure diagnostic needs a small
  reviewable artifact or an explicit no-op commit if no tracked change is made.

### 3. Critical evaluation of that critique

Capturing the baseline SHA makes the comparison reproducible even if the remote
advances. Contract-level equivalence is safer than exact log matching for
asynchronous failures, provided the concrete observed details are retained.
Resource cleanup is part of test validity. The mandatory push contract makes a
no-op branch awkward, but a diagnostic plan file is a legitimate reviewable
artifact if it records the analysis and does not masquerade as product proof.

### 4. Roll-up applied to critique

Capture `BASE_SHA` and `CANDIDATE_SHA` before each run, use the repository's
cleanup/status checks between runs, and record exact outputs in bead notes while
summarizing contract-level equivalence in the plan. The plan itself is the
small, reviewable diagnostic artifact; it must clearly label observations and
limits.

### 5. Revised tasks/subtasks

- T1.2 append baseline/candidate SHA and test-command identity to the log.
- T2.3 verify supervisor, Dolt, tmux, and listener state before/after the shard;
  collect prescribed Dolt diagnostics only if Dolt is actually unhealthy.
- T3.3 classify exact failures as same contract violation, different symptom,
  or inconclusive; never collapse timeout causes without evidence.
- T4.1 update this plan/bead notes as the diagnostic artifact if no source fix
  is proven; otherwise follow TDD and add only the owning test/change.
- T5-T6 retain affected checks and mandatory remote verification.

### 6. Explicit no-change decisions

Do not run a full local sweep when the assigned evidence is one REST shard. Do
not restart Dolt or kill broad tmux servers. Do not claim supervisor absence is
the root cause merely because cleanup reports it.

### 7. Proxy audit

The target truth is still candidate ownership. Required proof is now a clean,
SHA-pinned real-boundary comparison with resource health context. Cheaper
proxies remain non-authoritative. False completion includes an exact-looking
log match from a stale process and treating a no-op branch as evidence without
the plan/bead artifact. Even success leaves the lower-level cause unresolved;
that limitation is intentional and will be explicit.

## Planning pass 3 — counter 3

### 1. Full plan/tasks/subtasks

Final execution order: use the clean per-bead branch; record a three-line
execution log entry for workspace setup (done), discover the shard target, run
the baseline with bounded/targeted evidence, inspect the candidate and history,
write the minimal diagnostic artifact or TDD fix, run affected quality gates,
commit, push, verify origin, and reassign to refinery.

### 2. Critique, top to bottom

- The final plan must not assume a candidate checkout beyond the assigned
  branch; it should state the candidate SHA as the branch under review.
- “Bounded/targeted” must use available repository helpers rather than a custom
  timeout command that may not exist on macOS.
- The plan artifact itself is not proof of REST behavior; its claims must cite
  command output/status captured in the bead notes.
- A diagnostic-only commit may be rejected if it changes no source behavior,
  so the commit body must explain why the artifact is the assigned deliverable.
- The handoff must preserve the implementation bead open and route it to the
  refinery; only formula step beads may be closed.

### 3. Critical evaluation of that critique

These are handoff and evidence-boundary safeguards, not scope changes. The
claimed branch is a valid candidate only as a reproduction context; ownership
is established by comparison to the captured base. Repository helpers and
portable bounds reduce instrumentation errors. A plan artifact is honest only
when it distinguishes the artifact's documentation layer from the observed
integration layer.

### 4. Roll-up applied to critique

Proceed with no speculative source fix. Use portable commands and existing
runner helpers. Keep the plan concise and label all conclusions as observed,
attributed, or unresolved. If the test cannot run because the environment is
unavailable, escalate rather than create a “pass” artifact.

### 5. Revised tasks/subtasks

1. Finish T1 by confirming metadata, branch, base SHA, and clean state.
2. Finish T2 by locating and running shard-2 evidence; record process/cleanup
   state and explicit exit status.
3. Finish T3 by comparing current branch and baseline, then searching existing
   beads/history for matching symptoms.
4. Finish T4 with either a minimal evidence-only plan/bead update or RED/GREEN
   source change; do not modify expectations for pre-existing failures.
5. Finish T5 with applicable tests, vet/build as required, review, and clean
   state; append the execution log after each subtask.
6. Finish T6 with a detailed commit, remote push verification, metadata update,
   refinery assignment, and drain.

### 6. Explicit no-change decisions

No new role logic, API surface, integration abstraction, retry policy, test
skip, dashboard edit, or Dolt restart is authorized by this diagnostic. No
follow-up bead is created unless search proves a distinct root cause or a
durable fix owner is missing.

### 7. Proxy audit

Target truth: reproducibility and attribution of the three reported failures.
Required layer: actual REST-full shard 2 plus SHA-pinned baseline/candidate and
resource-state evidence. Useful proxies remain source review, unit tests, and
single reruns; none substitutes for the boundary proof. False completion is a
green rerun, changed expectation, or plan-only claim without bead evidence. If
everything succeeds, an environment-sensitive bug or deeper pipeline cause may
remain; the final handoff will state that the diagnostic establishes
pre-existence only, not root-cause repair.

## Evidence collected

This section records observations, not a substitute for the observed test
boundary:

- Baseline revision: `origin/main` and the claimed branch both resolved to
  `4b59013e33eabb0da2535c76e531d5bb15f9e596`.
- Baseline command: `GO_TEST_TIMEOUT=20m ./scripts/test-integration-shard
  rest-full-2-of-8`; it selected 21 tests and exited 1 after 539.751s.
- Baseline failures: `TestE2E_MultiAgent_Independent` omitted
  `CUSTOM_ROLE` for alpha/beta/gamma; `TestE2E_EnvVars_Custom` omitted
  `CUSTOM_FOO=bar` and `CUSTOM_BAZ=qux`; and
  `TestGastown_PipelineGitCommitMerge` timed out with bead `gc-68` still
  `open`, assignee `polecat`.
- Baseline cleanup: `gc supervisor stop: supervisor is not running`.
- Candidate revision: `origin/polecat/sdk-zta` at
  `da788188c8bd9934033ab64b972d45789b352704`; its diff from `origin/main`
  contains only `internal/mail/exec/exec_test.go`,
  `internal/mail/exec/mcp_conformance_test.go`, and `sdk-zta-plan.md`.
- Candidate targeted rerun, with the runner's ICU flags, reproduced the two
  environment failures with the same missing values. A candidate-only rerun of
  `TestGastown_PipelineGitCommitMerge` exited 1 after 85.810s with bead `gc-33`
  still `open`, assignee `polecat`; cleanup again reported the supervisor was
  not running. An earlier three-test candidate run stopped at a different
  startup symptom, so it is retained as an environmental variation rather than
  treated as an identical signature.
- Attribution: the real REST/full-integration process boundary reproduces all
  three reported contract violations on both revisions, and the candidate's
  changed paths do not overlap their integration tests or runtime code. This
  supports “not introduced by the `sdk-zta` commit itself.” The candidate
  branch is also six commits behind `origin/main`, so this A/B result does not
  prove byte-for-byte equivalence of every ancestor; it does not identify the
  underlying environment propagation or pipeline convergence causes.
- Fast baseline gate: `make test-fast-parallel` passed command-gc shards 1 and
  2 but failed shard 3 at `TestOrderDispatchExecManagedDoltCoercesInCityRuntimeDirForControlTraceDefault`
  (`order_dispatch_test.go:2939`) after its 10-second order-exec wait. This
  observes the unit/CLI test layer, is unrelated to the three REST-full
  failures, and does not justify changing source or expectations in this
  diagnostic branch.

## Plan approval and execution decision

Three refinement passes completed; no material information was lost. The plan
is approved for autonomous execution under the assigned polecat formula. The
assignment's scope is evidence and attribution, so implementation proceeds
only if the baseline/candidate comparison proves a branch-owned defect.
