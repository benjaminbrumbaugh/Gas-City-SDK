# sdk-cvf investigation plan

counter=3
initial_counter=0

## Full plan / tasks / subtasks

1. Establish scope: verify bead metadata, required branch, clean dedicated
   worktree, tool versions, and the direct runtime test/helper boundary.
2. Read `TESTING.md`, then reproduce the reported test serially once and with a
   finite `-count`; record pass/fail, elapsed time, and the configured timeout.
3. Inspect context cancellation, timer, replay cursor, stream publisher, and
   neighboring tests. Search history for prior fixes before proposing changes.
4. Classify the result as runtime contract defect, independently wrong test, or
   valid behavior with host-load-sensitive timing. If a source fix is justified,
   add a failing regression test first and make the smallest fix. Otherwise,
   preserve runtime code and record a durable diagnosis.
5. Run focused/affected checks proportional to the final diff, review ownership
   and branch shape, then push and hand off without closing the work bead.

## Architectural changes

Default: none. Keep timeout enforcement at the runtime dialog boundary and do
not introduce provider-specific behavior or a new abstraction for one failure.
Any implementation change must remain localized and preserve cancellation and
non-blocking stream behavior.

## Test plan and proxy audit

- Target truth: the helper returns by its configured timeout when irrelevant
  snapshots continue, without accepting a dialog.
- Required evidence layer: direct `internal/runtime` unit test plus repeated
  serialized process runs and source-level timer/cancellation inspection.
- Useful but insufficient proxies: one passing run, compile success, or a
  widened timing assertion. None proves the timeout contract under the target
  stream lifecycle.
- False completion to avoid: relaxing/deleting the assertion merely to clear
  the fast gate. If this plan succeeds, the bug could still remain if only one
  sample is collected or the test double does not model production, so report
  those limits explicitly.

## Support structures, docs, and execution order

- Keep this plan as the decision record; use `agent-execution.log` only as a
  temporary subtask log and remove it before handoff if not project-owned.
- No product docs are expected. Put the durable reproduction/classification in
  the bead notes.
- Sequence: read-only scope check -> policy/source/history inspection ->
  serialized reproduction -> test-first conditional patch -> proportional
  verification -> ownership/branch review -> refinery handoff.

## Stability and blocker avoidance

Do not touch unrelated dirty files, clear the shared Go cache, or use `/tmp`
for caches. Do not parallelize timing-sensitive test runs. Escalate only if a
required command is genuinely unavailable or the evidence remains blocked after
bounded local checks; do not substitute a proxy for target evidence.

## Candidate parallel work

Reading `TESTING.md`, inspecting the helper/test, and querying history are safe
to do independently. Timing runs stay serialized to avoid changing scheduler
load and invalidating observations.

## Planning pass 1 (counter=1)

### Revised plan

Add a final dirty-tree ownership check and distinguish a pre-existing failure
from a defect introduced by the branch that exposed it. Select test commands
from `TESTING.md`, and compare elapsed values to the contract rather than only
pass/fail.

### Critique of major sections

Scope is correct but must not imply the exposing branch owns runtime changes.
Tasks are correctly ordered but need source inspection before expectation edits.
Architecture appropriately defaults to no change. Evidence rejects assertion
widening but needs repeated runs. Temporary artifacts need explicit cleanup.
Stability and parallelism are adequate; final branch compliance must be a gate.

### Critical evaluation of the critique

These changes tighten evidence without expanding scope. Repeated runs alone do
not prove correctness; they must be paired with timer/source reasoning.

### Roll-up

Classify the outcome before editing: runtime defect permits a regression fix,
independently wrong test permits a contract-correct test fix, and host-load
flakiness gets diagnosis without cosmetic relaxation.

### No-change decisions

Do not modify unrelated files, widen timeouts without a changed contract, or
claim correctness from a single run.

## Planning pass 2 (counter=2)

### Revised plan

Snapshot dirty paths for comparison only; never restore them. Verify the
per-bead `polecat/sdk-cvf` branch before any commit and at handoff. Treat timing
numbers as observations, while the source contract remains primary.

### Critique of major sections

The scope remains bounded. Snapshotting must not imply ownership. Architecture
details should follow inspection, not assumptions. Repetition is useful but
host-dependent. Temporary snapshots should not become committed product data.
The handoff and proportional test gates are clear.

### Critical evaluation of the critique

The critique prevents destructive cleanup and overinterpretation of scheduler
timing. It preserves the test-first conditional patch path and no-code outcome.

### Roll-up

Use snapshots only for comparison, and make final branch/diff ownership a hard
handoff check. Keep timing thresholds tied to the existing contract.

### No-change decisions

Do not reset, stash, clean, or checkout over existing dirty changes; do not
parallelize the target test; do not treat samples as permission to change the
contract.

## Planning pass 3 (counter=3)

### Revised plan

Run a bounded evidence-first investigation, then either implement a test-first
minimal fix or hand off an explicit no-code diagnosis. State what each test
proves and does not prove, and run `go vet ./...` only if Go code changes.

### Critique of major sections

All required sections and classifications are present. The main residual risk
is noisy host load; source inspection, serialized repeats, and explicit
uncertainty reporting address it. No broad runtime refactor is warranted.

### Critical evaluation of the critique

No material planning gap remains. An inconclusive result is still reportable as
an unresolved pre-existing failure with exact commands and observations.

### Roll-up

Proceed without scope expansion, retain evidence-layer labels in bead notes,
and execute the exact refinery handoff contract.

### No-change decisions

No broad refactor, timeout relaxation, test deletion, or replacement of direct
runtime evidence with compile/semantic proxies.

## Post-refinement check

Three passes preserve the full plan, critiques, critique evaluations, roll-ups,
no-change decisions, proxy audit, test plan, support artifacts, documentation,
execution order, stability, blocker strategy, and parallel-work boundaries.
