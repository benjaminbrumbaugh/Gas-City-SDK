# sdk-x3c execution plan

counter=0

## Full plan

Determine whether the REST-full shard failures are environmental, regressions,
or defects owned by this branch. Reproduce the provider and live-contract
failures independently, inspect current code and relevant history, and keep
any repair behind the existing provider/store boundaries. If the failures are
confirmed pre-existing and not safely actionable here, preserve the evidence
in the bead and close only after the required gates and handoff are complete.

## Tasks and subtasks

1. Establish the current workspace and test baseline.
   - Preserve unrelated worktree changes.
   - Read the documented test tiers and identify the exact shard command.
   - Run the focused provider and live-contract tests with captured exit codes.
2. Investigate the custom-provider failure.
   - Trace environment propagation at the runtime/provider boundary.
   - Compare this branch with `origin/main` and relevant history.
   - Add a regression test first if a branch-owned defect is found.
3. Investigate the live-contract failures.
   - Trace path handling and supervisor cleanup at the integration boundary.
   - Inspect Dolt test-database setup/cleanup and history before changing code.
   - Add a regression test first if a repository-owned defect is found.
4. Resolve the smallest proven issue, or document a non-owned environmental
   result without masking failures or changing expectations.
5. Run proportional quality gates, update the bead, and leave a concise log.

## Architectural changes

Prefer no architectural change. Any repair must remain within the existing
runtime/provider or beads/store test boundary; do not put provider- or
DoltLite-specific behavior into generic SDK paths.

## Test plan

- Focused `TestE2E_CustomProvider` reproduction.
- Focused `TestGCLiveContract_BeadsAndEvents` reproduction.
- The documented REST-full shard if infrastructure permits.
- Package tests for any changed code, then the fast baseline and `go vet ./...`
  when code changes are made.
- Treat passing proxy tests as evidence only for the layer they observe; they
  do not prove the full live contract or external environment.

## Support structures

Use captured test output and git comparisons as evidence artifacts. No new
production abstraction is planned. The execution log records completed
subtasks; the bead carries durable findings.

## Docs

No documentation change is expected unless the current test contract or
operator-facing behavior changes.

## Execution order

Baseline and history first; provider and live-contract investigations may then
proceed independently; implement only after ownership is established; verify;
update the bead and hand off.

## Stability strategy

Use explicit test commands, avoid shared-cache cleanup, avoid changing test
expectations without a contract change, and preserve all pre-existing files and
branch state. Do not infer a root cause from a proxy or a single flaky run.

## Blocker avoidance

Use the existing shard runner and local fixtures. If Dolt or process fixtures
are unavailable, record the exact blocker and compare against `origin/main`
and history rather than inventing a replacement harness.

## Candidate subagent-parallel work

Provider-boundary diagnosis and live-contract/Dolt diagnosis are independent
candidate tracks. No subagent dispatch is available in the current execution
surface, so they will be handled as separate evidence tracks in this session.

## Planning pass 1 — counter=1

### Critique, top to bottom

- Full plan: correctly frames ownership before repair, but must explicitly
  distinguish branch-only failures from failures shared with `origin/main`.
- Tasks: the baseline is actionable; history inspection should happen before
  any proposed implementation, and the final disposition needs bead notes.
- Architecture: the boundary constraint is sufficient; it must forbid test
  fixture changes that conceal a real live-contract failure.
- Tests: focused tests and the shard are appropriate, but each result must
  name the observed layer and its limits.
- Support structures: captured output is enough; do not add a durable harness
  for a one-off environmental diagnosis.
- Docs: correctly avoids unrelated docs churn.
- Execution/stability/blockers: these protect shared state; include a final
  check for partial command artifacts and lease status.
- Parallel work: correctly identifies independent tracks; sequential execution
  is acceptable when no subagent surface is available.

### Critical evaluation of the critique

