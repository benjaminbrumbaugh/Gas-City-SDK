# sdk-7pn plan

counter: 3

## Full plan, tasks, and subtasks

1. Establish the baseline on clean `origin/main`.
   - Run the repository-required preflight checks, including `go vet ./...`.
   - Capture the exact failing package, compiler command, and missing-header
     evidence without mutating the checkout.
   - Compare the failure with the documented ICU prerequisite and inspect the
     dependency/build boundary before deciding whether a repository change is
     justified.
2. Decide the smallest maintainable remediation.
   - If the failure is caused by an incomplete repository contract, add a
     narrowly scoped build/configuration or documentation fix with a focused
     regression check.
   - If the failure is strictly an environment prerequisite already documented
     on clean base, do not paper over it with a skip or proxy test; record the
     evidence and hand off a no-op result to the refinery.
3. Verify the selected remediation at the layer it claims to affect.
   - Run focused tests/build checks for changed files.
   - Re-run `go vet ./...` when the environment permits, or preserve the
     bounded failure evidence and distinguish it from changed-surface tests.
   - Run formatting/static checks required by the formula.
4. Review the diff for scope, security, upstream alignment, and unintended
   changes; commit only the cohesive result.
5. Push the per-bead branch, record the verified work outcome, and reassign the
   open implementation bead to the refinery without closing it.

## Architectural changes

No architectural change is presumed. The ICU dependency is below the Gas City
integration surface; any fix must remain at the dependency/build/documentation
boundary and must not weaken repository-wide static-analysis guarantees.

## Test and evidence plan

- Target truth: `go vet ./...` can analyze the repository in the supported
  environment, including the Dolt ICU dependency.
- Required evidence layer: repository-wide Go static analysis from a clean
  base checkout, plus focused tests for any changed package/configuration.
- Useful but insufficient proxies: `go test` for an individual package,
  `go test ./...`, or checking only Gas City integration fixtures. These do not
  prove the C/C++ header include or whole-repository vet behavior.
- False-completion substitution to avoid: changing expectations or excluding
  the ICU package merely to make a local command green without an explicit
  supported-environment contract.
- If the plan succeeds, the original bug could still remain if the local
  command does not compile `go-icu-regex/internal/icu`; therefore the final
  report must name the observed layer and residual environmental limitation.

## Support structures

- `agent-execution.log` will record completed subtasks and evidence-layer
  boundaries.
- The plan file is the durable evidence record for this no-source handoff;
  `agent-execution.log` remains temporary coordination state.
- No new harness is warranted unless baseline investigation shows a repeatable
  repository-level regression that a focused test can observe.

## Documentation

Inspect the existing ICU prerequisite documentation and update it only if the
repository contract is inaccurate or incomplete. Do not duplicate architecture
or integration docs for an environment-only failure.

## Execution order

Plan refinement (three passes) -> workspace/preflight baseline -> dependency
and history inspection -> minimal remediation or evidence-only handoff ->
focused and required checks -> review/commit -> push/refinery handoff.

## Stability strategy

Keep the branch based on freshly fetched `origin/main`; use bounded commands
for potentially expensive checks; never disable vet globally; preserve
pre-existing worktree changes outside this per-bead worktree; and keep any
code change small enough to rebase cleanly onto upstream.

## Blocker avoidance

Use the existing documented ICU installation path or package metadata before
inventing a workaround. If the required system header cannot be installed in
this environment, capture the compiler error and report it as a pre-existing
environment blocker rather than modifying unrelated code. Escalate only after
the failure is reproduced twice and its owning layer is clear.

## Candidate parallel work

No independent subagent is available in this session. The investigation can be
split conceptually into (a) baseline/static-analysis evidence and (b)
dependency/history inspection, but both are short and share the same checkout;
sequential execution keeps the evidence attributable.

## Planning pass 0: initial draft

The draft keeps the decision open between a real repository-contract fix and a
no-op pre-existing failure. This is intentional until the clean-base vet
failure and the existing ICU documentation are inspected.

### Critique of major sections

- Tasks are appropriately ordered but must not assume that a bug fix exists;
  the issue may be an environment-only gate failure.
- Architectural changes correctly preserve the dependency boundary, but the
  plan should explicitly prohibit build-tagging away required source merely to
  satisfy local vet.
- Evidence distinguishes target truth from proxies and should remain in the
  final handoff.
- Support structures are intentionally minimal; adding a harness before the
  failure mechanism is known would be premature.
- Documentation scope is narrow and avoids touching product dashboard/docs
  unrelated to the failure.
- Execution and stability sequencing cover branch safety and required gates.
- Blocker avoidance needs an explicit retry limit and escalation path, added in
  the next pass.

### Critical evaluation of the critique

