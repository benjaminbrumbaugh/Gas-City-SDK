# sdk-3t4 plan

counter=3

## Initial full plan (counter 0)

### Objective and target truth

Make the local parallel Go-test gate resilient to another session running
`go clean -cache` against the host's shared build cache. The target truth is
that every Go invocation owned by `scripts/test-local-parallel` uses a
run-private, disk-backed `GOCACHE`, so external cache invalidation cannot
remove artifacts needed by the fast gate; the runner cleans only its own
cache on exit.

### Tasks and subtasks

1. Confirm the base behavior and owning boundary.
   - Read `TESTING.md`, the cache rules, Makefile targets, and runner scripts.
   - Search history and current branches for an existing cache-isolation fix.
   - Record the preflight result and evidence layer in `agent-execution.log`.
2. Add a regression test first.
   - Exercise the local-parallel runner's cache selection through its shell
     boundary with a fake `go` executable.
   - Prove final `go test` invocations do not use the shared cache path and
     that a clean of the shared path cannot remove a private-run sentinel.
3. Implement the smallest runner change.
   - Allocate a unique cache below `/var/tmp` for each local-parallel run.
   - Pass that path through the existing per-job environment allowlist.
   - Clean the owned cache on normal exit and preserve existing log/process
     cleanup and shell portability.
4. Review and verify.
   - Run the focused regression and shell syntax checks.
   - Run `make test-fast-parallel` with the normal host cache untouched, then
     run `go vet ./...` and the documented broader shard as practical.
   - Inspect the diff, `git diff --check`, and clean-tree state.
5. Commit the cohesive change and hand the branch to the refinery.

### Architectural changes

`scripts/test-local-parallel` remains the ownership boundary for local
parallel test process setup. The change is limited to cache lifecycle and
does not alter Go module-cache sharing, test selection, job concurrency,
timeouts, or product code. No new production abstraction is needed.

### Test plan and evidence layers

- Shell-boundary regression: fake `go` records the cache path supplied to
  discovery and test invocations; this proves environment plumbing, not Go's
  compiler internals.
- Focused runner self-test: proves private-cache cleanup/ownership behavior;
  it does not prove arbitrary external processes obey the repository policy.
- `make test-fast-parallel`: real process/build/test evidence for the gate's
  observable outcome; it does not by itself prove an adversarial cleaner was
  concurrent.
- `go vet ./...` and broader documented shards: compile/static confidence,
  not a substitute for the cache-isolation regression.

### Support structures

Use the existing `scripts/test-local-concurrency.sh`/`scripts` test seam or a
small adjacent shell-boundary test. Keep the cache path explicit, unique,
disk-backed, validated before cleanup, and visible in runner diagnostics.
Avoid a general cache manager or changes to unrelated shard entrypoints.

### Documentation

Update runner comments and, only if the implementation changes the stated
operator contract, the relevant testing guidance. Do not weaken the existing
`AGENTS.md` ban on `go clean -cache`.

### Execution order

Base preflight -> regression test (RED) -> implementation (GREEN) -> focused
checks -> fast gate -> vet/broader checks -> self-review -> commit -> push and
refinery handoff.

### Stability strategy

Keep Bash 3.2 compatibility, avoid `/tmp`, do not call `go clean -cache`, use
trap-based cleanup for only the owned cache, preserve the existing gate lock,
process-group cleanup, and env scrubbing. Do not change tests merely to match
the implementation; expectations change only for the new runner contract.

### Blocker avoidance

Do not touch the dirty reusable polecat home worktree. Work only in the
recorded bead worktree and branch. If the base gate fails for an unrelated
reason, record evidence and do not repair it in this branch. If cache cleanup
leaves orphaned artifacts, capture diagnostics before any cleanup and report
the bounded failure.

### Candidate parallel work

No independent subtask is large enough to split safely: the regression test
and runner change share one shell contract. A later review can independently
audit resource cleanup and shell portability if another agent is available.

### Proxy audit (initial)

- Target truth: the real local-parallel Go processes survive external shared
  cache invalidation.
- Required evidence layer: runner-to-child process environment plus a real
  fast-gate run; an adversarial cleaner can be represented in the focused
  harness only after cache ownership is proven.
- Cheaper but insufficient proxy: `rg` finding no `go clean -cache` calls.
- Tempting false completion: documenting the ban or changing only the parent
  shell while a child still receives the shared cache.
- If the plan succeeds but the bug remains: a child path could overwrite the
  private `GOCACHE`, cleanup could delete another session's directory, or the
  fast gate could still use the shared path during discovery/build.

## Planning pass 1 (counter 1)

### Revised plan/tasks/subtasks

Keep the change in `scripts/test-local-parallel`; allocate the private cache
before the runner snapshots Go environment values, and make the existing
`TEST_LOCAL_GOCACHE` fan-out path carry it to unit-core, cmd/gc, and integration
jobs. Add a focused shell-boundary regression rather than a static grep-only
test. Update the temp-path census only if the implementation adds a tracked
`${TMPDIR:-...}` fallback site.

