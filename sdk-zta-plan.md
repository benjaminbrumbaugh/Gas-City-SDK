# sdk-zta plan

counter=3

## Pass 3 — Final execution plan

### Full plan, tasks, and subtasks

1. Add a named test-only MCP provider factory with a finite two-minute
   operation budget and replace only mock-backed conformance constructors.
2. Add or extend a direct unit assertion that `NewProvider` retains its
   production 30-second default; do not weaken the runtime timeout test.
3. Run `gofmt`, the focused MarkRead test, the full MCP conformance test, the
   complete `internal/mail/exec` package, and the supported fast runner.
4. Run `go vet ./...` and an appropriate build, review the diff and execution
   log, commit the cohesive change, push and verify `origin/polecat/sdk-zta`,
   set work metadata, and hand the open bead to the refinery.

### Architectural changes

The only code boundary changed is the `_test.go` fixture boundary. Production
`Provider`, its default timeout, and the live MCP test remain untouched. The
helper's comment will state why the shell/`jq` mock needs extra scheduling
headroom and that the budget is not a product contract.

### Test plan and evidence discipline

Target truth is MCP mail semantic conformance under the supported loaded fast
runner with production timeout behavior preserved. Required evidence is the
real bridge-backed package test, the unchanged production-default assertion,
and a successful fast runner. Focused tests, full isolated package tests, and
the helper-value assertion are diagnostic evidence only. If the parallel run
fails elsewhere because the host remains saturated, record the owning log and
do not claim that this bead is green.

### Support structures, docs, and execution order

Keep `sdk-zta-plan.md` as the required planning artifact and remove only the
temporary execution log after its final entry is no longer needed, or include
it only if the project convention requires it. No docs update is needed. The
execution order is fixed: test/guard first → fixture implementation → focused
and package checks → parallel evidence → vet/build → review → commit/push/
refinery handoff.

### Stability strategy and blocker avoidance

Use no retries, sleeps, cache cleaning, branch rewrites, or shared fleet
process termination. The two-minute fixture budget is finite and substantially
below the package's 20-minute test budget. Any fast-run interruption remains a
verification limitation, not a success.

### Candidate parallel work

After the edit, focused package test and static diff review are independent
read-only checks, but broad fast-suite execution must be serialized with the
worktree. No subagent is available in this session; use tool-level parallelism
only for non-mutating checks.

### Critique of the plan

- The plan's two-minute value is evidence-based but still a judgment about
  fixture scheduling; it should be a named constant easy to revisit.
- Keeping the planning artifact in the branch may be unwanted repository
  noise, while deleting it would violate the requested plan-file evidence.
- The fast-suite gate may remain unavailable due external fleet load.

### Critical evaluation of the critique

The named constant makes the scheduling choice explicit and reviewable. The
requested plan file is itself a durable evidence artifact and should remain in
the branch unless repository policy rejects it; the temporary execution log
can be removed only after its information is preserved in commit notes or the
plan. The parallel gate is still attempted and its outcome reported exactly.

### Roll-up: apply evaluation to the critique

Keep the plan file committed with the change because the task instructions
explicitly require it and it records the evidence-layer decision. Keep the
execution log temporary, but append the final verification outcome before
removing it; do not hide a failed gate. Use the test constant, not a mutable
package default, and preserve the direct default check.

### Roll-up: revised plan/tasks/subtasks

1. Write the regression guard first and confirm it fails only if the default
   contract changes (TDD evidence).
2. Implement the mock-backed helper and replace five conformance call sites.
3. Run focused/full package checks, then retry the supported fast gate once.
4. Run vet/build and inspect all tracked/untracked files.
5. Append final evidence, commit plan + code, push/verify, and reassign bead.

### No-change decisions

- No `internal/mail/exec/exec.go` changes.
- No `NewProvider` API expansion.
- No test skip, retry, sleep, or timeout removal.
- No documentation or runner-policy changes.
- Keep the plan file; remove only the temporary execution log after recording
  its final state.

### Proxy audit

Target truth: the real MCP bridge completes provider semantics under the actual
fast-suite load while the production default stays 30 seconds. Required layer:
the package's bridge-backed test plus the real parallel runner and direct
default guard. Insufficient proxies: isolated tests, synthetic load, and helper
constant assertions. The original bug could remain if the final fast runner is
not executed, if the helper is accidentally used by live tests, or if the
host-wide failure is misclassified; the final checks explicitly guard all
three.