The critique preserves the key uncertainty and identifies the only material
gap: a bounded investigation must distinguish an unavailable host prerequisite
from a repository defect. The plan should also state that changing tests is
allowed only when the product/build contract changes or the expectation is
independently wrong.

### Roll-up: revised critique

Add explicit no-change decisions, a two-attempt diagnostic limit, and a test
expectation policy. Keep the source-tree change optional until evidence proves
it is needed.

### Roll-up: revised plan/tasks

The next pass will add those constraints and then re-check whether any required
task or evidence layer was lost.

### No-change decisions

- Do not modify upstream-owned Go code before reproducing the clean-base failure.
- Do not add a vet exclusion, build tag, or fake header solely to make the
  command pass.
- Do not touch the embedded dashboard, product dashboard, or unrelated
  Gastown integration fixtures.

## Planning pass 1: evidence and contract refinement

### Full plan/tasks/subtasks revision

1. Run `go vet ./...` on this clean branch and preserve the first failure's
   package, source include, and host toolchain details.
2. Read the current ICU prerequisite documentation, `go.mod` dependency
   metadata, and relevant git history to determine whether the repository
   promises a vendored/self-contained ICU build or an explicitly provisioned
   host dependency.
3. If the documented contract is accurate and the failure is host-only, make
   no product change; document the exact pre-existing gate result in the bead
   handoff. If the contract is inaccurate, make the smallest source-of-truth
   documentation or build-contract correction and test that correction.
4. Run changed-surface tests plus the required static checks, labeling each by
   the system layer observed. Do not claim repository-wide vet success from a
   narrower proxy.
5. Commit, push, verify the remote branch tip, and hand off to refinery.

### Architectural changes

Still none presumed. A valid implementation must preserve the boundary between
Go package analysis and the host ICU/C++ toolchain; it may clarify or enforce
that boundary but must not hide it.

### Test plan and evidence audit

- Target truth remains whole-repository vet with the ICU package compiled.
- Required evidence is one clean-base reproduction and one post-change result
  at the same layer, if a source change is made.
- `go test` or a package-local vet run can be reported as useful proxy evidence
  only, with its narrower layer stated.
- A tempting false completion is a CI/configuration skip that makes vet green
  while leaving the missing header unresolved; explicitly reject it.
- If the plan appears successful while the compiler still cannot include
  `unicode/regex.h`, the original failure remains. The final handoff must say
  so rather than overclaiming.

### Support, docs, and execution

Keep the temporary execution log outside the shipped diff. The plan itself is
the reviewable evidence artifact. Inspect and edit only the existing ICU prerequisite documentation if it is the correct
source of truth. Execute: reproduce -> inspect contract/history -> choose
no-op or minimal correction -> verify at matching layer -> review -> handoff.

### Stability and blocker avoidance

Run the diagnostic at most twice before escalating the same unresolved cause;
avoid installing or changing system dependencies through repository changes.
If no repository defect is found, the correct stable result is an evidence-only
handoff, not a speculative workaround.

### Candidate parallel work

No subagent is available. The history/documentation inspection could be
independent of the first vet run, but keeping both in one ordered evidence
chain avoids confusing host state with source state.

### Critique of pass 1

- The revised plan is now explicit about the target layer and no-op outcome,
  but it should name the exact contract decision points: supported OS/toolchain,
  required header provider, and whether CI installs it.
- The test policy is complete for changed code but should explicitly preserve
  the project's rule that expectations change only for a changed contract or an
  independently wrong test.
- The blocker limit is clear, but the final handoff should include the command
  and failure identity in concise form for the refinery.

### Critical evaluation of the critique

These are clarifications rather than new work. Adding them improves auditability
without broadening scope. The no-op path remains valid only if the existing
documentation actually names the unavailable ICU prerequisite and no code
contract claims self-containment.

### Roll-up: revised critique

Add those contract fields, test-expectation rule, and handoff evidence contents.
Retain the prohibition on workaround exclusions and the three-pass refinement
requirement.

### Roll-up: revised plan/tasks/subtasks

The next pass will add the explicit contract checklist, inspect whether any
information was lost from the initial draft, and finalize the plan before
implementation checks.

### No-change decisions

- Do not change tests unless the build contract changes or a test is proven
  independently incorrect.
- Do not treat a pre-existing base failure as caused by this bead's work.
- Do not install or vendor ICU speculatively; first verify repository intent.

## Planning pass 2: final pre-implementation review

### Full plan/tasks/subtasks revision

1. Reproduce the base failure with `go vet ./...`; record Go version, compiler,
   failing module/package, missing header, and whether the command reached the
   changed Gastown surface.
2. Verify the contract checklist:
   - supported OS/toolchain and host ICU provider;
   - current prerequisite documentation;
   - `go.mod`/module version and relevant source ownership;
   - CI/setup instructions that install or assume ICU.