### Top-to-bottom critique of every major section

- Objective: correctly names the cache race and the required observable.
- Tasks: test-first and history search are present; cleanup verification needs
  an explicit path-safety assertion.
- Architecture: correctly keeps ownership at the runner, but must confirm all
  modes using this runner benefit without changing direct shard commands.
- Test plan: environment capture is useful, but a fake cleaner must not be
  mistaken for compiler behavior.
- Support: trap cleanup can fail under SIGKILL; document that only normal-exit
  cleanup is guaranteed and ensure the directory is uniquely scoped.
- Docs: no contract drift should be introduced into the global hard ban.
- Execution/stability/blockers: order is sound; full-suite evidence must use
  documented sharded commands and never `go clean -cache`.
- Parallel work: a separate reviewer is optional, not a dependency.

### Critical evaluation of that critique

The critique exposes the main risk: cache isolation can be implemented while
the test proves only a string. The acceptance is about the process boundary,
so the regression must observe actual child `go` invocations and the cleanup
must use a narrowly recognizable owned path. Direct shard commands are outside
the local-parallel boundary and should remain unchanged unless evidence shows
the pre-push gate calls them directly.

### Roll-up: apply evaluation to critique

Require the test to record operation plus `GOCACHE`, assert all `test`
operations use the private path, and assert the private sentinel survives a
fake `go clean -cache` aimed at the original shared path. Require cleanup
guards to reject empty, root, and non-owned paths before recursive removal.

### Roll-up: apply revised critique to plan/tasks

Add an explicit owned-path cleanup helper or equivalent guard, and make the
test exercise the final child environment. Verify `fast`, `full`, and
`integration` modes only through the common runner wiring; do not duplicate
cache policy in each job.

### No-change decisions

- Do not alter `GOCACHE` in `scripts/test-go-test-shard` for direct callers.
- Do not modify module-cache handling.
- Do not add a cross-process lock that arbitrary `go clean` commands cannot
  acquire; isolation is the enforceable boundary.
- Do not remove the existing shared-cache documentation or hard ban.

### Proxy audit (pass 1)

- Target truth: every real child Go process in the local-parallel gate uses an
  owned cache unaffected by a shared-cache cleaner.
- Required evidence layer: shell harness observing child env plus real gate.
- Cheaper insufficient proxy: static cache assignment assertions.
- False-completion substitution: only changing the displayed path or isolating
  the discovery probe while build/test still uses the host cache.
- Residual failure path: the helper could allocate one path but a later
  `env -i` allowlist could drop it; the regression must catch that.

## Planning pass 2 (counter 2)

### Revised plan/tasks/subtasks

1. Write a RED test at the `scripts/test-local-parallel` boundary that uses a
   fake `go` and an explicit shared-cache sentinel.
2. Implement per-run `/var/tmp` cache allocation before `go env GOCACHE`, pass
   it through `TEST_LOCAL_GOCACHE`, and clean only the owned directory.
3. Add focused shell/syntax checks and run the real fast gate, then vet and
   broader sharded checks according to `TESTING.md`.

### Top-to-bottom critique of every major section

- Objective and architecture are appropriately narrow.
- The RED test must avoid recursively invoking the self-test already included
  in fast mode; a standalone script test or a Go test fixture is safer.
- The implementation must not rely on `mktemp -p`, which differs across macOS
  versions; use a `/var/tmp` template compatible with the repository floor.
- Cleanup must coexist with the current `EXIT` trap and gate-FD release.
- Tests must account for test-resource census and avoid adding unnecessary
  subprocesses to production code paths.
- Plan-file and execution-log artifacts should not leak into the shipped
  patch unless explicitly useful to maintainers.

### Critical evaluation of that critique

The cache behavior is shell-owned, so a shell self-test is the most direct
evidence and avoids Go AST resource-count changes. It can invoke a small
purpose-built probe mode only if that mode is clearly test-only and cannot be
mistaken for the production gate. Alternatively, a focused Go test already has
established subprocess fixtures, but it adds a process call and more setup.

The cleanest boundary is a reusable shell helper that allocates/validates the
cache and a direct test of that helper; the runner then sources it. This is a
real abstraction only if the helper has at least two consumers or provides a
stable test seam, so avoid it unless direct testability requires it.

### Roll-up: apply evaluation to critique

Prefer a focused Go test only if the existing scripts package resource census
accepts it without a baseline change; otherwise put the regression in the
existing shell self-test and verify it as a direct script boundary. Do not add
a production probe mode merely to make testing convenient. Keep the initial
implementation inline unless a helper is demonstrably reused.

### Roll-up: apply revised critique to plan/tasks

Before implementation, inspect existing subprocess/resource-count guards. If
the focused Go fixture would be a clean established pattern, use it. Otherwise
extend `scripts/test-local-concurrency.sh` with a direct cache-policy test and
call a narrowly scoped helper path that cannot recurse into fast jobs.

### No-change decisions

