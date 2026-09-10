# sdk-cvo plan

counter: 0

## Objective

Make `make lint-affected` and `make fmt-check-changed` work when the
checkout path contains spaces. Preserve the existing helper script contract,
keep the Makefile path resolution portable, and add a regression that runs a
real gate from a spaced checkout path.

## Full plan, tasks, and subtasks

1. Establish source and baseline evidence.
   - Inspect the Makefile helper-path definition, affected-gate recipes, and
     existing shell/Make test conventions.
   - Reproduce the failure from this spaced worktree before editing.
   - Check history for a prior fix or adjacent path-resolution conventions.
2. Implement the smallest boundary-local fix.
   - Keep path resolution at the Makefile/recipe boundary; do not alter the
     helper script's interface or duplicate its behavior in Make.
   - Replace whitespace-splitting Make list functions with a path mechanism
     that preserves spaces and remains correct when invoked from the repo.
   - Keep both affected gates on the same helper path and preserve quoting.
3. Add direct regression evidence.
   - Prefer an existing test seam; otherwise add a focused shell/Make test
     that copies or links the checkout under a path containing a space and
     invokes the actual affected target.
   - Assert the target reaches the helper and fails only for its intended
     validation result, not with a missing-script/path error.
   - Keep the test deterministic, disposable, and free of network or live
     city state.
4. Verify and hand off.
   - Run the RED/GREEN focused check, affected checks, formatting, and
     relevant Go quality gates where practical.
   - Review the diff against `origin/main`, preserve unrelated work, commit
     the cohesive change, push `polecat/sdk-cvo`, and hand the open work bead
     to the refinery.

## Architectural changes

No new abstraction is needed. The Makefile owns selecting repository-local
helpers, while `scripts/ci-static-select` remains the owning boundary for
static-check selection. The fix must prevent Make's list parser from
reinterpreting a filesystem path; it must not add role, environment, or
provider-specific behavior to the helper.

## Test plan and evidence discipline

Target truth: both documented Make targets can locate and execute
`scripts/ci-static-select` from a checkout whose absolute path contains a
space. Required evidence is an actual target invocation from such a path;
source inspection and a direct helper invocation are useful but insufficient
proxies. The tempting false completion is to quote the already-corrupted
variable or test only from a path without spaces. If this plan succeeds, the
bug could remain if another recipe reconstructs the helper path independently,
if the test exercises only one target, or if a relative-path assumption breaks
when Make is invoked outside the repository; inspect all call sites and test
both affected targets.

## Support structures

Use the existing Makefile, helper script, shell-test conventions, and a
temporary `agent-execution.log`. Use a disposable copied checkout only if the
repository's test seam requires it. Do not add production fixtures,
dependencies, or a second path-resolution utility.

## Docs

No user-facing documentation change is expected. Update comments only if the
corrected Makefile ownership or path invariant needs explanation. The plan
records the regression contract and evidence layer.

## Execution order and stability strategy

Source/history inspection -> RED reproduction -> focused regression ->
Makefile fix -> GREEN checks -> affected quality gates -> diff/ownership
review -> commit/push/refinery handoff. Work only in the bead worktree, keep
the branch based on `origin/main`, and avoid broad rewrites or generated-file
changes.

## Blocker avoidance and candidate subagent-parallel work

Independent read-only searches can be parallelized: Makefile call sites,
existing spaced-path tests, and history. The regression and implementation
remain sequential because the failing invocation defines the narrowest fix.
No subagent is required for this small boundary-local change; an independent
review of the Make expression and test layer would be the only useful parallel
work if one is available.

## Pass 1 critique (top-to-bottom)

- The objective names both failing gates and the space-sensitive boundary,
  but the exact portable replacement must be selected after inspecting the
  supported Make versions.
- The implementation scope is appropriately narrow and preserves the helper
  boundary; it must avoid depending on a shell feature unavailable to the
  project's supported environments.
- The regression requirement observes real target execution, but its expected
  helper result must distinguish successful path resolution from a legitimate
  static-check failure.
- The verification plan is proportional, though the repository's documented
  test gates should be confirmed before choosing the broadest sweep.

## Pass 1 critique evaluation

The main risk is claiming portability from a local shell-only workaround. The
source archaeology must identify the Make version and existing path idioms,
and the test must prove both targets invoke the helper from the actual spaced
worktree.

## Roll-up: revised critique applied

Task 1 now requires Make-version and path-idiom evidence. Task 3 requires
target-level assertions for both affected recipes and a failure classification
that cannot pass on a missing helper path.

## Roll-up: revised critique applied to tasks

Select the implementation only after checking the Makefile's portability
contract and make the regression command use the same environment a
contributor uses, with a disposable path containing a literal space.

## Explicit no-change decisions

- No changes to `scripts/ci-static-select` unless reproduction proves its
  interface itself is defective.
- No new path utility or abstraction.
- No role-specific logic, generated API changes, or runtime/provider edits.
- No weakening of lint or format expectations to make the gate pass.
- No mutation of the shared rig checkout or unrelated worktrees.

## Pass 2 critique (top-to-bottom)

- The plan now distinguishes Make parsing from shell execution and requires
  evidence at the public target boundary.
- It still needs a concrete decision on whether copying a full checkout is
  affordable or whether an existing harness can execute from a symlinked or
  temporary path without masking the absolute-path behavior.
- Testing both targets is necessary, but assertions should remain stable
  across tool availability and should not encode incidental diagnostic text.
- The no-change list prevents scope creep while allowing a comment if the
  Make expression would otherwise regress.

## Pass 2 critique evaluation

Use the smallest existing shell/Make harness. If no harness exists, a
disposable copy is acceptable only when it preserves the repository layout and
uses the real targets. The stable assertion is absence of the known missing
script/path error plus successful helper discovery, not a specific lint
finding.

## Roll-up: revised critique applied

Task 1 adds a search for existing path-space regression harnesses. Task 3
allows a disposable copy only as a fallback and defines evidence as the real
targets reaching the helper without matching the path-corruption diagnostic.

## Pass 3 critique (top-to-bottom)

- The source boundary, regression layer, and portability risk are explicit.
- The plan avoids a false completion based on quoting a value after Make has
  already split it.
- The evidence plan covers both call sites and identifies direct helper tests
  as insufficient.
- The execution order keeps the RED result before production edits and leaves
  no known cleanup or speculative architecture.

## Pass 3 critique evaluation

No material gap remains before source archaeology. After three passes, verify
that the chosen Make expression, test layer, and both target call sites remain
represented; revise this plan before editing if the current code exposes a
different owner.

## Final roll-up and approval state

The plan is approved for autonomous execution under `mol-polecat-work`. The
next action is read-only source archaeology followed by a failing focused
regression before production edits.

counter: 3
