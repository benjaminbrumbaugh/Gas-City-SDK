# sdk-gg7 plan

counter: 0

## Pass 0 — initial plan

### 1. Full plan, tasks, and subtasks

- Confirm the clean `origin/main` baseline and reproduce the reported
  repository quality failures.
- Inspect only `internal/beads/bdstore_conditional_release.go` for the
  reported import-grouping/format issue and identify whether the change is
  mechanically formatter-owned or requires design judgment.
- Write a failing or diagnostic-first test/check where useful, then apply the
  smallest idiomatic formatting fix. Do not broaden the patch to unrelated
  lint findings.
- Run targeted formatting and package tests, then the repository fast test and
  vet gates required by the formula. Record pre-existing lint limitations
  without treating them as proof of product behavior.
- Review the diff, commit the scoped change, push `polecat/sdk-gg7`, record
  producer metadata, and hand the bead to the refinery without closing it.

### 2. Architectural changes

No runtime, API, persistence, or abstraction-boundary change is planned. The
only intended source delta is import ordering in the named beads file.

### 3. Test plan and evidence boundaries

- `make fmt-check` observes repository formatting policy; it proves the tree
  is formatter-clean, not semantic correctness.
- The affected beads package tests observe package behavior and compilation;
  they prove no regression in this package, not repository-wide lint health.
- `make test-fast-parallel` observes the fast unit-test tier.
- `make vet` observes Go vet diagnostics only.
- `make lint` observes repository lint configuration; its existing findings
  will be recorded separately if unchanged by this patch.
- Compare the final diff against `origin/main` and verify only the named
  deliverable plus this required plan artifact are intentional.

### 4. Support structures, docs, and execution order

- Support structure: this plan file and the execution log required by the
  workspace instructions.
- Documentation changes: none; a mechanical import-group correction changes
  no user-facing contract.
- Order: baseline checks -> inspect -> minimal fix -> targeted checks -> fast
  gates -> self-review -> commit/push -> refinery handoff.

### 5. Stability strategy and blocker avoidance

- Preserve unrelated dirty changes in the reusable polecat home worktree.
- Work only in the per-bead worktree and branch.
- Never use `go clean -cache`; use the shared on-disk Go cache.
- If a check fails, classify whether the failure is caused by this diff before
  changing code. Escalate only after bounded diagnosis of a genuine blocker.

### 6. Candidate subagent-parallel work

No subagent is needed: the task is one file and the independent evidence
checks are cheap to run directly. Parallelizing edits would increase boundary
risk without reducing elapsed time.

### 7. Proxy audit

- Target truth: the repository quality gate should accept the unchanged
  baseline plus this scoped fix, while beads behavior remains unchanged.
- Required evidence layer: formatter output for formatting, Go package tests
  for behavior, and the configured repository gates for their own diagnostics.
- Useful but insufficient proxies: a clean `gofmt` invocation or a passing
  single package test alone.
- False-completion temptation: declaring success because `make test` passes
  while `make fmt-check` or the named lint gate still fails.
- If this plan succeeds yet the original bug remains, the likely cause is a
  missed formatter policy or an unrelated touched file; the final diff and
  rerun of the exact gate are intended to catch that.

### 8. No-change decisions

- Do not fix unrelated lint findings or reformat the repository broadly.
- Do not add a new abstraction, runtime behavior, or documentation page.
- Do not alter expectations merely to make a gate pass; any changed result
  must be justified by the product contract, which is not expected here.

## Pass 1 — critique and revision

### 1. Critique of the plan, top to bottom

- The task and scope are correctly narrow, but the plan should explicitly
  distinguish the required plan/log artifacts from product deliverables so
  they are not mistaken for source changes.
- The architecture section is appropriately no-op; the only risk is allowing
  a formatter to touch more than the named file.
- The evidence section needs a direct `internal/beads` package test command,
  because a repository fast suite is expensive and broad.
- The execution order should include a diff check immediately after the
  formatter and before any commit.
- The stability strategy correctly protects the shared cache and worktree but
  should record the exact baseline failures on the bead/log.
- The proxy audit identifies false completion, but it should call out that
  formatter cleanliness does not prove the lint configuration is healthy.
- The no-change decisions are sound and should remain binding.

### 2. Critical evaluation of that critique

The critique adds useful evidence boundaries without expanding the source
scope. A package test is a helpful fast signal, but cannot replace the
repository gate or prove unrelated lint health. Plan/log files are required
workflow artifacts and should be committed only if the repository workflow
expects them; otherwise they can be excluded from the product commit after
preserving their evidence in the bead. The exact formatter diff is the
authoritative source delta, so the post-format diff gate is mandatory.

### 3. Roll-up: apply evaluation to critique

Keep the one-file source boundary, add the direct beads package test, require
the exact formatter diff to contain only the expected import move, and retain
the baseline failure record. Treat workflow artifacts separately from product
behavior when deciding the final commit contents.

### 4. Roll-up: revised tasks and execution

- Reproduce and record baseline fmt/lint/test/vet results.
- Inspect the named file and run the configured formatter against that file
  only, or apply the equivalent import-group correction.