The critique adds useful ownership and evidence distinctions without changing
the objective. The fixture warning is important because changing setup can
turn a failing live contract into a false green. The lease check is operational
hygiene, not a new product requirement.

### Roll-up: revised critique

The plan must record branch-vs-baseline comparisons, layer-specific evidence,
and a final bead update. It should preserve the option of a no-code disposition
when the bead's stated pre-existing diagnosis is confirmed.

### Roll-up: revised plan/tasks/subtasks

Add an explicit comparison against `origin/main` before implementation, retain
the provider and Dolt tracks as separate boundaries, and require evidence
classification in the final bead note. No other section changes.

### No-change decisions

No new abstraction, no docs work, no expectation changes, and no new harness
are justified before reproduction and ownership evidence.

## Planning pass 2 — counter=2

### Critique, top to bottom

- Full plan: now captures shared-failure analysis, but "close" must depend on
  whether the issue is actually resolved or explicitly handed back as not
  owned.
- Tasks: focused reproduction and history are ordered correctly; add a
  non-mutating git/worktree check before running integration cleanup.
- Architecture: sufficiently narrow and aligned with upstream mergeability.
- Tests: proportional gates are clear; a failed test should not be converted
  into success by weakening assertions.
- Support/docs/execution: durable bead notes and the temporary execution log
  cover handoff; the plan should say whether the plan artifact is retained.
- Stability/blockers/parallelism: good safeguards; shared Dolt state may make
  repeated runs non-independent, so report that limitation.

### Critical evaluation of the critique

The closure distinction avoids claiming a fix for a non-owned failure. The
worktree check and Dolt-state limitation improve safety and evidence quality.
Retaining the plan is useful for reviewability and is consistent with existing
repo plan artifacts.

### Roll-up: revised critique

Use a disposition of fixed, confirmed pre-existing/non-owned, or blocked. Keep
git inspection read-only until ownership is proven. Report shared-state
limitations explicitly and retain the plan as the execution record.

### Roll-up: revised plan/tasks/subtasks

The final task now requires one of the three evidence-backed dispositions and
requires a clean read-only state check before any mutating test cleanup. The
test plan must report Dolt shared-state limitations.

### No-change decisions

The architecture, docs, and support-structure decisions remain unchanged.
No subagent dispatch or broad refactor is warranted.

## Planning pass 3 — counter=3

### Critique, top to bottom

- Full plan: complete and scoped to sdk-x3c; it should state that immediate
  execution proceeds under the user's request without a separate approval
  pause.
- Tasks: complete; finalization must include the claimed bead's status and
  execution log entry.
- Architecture: protects both upstream alignment and provider/store ownership.
- Tests: complete for both reported failures and broad gates; distinguish
  passing focused tests from passing the full live contract.
- Support/docs: appropriately minimal and reviewable.
- Execution/stability/blockers/parallelism: complete, with no destructive
  cleanup and no cache manipulation.

### Critical evaluation of the critique

The only addition is an execution-mode note: the user explicitly requested
immediate formula execution, so the plan is presented for visibility while
execution continues. This does not weaken the required evidence or approval
record; it avoids an unnecessary pause.

### Roll-up: revised critique

The plan is ready: it is evidence-first, boundary-aware, test-driven if code is
needed, and includes a no-code disposition. Immediate execution is authorized
by the current request.

### Roll-up: revised plan/tasks/subtasks

No further task changes. Proceed with baseline/history, then the two evidence
tracks, repair only if owned, verify, update the bead, and hand off.

### No-change decisions

Do not broaden scope to unrelated dirty files, do not touch the retained
dashboard, do not alter generated artifacts, and do not run destructive git,
tmux, or cache-cleaning commands.

### Lost-information check after three passes

Nothing material was lost: both failure surfaces, ownership comparison,
evidence limitations, test order, stability safeguards, and final disposition
remain represented. Plan is approved for immediate execution by the user's
explicit instruction.
