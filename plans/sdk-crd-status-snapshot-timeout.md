# sdk-crd status-snapshot timeout flake plan

counter: 0
status: planning
work_bead: sdk-crd
base: origin/main

## Pass 0 — full plan

### Objective and target truth

Determine whether `TestStatusSessionSnapshotKillsBdChildOnTimeout` is a
product defect, a test defect, or a pre-existing parallel-load flake, then
ship the smallest maintainable repair. The target truth is that a status
snapshot read which exceeds its request budget returns promptly and does not
leave the real `bd` child process running. The test must preserve both timeout
and child-cleanup assertions.

### Tasks and subtasks

1. Establish the baseline.
   - Confirm the exact `origin/main` revision and current diff surface.
   - Run the focused API test repeatedly with test caching disabled.
   - Run the focused test under the documented parallel integration shard.
   - Capture whether failures are assertion failures, fixture startup races,
     process-scheduling delays, or infrastructure failures.
2. Trace the owning boundary.
   - Read `statusSessionSnapshot`, scoped-store resolution, command execution,
     and process-group cancellation paths.
   - Compare the failing API call site with the analogous CLI/status tests.
   - Inspect history for the timeout fix and prior flake repairs before
     inventing a new mechanism.
3. Write the smallest RED proof if the current test does not isolate the
   reported race; otherwise reproduce the existing RED failure first.
   - Keep real process signaling as the required evidence layer.
   - Avoid replacing the child-process assertion with a fake or longer sleep.
4. Implement the narrow repair.
   - Prefer deterministic readiness/termination synchronization or an existing
     process-test helper over timing inflation.
   - Keep generic SDK code free of test-only scheduling assumptions.
   - Touch `cmd/gc/controller.go` only if the evidence proves its lifecycle
     behavior is the owner; otherwise keep the fix in the API/test boundary.
5. Verify and hand off.
   - Run focused tests, affected package tests, and the relevant integration
     shard with uncached results.
   - Run `go vet ./...` and repository-required hooks/gates proportionally.
   - Commit only the cohesive fix, push `polecat/sdk-crd`, record metadata,
     and reassign the open implementation bead to the refinery.

### Architectural changes

Expected: no new abstraction. If a seam is required, it must be a narrow
existing process-execution or cancellation boundary with at least two real
consumers. Do not move process policy into generic status code merely to make
the test easier. Preserve the layering rule that process I/O remains at the
runtime/provider edge and status code consumes typed results/errors.

### Test plan and evidence layers

| Evidence | Layer observed | Proves | Does not prove |
| --- | --- | --- | --- |
| Focused unit/integration test with fake `bd` + PID | real subprocess/process-group boundary | timeout returns and child is killed | full supervisor behavior under fleet load |
| Repeated focused run (`-count=10`, cache disabled) | same real boundary | local repeatability | representative parallel scheduler contention |
| Parallel integration shard | real package/process composition | behavior under repository load | all CI hosts or every provider |
| `go vet ./...` and affected tests | compile/static/test contracts | no local static/test regressions | product correctness beyond covered paths |

Cheaper fakes and unit seams are useful diagnostics, but cannot replace the
real process proof. A semantic preview or a passing isolated test is not screen
capture or parallel-load evidence. The tempting false completion is to loosen
the assertion, add a fixed sleep, retry until green, or only run the focused
test. The original bug could remain if the test passes only when no competing
processes are scheduled.

### Support structures

Reuse `processgrouptest` and existing PID-file helpers. Add a reusable helper
only if the same readiness/termination race is present in at least two tests;
otherwise keep the change local. Preserve diagnostics that identify the child
PID, elapsed duration, and last observed process state.

### Documentation

No product documentation change is expected. If the repair changes the
normative test policy or requires a durable waiver, update the appropriate
testing documentation and record the reason, owner, replacement proof, and
expiry. Do not document a pre-existing flake as fixed without fresh evidence.

### Execution order

Baseline and history → plan refinement → RED reproduction → minimal fix →
focused and parallel verification → static/quality gates → commit/push →
refinery handoff. No implementation begins before baseline evidence and the
planning passes are complete.