- Assert `git diff -- internal/beads/bdstore_conditional_release.go` is the
  expected minimal change; no unrelated Go file may change.
- Run `go test ./internal/beads`, `make fmt-check`, `make test-fast-parallel`,
  and `make vet`; rerun lint only to confirm the remaining findings are
  unchanged and outside the patch.
- Commit only the source fix plus explicitly required workflow artifacts after
  confirming repository conventions.

### 5. Explicit no-change decisions

No broad lint cleanup, no generated-file changes, no test expectation changes,
and no architecture or documentation changes.

### 6. Proxy audit

- Target truth: the named source file satisfies the repository formatter gate
  without changing beads semantics.
- Required layer: formatter diff plus package compile/tests and repository gate
  reruns.
- Insufficient proxies: `gofmt` alone, a clean working tree, or a passing
  package test without the exact `make fmt-check` result.
- False substitution: accepting the lint failure as fixed merely because the
  formatter passes; lint is a separate, currently pre-existing layer.
- Residual bug scenario: a formatter command could rewrite another file or
  repository policy could differ from the local tool; diff scope and exact
  gate rerun address both.

## Pass 2 — critique of the revision

### 1. Critique of Pass 1, top to bottom

- The revised source boundary is precise.
- The package test is correctly framed as behavior evidence, but it should be
  limited to the affected package and not be presented as a new test change.
- The command list is sufficiently broad; lint should remain observational
  because the assignment explicitly identifies its findings as pre-existing.
- The commit-artifact decision is still ambiguous; repository instructions
  require a plan file and execution log, so retain them as auditable artifacts
  unless the refinery rejects non-product files.
- The stability and proxy sections remain accurate.

### 2. Critical evaluation of that critique

The only material correction is to make the workflow artifacts intentional
commit contents, since they document the required planning and execution
evidence and are scoped to this bead. They must not be described as product
behavior. No test file is needed for a mechanical import-group correction;
the existing package suite is the correct evidence artifact.

### 3. Roll-up: apply evaluation to critique

Commit `sdk-gg7-plan.md` and `agent-execution.log` with the source fix, clearly
label them as workflow evidence, and do not add a new test. Keep lint as a
recorded pre-existing gate failure, not a target for speculative cleanup.

### 4. Roll-up: revised tasks and subtasks

- Finish baseline fast tests.
- Inspect and minimally correct the import groups.
- Run direct beads tests and all required final gates.
- Update the log after each completed subtask and record the evidence summary
  in the bead before refinery handoff.

### 5. Explicit no-change decisions

Do not touch unrelated worktrees, do not clean or reset the reusable polecat
home, and do not run cache-clearing commands.

### 6. Proxy audit

The target remains formatter acceptance of the named file. The required
evidence remains the formatter gate, package tests, and broad regression gates.
A green package test is useful but insufficient; a successful lint command is
not expected and would not prove the original formatting issue was fixed.
The original bug could remain if import grouping is corrected manually but the
repository tool applies another policy, so the exact gate rerun remains the
completion criterion.

## Pass 3 — final critical evaluation and roll-up

### 1. Critique of Pass 2, top to bottom

Pass 2 preserves the correct minimal boundary and evidence discipline. The
only remaining concern is that the plan/log artifacts could create review
noise for a one-line source fix. The workspace instructions explicitly require
both artifacts, so keeping them is a deliberate compliance decision rather
than accidental scope expansion.

### 2. Critical evaluation of that critique

The concern is real but subordinate to the explicit workspace requirement.
The artifacts are small, bead-specific, and do not alter runtime behavior.
Their presence makes the planning and evidence chain reviewable and does not
justify weakening the source or test boundary.

### 3. Roll-up: apply evaluation to critique

Proceed with exactly one source correction, the required plan/log artifacts,
and no other files. Treat a passing formatter gate plus unchanged package and
fast-test behavior as the success criterion; report lint's remaining baseline
findings explicitly.

### 4. Final plan and execution order

1. Complete base fast tests and record all baseline results.
2. Inspect `internal/beads/bdstore_conditional_release.go`.
3. Apply only the missing blank line/import grouping.
4. Run targeted package tests, exact formatter check, fast tests, and vet.
5. Review diff and log; commit on `polecat/sdk-gg7`.
6. Push and verify the remote head, set producer metadata, reassign to the
   refinery, and drain.

### 5. Explicit no-change decisions

No lint cleanup, no semantic edits, no test rewrites, no generated output,
and no docs changes.

### 6. Final proxy audit

- Target truth: the named formatting defect is removed without semantic drift.
- Required evidence: exact fmt gate, affected package tests, fast suite, vet,
  final diff, and remote-head verification.
- Cheaper insufficient proxies: local formatter invocation, `go test` alone,
  or a clean branch.
- False completion: treating the pre-existing lint failure as a reason to
  broaden scope or treating a passing test suite as proof of formatting.
- Remaining-failure scenario: if the formatter gate still emits the same diff,
  the task is not complete and the branch must not be handed off.

counter: 3
