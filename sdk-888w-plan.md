# sdk-888w execution plan

counter: 0

## 1. Full plan, tasks, and subtasks

1. Establish the exact `go vet` failure boundary.
   - Read `TESTING.md`, the Makefile quality-gate contract, CI setup actions,
     and the existing ICU-related tests/documentation.
   - Reproduce raw `go vet ./...` and the supported `make vet` path while
     capturing compiler flags and the missing-header diagnostic.
   - Inspect module/history evidence to distinguish a missing dependency from
     an incorrect repository workaround.
2. Design the smallest maintainable remediation.
   - Keep ICU setup at the developer/CI dependency boundary.
   - Preserve CGO-enabled coverage where the project contract requires it;
     do not hide the dependency by weakening vet or disabling CGO globally.
   - Add or update a focused contract test/documentation only where it protects
     the actual dependency path.
3. Verify and hand off.
   - Run focused tests first, then the affected fast gate and `go vet ./...`.
   - Record the exact evidence, limitations, and any environment prerequisite
     in `agent-execution.log` and bead notes.

## 2. Architectural changes

Expected change surface is limited to build/developer setup, CI dependency
declarations, and their contract tests or documentation. No runtime, beads,
session, API, or role behavior should change. The ownership boundary is the
toolchain provisioning layer: it must make the native transitive dependency
available to static analysis without leaking ICU assumptions into Go packages.

## 3. Test plan

- RED: reproduce the failing raw vet command or an equivalent missing-header
  fixture before changing expectations or setup.
- Focused Makefile/CI contract tests covering ICU provisioning and flag wiring.
- `make test-fast-parallel` (documented fast baseline).
- `go vet ./...` in the current environment, with the result classified as
  direct target evidence rather than a proxy.
- If the source surface changes, run the affected package tests and inspect
  formatting; do not claim a local pass if the host lacks the prerequisite.

Evidence discipline: raw `go vet ./...` observes the full Go static-analysis
and native-CGO compilation layer; it does not prove runtime behavior or CI
runner provisioning. Contract tests observe repository declarations and can
prove they remain present, but cannot prove a hosted runner actually has the
headers. A CI workflow review proves intended provisioning, not successful
installation on every runner.

## 4. Support structures

Use existing Makefile variables, CI setup actions, contract tests, and Go
module cache diagnostics. Add no new harness unless an existing test cannot
observe the dependency boundary. Keep transient compiler logs under
`/var/tmp`; keep the plan and execution log in this worktree.

## 5. Documentation

Update the nearest developer/build documentation if the required local
dependency or supported command is currently undiscoverable. Keep the
explanation precise about macOS Homebrew versus Linux package provisioning,
and do not imply that a semantic preview or CGO-disabled test replaces the
target vet check.

## 6. Execution order

Plan and history audit -> raw and supported command reproduction -> dependency
boundary inspection -> RED contract test if needed -> minimal remediation ->
focused verification -> fast baseline/vet -> evidence and handoff.

## 7. Stability strategy

Do not alter product expectations solely to make the local host green. Do not
run `go clean -cache`, set shared caches to `/tmp`, or install ad hoc files
inside the repository. Keep CI commands noninteractive and make native setup
idempotent. If the host cannot satisfy a prerequisite, report that as a
toolchain limitation with the exact command and layer observed.

## 8. Blocker avoidance

Preserve unrelated untracked artifacts already present in this reused
worktree. Work only on `polecat/sdk-888w`; use `git show` for archaeology and
avoid broad filesystem traversal. If a dependency installation requires
credentials or external human action, record and escalate rather than hiding
the failure behind a proxy.

## 9. Candidate parallel work

History archaeology, Makefile/CI contract inspection, and environment header
census are independent read-only investigations. Reproduction and any source
change remain sequenced so RED evidence is not confused with a later setup.

## 10. Proxy audit

- Target truth: the documented repository quality gate can run `go vet ./...`
  with the transitive ICU headers available on supported developer/CI hosts.
- Required evidence layer: actual `go vet ./...` process output after the
  repository's intended dependency setup, plus focused tests for the setup
  contract.
- Useful but insufficient proxies: `CGO_ENABLED=0 go vet`, a package-only vet,
  a passing contract test, or the presence of an ICU package without a compile
  probe.
- Tempting false completion: skipping the CGO package, weakening the vet
  target, or documenting an install without verifying flags/header resolution.
- If the plan succeeds, the original bug could remain if only CI YAML or a
  fixture passes while raw local vet still cannot locate `unicode/regex.h`;
  the final evidence must state whether the target command itself passed.

## Planning pass 1

### Critique of the plan, top to bottom

- The target is correctly framed as a toolchain boundary, but the plan must
  compare raw `go vet` with the repository-supported `make vet` because their
  environment propagation can differ.
