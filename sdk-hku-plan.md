# sdk-hku execution plan

counter: 0

## 1. Full plan, tasks, and subtasks

1. Establish the exact failure boundary.
   - Read the integration test, shard selection, parallel runner, and testing
     policy.
   - Inspect recent history and existing lifecycle/resource diagnostics.
   - Run the focused agent suspend/resume test repeatedly without test-cache
     reuse and capture process, port, and test-runner evidence.
2. Reproduce under the documented concurrent boundary.
   - Run `rest-full-1-of-8` concurrently with the neighboring rest-full shards
     using the repository runner and preserve the first failure.
   - Compare the failing shard's resources and logs with an isolated run.
   - Determine whether a runner/resource collision, supervisor lifecycle race,
     or test assertion is responsible.
3. Make the smallest justified correction, if any.
   - Preserve the real suspend assertion and its report-based outcome.
   - Change runner/test infrastructure only if evidence identifies a concrete
     ownership or scheduling defect; otherwise record a diagnosis and no code
     change.
4. Verify and hand off.
   - Re-run the focused owner and the affected loaded shard when feasible.
   - Run relevant quality gates for any changed code.
   - Append concise evidence to `agent-execution.log`, update the bead, and
     close it only when the acceptance condition is actually met.

## 2. Architectural changes

No production architecture change is planned initially. The observed layer is
the integration runner plus real REST/process/supervisor lifecycle. Any fix
must remain at the owning test-harness/resource boundary and must not leak
parallel-runner assumptions into session or supervisor production code.

## 3. Test plan

- Focused `TestE2E_SuspendResume_Agent` with `-count` and cache disabled.
- The exact `rest-full-1-of-8` shard in isolation.
- The documented eight-way `rest-full` concurrent load, preserving failure
  output and resource diagnostics.
- Relevant package tests and `go vet ./...` if source changes are made.

Evidence discipline: the focused test observes the real REST/process/
supervisor lifecycle but not concurrent shard contention. The loaded runner
observes the reported scheduling/resource boundary but does not by itself
identify the internal race. Passing isolated or repeated runs is not proof
that the loaded failure is fixed.

## 4. Support structures

Use existing shard scripts, test helpers, process/listener inspection, and
temporary on-disk logs. Add a diagnostic harness only if it materially exposes
the concurrent resource boundary and can be kept deterministic and bounded.

## 5. Documentation

Record diagnosis, exact commands, and evidence in the bead notes and execution
log. Update code comments or testing documentation only if the current
resource ownership contract is wrong or newly clarified.

## 6. Execution order

Plan and history audit -> focused baseline -> isolated shard -> concurrent
reproduction -> evidence correlation -> minimal fix or no-change diagnosis ->
verification -> bead handoff.

## 7. Stability strategy

Do not weaken the assertion, add retries, inflate lifecycle waits, or convert a
real process proof into a semantic proxy. Use bounded commands, preserve first
failure status, and inspect live processes/listeners rather than stale status
files. Do not clear the shared Go build cache.

## 8. Blocker avoidance

Keep all artifacts in this worktree or `/var/tmp`; do not touch unrelated
worktree files or personal tmux servers. If host capacity prevents the loaded
run, report that as an infrastructure limitation with the exact partial
evidence rather than claiming success.

## 9. Candidate parallel work

History archaeology, test/runner inspection, and environment/resource census
are independent. Focused and loaded test execution must be sequenced enough to
avoid confusing concurrent runs with the experiment being measured.

## 10. Proxy audit

- Target truth: under the documented parallel runner, suspend prevents the
  managed agent from restarting until resume, and resume permits the agent to
  restart.
- Required evidence layer: real REST request, controller/supervisor process,
  agent process, and report filesystem outcome under concurrent shard load.
- Useful but insufficient proxies: an isolated focused pass, a unit test of a
  suspend state transition, or a report-file check without process evidence.
- Tempting false completion: retrying the shard, extending the one-second
  assertion window, or treating a clean candidate/base isolated A/B as a fix.
- If the plan succeeds, the original bug could remain if the successful runs do
  not recreate the same concurrent load/resource collision; that residual risk
  must be stated explicitly.

## Planning pass 1

### Critique of the plan, top to bottom

- Tasks correctly preserve the reported loaded failure as the target, but the
  loaded command must use the same `LOCAL_TEST_JOBS` and environment policy as
  the documented runner.
- The architecture section properly keeps the investigation out of production;
  it should explicitly distinguish test-runner process ownership from the
  supervisor's managed process ownership.
- The test plan needs exact log paths and live resource snapshots so a failure
  can be correlated to another shard rather than inferred from timing.
- Support structures should prefer existing runner diagnostics before adding a
  new harness.
