# sdk-bdl plan

counter: 0
status: planning

## Objective

Restore the repository's documented `go vet ./...` gate on supported macOS
without mutating global Go toolchains or Homebrew state. Prefer the smallest
fork-owned, hermetic dependency boundary that keeps upstream alignment easy.

## Pass 0 — full plan (counter 0)

### Tasks and subtasks

1. Establish the failure boundary.
   - Record current branch/worktree state and the clean-base comparison.
   - Reproduce `go vet ./...` and identify the exact package, compiler, and
     missing ICU artifact.
   - Inspect `go.mod`, module source, build tags, existing scripts, CI, and
     documentation for supported dependency conventions.
2. Select the smallest maintainable fix.
   - Prefer an existing upstream/history solution if one exists.
   - Keep ICU provisioning behind a repository-owned dependency/setup boundary;
     do not add role, product, or host-specific assumptions to SDK code.
   - Reject solutions that merely skip the failing package or suppress vet.
3. Implement test-first.
   - Add or update a focused test/diagnostic that proves the supported macOS
     path is discoverable and enforced.
   - Implement the dependency/bootstrap/configuration change.
   - Document the supported command and failure mode at the owning boundary.
4. Verify the target layer.
   - Run the focused evidence artifact and `go vet ./...` on the supported
     environment.
   - Run affected tests, `go test` as appropriate, and `go vet`/quality gates.
   - Review the diff for upstream compatibility, security, and cleanup.
5. Handoff.
   - Commit only cohesive changes on `polecat/sdk-bdl`.
   - Push and verify the remote ref, record metadata, and reassign to refinery.

### Architectural changes

No new SDK abstraction is planned initially. The likely change is a small
repository-owned dependency/provisioning boundary (script, config, or module
selection) plus its evidence and documentation. Generic Go packages must not
learn about the host's package manager or T3/Dolt-specific behavior.

### Test plan and evidence layers

- Target truth: the supported macOS repository workflow can run `go vet ./...`
  successfully with the required ICU headers available through the documented
  hermetic path.
- Required evidence layer: the actual Go toolchain/vet invocation and the
  dependency path it resolves, not just a parser or file-existence check.
- Useful but insufficient proxies: checking for `unicode/regex.h`, a dry-run
  setup command, or a passing subset that excludes `go-icu-regex`.
- False-completion risk: changing tags or scripts to skip cgo would make a
  green proxy while leaving the supported build unable to vet the package.

### Support structures, docs, execution order, and stability

- Support structures: one focused diagnostic/test only if it protects the
  dependency contract; no speculative abstraction.
- Docs: update the owning contributor/build documentation with prerequisites,
  exact commands, and the boundary's failure behavior.
- Order: evidence first, history/source inspection second, test/diagnostic
  first, implementation, full verification, then handoff.
- Stability: use explicit paths/versions and atomic repository changes; never
  mutate Homebrew or global toolchains. Preserve unrelated worktree files.
- Blocker avoidance: use git history before rebuilding; escalate only after
  bounded reproduction and source inspection. No subagent-parallel work is
  useful because the dependency and vet boundary must be evaluated together.

### Critique of every major section

- Objective: acceptance permits a documented, enforced hermetic path if a
  fully passing host build is not possible; the plan must not overpromise.
- Tasks: baseline comparison and source inspection are necessary, but must
  distinguish missing headers from missing libraries/linker flags.
- Architecture: a script alone may be non-hermetic; the implementation must
  make the dependency source and version explicit.
- Tests/evidence: a successful vet run proves the observed environment only;
  it does not prove every macOS version. The docs and enforcement test must
  cover portability limits.
- Support/docs: documentation must name the actual owner and command rather
  than send maintainers to global mutable state.
- Execution/stability: pre-existing untracked files must remain untouched and
  no broad cleanup command is safe.

### Critical evaluation of the critique

The critique correctly guards against a cosmetic green gate, but it does not
yet identify whether the repository can vendor ICU headers or whether
`go-icu-regex` requires a system library at link time. That distinction changes
the viable solution. The plan therefore keeps implementation choice open until
the module and history are inspected. The evidence layer is appropriately
defined as real vet/build behavior, with documentation as the enforcement
boundary when host-level completion is impossible.

### Roll-up: apply evaluation to critique

Add explicit checks for header-only versus header-plus-library requirements,
module version provenance, and whether existing CI already has a supported
dependency installation path. Treat a repository setup script as enforcement
only if it fails closed with actionable diagnostics and pins its inputs.

### Roll-up: apply revised critique to plan

The investigation task now includes C/C++ compile/link inspection, history
search, and CI/workflow inspection before choosing between vendoring, a pinned
toolchain artifact, or a documented platform prerequisite. The test task must
exercise the real dependency boundary; no skip-based workaround is acceptable.

### No-change decisions

- Do not change Go role/configuration behavior; this issue is build tooling.
- Do not add a new SDK interface or alter generic runtime/beads boundaries.
- Do not modify global Homebrew/Go state or use `go clean -cache`.

## Pass 1 — refined plan (counter 1)

### Full plan/tasks/subtasks

Retain the Pass 0 objective and sequence. Add a source/history matrix with:
module version, cgo files, required headers, linker flags, upstream commits,
CI setup, and the smallest owned fix. Record each candidate's exact observed
layer and failure mode before implementation.

### Critique top-to-bottom

The refined plan is evidence-led and respects the boundary, but the source/
history matrix could expand scope if it searches unrelated history. Limit it to
`go.mod`, `go.sum`, dependency paths, CI/build scripts, and targeted log/path
history. Ensure test-first means a failing contract test exists before the
implementation, not merely a post-hoc command transcript.

### Critical evaluation of critique top-to-bottom

