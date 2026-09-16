# sdk-81su execution plan

counter: 0

## 1. Full plan, tasks, and subtasks

1. Establish the exact failure boundary.
   - Read `TESTING.md`, the Makefile vet/build contract, ICU setup actions,
     and current documentation.
   - Reproduce raw `go vet ./...`, the supported `make vet` target, and the
     same command with the Makefile's explicit flags when available.
   - Confirm the installed header layout and inspect history for prior ICU
     fixes before changing source.
2. Select the smallest owner-boundary remediation.
   - Keep native dependency provisioning in developer/CI setup and flag
     propagation in the Makefile; do not weaken CGO-enabled vet.
   - Add only focused documentation or contract coverage justified by RED
     evidence. Do not duplicate an existing installer or alter expectations
     merely to make this host green.
3. Verify and hand off.
   - Run focused tests, the documented affected/full test gate, `go vet ./...`
     and any relevant documentation check.
   - Record commands, outcomes, evidence layers, limitations, and the final
     source delta in `agent-execution.log` and bead notes.

## 2. Architectural changes

Expected scope is the build/developer setup boundary: documentation, Makefile
environment wiring, CI dependency declarations, and their contract tests if a
gap is found. No runtime, beads, session, API, or role behavior should change.
The transitive `github.com/dolthub/go-icu-regex` CGO package is the observed
native dependency. ICU assumptions must remain outside Go package logic and
must be owned by the platform provisioning or build-command boundary.

## 3. Test plan

- RED: capture raw `go vet ./...` failure, if reproducible, before changing
  expectations or setup.
- Compare `make vet` with raw vet and with explicit ICU include/library flags.
- Run focused Makefile/CI contract tests for any changed setup contract.
- Run the affected detector when configured, otherwise the documented fast
  test baseline, plus the repository documentation check if README changes.
- Run plain `go vet ./...` and classify its result as direct target evidence;
  `CGO_ENABLED=0 go vet ./...` is only a diagnostic proxy.

Evidence discipline: raw `go vet ./...` observes repository-wide Go static
analysis and native-CGO compilation on this host; it does not prove CI runner
provisioning or runtime behavior. Contract tests observe repository
declarations and flag wiring; they do not prove an actual hosted runner can
install or compile with ICU. `make vet` observes the supported local gate,
not an unwrapped ad-hoc command.

## 4. Support structures

Use the existing Makefile variables, setup actions, contract tests, module
cache, and compiler diagnostics. Keep bounded transient logs under `/var/tmp`.
Maintain this plan and `agent-execution.log` in the per-bead worktree. Add no
new harness unless an existing test cannot observe the dependency boundary.

## 5. Documentation

If local setup is technically supported but undiscoverable, update the nearest
developer/build documentation to name `make vet` and the platform-specific ICU
package/setup. State precisely that macOS Homebrew ICU is keg-only and that
Linux requires the development package. Do not present CGO-disabled vet,
declaration tests, or an install instruction as proof that the target command
ran successfully.

## 6. Execution order

Plan -> workspace/branch checks -> history and dependency audit -> raw and
supported command reproduction -> RED contract test if needed -> minimal
source/documentation change -> focused verification -> fast/affected gate and
vet -> execution log and refinery handoff.

## 7. Stability strategy

Preserve unrelated work in the polecat home; all edits stay in this worktree.
Do not run `go clean -cache`, set `GOCACHE` or `TMPDIR` to `/tmp`, or install
ad-hoc headers in the repository. Use noninteractive dependency commands.
Keep the patch small and upstream-friendly. If this host cannot satisfy the
native prerequisite, report that as a toolchain limitation with exact command
output rather than claiming a product fix.

## 8. Blocker avoidance

Use `git show` and bounded `rg` searches for archaeology. Avoid broad
filesystem traversal. Existing branch/worktree metadata is authoritative; do
not switch to the polecat home. If credentials or human-only dependency setup
is required, preserve the evidence, escalate to Witness, and leave the bead
open rather than hiding the failure behind a proxy.

## 9. Candidate parallel work

History archaeology, Makefile/CI contract inspection, and host ICU/header
census are independent read-only investigations. Command reproduction and
source edits remain sequenced so RED evidence is not confused with setup after
the change.

## Planning pass 1

### Critique of the plan, top to bottom

- The target and likely ownership boundary are correct, but raw vet must be
  compared with the repository-supported `make vet` path.
- The architecture section correctly avoids runtime changes; it should retain
  the explicit transitive package name so a future fix does not drift.
- The test plan distinguishes direct and proxy evidence, but must verify the
  actual header spelling/layout rather than assuming package installation is
  sufficient.
- The handoff should classify documentation-only remediation separately from
  a source fix or an unresolved host prerequisite.

### Critical evaluation of that critique

The header census is decisive: an absent header calls for provisioning, while a
present header outside the compiler search path calls for flag propagation.
History is needed before adding a second owner for either behavior.

### Roll-up: apply evaluation to critique

Add a direct module-cache/header census, compiler include-path observation, and
history check before selecting between provisioning, Makefile wiring, and docs.

