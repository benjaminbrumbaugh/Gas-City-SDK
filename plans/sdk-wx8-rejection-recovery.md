# sdk-wx8 Rejection-Recovery Plan

counter: 3
status: ready

## Pass 0: full plan

### Objective

Repair the refinery rejection by adding the repository-required untagged
`testenv_import_test.go` bootstrap to each newly introduced test package,
without changing production behavior or weakening the dedicated test
environment guard.

### Tasks and subtasks

1. Confirm the rejected surface.
   - Read the rejection and locate `TestRequiresDedicatedTestenvImportFile`.
   - Compare the two affected package directories with neighboring packages.
2. Add the smallest test-only boundary files.
   - Keep the files untagged so the package always imports the test environment.
   - Match the repository's existing import convention and avoid production edits.
3. Verify and hand off.
   - Run the guard, focused package tests, affected tests, and formatting/diff checks.
   - Review the inherited two-commit diff after rebase and push the dedicated branch.

### Architectural changes

None in runtime, domain, CLI, API, or persistence code. The change only makes
the test packages declare the same test-environment boundary already enforced
by repository policy.

### Test plan

- Guard-level evidence: `TestRequiresDedicatedTestenvImportFile` must pass.
- Package-level evidence: `go test ./internal/convoycallback ./internal/launchorigin`.
- Diff-level evidence: `gofmt` and `git diff --check`.
- Broader evidence: the rig-configured affected test command and required vet
  or dashboard checks if the affected detector selects them.

### Support structures and evidence

The guard observes repository test-package policy, while Go tests observe
package compilation and behavior; neither proves end-to-end callback behavior,
so the inherited implementation tests remain part of the reviewed diff.
`agent-execution.log` records completed subtasks temporarily. No new
abstraction or fixture is needed.

### Documentation

No product or architecture documentation changes. The rejection is a test
infrastructure boundary repair and the existing implementation plan remains
the source of design decisions.

### Execution order

1. Inspect guard and neighboring imports.
2. Add both bootstrap files.
3. Run focused guard/package checks.
4. Run affected quality gates, review, commit, push, and hand off.

### Stability strategy

Use only the existing beads test-environment import. Keep the patch additive,
package-local, and deterministic; do not alter shared Dolt, tmux, generated
dashboard assets, or production code.

### Blocker avoidance

The branch has already been rebased onto `origin/main`; preserve both
implementation commits and add a focused test-only commit. If a broad gate
fails outside this diff, identify the exact failure and follow the existing
beads deduplication/escalation policy rather than masking it.

### Candidate parallel work

Guard inspection and neighboring-package comparison are independent reads, but
the two tiny edits and all verification remain serialized in this worktree.

### Proxy-domain audit

- Target truth: refinery accepts the branch because every new test package
  declares the required environment boundary while implementation behavior is
  unchanged.
- Required evidence layer: repository guard plus Go compilation/tests for both
  packages.
- Cheaper insufficient proxies: merely checking file names, or running only
  one package's tests.
- Tempting false completion: deleting/weakening the guard or adding a tagged
  import that does not apply to ordinary test runs.
- If this succeeds but the bug remains: the files could be misplaced, use the
  wrong import, or the affected test command could omit the packages; the guard
  and explicit package tests catch those cases, while the final diff review
  checks no production path was changed.

## Pass 1: critique and roll-up

### Critique

The plan is appropriately narrow, but it should name the exact guard source
and verify the import path from an existing package before editing. It should
also make clear that test expectation changes are forbidden because the
product contract did not change.

### Critical evaluation of critique

Both additions are convention-sensitive: guessing the import or using a build
tag would recreate the rejection. Naming the guard and comparing a known-good
file are useful safeguards. The no-expectation-change rule prevents a proxy
fix from hiding a real regression.

### Revised roll-up

Before editing, inspect the guard implementation and two nearby compliant
files, then copy only the established untagged import pattern. Keep all
existing assertions unchanged and record that rationale in the commit.

### No-change decisions

No production architecture, API schema, generated file, or documentation
changes are warranted. No subagent is needed for a two-file mechanical repair.

## Pass 2: critique and roll-up

### Critique

The revised plan covers the rejection, but the final verification should
explicitly use the same affected-test detector as CI and confirm the branch
shape/remote identity before reassignment. The temporary execution log must be
removed before the clean commit.

### Critical evaluation of critique

The affected detector is the strongest local evidence available for the
branch's changed surface, and branch identity is part of the refinery contract
after recovery. Removing the temporary log prevents accidental unrelated
artifacts from entering the patch.

### Revised roll-up

Run the repository guard, focused tests, affected detector, and required
quality gates; inspect `git status`, branch shape, and remote tip before the
done sequence. Keep `agent-execution.log` outside the committed diff after
using it for milestones.

### No-change decisions

Do not reimplement or retest the already-reviewed launch-origin feature beyond
the affected package checks. Do not weaken any guard, alter test expectations,
or introduce a new test helper abstraction.

## Final roll-up

The safe implementation is two untagged, package-local environment imports,
followed by guard/package/affected verification and normal refinery handoff.
The target behavior and product contract remain unchanged.
