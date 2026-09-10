# sdk-85j investigation plan

counter: 3

## 1. Full plan, tasks, and subtasks

- Establish the current branch/worktree boundary and preserve unrelated files.
- Read the project testing guidance and the debouncer implementation/test.
- Reproduce `TestTickDebouncer_IndependentInstances` repeatedly in isolation,
  under package load, and with race detection where practical.
- Trace timing, goroutine, timer, and cleanup behavior to identify whether the
  assertion is flaky, environment-sensitive, or a real contract defect.
- Search history and nearby tests for the intended debounce contract.
- If a minimal product/test fix is justified, write a failing test first,
  implement it in the owning boundary, and run affected quality gates.
- Otherwise leave code unchanged and record evidence in the bead handoff.

## 2. Architectural changes

- Expected default: no architectural change; this is a timing/load diagnosis.
- Any code change must remain inside the debouncer/test boundary and must not
  couple the runtime to a specific role or unrelated test contract.

## 3. Test plan

- Run the named test repeatedly with controlled counts and verbose output.
- Run the containing package tests and the race-enabled named test if feasible.
- Compare behavior with and without parallel/package load.
- Do not treat a passing rerun as proof of product correctness; distinguish
  assertion-layer evidence from runtime timer behavior.

## 4. Support structures and evidence

- Use existing Go test tooling and git history; add no permanent harness unless
  it materially improves reproducibility.
- Capture command, count, timing, and failure mode in the final handoff.
- Temporary `agent-execution.log` records completed subtasks only.

## 5. Documentation

- No product documentation change is expected.
- Update only durable issue notes/metadata required for the refinery handoff.

## 6. Execution order

1. Preflight and read guidance.
2. Inspect implementation/test and history.
3. Reproduce under increasing load and race detection.
4. Decide whether evidence supports a scoped fix.
5. Run relevant checks, review diff, commit if changed, and submit.

## 7. Stability strategy

- Avoid changing timing constants merely to make a test pass.
- Prefer synchronization based on observable state over sleeps if a defect is
  proven.
- Keep test isolation and cleanup deterministic.
- Never alter the shared Go build cache or use `/tmp` for caches.

## 8. Blocker avoidance

- Use package-local commands and existing scripts rather than broad scans.
- If Dolt is slow, follow the prescribed diagnostics before escalation.
- If the failure cannot be reproduced, report the exact negative evidence and
  retain the test contract for a later loaded-environment reproduction.

## 9. Candidate subagent-parallel work

- None: this small diagnosis depends on one shared timing observation and the
  available execution context; splitting it would risk inconsistent evidence.

## 10. Proxy-domain audit

- Target truth: independent debouncer instances each fire exactly once after
  their own configured interval when independently triggered.
- Required evidence layer: the runtime debouncer's timer/goroutine behavior,
  observed by the named Go test and, if needed, focused instrumentation.
- Cheaper useful-but-insufficient proxies: one successful rerun, a sleep-based
  test, or a package compile; these only show partial health.
- Tempting false-completion substitution: loosening the assertion or increasing
  timeouts without proving independent timer behavior.
- If this plan succeeds while the original bug remains: scheduler load or
  shared state may still suppress one callback outside the sampled runs.

## 11. Planning pass 1 — counter 1

### Critique of the plan, top to bottom

- Scope is appropriately narrow, but the reproduction matrix must include the
  exact fast-gate command and package-load conditions, not only isolated runs.
- The architecture section correctly avoids speculative changes, but the timer
  callback/channel race needs explicit consideration before any test edit.
- The test plan needs to distinguish `-count` repetition from true concurrent
  package load and must record the first failing run, not just aggregate pass.
- The evidence section should include source revision and compiler mode because
  CGO availability already affects whether the package can build here.
- The history step should compare the current base with the known sdk-wx8 test
  adjustment and any sdk-dsi contract-only diff.
- The stability strategy should reject widening sleeps as a first response.

### Critique of that critique