### Roll-up: apply revised critique to plan

The implementation gate now requires current source and history evidence before
any test expectation or setup change. The final report must name whether direct
plain vet passed, not infer it from a proxy.

### No-change decisions

- Do not disable CGO or weaken vet expectations.
- Do not replace the native dependency or add a parallel regex implementation.
- Do not treat existing CI comments or a package name as proof of local setup.

### Proxy audit

- Target truth: the supported repository quality gate can run CGO-enabled
  `go vet ./...` with ICU available on supported hosts.
- Required evidence layer: actual `go vet ./...` after intended setup, plus
  focused declarations/flag tests.
- Useful but insufficient proxies: `CGO_ENABLED=0 go vet`, package-only vet,
  passing contract tests, or an ICU package existing without a compile probe.
- Tempting false completion: skipping the CGO package or merely documenting an
  install command.
- If this plan succeeds, the bug could remain if only YAML/tests pass while
  direct vet still cannot resolve `unicode/regex.h`; record that explicitly.

counter: 1

## Planning pass 2

### Critique of the refined plan, top to bottom

- The direct probe is now required, but archaeology must explicitly check the
  existing ICU Makefile/CI fixes and any prior docs change before duplicating
  them.
- The boundary remains appropriately narrow and compatible with upstream.
- Any changed expectation needs a RED/GREEN justification; existing passing
  contract tests should be treated as evidence the repository may already be
  correct.
- The final result needs a clear no-change diagnosis path if only the raw
  unwrapped command fails while `make vet` passes.

### Critical evaluation of that critique

The repository already contains platform-specific ICU setup. Duplicating it
would create divergent ownership, while the reported failure may be a user
invoking a command outside the supported wrapper. The source change is only
justified by a demonstrated gap at the owning boundary.

### Roll-up: apply evaluation to critique

Make current behavior, history, and direct command output primary evidence.
Use RED/GREEN only for a genuinely missing contract. Prefer a concise README
clarification when the implementation is correct but the supported command is
not discoverable.

### Roll-up: apply revised critique to plan

The execution order now prioritizes history and current-contract inspection,
then distinguishes a README-only fix from a native setup fix. Handoff notes
must state why test expectations were or were not changed.

### No-change decisions

- Do not add a second ICU installer if existing setup actions own installation.
- Do not alter a correct contract test expectation.
- Do not call the issue fixed from `CGO_ENABLED=0` evidence alone.

### Proxy audit

- Target truth: a developer or supported CI runner can execute the required
  CGO-enabled vet gate through its documented owner boundary.
- Required evidence layer: direct target invocation plus the exact Makefile/CI
  setup path and focused tests.
- Useful but insufficient proxies: source-string checks, `make vet` alone if
  the task is specifically raw vet, or disabled-CGO vet.
- Tempting false completion: adding docs without checking that `make vet`
  actually supplies usable flags, or adding flags without checking headers.
- The original failure could remain if README guidance points to `make vet` but
  that target still fails on this host; direct execution decides that.

counter: 2

## Planning pass 3

### Critique of the refined plan, top to bottom

- Investigation, ownership, and no-change rules are complete; the execution
  log must preserve exact commands and classify each evidence layer.
- The test plan covers direct vet, supported vet, focused contracts, and fast
  tests; it should also record the pre-commit hook configuration before push.
- The residual-risk statement must be copied into bead notes if raw vet remains
  unavailable locally.

### Critical evaluation of that critique

No new architectural boundary is exposed. Valid outcomes are a small
documentation/setup correction or an evidence-backed no-source-change report.
Both require keeping local host truth separate from CI intent and proxy tests.

### Roll-up: apply evaluation to critique

Add hook-state capture and preserve the direct-vet limitation in the handoff.
Do not broaden the patch to unrelated runtime or orchestration code.

### Roll-up: apply revised critique to plan

Proceed with the history-informed probe, RED/GREEN only when justified, then
run the documented quality gates and record direct evidence before submission.

### No-change decisions

- Do not claim success from declaration-only evidence or semantic proxies.
- Do not close the implementation bead; submit it to the refinery.
- Do not leave known cleanup or an unresolved host prerequisite unexplained.

### Proxy audit

- Target truth: the exact required static-analysis command works under the
  supported platform setup, or its unsupported invocation is clearly framed.
- Required evidence layer: direct `go vet ./...` output and supported `make vet`
  output, with setup/contract tests as secondary evidence.
- Useful but insufficient proxies: `CGO_ENABLED=0 go vet`, CI YAML review, or
  a README diff.
- Tempting false completion: treating the existing CI install as proof of this
  macOS host, or treating a green proxy as proof of native compilation.
- The bug could remain if the direct command is never rerun after edits; rerun
  it and report failure honestly if the environment still lacks usable ICU.

counter: 3

## Lost-information check after three passes

Retained: direct target command, supported wrapper distinction, native
dependency/header layout, history archaeology, owner boundary, RED/GREEN test
discipline, quality gates, hook state, proxy audit, and residual-risk handling.
No material information was lost during refinement. Execute under the assigned
polecat workflow without waiting for an additional runtime approval.