- Stability and proxy sections correctly reject retries and relaxed waits;
  they should also call out that a changed test expectation requires a changed
  product contract, which is absent here.

### Critical evaluation of that critique

The central risk is cross-shard process/resource interference, not a missing
unit assertion. The runner creates many real cities and subprocesses, so the
diagnostic must capture the shard log, process tree, listeners, and temporary
city paths at failure. A single concurrent run can distinguish whether the
failure is reproducible, but cannot prove causality; history and isolated
comparison remain necessary.

### Roll-up

Make the loaded runner's own artifacts authoritative for the experiment. Use
focused runs only as a lower-layer control. Do not modify the lifecycle test
until a concrete race or resource ownership defect is observed.

### No-change decisions

- No assertion change, retry, timeout inflation, or quarantine.
- No production session/controller change based on the existing A/B evidence.
- No dashboard, API, or OpenAPI work; those surfaces are unrelated.

counter: 1

## Planning pass 2

### Critique of the refined plan, top to bottom

- The experiment sequence is sound, but it should include a preflight for
  existing gc/supervisor processes and listeners without killing unrelated
  processes.
- The ownership boundary is clear; any cleanup must target only the test's
  known process group or city paths.
- The evidence plan should capture whether the observed “restarted” report was
  produced by the original process or a new process, if the harness exposes
  that identity.
- The handoff needs an explicit distinction between “reproduced,” “not
  reproduced,” and “blocked by host capacity.”

### Critical evaluation of that critique

Preflight is necessary because stale supervisors can create exactly the false
restart signal under a parallel run. It must remain observational unless a
known test-owned city is identified. Report content and process identity are
stronger evidence than elapsed time, while the public REST outcome remains the
contract that must be preserved.

### Roll-up

Add a read-only process/listener/CWD census before and after each loaded run,
and correlate any unexpected report with the test-owned city and process
identity. If the current helpers do not expose identity, record that limitation
instead of inventing a weaker assertion.

### No-change decisions

- Do not clean up by bare `tmux kill-server`, broad `pkill`, or stale PID/state
  file manipulation.
- Do not add process identity to the product contract solely for diagnostics.
- Do not treat a clean isolated run as resolution of the loaded failure.

counter: 2

## Planning pass 3

### Critique of the refined plan, top to bottom

- The plan now covers the actual boundary and safe diagnostics; it should state
  that any source change requires RED/GREEN evidence and the relevant fast
  gates.
- The support structure remains intentionally small and avoids a permanent
  harness unless repeated diagnosis proves it useful.
- The residual-risk statement is complete but must be included in the bead
  handoff even if no code changes result.

### Critical evaluation of that critique

No missing task or architectural boundary is exposed. The assignment is a
pre-existing load-sensitive integration failure with no justified product
change. The most likely valid completion is an evidence-backed diagnosis and
preserved assertion, unless the loaded run identifies a narrow harness defect.

### Roll-up

Proceed with the plan immediately under the claimed bead. Use the documented
runner first, preserve first-attempt failures, and make a small test-harness
fix only when the evidence identifies one. Apply RED/GREEN and quality gates
to any code change; otherwise close with the exact diagnostic result and
remaining risk.

### No-change decisions

- Do not change the product or integration assertion merely to make the shard
  green.
- Do not claim the race is fixed when only isolated tests pass.
- Do not leave a known diagnostic artifact or vague follow-up unrecorded.

counter: 3

## Lost-information check after three passes

Retained: the real lifecycle target, required loaded evidence layer, isolated
controls, process/resource ownership boundary, no-retry rule, safe cleanup,
RED/GREEN requirement for source changes, and explicit residual-risk handling.
No material information was lost during refinement. The claimed bead is the
approval to execute this plan immediately.

## Execution evidence

- Focused control: `CGO_ENABLED=0 go test -tags integration -count=1` for the
  exact target passed in 61.49s.
- Isolated `rest-full-1-of-8`: failed first in `TestGastown_PipelineMailChain`
  after 170.16s; it did not produce a suspend/resume result.
- Eight-way REST load: shard 1 included the exact target and reported only an
  unrelated `TestGastown_PipelineMailChain` failure; other shards showed
  unrelated pipeline/mail/bead timeouts and supervisor cleanup failures.
- Direct target under seven concurrent REST shards: passed in 52.260s.
- Repeated focused control: exact target passed 3x in 47.389s with
  `CGO_ENABLED=0`.
- No source or test expectation change was made. The loaded boundary remains
  unresolved: the target flake did not reproduce, while unrelated tests show
  host-load/resource starvation and supervisor cleanup instability.
- Controlled exact-target fan-out: eight concurrent copies all passed in
  82.247s–89.788s, versus 47.389s for the prior three-copy focused control.
  Concurrency increased lifecycle duration but did not reproduce the restart
  assertion failure.

