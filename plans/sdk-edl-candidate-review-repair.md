# Plan: migrate candidate-review formula to the graph.v2 bead contract

counter=0

## Pass 0 — initial plan

### Full plan, tasks, and subtasks

1. Confirm the failure on a clean `origin/main`-based worktree.
   - Inspect the shipped formula and the two named bootstrap tests.
   - Record the preflight compiler/environment limitation if present.
2. Make the smallest source correction in
   `examples/gastown/formulas/mol-candidate-review-repair.toml`.
   - Rename the local shell variable `BEAD_ID` to `SOURCE_ID` at its
     declaration, validation, display, and worker-invocation references.
   - Preserve `{{convoy_id}}`, the one-child convoy guard, and all worker
     behavior.
3. Verify the graph.v2 contract.
   - Run `TestShippedFormulasHaveNoLegacyIssueRefs`.
   - Run `TestShippedWorkflowFormulasComposeUnderGraphV2`.
   - Run formatting/diff checks and the repository quality gates required by
     the polecat formula, using the host's Homebrew ICU include/library paths
     when Go's cgo compile requires them.
4. Review the diff for scope, commit it on `polecat/sdk-edl`, push it, record
   the producer outcome, and hand the open work bead to the refinery.

### Architectural changes

One pack-owned formula description changes its local variable spelling to
avoid the formulas v2 reserved legacy symbol. No SDK runtime, parser,
controller, beads, or API boundary changes are needed.

### Test plan and evidence discipline

Target truth: the shipped candidate-review formula and its transitive
graph.v2 composition contain no legacy `bead_id` symbol and compile under the
reserved-symbol contract.

Required evidence layer: `internal/bootstrap` tests parse and resolve the
actual shipped TOML files, then validate the resolved formula and a synthetic
graph.v2 extension. These tests prove formula-source/compile compatibility;
they do not prove a live candidate-review repair run or the shell worker's
mutation behavior.

Useful but insufficient proxies: `rg` confirms the textual token is gone and
`git diff --check` confirms patch hygiene, but neither exercises formula
resolution. A tempting false completion is to rename only the prose or only
the declaration while leaving `$BEAD_ID` references; the parser test must
remain the completion gate.

### Support structures, docs, and execution order

- Support artifact: this plan and temporary `agent-execution.log`; neither is
  product logic.
- No documentation update is warranted because the existing formula already
  documents the convoy-derived contract; this is a legacy-symbol cleanup.
- Sequential order: preflight → patch → targeted tests → broader quality
  checks → diff review/commit → push/refinery handoff.

### Stability and blocker strategy

Keep the unrelated dirty polecat-home worktree untouched. Work only in the
per-bead worktree, branch from freshly fetched `origin/main`, and stage only
the formula plus the required plan/log artifacts. If the targeted test is
blocked by missing macOS ICU headers, use the installed Homebrew `icu4c`
paths and report any remaining environment failure separately from the
formula result. Do not weaken or replace the target tests.

### Candidate subagent-parallel work

None: the one-line contract migration and its parser tests are tightly
coupled, and parallel work would add coordination cost without reducing risk.

### Critique of the plan, top to bottom

- Scope is appropriately narrow, but the plan must ensure every occurrence of
  the legacy token is removed from the resolved description, not just the
  first assignment.
- The architecture section correctly places the change in the pack boundary;
  it should explicitly avoid adding a new formula variable.
- The test plan names the required layer and its limits; broader gates are
  useful but should not obscure the two acceptance tests.
- The stability strategy must ensure the plan/log files do not capture
  unrelated worktree changes.

### Critical evaluation of the critique

The critique is sound. The likely regression is a partial textual rename, so
an explicit occurrence audit and the two resolver tests are sufficient. A new
formula variable would be an architectural regression because `convoy_id` is
runtime-injected and `BEAD_ID` is only a local shell name. The worktree is
clean from `origin/main`, so unrelated files cannot be staged accidentally.

### Roll-up: revised critique

Add an explicit post-edit `rg -n 'BEAD_ID|bead_id'` audit limited to the target
formula and confirm the diff contains no unrelated paths. Treat the targeted
tests as the acceptance evidence; broad gates remain secondary health checks.

### Roll-up: revised plan/tasks/subtasks

