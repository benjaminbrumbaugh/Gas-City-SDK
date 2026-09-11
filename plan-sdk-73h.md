# Plan: sdk-73h — real transport proof under load

Counter: 0
Scope: diagnose and fix the pre-existing `cmd/gc/TestPhase2WorkerCoreRealTransportProof`
startup flake under parallel tmux/provider load without weakening the real-transport
proof; keep the change isolated to `cmd/gc/controller.go` unless evidence requires a
minimal adjacent test/support edit.

## Pass 0 (counter=0)

### 1. Full plan, tasks, and subtasks

- Load the current controller startup/lifecycle path and the failing test harness.
- Reproduce or characterize the deadline failure on fresh `origin/main`; preserve a
  baseline result and distinguish controller scheduling from tmux/provider behavior.
- Write a failing regression test first at the narrowest deterministic seam.
- Implement the smallest controller-side scheduling/readiness fix; do not replace the
  real transport with a fake or relax proof assertions.
- Run targeted tests, affected tests, and required Go quality gates.
- Record execution evidence, commit only the cohesive fix, and hand off the branch.

### 2. Critique of the plan

The task description identifies candidate controller code but does not yet prove the
failure mechanism. A timing sleep or larger deadline could mask contention without
fixing readiness. The test must observe the real tmux/provider boundary and remain
parallel-safe. A baseline on the exact branch and focused diagnostics are required
before editing.

### 3. Critical evaluation of that critique

The critique correctly rejects symptom-only timing changes, but the controller may
already expose a readiness contract that the test is bypassing. Inspect code and
history before inventing a new abstraction. Any added synchronization must not alter
normal single-session semantics or introduce role/provider knowledge into generic
controller code.

### 4. Roll-up: apply evaluation to critique

First identify the existing start/readiness contract and all callers. Prefer a minimal
ordering or bounded-wait correction over a new interface. Use git history and the
candidate commit named in the bead to recover prior intent.

### 5. Roll-up: apply revised critique to tasks

Add explicit archaeology and a focused concurrent harness to the investigation task;
make the regression test fail before implementation. Keep fixture and test changes
only where they prove the target layer rather than serving as a proxy.

### 6. No-change decisions

- No change to transport-proof assertions or real tmux/provider usage.
- No increase to deadlines without evidence that the existing operation is correct and
  only the scheduling window is mis-sized.
- No broad refactor, new role/provider abstraction, or unrelated cleanup.

### 7. Proxy audit

- Target truth: real worker startup succeeds under the same parallel load that failed,
  with the actual tmux/provider transport and readiness semantics intact.
- Required evidence layer: `cmd/gc` integration test exercising real sessions, plus
  controller diagnostics that identify startup ordering/deadline behavior.
- Useful but insufficient proxy: isolated unit tests or a fake provider; they can prove
  local state transitions but not real tmux scheduling.
- False completion: making the deadline longer, retrying blindly, or replacing the
  real provider with a fake and observing green tests.
- Residual-risk check: if all planned checks pass while the original failure remains,
  the likely cause is uncontrolled parallel resource contention not exercised by the
  focused run; retain the parallel shard as a gate.

## Pass 1 (counter=1)

### 1. Plan refinement

Inspect `controller.go`, the named integration test, provider/session startup APIs,
the candidate history, and exact baseline/candidate commits. Run the narrow failing
test with the repository's documented environment before changing code. Add a
regression assertion at the first observable incorrect readiness transition.

### 2. Critique

A single reproduction may be nondeterministic. The plan needs repeated runs or the
existing four-provider parallel shard, and must capture whether failures are all the
same deadline or distinct provider errors. Source changes must be proven necessary by
the failing test, not inferred from the candidate file list.

### 3. Critical evaluation of the critique

Repeated full shards are expensive but appropriate after a cheap targeted probe. The
regression test should be deterministic and scoped; repeated integration runs are
confirmation, not the sole regression mechanism. Existing logs are evidence, not a
substitute for current code behavior.

### 4. Roll-up

Use staged evidence: static path/history inspection, one targeted baseline run, then
bounded repeated/parallel reproduction. Choose the smallest testable controller seam.

### 5. Roll-up to tasks

Preserve exact commands and outcomes in the execution log. If baseline passes, use
the load test to reproduce; if it cannot reproduce locally, do not fabricate a fix—
use history and code invariants to make only an evidence-backed change.

### 6. No-change decisions

- Do not weaken timeout assertions or skip provider cases.
- Do not expand scope to examples unless controller evidence requires it.

### 7. Proxy audit