### Information-loss check after three passes

Retained: assignment scope, branch/worktree boundary, observed timings,
runner settings, timeout ownership hypothesis, production-contract guard,
required evidence layers, no-change decisions, stability limits, and handoff
steps. No material requirement from earlier passes was dropped.

Plan finalized for autonomous execution; no approval wait is introduced by the
polecat formula.

## Pass 2 — Boundary decision

### Full plan, tasks, and subtasks

The supported fast invocation used `LOCAL_TEST_JOBS=2` and inner `-p=1`, but
the host had several independent fast-suite processes. Its unit-core command
was still running after roughly 17 minutes, while the isolated MCP suite takes
about 66 seconds and launches a large shell/`jq` process graph. This is direct
runner/resource evidence consistent with the reported `Send` timeout after
34.37s. Implement a test-only load budget for the MCP mock-backed providers,
then retain a direct test that verifies `NewProvider`'s production default is
unchanged. Re-run the focused conformance test, full package, and the supported
fast suite when the host is not saturated; record any environmental limitation
instead of treating interruption as a pass.

### Architectural changes

Add one test-only constructor/helper in `internal/mail/exec` for mock-backed
MCP conformance providers. It raises only the fixture operation budget enough
to tolerate supported suite contention; `Provider` and its public constructor
remain unchanged. The real MCP/live test continues using the production
default, so this boundary cannot silently alter runtime behavior.

### Test plan and evidence discipline

- Target truth: all MCP conformance semantics complete under fast-suite load;
  no production timeout contract change.
- Required evidence: MCP conformance through the real bridge plus a direct
  assertion of the production default and a rerun of the supported parallel
  suite.
- Insufficient proxies: isolated conformance, merely checking helper values,
  or a run while the host is idle.
- False completion: treating this invocation's Ctrl-C as a pass, or increasing
  the production default instead of isolating the test fixture.
- Residual risk: unrelated host-wide contention may still cause the fast suite
  to fail; distinguish that from the MCP test by preserving package logs.

### Support structures, docs, and execution order

No new harness or docs. Use the existing fixture helper and the direct timeout
test as evidence artifacts. Order: write tests/fixture change → focused red or
direct invariant test → implementation → package checks → fast suite → vet and
build → review and handoff.

### Stability strategy and blocker avoidance

The test-only budget must remain finite and documented; it must not disable
timeouts. Do not touch shared fleet processes or use destructive cache cleanup.
If the next fast run is still blocked by fleet contention, preserve the exact
diagnostics and escalate rather than claiming success.

### Candidate parallel work

No independent implementation remains: the helper and direct default check
touch the same test boundary. Focused package checks and static review can be
run in parallel only after the edit is complete.

### Critique of the plan

- The evidence is strong for test-fixture starvation but does not prove that a
  two-minute budget is sufficient or semantically appropriate.
- A direct assertion of an unexported field can overfit implementation details.
- The interrupted fast run cannot satisfy the required parallel evidence gate.

### Critical evaluation of the critique

The budget should be chosen from the observed 34.37s failure with headroom but
remain much lower than the 20-minute package timeout; two minutes is bounded,
explicit, and still catches hangs. The default timeout assertion is intentional
because preserving the production contract is part of this fix, and it avoids
using the test helper as evidence of runtime behavior. A fresh fast run remains
mandatory before handoff.

### Roll-up: apply evaluation to the critique

Use a named test constant and helper, not a package-wide mutable default. Add a
small production-default test only if no existing test checks it. Keep the
fast-suite result as a required final gate; if environmental saturation makes
it non-repeatable, report the exact layer and do not overstate verification.

### Roll-up: revised plan/tasks/subtasks

1. Add a failing/guarding test for the untouched production timeout if needed.
2. Add the test-only MCP provider helper with a finite load budget.
3. Replace mock-backed MCP conformance constructors only.
4. Run focused + full package tests and the fast suite.
5. Run vet/build, inspect diff, commit, push, and hand off.

### No-change decisions

- `internal/mail/exec/exec.go` production timeout remains 30 seconds.
- No retry loops, sleeps, test skips, or global runner changes.
- No changes to `mail.Provider` contracts or public docs.