The critique improves evidence quality but could overemphasize the historical
test-only adjustment. The product contract is not established by the test's
sleep window; timer delivery and cancellation semantics remain the primary
questions. Package-load stress should remain bounded and reproducible, while
the exact gate may be unavailable if it depends on another branch's files.

### Roll-up and revised tasks

Add a revision/working-tree snapshot, run the named test with cache disabled and
high repetition, run race mode, and inspect the exact sdk-dsi ancestry/diff if
available. Read the callback's ordering carefully before changing expectations.
Preserve the distinction between build-environment failure (missing ICU with
CGO) and the target assertion.

### No-change decisions

- No production timer change based on one reported failure.
- No assertion weakening, retry loop, or timeout inflation as diagnosis.
- No permanent diagnostic harness unless existing commands cannot expose the
  relevant race.

## 12. Planning pass 2 — counter 2

### Critique of the revised plan, top to bottom

- The revised reproduction matrix is sufficient for the unit boundary but does
  not yet define what result would establish a test-observation defect.
- The source inspection should test whether `time.AfterFunc` can run after the
  observation deadline and whether the helper's count window is semantically
  aligned with the expected callback time.
- Race mode is useful for shared-memory ordering, but a clean race run cannot
  prove scheduler fairness or absence of timer starvation.
- Branch archaeology should use immutable `git show`/`git diff` and avoid
  altering the shared checkout.

### Critique of that critique

The key decision criterion can be made concrete: if the test fails while the
timer callback is known not to have delivered by its 5ms second window, the
test is timing-sensitive; if the callback delivered to another instance or
state was shared, the implementation is suspect. The current helper exposes
no callback instrumentation, so a temporary local diagnostic may be justified
only if repeated runs cannot classify the result.

### Roll-up and revised tasks

Record timer deadlines and observation-window semantics from the code, run
isolated/repeated/race/package tests, and use targeted temporary instrumentation
only as an uncommitted diagnostic if needed. Compare outcomes to the exact
reported failure and report limits of each evidence layer.

### No-change decisions

- Do not claim package success proves the full pre-push gate.
- Do not infer that current main contains the reported sdk-dsi state.
- Do not modify the test until the intended contract and failure edge are
  independently demonstrated.

## 13. Planning pass 3 — counter 3

### Critique of the plan, top to bottom

The plan now covers repository state, owner code, historical context, focused
reproduction, load/race sensitivity, and evidence limits. It still needs an
explicit handoff outcome for both possible paths: a minimal tested patch, or a
no-code pre-existing-failure report with commands and likely cause. The final
quality gate should be proportional to whether code changed.

### Critique of that critique

The proposed two-path handoff is correct and prevents a diagnosis from being
represented as an unverified fix. Since the assignment explicitly says the
failure predates sdk-dsi and the current branch does not change the runtime or
test, the no-code path is the default unless evidence contradicts it.

### Roll-up: final execution plan

1. Snapshot branch, ancestry, and exact relevant diffs.
2. Reproduce the named test with `CGO_ENABLED=0`, `-count`, `-count=1 -race`,
   and the documented package/load shard as feasible.
3. Analyze timer callback ordering and history; use temporary diagnostics only
   to classify an otherwise ambiguous failure.
4. Make a narrowly scoped TDD fix only if the current code violates the
   independently established contract; otherwise leave source unchanged.
5. Run the affected test gate, vet, and required hook checks for any code
   change; update durable bead notes with evidence and hand off.

### Explicit no-change decisions

- No source or test contract change is justified by an isolated inability to
  reproduce a pre-existing timing failure.
- No docs change is needed for a diagnosis.
- Untracked files present before this assignment remain untouched except for
  this plan and the required temporary execution log.

### Lost-information check after three passes

The refined plan retains the original target, architecture boundary, TDD gate,
proxy audit, stability constraints, blocker avoidance, history search, and
handoff requirements. It adds exact-gate comparison, CGO/build evidence, timer
ordering criteria, and explicit no-code completion behavior; no original
requirement was lost.