- Keep `/var/tmp` explicit; never inherit `/tmp` for `GOCACHE`.
- Keep module cache shared for build efficiency and because `go clean -cache`
  does not target it.
- Do not promise crash-proof removal after SIGKILL; ownership safety is the
  invariant, and normal-exit traps handle routine cleanup.
- Do not change the acceptance test's reason from infrastructure race to a
  product failure.

### Proxy audit (pass 2)

- Target truth: private cache allocation is active before all child builds.
- Required evidence layer: child process observation, plus an owned-cache
  cleanup check.
- Cheaper insufficient proxies: source grep, runner log text, or a unit test
  that never starts the fan-out.
- False completion: a private directory created after one child has already
  called `go env` or a direct shard path bypasses the setup.
- Residual failure path: cleanup races with a sibling and removes a cache that
  is not owned; the path guard and unique `mktemp` scope must prevent this.

## Planning pass 3 (counter 3)

### Final plan/tasks/subtasks

1. Add a direct, red shell-boundary regression using a fake `go` invocation
   recorder if it can run without recursive fast-mode entry; otherwise add the
   smallest established `scripts` subprocess fixture and document its layer.
2. Add `test-local-parallel` private-cache setup before `go env` capture,
   explicit `/var/tmp` allocation, safe normal-exit cleanup, and diagnostics.
3. Run focused tests, `make test-fast-parallel`, `go vet ./...`, and the
   documented broader shard(s) within available time; distinguish host or
   pre-existing failures from regressions.
4. Remove only temporary planning/execution artifacts that are not part of the
   reviewed change, commit the implementation with evidence in the body, and
   hand off via the refinery protocol.

### Top-to-bottom critique of every major section

- The objective is testable at the shell process boundary.
- The implementation is one-file and preserves existing fan-out semantics;
  the only likely extra maintenance is a temp-path census if the code uses a
  fallback expression.
- The test must assert the final `go test` cache, not just `go env` output, and
  must prove the original shared path is not mutated by the runner's cleanup.
- A real fast-gate run is necessary but cannot simulate an adversarial cleaner;
  retain the focused boundary test as the unique regression owner.
- Cleanup must run in the existing `EXIT` trap order and never recursively
  remove a caller-selected arbitrary directory.
- Documentation should explain the local runner contract without suggesting
  users may run `go clean -cache`.

### Critical evaluation of that critique

The final plan is internally consistent if the focused test can observe the
child process. The existing `env -i` allowlist already exports
`TEST_LOCAL_GOCACHE`; changing only its source value is the smallest correct
boundary. A direct cache sentinel test can be implemented without a new
production abstraction by making the cache directory deterministic through a
test-only path override, but that override would itself be a new contract. A
better test can parse the runner's emitted owned path and inspect recorder
output, avoiding a production override.

### Roll-up: apply evaluation to critique

Use a focused Go subprocess fixture only if it can execute the script with a
fake `go` and does not introduce a forbidden resource baseline. Otherwise use
the existing direct shell self-test. In either case, assert operation-level
cache values and ownership-safe cleanup. The implementation should emit one
diagnostic line naming the owned cache path for operators and tests.

### Roll-up: apply revised critique to plan/tasks

During RED, choose the smallest existing test harness after a quick feasibility
check. Keep the implementation contract independent of the test: no
test-specific cache path variable is required. Use a path pattern under
`/var/tmp` and only remove paths created by this invocation. Update comments and
the exact temporary-path census if mechanically required.

### No-change decisions

- No source-code role/config changes; this is test infrastructure only.
- No `go clean -cache` in implementation or verification.
- No direct edits to the dirty reusable polecat home or shared rig root.
- No broad refactor of all Go test entrypoints unless focused evidence shows
  the pre-push fast gate bypasses `test-local-parallel`.

### Proxy audit (pass 3)

- Target truth: concurrent external cleaning of the host cache cannot make the
  local-parallel gate lose required build artifacts.
- Required evidence layer: actual `scripts/test-local-parallel` child env and
  owned cache lifecycle, plus the real gate run.
- Cheaper insufficient proxies: static source checks, a passing unit suite,
  or a semantic preview of the runner output.
- False completion: private cache setup is added but one jobspec or discovery
  build still uses the shared path; operation-level recording catches this.
- If all planned checks pass but the bug remains: the external cleaner might
  target a shared parent containing the private cache, or cleanup might remove
  sibling-owned paths. Explicit `/var/tmp` sibling directories and strict
  ownership guards are required.

The three passes retain all required information and converge on the same
small boundary. Proceed with the approved execution order.

## Rejection recovery pass (counter 3)

The preserved implementation initially added the cache regression as a Go
subprocess test. The refinery's full fast baseline correctly rejected that
shape because it raised the checked resource census by one call and one file.
The implementation itself remains valid; the regression now lives in the
existing shell self-test boundary, launches the real parallel runner with a
fake Go command, and leaves the ledger unchanged. The runner propagates a
narrow guard through its child environment so nested fixture execution cannot
recurse. Focused shell, scripts-package, and resource-census checks pass.