### Proxy audit

The target remains real MCP bridge semantics in the supported parallel suite.
The required evidence is the actual package test plus the parallel runner and a
production-default guard. Isolated tests and helper-value assertions are
insufficient. The bug could remain if only the helper is tested without a
loaded fast run, or if an unrelated fleet timeout is mistaken for this fix.

## Pass 0 — Initial plan

### Full plan, tasks, and subtasks

1. Establish the baseline on `origin/main`.
   - Run the focused MCP mail conformance test once and under repeated load.
   - Record whether the failure is deterministic, a real provider defect, or a
     resource-sensitive test-harness timeout.
2. Inspect the exec provider, MCP bridge fixture, and fast-suite runner.
   - Keep the production `mail.Provider` contract as the target behavior.
   - Identify shared state, subprocess fan-out, timeout budgets, and cleanup
     boundaries that can explain the failure.
3. Write a regression test or diagnostic at the layer that observes the actual
   failure. Do not replace parallel-suite evidence with an isolated test.
4. Implement the smallest scoped fix, preserving provider semantics and
   avoiding a generic timeout increase without evidence that the contract
   requires it.
5. Review the diff and run focused, affected, fast-suite, vet, and build gates
   appropriate to the final surface.
6. Commit a cohesive change, push `polecat/sdk-zta`, verify the remote ref, and
   hand the open work bead to the refinery.

### Architectural changes

Prefer a test-fixture or runner boundary change if evidence shows contention;
change `internal/mail/exec` production behavior only if the provider timeout
itself is the demonstrated defect. Keep per-test state under `t.TempDir()` and
ensure subprocesses receive explicit stdin and deterministic environment.

### Test plan and evidence discipline

- Target truth: the MCP bridge, when used through `mail.Provider`, completes
  conformance semantics under the supported fast-suite parallel load.
- Required evidence layer: the actual `internal/mail/exec` test under external
  package parallelism, plus focused semantic conformance tests.
- Useful but insufficient proxies: one isolated subtest, a cached passing test,
  or a mock-only script test that does not exercise the real bridge.
- False-completion substitution to avoid: widening a timeout or deleting a
  slow subtest merely because the isolated test passes.
- If this plan succeeds but the bug remains, the likely cause is unmeasured
  process/I/O contention outside the focused invocation; repeat the fast-suite
  load test before claiming completion.

### Support structures, docs, and execution order

Use this plan plus temporary `agent-execution.log` for traceability. Add a
focused harness only if repeated execution cannot distinguish product failure
from runner contention. No user documentation is expected unless the public
provider contract or supported test workflow changes. Execute baseline → plan
refinement → regression evidence → fix → focused checks → parallel checks →
full quality gates → handoff.

### Stability strategy and blocker avoidance

Never mask failures with retries, skipped tests, or a broad timeout increase.
Keep the bead branch based on freshly fetched `origin/main`; if the ledger or
shared test runner is unavailable, capture the failure and escalate rather than
inventing a proxy.

### Candidate parallel work

Independent read-only work can run in parallel: inspect fixture/bridge code,
inspect the test runner, and search history for prior fixes. Implementation
stays serial until the failure layer is established.

### Critique of the plan

- The initial plan risks spending effort on production code when the bead
  description only proves a parallel resource failure.
- A single focused test cannot prove behavior under fast-suite load.
- A stress harness could itself become a new flaky artifact if it relies on
  arbitrary sleeps or host-specific process counts.
- The plan must distinguish test timeout, subprocess timeout, and mock-state
  corruption before selecting a fix.

### Critical evaluation of the critique

The critique is valid: the first implementation hypothesis must remain open.
The required evidence is a controlled comparison of isolated, package-parallel,
and fast-suite execution, with timeout/error provenance captured. The harness
should use existing runner controls rather than inventing timing assumptions.

### Roll-up: apply evaluation to the critique

Add an explicit failure taxonomy and require the diagnostic to identify which
process owns the timeout. Treat an independently passing test as semantic
evidence only, not a concurrency proof. Do not change production timeout
defaults until the command-level timeout is ruled out.

### Roll-up: revised plan/tasks/subtasks

Before implementation, classify the failure as one of: provider semantic error,
bridge/mock state race, subprocess leak, or host/runner resource starvation.
Then write evidence for that classification and fix only the owning boundary.