The target remains the real parallel transport layer. Unit coverage can validate the
controller ordering but cannot prove tmux capacity or process startup; the parallel
integration shard remains necessary. A green baseline alone is not product evidence.

## Pass 2 (counter=2)

### 1. Plan refinement

Implement only after the failing behavior and contract are known. Run the new test
red, apply the smallest fix, then run it green under repetition, the affected test
command, and the documented broader shard when feasible.

### 2. Critique

Controller startup changes can affect all providers and lifecycle cleanup. Tests must
cover success, deadline/cancellation, and repeated starts; review for goroutine leaks,
duplicate starts, stale readiness, and error propagation.

### 3. Critical evaluation of the critique

Those edge cases are relevant only if the changed code owns them. Avoid speculative
tests for unrelated providers, but explicitly inspect cancellation and cleanup on the
actual changed path. `go vet` and package tests provide compiler/static evidence, not
real process proof.

### 4. Roll-up

Add edge tests only at the changed boundary, verify no race-prone shared state is
introduced, and preserve the existing error/deadline contract unless the bug proves it
wrong.

### 5. Roll-up to tasks

Self-review includes diff boundary, tests, race/leak considerations, formatting,
affected tests, vet, and final clean tree. Keep `agent-execution.log` outside the
product commit if it is only session evidence.

### 6. No-change decisions

- No broad `go test ./...` substitution for the integration shard.
- No unrelated cleanup in controller or examples.

### 7. Proxy audit

The test matrix must include the real transport proof after the fix. Passing unit or
semantic tests does not establish screen/process behavior; the integration shard is
the required target-layer evidence. If load still fails, the plan is incomplete even
if focused tests are green.

## Pass 3 (counter=3)

### 1. Final plan

Execute the refined sequence: inspect/history → baseline/reproduce → red regression
test → minimal controller fix → focused and affected tests → broader integration/load
confirmation → vet/build/status → commit/push/refinery handoff.

### 2. Critique

The only remaining risk is overclaiming if the shard is unavailable or flaky. Report
exactly which layer each check observes and leave the branch unsubmitted if required
gates fail.

### 3. Critical evaluation of the critique

This is addressed by preserving logs and explicitly distinguishing pass, fail, and
not-run results. The branch handoff can proceed only after the formula's local gate
passes; unresolved infrastructure failures require escalation rather than a weakened
test.

### 4. Roll-up

The implementation boundary, evidence boundaries, and failure handling are now
explicit. Proceed without further architectural expansion.

### 5. Roll-up to tasks

Use only the changed worktree, append one concise line to `agent-execution.log` after
each completed subtask, and keep the work bead open for refinery.

### 6. No-change decisions

The scope and evidence plan are accepted unchanged; no additional support structure
is warranted unless reproduction reveals a missing diagnostic.

### 7. Proxy audit

Final success requires real transport evidence under load, not merely a passing
controller test. The original bug could remain if only isolated tests are run or if
the proof is weakened; both substitutions are explicitly rejected.

## Information-loss check after three passes

Retained: candidate file boundary, baseline/candidate archaeology, TDD order, real
transport requirement, parallel-load confirmation, cancellation/cleanup review,
quality gates, branch/refinery handoff, and explicit proxy limits. No requirement from
the assignment was dropped during refinement.

## Execution findings

- Exact `origin/main` real-transport proof passed once with `CGO_ENABLED=0`.
- Candidate `origin/polecat/sdk-ai0.1` passed the targeted four-profile concurrent
  run and the fresh four-process `packages-cmd-gc-6-of-6` run locally; this is useful
  reproduction evidence but does not disprove a scheduler-sensitive CI flake.
- The candidate controller code used a one-slot completion channel with blocking sends.
  The red regression test demonstrated that a full coalescing channel can block a
  registration producer; the implementation now drops redundant notifications.

## Verification findings

- Focused watcher unit tests passed normally and under `-race`.
- The real transport proof passed once in the task worktree and concurrently for
  all four reported provider profiles; this observes actual tmux/provider startup,
  not just the watcher helper.
- The affected `cmd/gc` process shard 6 of 6 passed 1,640 tests in 241.864 seconds.
- `CGO_ENABLED=0 go vet ./...` passed. Default `go vet ./...` remains unavailable on
  this host because the installed ICU headers are missing; that is an environment
  dependency failure, not a changed-package diagnostic.
- The unsharded `cmd/gc` package run timed out after 10 minutes while other broad
  repository runners were active; the bounded sharded run is the authoritative
  affected-suite evidence required by `TESTING.md`.

The added test documents the product contract introduced by this fix (coalesced
completion notification is non-blocking); no existing expectation was weakened.
