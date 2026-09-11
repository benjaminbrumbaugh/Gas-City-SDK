# sdk-lfc: portable local load detection

Initial planning counter: 0
Final planning counter: 3

## Initial full plan (counter 0)

### Objective and boundaries

Make `scripts/test-local-job-count` observe the macOS 1-minute load average
when no explicit `GC_TEST_LOCAL_LOADAVG` is supplied. Preserve the existing
override contract, Linux `/proc/loadavg` behavior, CPU/memory sizing, and the
`min_auto_jobs` floor. Keep the change isolated to the local runner boundary
and its existing shell self-test; no Go test assertions or product behavior
change is in scope.

### Tasks and subtasks

1. Load context and setup.
   - Verify the claimed bead and formula.
   - Work from the bead-scoped `polecat/sdk-lfc` worktree based on
     `origin/main`; record worktree/branch metadata.
   - Run the base self-test as a preflight.
2. RED test.
   - Extend `scripts/test-local-concurrency.sh` with a deterministic Darwin
     source test that leaves `GC_TEST_LOCAL_LOADAVG` unset, supplies a fake
     `sysctl -n vm.loadavg`, and verifies subtraction plus saturation at 2.
   - Keep the existing override, Linux-oriented, malformed-input, and floor
     assertions intact.
   - Run the self-test and confirm the new assertion fails because the current
     detector has no Darwin source.
3. GREEN implementation.
   - Preserve the explicit `GC_TEST_LOCAL_LOADAVG` branch first.
   - Select the host platform (with a narrowly named test seam) and retain
     `/proc/loadavg` for Linux/default non-Darwin behavior.
   - On Darwin, call `sysctl -n vm.loadavg` and parse the first numeric value
     from its brace-delimited output.
   - Return no sample on malformed/unavailable platform output so existing
     sizing remains conservative rather than inventing load.
4. Refactor and evidence.
   - Update comments/static checks to describe both platform sources.
   - Run the shell self-test, relevant script/Go checks, and inspect the diff.
   - Verify Linux override and `/proc` behavior remain covered and the Darwin
     path has no dependency on host load.
5. Handoff.
   - Commit the cohesive change on `polecat/sdk-lfc`.
   - Run affected tests/quality gates, push and verify the remote SHA.
   - Record the shipped outcome, reassign the work bead to the refinery, and
     drain the session per the formula.

### Architectural changes

One existing shell function gains a platform-specific input fallback. No new
package, interface, persistent state, role behavior, or API surface is added.
The test seam is environment-scoped like the existing `GC_TEST_LOCAL_*`
detectors and is used only to select Darwin deterministically; the command
boundary remains the real `sysctl` executable in production.

### Test plan and evidence layers

- `scripts/test-local-concurrency.sh` is the owning evidence layer: it invokes
  the actual job-count subprocess and proves observable fan-out decisions.
- RED: Darwin platform seam plus fake `sysctl` must fail before production code
  changes.
- GREEN: moderate Darwin load reduces 16 CPUs predictably; saturated Darwin
  load returns exactly `min_auto_jobs` (2); no-load override, fractional load,
  small-machine, malformed override, and existing wiring tests remain.
- Run `bash scripts/test-local-concurrency.sh` directly, then the affected
  runner/Go tests configured by the formula and `go vet ./...` as feasible.
- This test proves the shell detector’s source selection and arithmetic. It
  does not prove a real macOS kernel’s `sysctl` availability, system load, or
  full `test-fast-parallel` scheduling under contention. A fake command is a
  deterministic but intentionally insufficient proxy for live host behavior.

### Support structures and docs

Reuse the existing shell self-test and environment seams; add no production
helper solely for tests. Add no user documentation because this is an internal
runner portability fix. Update local comments and test names so maintainers
can identify the observed layer and source contract.

### Execution order

Preflight -> plan review -> RED test -> focused failure confirmation -> narrow
implementation -> focused GREEN run -> static/diff review -> affected tests ->
quality gates -> commit/push/refinery handoff.

### Stability and blocker avoidance

Use explicit CPU/memory pins in subprocess tests. Use a fixture directory under
`/var/tmp` with a targeted cleanup trap, never the shared `/tmp` tmpfs. Avoid
live load assertions, sleeps, polling, broad filesystem scans, and changes to
the focused worker test. If a base failure appears, do not repair it under this
bead; deduplicate/report according to the formula. If push or ledger access
fails, preserve the branch and escalate rather than claiming completion.