### No-change decisions

- No docs change unless the supported contract changes.
- No new abstraction before a second implementation or repeated boundary need.
- No role/configuration changes; this task is isolated to mail test/provider
  behavior.

### Proxy audit

Target truth is semantic conformance under real supported parallel execution.
The required evidence layer is the package test plus the fast-suite runner. A
focused pass is useful but insufficient. The tempting false completion is to
call the issue solved after one isolated pass. The original bug can remain if
only the subprocess count or timeout is changed without a loaded-suite run.

## Pass 1 — Evidence-informed refinement

### Full plan, tasks, and subtasks

The first focused run passed `MarkRead_FiltersFromInbox` in 1.74s; the full
`TestMCPMailConformance` run passed in 65.79s. The latter is inherently
subprocess-heavy, so the next task is to finish the supported `make
test-fast-parallel` baseline and inspect its retained output for the exact
operation timeout. Then reproduce that operation with the same runner controls
or use the captured failure if it recurs. Only after that evidence will the
regression test and fix be chosen.

### Architectural changes

No production architecture change is justified yet. The current candidate
boundaries are: the MCP test fixture's shell/mock process graph, the fast-suite
runner's resource budget, and the exec provider's per-operation timeout. A fix
must remain at the narrowest boundary proven responsible.

### Test plan and evidence discipline

- Target truth remains provider conformance under supported parallel execution.
- The fast-suite result is the required runner-level evidence; the focused and
  full isolated tests are semantic/fixture diagnostics only.
- A pass of the full package in isolation still cannot rule out starvation.
- If the fast baseline passes, use repeated supported runs or a bounded load
  diagnostic before claiming the bead is stale; do not manufacture a failure
  with arbitrary CPU hogs.

### Support structures, docs, and execution order

Retain the plan and execution log. Prefer existing fast-suite logs and runner
controls to a new harness. Add no docs unless the provider timeout contract is
changed. Finish baseline → refine diagnosis → add regression evidence → fix →
affected tests → fast suite → vet/build → handoff.

### Stability strategy and blocker avoidance

The 30-second provider timeout is a real production contract candidate, so do
not silently lengthen it merely to make a shell fixture green. If the test
fixture is the only layer exceeding the budget, scope any timeout adjustment
to that fixture/test provider. If the production path fails under equivalent
load, evaluate a documented timeout change with a direct timeout test.

### Candidate parallel work

While the baseline runs, history and fixture/runner inspection can proceed
independently. No concurrent edits are allowed until ownership of the timeout
is established.

### Critique of the plan

- The initial plan was too broad about a possible new harness; existing runner
  logs may already provide enough evidence.
- The baseline's configured job count must be recorded, because host capacity
  changes the meaning of a pass or failure.
- The plan needs an explicit guard against changing `exec.Provider` for a test
  fixture problem.

### Critical evaluation of the critique

These corrections improve boundary discipline. A supported runner invocation
is stronger than synthetic load, and the provider's timeout test protects the
production contract if only test code changes. The final diagnosis must still
explain why the existing fixture exceeds 30 seconds under load.

### Roll-up: apply evaluation to the critique

Require the baseline record to include `LOCAL_TEST_JOBS`, inner `-p`, the
failing subtest, and whether the error text is a provider context timeout or a
Go test timeout. Keep the implementation choice open between fixture
optimization and a production contract change, but make the latter require a
new direct test.

### Roll-up: revised plan/tasks/subtasks

1. Complete and record the supported baseline.
2. Classify timeout owner from output/process evidence.
3. Add a regression that reproduces the classification without timing sleeps.
4. Apply the smallest fix at the owning boundary.
5. Re-run isolated semantics and the real fast suite.

### No-change decisions

- Do not alter `Provider.timeout` yet.
- Do not remove or skip `MarkRead_FiltersFromInbox`.
- Do not add a synthetic stress runner unless the supported baseline cannot
  distinguish the failure layer.

### Proxy audit

Target truth is still loaded provider semantics. The required evidence layer is
the supported fast runner plus the provider's direct contract test. Isolated
package success and a synthetic stress pass are cheaper but insufficient. The
original bug can remain if a timeout is increased without re-running the real
parallel suite or if a fixture-only change masks production slowness.