- Fresh documented fan-out on 2026-09-12: `GC_PUSH_GATE_NO_CAP=1
  LOCAL_TEST_JOBS=8 CGO_ENABLED=0 ./scripts/test-local-parallel integration`
  launched 29 jobs with `inner_p=1` and failed across all eight REST-full
  shards. `rest-full-1-of-8` again failed only
  `TestGastown_PipelineMailChain` (30.09s): the mayor session remained
  `creating`, and cleanup reported the supervisor already stopped.
  Neighboring REST shards independently showed bead/session/mail timeouts,
  tmux server disappearance, supervisor termination, and a 20s REST request
  timeout; the formula/review and smoke jobs also failed under the same load.
  `TestE2E_SuspendResume_Agent` did not fail in this run. This is direct
  loaded REST/process/supervisor evidence of broad host-resource and lifecycle
  instability, not proof that the assigned restart race is fixed or identified.

- Fresh follow-up: a new eight-way integration fan-out was stopped before any
  REST shard because a corrected process census found sibling worktree
  `work/sdk-8xp` running `make test-cmd-gc-process-parallel`; its logs contain
  only the first compile wave and no target result. The exact target then
  failed once at the preserved suspend assertion and passed twice (one pass in
  30.930s). The first two runs reported the isolated supervisor already gone
  during cleanup; the third did not. Existing API routing and controller
  mutation-poke tests pass with `CGO_ENABLED=0`. This strengthens the
  load-sensitive diagnosis but still does not identify a deterministic restart
  race or justify a source/assertion change.

- Fresh post-load focused control: the exact target passed with
  `CGO_ENABLED=0 go test -tags integration -count=1 -run
  '^TestE2E_SuspendResume_Agent$' -timeout 5m ./test/integration` in 41.340s.
  This observes the real REST/process/supervisor path in isolation only.

- Witness follow-up on 2026-09-12: re-ran `gc hook --claim --json` and
  refreshed the `sdk-hku` lease. A broader preflight found another
  `test-local-parallel fast` wave and multiple `cmd/gc` shard processes already
  active on the shared host. The next full fan-out therefore was not a valid
  clean experiment: it was stopped before any `rest-full` shard started, after
  package-shard logs only. Its known escaped shard wrapper was terminated and
  no process referencing the run remained. This is host-capacity evidence, not
  target behavior; the assignment remains open and the assertion is unchanged.

- Fresh bounded fan-out on 2026-09-12: after a clean preflight,
  `GC_PUSH_GATE_NO_CAP=1 LOCAL_TEST_JOBS=8 CGO_ENABLED=0
  ./scripts/test-local-parallel integration` admitted the first eight package
  jobs for about 10 minutes but never admitted a REST-full shard. The run was
  stopped through its owned process-group trap; per-job logs are preserved at
  `/var/tmp/gc-sdk-hku-rest-full-20260912-194111` and contain zero REST-full
  logs. This is direct evidence of the loaded runner's package queue/resource
  boundary, not a suspend/resume result.

- Post-load focused control: the exact `TestE2E_SuspendResume_Agent` passed in
  55.054s with `CGO_ENABLED=0`, observing the real REST/process/supervisor
  lifecycle in isolation. It does not exercise the full concurrent matrix and
  does not prove the loaded restart race is fixed. No source or assertion
  change is justified.

- Fresh bounded eight-way REST cohort at 2026-09-12 22:31 PDT: launched
  `rest-full-1-of-8` through `rest-full-8-of-8` concurrently with separate
  logs under `/var/tmp/gc-sdk-hku-rest-cohort-20260912-223045`. Host load was
  already 11.04/12.85/16.69 and rose to about 79/43/29 while the cohort ran.
  Shard 1 failed `TestGastown_PipelineMailChain` after 396.760s; shard 2
  failed `TestGastown_PipelineGitCommitMerge`; shard 4 failed
  `TestGastown_PolecatPoolProcessing`; shard 6 failed mail/work/refinery
  flows; shard 7 failed pool/dog/graph/mail tests and recorded repeated
  `no tmux server` plus a Dolt dirty-table migration error; shard 8 failed
  convoy tracking. Shards 3 and 5 had not produced test results at the
  bounded stop. Cleanup repeatedly reported `supervisor is not running`.
  `TestE2E_SuspendResume_Agent` did not fail. The owned process group was
  stopped and a census found no cohort process remaining.

- Post-cohort exact control at 2026-09-12 22:40 PDT:
  `CGO_ENABLED=0 go test -tags integration -count=1 -run
  '^TestE2E_SuspendResume_Agent$' -timeout 5m ./test/integration` passed in
  30.901s. This observes the real REST/process/supervisor lifecycle in
  isolation only; it does not prove the loaded restart race is fixed. No
  source or assertion change is justified.