### Stability strategy

Use notification/readiness facts instead of elapsed sleeps where the fixture
can expose them. Bound all black-box waits, preserve cleanup on assertion
failure, and keep test resources per-test (`t.TempDir`, isolated fake `bd`,
process-group cleanup). Treat every first-attempt flake as a defect signal,
not as permission to retry it into green.

### Blocker avoidance

Use the existing sharded runners from `TESTING.md`; do not use `go clean
-cache`, shared `/tmp` caches, or broad process cleanup. If Dolt or external
credentials become relevant, collect the prescribed diagnostics and escalate
without restarting shared services. If requirements remain ambiguous after
history and tests, escalate to the witness with the evidence bundle.

### Candidate parallel work

If parallel agents are available, split independent read-only work into:

- reproducibility matrix: focused versus parallel shard runs;
- process-boundary audit: command runner, cancellation, and process-group
  helper history;
- candidate diff audit: `cmd/gc/controller.go` and related fixtures.

Their findings must be reconciled against the same target truth before any
code change is accepted.

## Pass 0 critique — section-by-section

- Objective: correctly distinguishes product behavior from test stability, but
  should explicitly require comparison against exact baseline and candidate
  revisions because the bead calls the failure pre-existing.
- Tasks: covers reproduction, history, implementation, and handoff; it risks
  assuming a code fix is required even if the flake is infrastructure-only.
- Architecture: appropriately avoids a speculative abstraction, but should
  require evidence before touching `cmd/gc/controller.go`.
- Tests/evidence: correctly names the real-process layer and proxy limits; it
  should include cleanup verification after assertion failure, not only normal
  completion.
- Support/docs: appropriately conservative; helper extraction needs a strict
  reuse threshold.
- Execution/stability: sound, but the order must include the exact baseline
  comparison before any candidate rerun.
- Blockers/parallel work: useful and bounded; no subagent output may substitute
  for target-boundary evidence.

## Pass 0 critique evaluation

The critique identifies the main risk: treating a reported flake as an
automatic production-code task. It also exposes that the plan needs a clear
no-change outcome and a before/after comparison. It does not reveal an
architectural gap requiring a new layer. The plan should therefore make
classification (production defect, test defect, or environment flake) an
explicit gate and retain the real-process assertion in every outcome.

## Pass 0 roll-up

Apply the classification gate to the objective, baseline task, architecture
decision, and handoff criteria. Add exact baseline/candidate comparison and a
recorded no-change decision. Keep the process-group proof mandatory even when
the repair is test-only.

## Pass 0 no-change decisions

- No new product primitive or role/config behavior.
- No dashboard/API wire change; OpenAPI and dashboard gates are out of scope
  unless the implementation unexpectedly crosses those paths.
- No automatic retry, timeout inflation, or weakened expectation.
- No docs update unless a normative contract actually changes.
- No implementation until the baseline classification is evidenced.

## Pass 1 — refined plan

counter: 1

Add an explicit evidence matrix to the baseline task: exact `origin/main`, the
candidate revision named by the bead if recoverable from history, and the
current branch must be run with the same focused command and parallel shard.
Record exit status, test count, elapsed time, failure phase, and process/PID
cleanup observations. If the exact candidate is not available, state that
limitation rather than inferring from the description.

Add a decision gate after baseline:

- production defect: write a regression first and change production code;
- test defect: write a deterministic fixture proof and preserve production
  semantics;
- environment-only flake: do not weaken the test; capture the evidence and
  file/update a follow-up bead if the current bead cannot own infrastructure.

The candidate `cmd/gc/controller.go` surface is not presumed relevant. The
history audit must locate the actual ownership before any edit. A no-change
completion is valid only when the existing test passes on exact baseline and
candidate, the failure is attributable to loaded scheduling/infrastructure,
and the finding is durably recorded for the refinery.

## Pass 1 critique — section-by-section

- Objective and decision gate: now falsifiable and allows a correct no-change
  result; it should also say whether the work bead can be handed off with only
  evidence or needs a follow-up bead.
- Tasks: exact revision comparison is stronger; the process must avoid running
  unrelated dirty-tree code from the reusable polecat home.