The implementation task now includes a complete occurrence audit, and the
self-review task explicitly checks that only the intended formula contract
cleanup is staged. All other tasks remain unchanged.

### No-change decisions

- Do not change `internal/bootstrap/pack_formula_graphv2_test.go`; its tests
  correctly expose the shipped formula defect.
- Do not replace convoy derivation with a direct bead lookup; that would cross
  the graph.v2 runtime contract.
- Do not add compatibility aliases or parser exceptions; the formula should
  conform to the v2 contract.
- Do not change the worker script, order, docs, or unrelated formulas.

## Pass 1 — contract-focused refinement

counter=1

### Full plan/tasks/subtasks

Retain the Pass 0 plan. The concrete edit is a complete local-variable rename
from `BEAD_ID` to `SOURCE_ID` in the formula description. Validate the
resolved formula through both existing bootstrap tests, then run the required
quality gates and hand off the branch.

### Critique

The acceptance criteria say both tests must pass on `origin/main` plus this
fix. The plan correctly tests both, but should also verify the formula's
declared variables do not gain a `bead_id` entry during editing.

### Critical evaluation of the critique

This is a useful guard against solving the textual symptom by declaring a
forbidden variable. The formula's `[vars]` section must remain limited to the
existing repair and gate inputs.

### Roll-up: apply evaluation to critique

Add a source inspection of the `[vars]` section to the self-review and use the
resolved parser tests as the authoritative validation.

### Roll-up: apply revised critique to plan/tasks/subtasks

The edit and review subtasks now include both declaration/reference coverage
and a check that no `bead_id` variable is declared.

### No-change decisions

No new test is needed: the shipped-formula test already covers both direct
and transitive legacy references. No runtime code is in scope.

## Pass 2 — delivery and evidence refinement

counter=2

### Full plan/tasks/subtasks

Execute the refined plan in one isolated branch: establish the clean baseline,
apply the formula-only rename, run occurrence and variable audits, run both
targeted tests, run broader mandated checks as feasible, commit, push, record
metadata, and reassign the open bead to the refinery.

### Critique

The delivery sequence must preserve the producer commit identity and branch
shape required by the refinery. A successful test run in the wrong worktree
would be misleading, so commands must use the recorded per-bead worktree.

### Critical evaluation of the critique

This is correct and operationally important. The worktree and branch were
recorded before implementation, and final checks will verify both the clean
tree and `polecat/sdk-edl` branch before push.

### Roll-up: apply evaluation to critique

Keep the branch/worktree identity check in the final review and do not amend
the implementation commit after recording its SHA.

### Roll-up: apply revised critique to plan/tasks/subtasks

The submit sequence includes branch-shape, remote-head, and clean-tree checks;
the formula change remains the only product delta.

### No-change decisions

No subagent split, new harness, generated fixture, or documentation change is
justified by this single-token formula migration.

## Pass 3 — final loss-of-information check

counter=3

### Full plan/tasks/subtasks

The final plan is: restore the proven `BEAD_ID` → `SOURCE_ID` correction in the
shipped candidate-review formula; prove direct and transitive formulas v2
validation; run hygiene and mandated quality checks; commit and push
`polecat/sdk-edl`; handoff the still-open bead to the refinery.

### Critique

No critical requirement was lost: source boundary, reserved-symbol contract,
test layer, environment caveat, branch/worktree discipline, and refinery
handoff are all retained.

### Critical evaluation of the critique

The final plan still distinguishes parser-test truth from textual proxies and
does not claim a live worker run that was not performed. It also preserves the
instruction not to close the implementation bead.

### Roll-up: apply evaluation to critique

Proceed without expanding scope. Record test outcomes and the stale-base
finding precisely in the execution log and final handoff. The history audit
confirmed `c5ceae91d` as the prior proven source-binding fix; this is the
deliberate choice over `WORK_BEAD_ID`.

### Roll-up: apply revised critique to plan/tasks/subtasks

The source edit remains one formula-only rename, now using the historically
proven `SOURCE_ID` name. Targeted evidence, quality gates, and handoff remain
the complete execution path.

### No-change decisions

No additional files, abstractions, tests, or docs are needed. The test
expectations are independently correct and will not be changed.