3. Inspect history for an existing fix or prior parity work before inventing a
   mechanism. Choose exactly one outcome: minimal contract correction with a
   focused regression check, or evidence-only no-op handoff.
4. Run matching-layer verification. For a source change, run focused tests,
   formatting, vet/build checks, and the required affected/full test command;
   for a no-op, rerun the baseline gate only as practical and preserve the
   pre-existing failure without altering expectations.
5. Review scope and upstream mergeability, remove temporary planning/log files
   from the product diff, commit the cohesive result, push and verify the
   remote branch, record work outcome, and reassign the open bead to refinery.

### Architectural changes

No architectural changes. The only acceptable source delta is at the existing
dependency/build/documentation boundary, with no generic SDK behavior or
role-specific logic added.

### Test plan and proxy audit

The target truth is repository-wide vet including `go-icu-regex/internal/icu`.
The required evidence layer is the same command on clean base and, if changed,
the same command after the fix. Package tests and fixture checks are cheaper,
useful proxies but do not prove the header include or whole-repository static
analysis. A skip/exclusion or fake header is a false-completion substitution.
If all focused checks pass but the ICU header is still unavailable, the original
bug remains; report that explicitly. Test expectations may change only when the
product/build contract changed or the existing expectation is independently
wrong; a host failure alone is neither.

### Support structures, docs, execution order

Use the temporary `agent-execution.log` for completed subtasks and evidence
layers. Keep the plan as the durable evidence artifact. Update only the existing ICU prerequisite
documentation when inspection proves it incomplete. Execute the five tasks in
order, with no parallel source edits.

### Stability strategy and blocker avoidance

Use the freshly fetched `origin/main` and bounded commands. Do not mutate the
shared rig or reusable home worktree. After two reproducible attempts, if the
same host prerequisite blocks progress and no repository defect is present,
handoff with evidence and escalate only through the authenticated bead/mail
path if required. Never leave a known workaround or cleanup vague.

### Candidate parallel work

No subagents are available or warranted: the target is a single compiler/build
boundary and split investigations would not reduce risk.

### Critique of pass 2

- The plan now fully separates target truth from proxies and names the no-op
  outcome, but the final review must ensure the temporary plan and execution
  log are not accidentally committed.
- The contract checklist covers source, docs, and CI, but it must not expand
  into redesigning dependency management without evidence.
- The handoff is operationally complete and respects the implementation-bead
  rule: only formula step beads may be closed; the work bead remains open.

### Critical evaluation of the critique

The remaining risks are procedural rather than architectural. The explicit
cleanup check prevents temporary artifacts from leaking, and the scope guard
prevents a pre-existing host failure from becoming speculative dependency work.

### Roll-up: revised critique

No substantive plan changes are needed. The final pass should confirm that no
initial requirement was lost and preserve the exact evidence-layer language in
the eventual bead note.

### Roll-up: revised plan/tasks/subtasks

Proceed with the finalized five-step plan above. Do not implement until the
clean-base preflight has established whether the issue is repository-owned.

### No-change decisions

- No Go code, build exclusion, vendored header, or test relaxation is planned
  without evidence of a repository defect.
- No dashboard, docs area outside the ICU prerequisite, or Gastown fixture is
  in scope.
- No implementation-bead closure is permitted; refinery owns closure.

## Investigation results

- Clean `origin/main` at `23d7e56e8` reproduces the reported raw command failure:
  `go vet ./...` exits 1 in `github.com/dolthub/go-icu-regex/internal/icu`
  because `file.cpp:3` cannot find `unicode/regex.h`.
- The host has the header at `/opt/homebrew/opt/icu4c@78/include/unicode/regex.h`,
  but a bare Go invocation does not receive Homebrew's keg-only include path.
- The supported `make vet` wrapper exits 0, as does
  `CGO_CPPFLAGS="-I$(brew --prefix icu4c)/include"
  CGO_LDFLAGS="-L$(brew --prefix icu4c)/lib" go vet ./...`.
- Existing `README.md`, `docs/getting-started/installation.md`, `Makefile`,
  and CI setup already document/provision this boundary. `sdk-kus` has no
  relevant dependency, Makefile, or CI delta from `origin/main`.
- Decision: no source workaround or suppression is justified. The raw failure
  is an ambient invocation problem; the repository-supported static-analysis
  gate is green on this host.

## Three-pass information-loss check

The refined plan retains: baseline reproduction, dependency/history inspection,
the target-vs-proxy evidence distinction, contract-aware test policy, minimal
scope, temporary artifact cleanup, blocker limits, branch/refinery handoff,
and the possibility of a justified no-op. No requirement from pass 0 was lost.
