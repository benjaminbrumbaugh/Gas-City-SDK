# sdk-k49 plan

counter: 3

## Pass 0 — initial plan

### Full plan

Investigate and fix the pre-existing `internal/doctor` test flake in
`TestCustomTypesCheck_TableDrift`, where `t.TempDir` cleanup can report a
non-empty directory during the fast unit suite. Preserve the test's contract
and make cleanup deterministic without weakening assertions or hiding child
process failures.

### Tasks and subtasks

1. Read repository testing guidance and inspect the doctor test/helper and its
   subprocess/filesystem lifecycle.
2. Reproduce the reported failure under the relevant runner and with repeated
   or concurrent invocations; identify which process or file remains alive.
3. Write or strengthen a focused regression test first, so the failure is
   observable at the correct unit-test layer.
4. Implement the smallest lifecycle fix at the owning abstraction boundary.
5. Run the focused test, affected tests, required fast checks, `go vet`, and
   review the diff for unrelated changes.
6. Commit, push the per-bead branch, record verification metadata, and hand
   the work bead to the refinery without closing it.

### Architectural changes

Prefer no production architecture change. If the defect is in a shared test
helper, keep the fix in the test/evidence boundary; if production code owns
the child-process lifecycle, use its existing context/wait boundary rather
than adding test-only sleeps or global state.

### Test plan

- Focused `internal/doctor` reproduction before and after the fix.
- Repeated/concurrent invocation sufficient to exercise cleanup races.
- Affected package tests using the rig's configured command.
- Fast unit baseline, then `go vet ./...` and any repository-mandated gates.

### Support structures

Use a temporary diagnostic harness or test-only instrumentation only if it
helps identify the remaining child/file and is removed or kept as a focused
regression test once the root cause is known. Maintain `agent-execution.log`
with one concise entry per completed subtask.

### Documentation

No user documentation is expected unless the final lifecycle contract or
developer testing guidance changes. Keep comments limited to the invariant
that makes cleanup safe.

### Execution order

Plan/refine → inspect guidance and code → baseline/reproduce → regression test
→ implementation → focused and affected checks → review/commit → push and
refinery handoff.

### Stability strategy

Use explicit ownership and synchronization (process wait, pipe closure,
cleanup ordering, or equivalent) rather than sleeps, retries that mask the
failure, or relaxing `t.TempDir` cleanup. Preserve failure visibility and
make the smallest change compatible with parallel tests.

### Blocker avoidance

Do not modify the reusable polecat home checkout or unrelated pre-existing
files. Do not restart Dolt for ordinary test work. If the failure is caused by
an external runner condition, capture bounded evidence and escalate instead of
shipping a false fix.

### Candidate subagent-parallel work

No subagent is necessary initially because the root-cause investigation and
test-first change share the same small package and worktree. Independent
repository-wide test diagnostics could be split only after the focused cause
is understood.

### Proxy audit

- Target truth: repeated fast-suite execution leaves no child process or
  filesystem entry that violates `t.TempDir` cleanup, while test assertions
  still detect table drift.
- Required evidence layer: the `internal/doctor` Go test under the same
  process/concurrency mode that exhibited the flake.
- Cheaper but insufficient proxies: one passing isolated run, `CGO_ENABLED=0`,
  a clean `go vet`, or checking that a directory is empty before test return.
- Tempting false completion: adding a sleep/retry, disabling cleanup, or
  changing the test to ignore cleanup errors.
- If this plan succeeds yet the bug remains: the actual suite's process
  scheduling or child lifecycle differs from the reproduction, or another
  test/helper still leaks a handle; repeated runner-level evidence must guard
  against that.

## Pass 1 — critique of Pass 0

### Full plan

The initial plan was directionally correct but too broad: the repository
already contains a bounded cleanup retry, and history identifies the missing
case. `TestCustomTypesCheck_TableDrift` and its sibling use `t.TempDir()` for
`HOME`; `bd` writes `$HOME/.beads/`, so that HOME directory can outlive the
subprocess's apparent exit. The working directory has a custom retry cleanup,
but HOME is left to `t.TempDir`'s single-shot cleanup. Commit `f7fe81dc1`
provides a proven fix: make guarded cleanup and test-owned HOME helpers, then
use them for every custom-types subprocess fixture.

### Tasks and subtasks

1. Add focused helper-contract tests first, including retry-until-success,
   retry-budget exhaustion, cleanup registration/order, and HOME pinning.
2. Confirm the new tests fail on the unmodified code for the intended missing
   helper/guard behavior.
3. Move the existing retry helper into a dedicated doctor test support file;
   make `guardedTempDir` register it after `t.TempDir`, and make
   `testOwnedHome` use it.
4. Replace all three custom-types test fixture allocations with the helpers.
5. Run focused, repeated, package, affected, fast, and vet checks as available.

### Architectural changes

No production architecture change. The ownership boundary is the doctor test
fixture layer: all directories written by real `bd` subprocesses get the same
cleanup contract, while product code and check semantics remain unchanged.

### Test plan

The new helper tests prove the cleanup mechanism structurally without waiting
on a real race. The existing TableDrift test remains the real `bd`/embedded
Dolt integration at the doctor package boundary. Run it isolated and under
the package/fast runner; one green isolated run remains insufficient evidence.