- The architecture section properly avoids runtime changes; it should name the
  transitive `go-icu-regex` package as the observed native dependency.
- The test plan separates direct vet evidence from declaration-only tests, but
  it needs an explicit check for the actual header spelling/layout.
- Support and stability constraints are adequate and should retain any
  existing tests that protect Makefile OS-specific branches.

### Critical evaluation of that critique

The header spelling/layout check is decisive: an absent `unicode/regex.h` is
not repaired by an include-path change, while a present header outside the
compiler search path is. The fix must therefore follow the observed layer,
not assume that installing a package is sufficient.

### Roll-up

Add a direct module-cache/header census and compiler include-path observation
before selecting between dependency provisioning, flag propagation, or a
dependency-version correction.

### No-change decisions

- Do not disable CGO or change `go vet` expectations before the direct probe.
- Do not replace the native dependency with a parallel regex implementation.
- Do not treat existing CI comments as proof of local developer support.

counter: 1

## Planning pass 2

### Critique of the refined plan, top to bottom

- The direct probe now identifies the likely cause, but the plan should inspect
  history for prior ICU fixes before inventing a new path.
- The architecture boundary remains correct; changes should be minimal and
  mergeable against upstream.
- The test plan needs a RED/GREEN distinction for any contract test whose
  expectation changes, because existing tests may already encode the fix.
- Handoff should distinguish source remediation, documentation-only diagnosis,
  and an unresolved host prerequisite.

### Critical evaluation of that critique

History is especially important here because the repository already contains
platform-specific ICU provisioning logic. Repeating it in another script
would create divergent ownership. Existing passing contract tests may also
prove that the repository is already correct and the bead is only a local
environment report.

### Roll-up

Treat current repository behavior and history as primary evidence. Make no
source change if the supported gate already handles the dependency and raw
`go vet` is intentionally outside the supported contract; otherwise add only
the missing owner-boundary correction with a failing test first.

### No-change decisions

- Do not alter an independently correct test expectation.
- Do not add a second ICU installer when setup actions already own the install.
- Do not call the bug fixed from a CGO-disabled baseline alone.

counter: 2

## Planning pass 3

### Critique of the refined plan, top to bottom

- The investigation and ownership rules are complete; the final execution log
  must include the exact command layer observed and not just a summary.
- The test plan covers the repository's supported fast gate and direct vet;
  it should also record the configured pre-commit hook state before handoff.
- The residual-risk statement is explicit and should be carried into bead notes
  if the host remains unable to run raw vet.

### Critical evaluation of that critique

No missing architectural boundary is exposed. The likely valid outcomes are a
small setup/documentation correction, or an evidence-backed no-change report
if the repository already provisions ICU and only the unwrapped raw command is
unsupported on this host. Both outcomes require preserving the distinction
between local environment truth and CI intent.

### Roll-up

Proceed with the history-informed direct probe, RED/GREEN only for a justified
source change, and the documented quality gates. Keep the branch small and
handoff-ready even if the final result is a diagnosis rather than code.

### No-change decisions

- Do not claim success from semantic or declaration-only evidence.
- Do not close an implementation bead; submit it to the refinery for review.
- Do not leave known cleanup or the host prerequisite unexplained.

counter: 3

## Lost-information check after three passes

Retained: exact target command, native dependency/header layout, supported
Makefile-vs-raw-vet distinction, history archaeology, ownership boundary,
RED/GREEN discipline, required gates, proxy audit, and residual-risk handling.
No material information was lost during refinement. Execute the plan now; the
plan is recorded for review without waiting on an additional runtime approval.

## Execution evidence

- `go vet ./...` reproduced the reported failure on this macOS host:
  `github.com/dolthub/go-icu-regex/internal/icu` could not find
  `unicode/regex.h`.
- The ICU headers are present under Homebrew's `icu4c@78` prefix. The failure
  is therefore an include-path propagation issue for the raw command, not a
  missing repository dependency.
- `make vet` passed because the Makefile discovers the Homebrew prefix and
  exports `CGO_CPPFLAGS`/`CGO_LDFLAGS`; raw `go vet ./...` with those same
  explicit flags also passed.
- `make test-ci-policy` passed, including the existing CI/Homebrew/Linux ICU
  provisioning contract tests.
- The only product-facing change is the README command guidance. No test
  expectation or production behavior changed.
- `make test-fast-parallel` passed all fast jobs, including six `cmd/gc` shards.
- `make check-docs` passed after the README clarification.

## Final assessment

The original failure is reproducible only for the unwrapped raw command on
this macOS host. The repository-supported `make vet` path is green and the
required ICU setup is already owned by the Makefile and CI actions. The README
now names that supported path, so no source or test-expectation change is
justified. A raw `go vet ./...` invocation without the platform flags remains
an environment/configuration limitation, not evidence of a product defect.