### Candidate parallel work

Potential independent work would be (a) a history search for prior load-source
implementations and (b) an independent review of shell portability. The change
is small and the test/implementation boundary is tightly coupled, so parallel
editing would increase merge risk; perform both checks locally and keep one
cohesive commit.

## Planning pass 1 (counter 1)

### 1. Full plan/tasks/subtasks review

The plan covers the requested macOS source, override preservation, Linux
behavior, floor, deterministic coverage, runner tests, and handoff. It names
the exact files and keeps the boundary local. The RED test must be added before
the detector change.

### 2. Critique top-to-bottom

- Objective: “non-Darwin behavior” is broader than the explicit Linux
  compatibility requirement and could accidentally hide unknown-host behavior.
- Test: a fake `sysctl` alone could pass if the production path still calls
  `/proc`; the platform selection seam must be asserted and the override must
  remain unset in the Darwin case.
- Parser: brace-delimited output needs numeric validation, not just a first
  token, to avoid arithmetic errors under unexpected command output.
- Stability: cleanup must target the exact fixture path and not use an unsafe
  default temporary directory.
- Gates: “as feasible” is vague; use the repository’s documented affected
  runner and vet commands, while recording any infrastructure failure.

### 3. Critical evaluation of that critique

The Linux path should stay exactly first-class and explicit, while unknown
platforms can retain the historical `/proc` attempt as a safe fallback. The
Darwin test should set only the platform seam and `PATH`, not the load override,
so it proves the source path. An `awk` numeric scan is portable to the existing
shell environments. The test’s exact temp directory is sufficient. The
affected command is discoverable from the rig formula vars; repository gates
remain mandatory when code changes.

### 4. Roll-up: applied evaluation to critique

Clarify implementation as: explicit override first; platform test seam or
`uname -s`; Darwin `sysctl`; otherwise existing `/proc`. Parse only a token
matching a non-negative decimal. Add static assertions for the Darwin source
and seam, and assert moderate/saturated Darwin results. Replace vague gate
language with named commands after inspecting the rig configuration.

### 5. Roll-up: revised plan/tasks/subtasks

Add a Darwin source fixture with no `GC_TEST_LOCAL_LOADAVG`, a moderate value,
and a high value. Keep the old `/proc` implementation intact for Linux. Make
the platform seam explicit and test-only by naming it `GC_TEST_LOCAL_PLATFORM`.
Use an `awk` loop to find the first numeric field in `sysctl` output.

### 6. No-change decisions

Do not alter `min_auto_jobs`, job arithmetic, `GC_TEST_LOCAL_LOADAVG` parsing,
the worker test, Makefile fan-out, or add a new executable/interface.

### 7. Proxy audit

- Target truth: default macOS job counting reduces fan-out from live `vm.loadavg`
  and floors saturated load at 2.
- Required evidence layer: subprocess test of the actual shell script with a
  Darwin source path and deterministic `sysctl` output; live macOS smoke is
  useful supplementary evidence only.
- Cheaper insufficient proxy: grep-only checks or a Linux override test.
- False completion: passing `GC_TEST_LOCAL_LOADAVG=55` would prove only the
  override, not macOS detection; the Darwin case must leave it unset.
- Residual risk: a successful fake command cannot prove kernel command
  availability or real host scheduling. If the plan succeeds, the bug could
  still remain if `uname`/`sysctl` differs on an unusual macOS release; source
  parsing and the real command invocation remain the only unproved live edge.

## Planning pass 2 (counter 2)

### 1. Full plan/tasks/subtasks review

The revised plan now gives the implementation a clear precedence order and a
deterministic platform test. It preserves all existing tests and limits the
change to source selection/parsing.

### 2. Critique top-to-bottom

- “Otherwise existing `/proc`” must not cause a macOS run to read `/proc` before
  `sysctl`, because the reported regression is specifically missing Darwin
  load. The branch must dispatch Darwin first.
- A command failure or malformed output must not become an empty string that
  later bypasses the floor; the detector should return failure cleanly.
- A platform seam in production code could become an undocumented operational
  override if its name is not clearly test-only in comments and tests.