### Support structures

Use `internal/doctor/tempdir_guard_test.go` as the focused support boundary,
keeping `checks_custom_types_test.go` about custom-types behavior. Keep the
temporary execution log until final review, then remove it before handoff so
the branch contains no process artifact.

### Documentation

No product docs. Comments in the helper should document LIFO cleanup ordering,
why HOME is a real bd write target, and why final removal errors are logged
instead of turning a cleanup race into a false product failure.

### Execution order

Refine → add helper tests and run RED → implement helper and fixture migration
→ GREEN focused tests → package/runner evidence → review and cleanup → commit
and hand off.

### Stability strategy

Keep bounded retry as a backstop, but apply it to the actual HOME and working
directories. Do not add sleeps to product code, disable cleanup, or change
assertions. The helper-contract tests use zero delay and injected removal so
they verify synchronization semantics without elapsed-time dependence.

### Blocker avoidance

Use the known historical patch as evidence, but rebase its relevant slice onto
current `origin/main`; do not port unrelated shared fixture or CI changes from
later history. The default CGO test path currently lacks ICU headers, so use
the documented `CGO_ENABLED=0` path for local doctor evidence and report the
default-build prerequisite separately.

### Candidate subagent-parallel work

No split: helper design, callers, and tests are one cohesive package-local
change. A second agent would duplicate the same fixture-boundary reasoning.

### Proxy audit

- Target truth: every real bd subprocess-owned temp directory, including
  test-owned HOME, is retried before `t.TempDir`'s own cleanup and the fast
  suite no longer fails after a passing test body.
- Required evidence layer: doctor helper contract tests plus the real
  `TestCustomTypesCheck_TableDrift` in package/runner context.
- Useful but insufficient proxies: isolated repeated TableDrift, package pass,
  or the retry helper unit tests alone.
- False completion: guarding only `dir`, widening the old retry budget while
  leaving HOME unguarded, or making TempDir cleanup errors disappear.
- Residual risk: an unrelated real-bd fixture may still need the same shared
  guard; this patch must not claim repository-wide fixture coverage.

## Pass 2 — critical evaluation of Pass 1

### Critique of the critique

The narrowed diagnosis is supported by the historical Mac failure and current
source: HOME is the first temp directory in the failing test and is not
custom-cleaned. The proposed helper tests are valuable because cleanup-order
registration is otherwise invisible after `t.TempDir` succeeds. However, the
plan must not overstate the evidence: Linux cannot reproduce the Mac-specific
race here, and the current existing retry helper can return nil while a child
recreates files after removal. The helper contract should log final failure,
not silently swallow it, and callers should cover `MissingTypes` as well as
both live-store tests because it also pins HOME and may be run in the same
package.

### Evaluation

No production lifecycle fix is justified: the observed failure is test fixture
teardown after a subprocess has exited, and the proven historical repair stays
in test support. The test-first sequence is acceptable even though the first
RED may be a compile failure for the missing helper; the injected-removal
tests then provide behavioral RED/GREEN proof. Keep the patch package-local
and avoid pulling in later cross-package `beadstest` centralization.

## Pass 3 — roll-up and final plan

### Revised plan and tasks

Implement the relevant `f7fe81dc1` slice: add focused cleanup-helper tests;
move retry mechanics to `tempdir_guard_test.go`; expose private doctor-test
helpers `guardedTempDir` and `testOwnedHome`; use them for all three
custom-types tests; run focused RED/GREEN and package/runner checks; remove
the temporary log; commit and submit the per-bead branch.

### Lost-information check

Retained: the original test contract, real subprocess boundary, CGO/ICU
limitation, fast-suite evidence distinction, no production architecture
change, no sleeps in new helper tests, branch/worktree discipline, and
refinery handoff requirements. Added: the unguarded HOME root cause, exact
historical evidence, cleanup-order proof, and the `MissingTypes` caller.

### Explicit no-change decisions

- Keep `CustomTypesCheck` production code unchanged.
- Keep the TableDrift assertions unchanged; this is test-fixture cleanup, not
  a product contract change.
- Do not widen the retry budget as a substitute for guarding HOME.
- Do not port shared `internal/beads/beadstest` or CI changes from later
  commits; that is broader scope than this bead.
- Do not claim the default CGO build is green without ICU headers.

## Execution evidence

- RED: the new helper-contract tests failed to compile because the guard seam
  was absent on `origin/main`.
- GREEN: focused helper and three real custom-types subprocess tests pass with
  `CGO_ENABLED=0`.
- Stability: `TestCustomTypesCheck_TableDrift` passes five consecutive runs;
  the full `internal/doctor` package passes once.
- Static evidence: `CGO_ENABLED=0 go vet ./...` passes. The default CGO path
  remains environment-blocked by missing ICU headers (`unicode/regex.h`).

## No-change decisions

- No production design change is assumed until the lifecycle owner is found.
- No test expectation will change unless the product/test contract is shown to
  be independently wrong; cleanup stability alone is not a contract change.
- No broad refactor or new abstraction will be introduced for a single
  implementation unless two concrete owners require it.