- Architecture: still conservative and correctly treats `cmd/gc/controller.go`
  as an unproven candidate surface.
- Evidence: the matrix is actionable; it should distinguish test-cache state
  from Go build-cache state and avoid shared-cache mutation.
- Support/docs/stability: adequate, with cleanup ownership still needing an
  explicit final process check.
- Handoff: a no-change branch still needs a reviewable artifact and refinery
  note, not an assertion that “nothing happened.”

## Pass 1 critique evaluation

The refined plan is now safe for a pre-existing-flake bead, but no-change
evidence must still be a deliverable. The remaining risks are operational:
wrong worktree, hidden test cache, and unverified child cleanup. Add explicit
worktree identity and final process-state checks to the evidence record and
handoff.

## Pass 1 roll-up

Require all commands from the bead worktree on `polecat/sdk-crd`; use
`-count=10` or `-count=1 -count=...` with test caching disabled as appropriate,
without changing `GOCACHE`. Add a final bounded child-state check and preserve
the test's cleanup kill as failure containment, not as proof of success.

## Pass 1 no-change decisions

- Keep the plan file as the sole planning artifact; use beads for follow-up
  tracking rather than adding a markdown task list.
- Do not add a new process abstraction based on one flaky test.
- Do not treat an isolated green run as sufficient evidence.
- Do not close the implementation bead; only the refinery closes it.

## Pass 2 — refined plan

counter: 2

The implementation gate is now: first reproduce or explain the reported
failure on the exact layer; then add only the smallest owning proof. Any test
fixture change must prove readiness through a file/channel/process fact and
must leave the assertion about child termination intact. Any production change
must have a RED test that fails before the change and a GREEN result afterward.

The self-review gate must report four independent facts: branch/worktree
identity, diff ownership, focused result, and parallel-shard result. A no-code
outcome must report why the existing code is correct and why the observed
failure belongs to the environment or runner, with the exact commands and
revisions recorded in the bead note.

## Pass 2 critique — section-by-section

- Objective/tasks: the classification and RED/GREEN requirements are now
  complete; exact candidate recovery remains a possible external blocker.
- Architecture: the boundary rule and no-abstraction decision are stable.
- Evidence: four self-review facts prevent a passing test from masking a
  wrong checkout, but “diff ownership” needs to be a human-readable summary,
  not only a clean git status.
- Support/stability: process cleanup is covered; avoid claiming `kill -KILL`
  in failure cleanup as evidence that production cancellation worked.
- Docs/handoff: the bead note must preserve the evidence because the plan file
  is branch-local and the refinery review may happen elsewhere.

## Pass 2 critique evaluation

The plan is operationally complete. The remaining distinction is between
diagnostic cleanup and product cleanup: a test's emergency kill prevents leaked
processes but cannot prove the implementation killed them. That distinction
must appear in the final evidence and review note.

## Pass 2 roll-up

Add the diagnostic-versus-target cleanup distinction to the test review and
handoff. Preserve branch-local plan edits as part of the cohesive change only
if they are useful review evidence; otherwise the bead note carries the final
result and the plan remains a tracked planning artifact.

## Pass 2 no-change decisions

- Do not convert a process integration test into a fake-only unit test.
- Do not add retries or sleeps to hide a race.
- Do not broaden the change to dashboard, OpenAPI, or unrelated controller
  lifecycle code without direct evidence.

## Pass 3 — final roll-up

counter: 3

Execute the refined sequence: exact baseline classification, history and
boundary audit, RED reproduction, narrow fix or evidence-only classification,
focused uncached repetition, parallel integration shard, static checks, clean
commit, remote push verification, and refinery handoff. The final report must
name the target truth, evidence layer, what was not proved, whether code
changed, and any follow-up bead or residual risk. If the original bug could
remain after all work, state that explicitly and do not claim completion.

## Pass 3 critique

The plan now covers the required tasks, architecture, tests, support,
documentation, execution, stability, blockers, parallel candidates, three
refinement passes, and proxy audit. It prevents the two likely false
completions: changing expectations to fit a flake and accepting an isolated
green result. No further structural change is warranted before evidence.