- A broad `go vet ./...` may be expensive under contention but is still a
  required quality gate; run it after focused evidence.

### 3. Critical evaluation of that critique

Darwin-first dispatch is required. Returning nonzero from the source helper is
already the script’s established contract (`detect_loadavg || true`), so no
new error protocol is needed. Existing `GC_TEST_LOCAL_*` seams establish the
repository convention; document the new one beside its use and do not mention
it as a user tuning knob. Focused checks should run before broad vet.

### 4. Roll-up: applied evaluation to critique

Use `case` on the platform before source selection: Darwin calls `sysctl`,
other platforms use `/proc/loadavg`. Validate the parsed token with the same
non-negative decimal shape accepted by the explicit override. Keep unavailable
source behavior unchanged. Run focused script tests first, then affected
checks and vet.

### 5. Roll-up: revised plan/tasks/subtasks

The implementation subtask explicitly includes Darwin-first dispatch, numeric
validation, and a documented test-only platform seam. The test includes
moderate and saturated values and static source/seam assertions.

### 6. No-change decisions

Do not add a second load override, do not change the CPU threshold that gates
load awareness, and do not treat live `sysctl` as a required CI dependency.

### 7. Proxy audit

- Target truth: platform dispatch reaches Darwin’s `vm.loadavg` and uses its
  first sample in the same arithmetic as Linux.
- Required evidence: script subprocess with fake Darwin `sysctl`, no load
  override, moderate and saturated values; existing Linux `/proc` evidence is
  retained where available.
- Insufficient proxy: static grep for `vm.loadavg` without observing output.
- False completion: adding a Darwin branch after an unconditional `/proc`
  return would leave macOS broken while all static checks pass.
- Residual risk: actual sysctl command permissions/format on the host are not
  exercised by the deterministic test; a local macOS smoke run can supplement
  but cannot replace the deterministic assertion.

## Planning pass 3 (counter 3)

### 1. Full plan/tasks/subtasks review

The plan is complete for the requested bug: it has a minimal architecture,
TDD order, deterministic regression coverage, explicit preservation decisions,
runner wiring checks, and the mandated handoff. The worktree and temporary
artifact locations are safe and bounded.

### 2. Critique top-to-bottom

The only remaining concern is test portability: fixture creation must work on
macOS and Linux, and the fake `sysctl` must honor `-n vm.loadavg` enough to
catch incorrect invocation. The shell script test should not assume `strace`.
The final diff must not accidentally include unrelated worktree files.

### 3. Critical evaluation of that critique

`mktemp -d -p /var/tmp` is available on the target macOS host and Linux CI;
the fake command can reject unexpected arguments. Existing `strace` coverage
can remain optional, while the deterministic Darwin test is mandatory. Git
status and explicit path staging bound unrelated changes.

### 4. Roll-up: applied evaluation to critique

Make the fake command validate its arguments and emit brace-delimited load
values. Use a targeted cleanup trap. Keep optional live `/proc` tracing as a
separate Linux-only convenience if it remains correct; it must not be the
Darwin regression proof.

### 5. Roll-up: revised plan/tasks/subtasks

During RED/GREEN, add the fake command fixture in the existing self-test,
verify `sysctl -n vm.loadavg` invocation, and ensure the new mandatory cases
run on every host. Preserve the optional strace check only as supplemental
evidence, updating its description if needed.

### 6. No-change decisions

No product docs, Go packages, API contracts, worker tests, or test assertions
unrelated to local concurrency will be changed. No live process load will be
used to decide pass/fail.

### 7. Proxy audit

- Target truth: local gate fan-out is load-aware on macOS, including saturation.
- Required layer: the real `scripts/test-local-job-count` subprocess through
  the self-test’s Darwin platform path and command boundary.
- Useful but insufficient: host `sysctl` inspection, static source checks, or
  explicit load override cases.
- Tempting false completion: seeing `vm.loadavg` in source or passing the
  worker test in isolation; neither proves the gate’s fan-out decision.
- Remaining failure mode after full success: a macOS-specific shell/toolchain
  difference outside the tested command/format contract could still break
  detection; this is recorded as live-host residual risk rather than hidden.

### Final execution decision

Proceed autonomously under the claimed `sdk-lfc` implementation assignment.
The plan is sufficiently constrained; no human choice is needed to implement
the requested behavior.