The scope guard is sound. However, a contract test that requires unavailable
ICU headers cannot be made portable by pretending to install them. Prefer a
hermetic self-check that validates the repository's selected artifact and then
run real vet where the supported artifact is available. If no artifact can be
checked in or fetched reproducibly, the acceptance path must be documentation
plus fail-closed enforcement, not a false portable test.

### Roll-up: apply evaluation to critique

Define two evidence modes explicitly: real-vet mode on a supported prepared
macOS environment, and fail-closed dependency-contract mode for environments
without the artifact. Do not call the latter proof that vet passes.

### Roll-up: apply revised critique to plan

The implementation will expose the actual supported mode and make unsupported
hosts fail with a precise instruction. Tests will cover detection/enforcement;
the real vet run remains the acceptance evidence for the prepared environment.

### No-change decisions

- Keep the existing package dependency unless source/history proves it is
  impossible to support at the current version.
- Do not introduce network downloads into ordinary `go vet` execution.

## Pass 2 — refined plan (counter 2)

### Full plan/tasks/subtasks

Execute the matrix in a bounded way, write the smallest failing test or
diagnostic for the chosen contract, implement the repository-owned path, then
run focused and broad quality checks. Preserve a clear distinction between
dependency provisioning and vet execution in code/docs.

### Critique top-to-bottom

This is implementable, but "repository-owned path" still permits either a
script or committed artifact. The choice must be based on license, size,
platform coverage, and whether the dependency is a build input or runtime
input. The plan should also require checking generated files and CI references
for consistency.

### Critical evaluation of critique top-to-bottom

The additional constraints prevent an oversized or legally ambiguous vendor
drop. Generated files are not expected for this bug, but searching for them is
cheap and avoids a split contract. The current worktree has unrelated
untracked files; verification must use `git diff` scoped to this branch and
must not clean them.

### Roll-up: apply evaluation to critique

Add explicit artifact-size/license/platform checks and a generated-file search.
Keep any vendored content minimal and traceable; otherwise prefer a pinned
developer dependency path with fail-closed checks.

### Roll-up: apply revised critique to plan

Before implementation, inspect dependency licensing and repository size norms,
then choose the smallest supported path. Before commit, confirm no generated
outputs or unrelated files are included and record the actual evidence command.

### No-change decisions

- Do not commit unrelated pre-existing untracked files.
- Do not claim the clean-base reproduction is fixed until the actual target
  layer has passed or the documented enforcement path is validated.

## Pass 3 — final plan (counter 3)

### Full plan/tasks/subtasks

1. Reproduce and inspect the exact ICU header/library boundary, relevant module
   source/version, repository history, CI, and docs.
2. Write a failing focused contract test/diagnostic for the chosen supported
   dependency path.
3. Implement the smallest pinned/hermetic repository-owned fix, with precise
   fail-closed behavior and documentation.
4. Verify the real vet layer where supported, plus focused tests, affected
   package tests, `go vet`, and required project gates available locally.
5. Review scoped diff, commit, push/verify, update bead metadata, and hand off
   to refinery without closing the implementation bead.

### Critique top-to-bottom

The final plan covers acceptance and handoff. Its only remaining risk is
assuming a dependency solution before evidence; step 1 explicitly prevents
that. It also avoids claiming a proxy as target proof and preserves upstream
mergeability.

### Critical evaluation of critique top-to-bottom

No material gap remains. If the supported dependency cannot be made hermetic
without a large or unsafe change, the correct deliverable is the smallest
enforced documented path, with the limitation stated and the real failure
preserved rather than hidden.

### Roll-up: apply evaluation to critique

Keep the final implementation choice evidence-driven and bounded. Treat
quality-gate failures unrelated to the patch as evidence to report, not a
reason to weaken the gate.

### Roll-up: apply revised critique to plan

Proceed with the final five-step plan. Use explicit evidence labels in the
execution log and final handoff: what each check proves, its observed layer,
and what it does not prove.

### No-change decisions

- No changes to generic SDK architecture, role behavior, or global toolchains.
- No suppression of vet, cgo, or the affected package.
- No waiting for approval after this plan; the assigned polecat workflow
  requires immediate execution and refinery handoff when complete.

## Plan approval / execution note

The final plan is recorded for reviewability and is approved for execution by
the active assigned-work workflow; implementation begins after this artifact.

## Execution evidence

- `go vet ./...` reproduced the reported macOS failure at the native cgo
  dependency: `github.com/dolthub/go-icu-regex/internal/icu` could not find
  `unicode/regex.h` without ICU flags.
- Homebrew already provides ICU under the keg-only `icu4c@78` prefix. The
  repository Makefile discovers that prefix and exports the include/library
  flags; no global state was changed.
- `make vet` passed, and raw `go vet ./...` passed when invoked with those
  same repository-owned ICU flags.
- `make test-ci-policy` passed, including the existing macOS/Linux ICU setup
  contract tests. The active `.githooks/pre-commit` path is `.githooks`.
- `make test-fast-parallel` completed all six `cmd/gc` shards, but the
  `scripts` package failed its workspace-census test on unrelated sibling
  worktrees (`gc-msns` and `sdk-njx`). This does not observe the ICU/vet
  boundary and no unrelated cleanup was performed.
- The current `HEAD` equals `origin/main`, which already contains the prior
  ICU provisioning and README guidance fix. No product or test expectation
  change is justified for this bead.

## Final assessment

The reported issue is already resolved on the current base branch. The raw
unwrapped command remains an unsupported local invocation when its environment
does not contain ICU flags; the documented and enforced repository gate is
`make vet`, which runs the real cgo-enabled `go vet ./...` with the required
paths. Handoff should preserve this evidence rather than add a duplicate
installer or weaken the vet target.