## Pass 3 critique evaluation

The critique is adequate and does not identify lost information. The only
remaining uncertainty is the exact candidate revision named by the bead; the
plan already treats that as a discoverable constraint and requires explicit
recording if unavailable.

## Final decisions

- Proceed without waiting for interactive approval because the assigned
  polecat formula authorizes execution; the plan is the durable approval
  record for this session.
- Preserve the real-process timeout and child-cleanup assertions; change only
  the test fixture budget, not the production budget.
- Make no production change unless the target-boundary RED proof requires it.
- Hand off only after remote branch identity and commit verification succeed.

## Evidence update and classification

- Baseline used for this investigation: `origin/main` at
  `5d640fc770e09a909428feaa1fea870c671c10a0`.
- The normal focused run failed before the target because the host lacked the
  ICU header required by `go-icu-regex`; this is an environment preflight
  failure. The repository-compatible `CGO_ENABLED=0` focused run passed, and
  ten uncached repetitions passed in 0.640s.
- The historical failure artifact named by the bead reports only
  `TestStatusSessionSnapshotKillsBdChildOnTimeout` failing after 5.35s because
  `bd-child.pid` was never written. The failure is at fixture readiness after
  the status call's 200ms budget, not a child-survival assertion. The same
  target passed on the exact baseline and candidate revisions named by the
  bead.
- An earlier `CGO_ENABLED=0 make test-integration-shards-parallel` run
  exercised `internal/api` under the real integration lane with
  `GC_FAST_UNIT=0`; `internal/api` passed in 81.343s, including the original
  target process test. That run preceded the final fixture edits and is
  recorded as baseline load evidence, not final verification.

Classification: pre-existing, load-sensitive test-fixture/process-start
flake. The available evidence does not implicate `cmd/gc/controller.go` or
justify changing production behavior, weakening the timeout/cleanup
expectations, adding retries, or inflating the production deadline. The
test-only budget is adjusted only to make fixture startup observable. The
required truth is still covered by the focused and current loaded real-process
passes, while
the historical artifact proves that a heavier host can prevent the fake
`bd` from reaching its PID-file write before the short timeout. That artifact
does not prove a child survived, so it must not be converted into a product
failure claim.

Decision update after RED reproduction: a fixture-only repair is warranted.
The test's 200ms override was below the production `statusStoreReadTimeout`
budget and could cancel the real `bd` shell before it wrote the child PID
under loaded scheduling. Matching the production one-second budget removed
the first startup race, but 2 of 20 repetitions still missed the PID file
when the process-wide `bd` execution semaphore was saturated. Increasing the
deadline further would make the test slow and would still leave an unbounded
queue dependency.

The final repair therefore gives this API boundary test a small context-bound
`CommandRunner` that intentionally avoids the process-wide `bd` semaphore.
Its fake `bd` writes its own PID as the first synchronous action and then
`exec`s a long sleep. The test-only budget is two seconds: production remains
at one second, while the extra bounded margin prevents an overloaded runner
from canceling the shell before fixture startup. The test still observes a
real subprocess, verifies the status call returns within its ten-second
guard, and asserts that the process is gone; the lower-level
`TestKillCommandTreeKillsProcessGroup` remains the owner of production
process-group descendant cleanup. This changes no product behavior and does
not weaken the timeout or cleanup assertions. Twenty uncached real-signal
repetitions passed in 41.028s after the final fixture repair; a prior
one-second run missed fixture startup once under heavier host starvation.

The full `internal/api` package run was attempted with `CGO_ENABLED=0
GC_FAST_UNIT=0` under shared-host load and failed after 118.733s; the captured
output was truncated and did not identify this target as the failing test.
A separate status-family run exposed the two unchanged sibling fixtures'
pre-existing startup races under the same starvation, so this bead remains
narrowly scoped to the reported session-snapshot test. The loaded integration
sweep passed the core and command shards and failed an unrelated tmux modal
precondition; its two formula shards were interrupted after remaining
stalled. These outcomes are recorded as environment/load evidence, not as
proof of the repaired target.
