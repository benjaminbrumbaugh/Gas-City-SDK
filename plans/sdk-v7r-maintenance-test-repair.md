# sdk-v7r maintenance test repair plan

## Planning state

counter: 0

Target truth: `TestMaintenanceDoltScriptsParseManagedRuntimeStateWithPortableSed`
must complete reliably under `CGO_ENABLED=0`, while the maintenance script still
parses managed runtime state and cleans only processes in its owned run-root.
The test observes the shell-script/process-cleanup layer; passing it does not
prove all Dolt lifecycle behavior or production supervisor behavior.

## Pass 1 — baseline plan

### Full plan and tasks

1. Load the rejected branch context and verify the branch/worktree metadata.
2. Run the required preflight and reproduce the focused `examples/gastown`
   test with a bounded timeout, collecting process evidence if it hangs.
3. Inspect the existing fix, maintenance script, test fixture, and relevant
   history. Preserve the upstream/fork boundary and avoid broad cleanup.
4. Add or adjust the smallest test-first evidence needed to demonstrate the
   owned run-root cleanup contract, then implement the corresponding repair.
5. Run affected tests, vet/build gates appropriate to the touched Go/scripts
   surface, review the diff, commit, push, and hand off to the refinery.

### Subtasks

- A: branch/rejection metadata and clean-diff baseline.
- B: preflight and bounded focused reproduction.
- C: inspect script/test/process ownership and history.
- D: test-first repair and regression coverage.
- E: affected tests, quality gates, review, commit, push/handoff.

### Architectural changes

No new abstraction is planned. Keep process ownership scoped at the existing
maintenance-script/run-root boundary; do not move Dolt-specific behavior into
generic SDK code.

### Test plan

- Read `TESTING.md` and use documented runners.
- Run the focused `CGO_ENABLED=0 go test -count=1 -timeout 5m ./examples/gastown`.
- Run package tests for any changed Go package and the configured affected test
  command, then `go vet ./...` if feasible.
- Treat a timeout as process-layer evidence, not as proof that the product fix
  is correct; inspect child processes and fixture cleanup.

### Support structures

- Maintain temporary `agent-execution.log` with one line per completed
  subtask.
- Keep the plan as the decision/evidence record.
- Use bounded diagnostics and existing fixtures; add no permanent harness unless
  it materially reduces uncertainty.

### Docs

No product documentation change is expected. Update the plan or focused test
comments only if the ownership contract needs clarification.

### Execution order

A → B → C → D → E. Parallel work is not appropriate for D because test and
implementation must remain a single coherent boundary; independent history
inspection can be done during C but no subagent is available/needed.

### Stability strategy

Use explicit run-root prefixes, deterministic temporary directories, bounded
test commands, and cleanup assertions. Avoid global process kills, shared tmux
cleanup, cache clearing, and unrelated rebases.

### Blocker avoidance

Use `git show`/`git log` for archaeology, `TESTING.md` runners for broad tests,
and escalation only after bounded reproduction and two or three diagnosis
attempts. Do not restart Dolt without the prescribed diagnostics if Dolt is
actually unresponsive.

### Candidate subagent-parallel work

None: the branch already contains a prior fix and the remaining work is a
single script/test process-ownership boundary. Parallel edits would increase
rebase risk.

### Proxy-domain audit

- Required evidence layer: shell-script process behavior plus the Go test that
  exercises it; unit tests are useful but insufficient for real process cleanup.
- Cheaper proxies: parser-only tests and static diff review.
- Tempting false completion: accepting a passing test after weakening the
  fixture or changing the timeout.
- Residual bug scenario: a process outside the managed run-root is still killed,
  or a real child remains orphaned while the test exits successfully.

### No-change decisions

- No generic process manager abstraction.
- No product dashboard/API changes.
- No role/configuration changes.
- No test expectation changes unless the existing expectation is independently
  wrong or the process-ownership contract is explicitly corrected.

## Pass 2 — critique and refinement

### Critique of Pass 1, top-to-bottom

- Full plan: correctly prioritizes the rejected branch and bounded reproduction,
  but must explicitly include rebase/conflict verification before final push.
- Subtasks: complete, though C should compare the branch with current
  `origin/main` so preserved source changes are not accidentally reintroduced.
- Architecture: appropriately conservative; clarify that the existing run-root
  prefix is the ownership boundary being verified.
- Tests: focused test and vet are right; add a direct check that no unrelated
  process is targeted if the current fixture can support it.
- Support/docs: execution log requirement is covered; no permanent diagnostic
  should be created for a one-off hang.
- Execution/stability/blockers: sound, but explicitly require status/metadata
  verification immediately before handoff.
- Parallelism/proxy audit: appropriately rejects parallel edits and names the
  false-completion risk.

### Critical evaluation of that critique

The critique improves merge safety and evidence specificity without expanding
scope. A direct unrelated-process assertion is only warranted if it is already
supported by the script contract; otherwise static inspection plus the existing
test is safer than inventing a brittle test. Rebase verification is mandatory
because the bead was rejected for conflict against a newer base.

### Roll-up: apply evaluation to critique

Retain the minimal test surface, add explicit base/rebase checks, and define
run-root ownership as the boundary. Do not add speculative process fixtures.

### Roll-up: apply revised critique to plan/tasks

Update C/E to compare against current `origin/main`, resolve only the known
conflict files if needed, rerun the focused test after rebasing, and verify the
branch/metadata contract before push and refinery handoff.

### No-change decisions

No new abstraction, broad fixture, documentation page, or parallel workstream.

## Pass 3 — final review

### Critique of Pass 2, top-to-bottom

- Plan/tasks: now covers the known rejection mode and branch handoff.
- Architecture: remains isolated to the existing maintenance boundary.
- Evidence: distinguishes process-layer completion from product-wide proof and
  avoids timeout/fixture weakening.
- Operations: includes bounded tests, execution logging, and no unsafe cleanup.
- Handoff: includes branch, metadata, push verification, and refinery routing.

### Critical evaluation of that critique

No material omission remains. The plan must still preserve untracked provider
skill files and avoid destructive cleanup in the shared workspace. Since this
worktree is dedicated, ordinary local edits are safe; only files in the current
worktree may change.

### Roll-up: apply evaluation to critique

Add an explicit preservation check to the execution and final review steps;
otherwise keep the plan unchanged.

### Roll-up: apply revised critique to plan/tasks

Before commit, inspect `git status --short`, stage only task files, preserve
pre-existing untracked skill materialization, and ensure no shared-rig path was
touched. After push, verify the remote branch SHA equals local HEAD.

### No-change decisions

The scope, architecture, test strategy, and evidence standard remain unchanged.

## Final plan after three passes

Lost-information check: all original requirements remain represented — target
truth and evidence layer, implementation/test subtasks, architecture boundary,
support log, docs, execution order, stability, blocker avoidance, parallel-work
decision, proxy audit, and explicit no-change decisions. Proceed with the final
plan above; no approval pause is needed under the polecat execution contract.

counter: 3
