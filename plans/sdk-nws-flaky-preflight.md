# sdk-nws flaky ZCode preflight plan

counter: 0

## Initial plan (counter 0)

### Objective

Investigate and repair the fast-suite failure where
`TestProbeZCodeNeedsBundleAndKey` observes an invalid local Node executable
while the focused test passes. Preserve the readiness contract: a bundle is
ready only when its configured executable reports a supported version and the
required key is present. The change should make parallel execution independent
of ambient process/test state and should include evidence for the failure layer.

### Tasks and subtasks

1. Reproduce the reported failure from a clean `origin/main`-based worktree.
   - Run the focused tmux/runtime test in isolation.
   - Run the relevant package tests repeatedly and under the documented fast
     runner if available.
   - Capture environment, temporary bundle setup, subprocess command, and
     cleanup behavior without exposing secrets.
2. Trace the readiness boundary.
   - Read the production probe and the failing test helper.
   - Identify shared environment, executable lookup, temp paths, process
     lifecycle, or parallel-test mutations.
   - Check recent history and the refinery branch context before rebuilding a
     missing fix.
3. Write a failing regression test at the narrowest affected layer.
   - Make the test exercise the real readiness probe and preserve the
     invalid-configuration behavior for genuinely unsupported Node.
   - Avoid tests that merely assert a mocked version or serial scheduling.
4. Implement the smallest boundary-contained fix.
   - Keep provider/test-environment concerns in the tmux runtime package.
   - Do not weaken version/key validation or introduce role/config assumptions.
5. Verify and hand off.
   - Run formatter, focused tests, affected tests, fast baseline as feasible,
     and `go vet ./...`.
   - Review the final diff for unrelated files, security-sensitive output,
     race-prone cleanup, and upstream mergeability.
   - Commit on `polecat/sdk-nws`, push, verify the remote SHA, and reassign the
     work bead to the refinery without closing it.

### Architectural changes

Expected: no broad architectural change. If a fix is needed, it will remain
behind the existing runtime/tmux readiness boundary, with test-only harness
state isolated per test. Any production change must be justified by a proven
cross-test/process boundary defect and must retain the same readiness contract.

### Test plan

- Focused `internal/runtime/tmux` readiness test, first failing before the fix.
- Repeated package-level run to detect load sensitivity.
- Relevant fast-suite shard using the repository's documented runner.
- `go vet ./...` and any required build/lint commands.
- If the issue is not reproducible, retain a deterministic regression test or
  diagnostic that proves isolation and documents what remains unproven.

### Support structures and evidence

- Temporary `agent-execution.log` records completed subtasks and overall
  progress; it is operational evidence, not product state.
- This plan records decisions and the three required critique passes.
- Test output is evidence of Go/package behavior only; it does not prove
  external CI scheduling or every host's Node installation.
- A deterministic harness is useful-but-insufficient proxy evidence if it does
  not exercise the real subprocess and cleanup path.

### Docs

No user-facing docs are expected unless the fix changes a supported runtime
configuration or a documented prerequisite. If behavior or setup guidance
changes, update the closest existing developer/runtime documentation rather
than adding a duplicate page.

### Execution order

Plan refinement -> preflight baseline -> repository/history investigation ->
failing regression test -> implementation -> focused/affected verification ->
quality gates -> commit/push/refinery handoff.

### Stability strategy

Use per-test temporary directories, explicit subprocess environments, bounded
commands, and cleanup that cannot remove another test's resources. Do not use
global Node/path mutation as a synchronization mechanism. Keep retries for
diagnosis only; the product fix must remove the race or ambiguity.

### Blocker avoidance

Prefer existing `go test`/Make targets and history search. If the fast runner
is flaky for unrelated reasons, separate base and branch evidence, file a
follow-up bead only for remaining independent work, and do not claim the
readiness contract is proven by a passing isolated test.

### Candidate parallel work

- One independent read-only history search for prior ZCode/readiness fixes.
- One independent review of the test helper's temp/process/environment
  isolation.
- Keep implementation and final verification in this worktree after those
  observations so commits remain cohesive.

### Proxy audit

- Target truth: parallel fast-suite runs must not cause the real ZCode
  readiness probe to classify a valid temporary bundle as invalid because of
  shared test state, while truly invalid Node remains invalid.
- Required evidence layer: the real Go probe plus the real subprocess and
  temporary bundle lifecycle under concurrent/repeated package execution.
- Cheaper useful-but-insufficient proxies: focused tests, a single package run,
  `go test -run`, and static inspection.
- Tempting false completion: making the test serial, skipping the readiness
  assertion, accepting any Node version, or mocking version output.
- If this plan fully succeeds, the original bug could remain if only an
  isolated semantic test passes and no concurrent/fast-suite execution is
  observed; that risk must be stated in the handoff.

### No-change decisions

- No new abstraction or runtime provider is planned before the boundary defect
  is identified.
- No relaxation of Node version/key validation is acceptable.
- No dashboard, API, or role behavior is in scope.

## Planning pass 1 (counter 1)

### Critique of the initial plan, top-to-bottom

- Objective: correctly protects the readiness contract, but it assumed the
  failing layer was tmux/runtime because the assignment metadata names
  `internal/runtime/tmux/adapter.go`. The named failing test is actually
  `internal/api/handler_provider_readiness_test.go`, so the objective must
  distinguish API probe state from the adapter's live process behavior.
- Tasks: history and concurrency investigation are appropriate, but the plan
  omitted the inherited `ZCODE_NODE_BIN` environment as a first-class input.
  The test stages a temporary `node` but does not clear the process-level
  override before calling `probeZCode`.
- Architectural changes: the no-broad-change constraint remains right. The
  likely fix is test isolation, not production behavior, unless the probe is
  shown to mishandle an explicitly configured path.
- Test plan: focused and fast-suite runs remain necessary, but a regression
  test must start with an intentionally stale `ZCODE_NODE_BIN` and prove the
  test's staged executable is selected. This must not change the production
  precedence rule for an explicit configured binary.
- Support/evidence: the evidence layers were named, but the initial plan did
  not require recording the actual resolved node path and environment. Add a
  diagnostic assertion/detail where useful; do not log API keys or inherited
  secrets.
- Docs: still no user-facing docs are expected for a test-only isolation fix.
- Execution order: history should precede implementation and the base
  preflight must be completed before any product/test change.
- Stability: per-test temp directories are good, but package-global probe
  overrides are a separate risk. Existing tests mutate them serially; do not
  introduce `t.Parallel` into this file or silently synchronize production
  state around tests.
- Blocker avoidance: isolate test-environment failures from product failures;
  a single pass cannot establish fast-suite stability.
- Parallel work: no subagent facility is available in this session, so retain
  independent read-only history and test-environment slices conceptually but
  execute them sequentially in this worktree.
- Proxy audit: the target/evidence distinction is sound, but the initial plan
  did not explicitly test ambient env contamination. Add it as the required
  negative control.
- No-change decisions: retain all three; additionally, do not alter
  `zcodeNodeFloorDetail` merely to make a stale path fall back, because that
  would change the explicit `ZCODE_NODE_BIN` contract.

### Critical evaluation of that critique

The metadata discrepancy is evidence of stale handoff context, not proof that
tmux is uninvolved. The API readiness test calls the same configured Node
override consumed by the adapter, so both layers matter, but the change should
start at the failing test boundary. An inherited `ZCODE_NODE_BIN` is a strong
candidate because the test only sets it in the node-floor test and the Makefile
deliberately forwards the host value. However, a test-only clear can mask a
real shared-process mutation if another test leaves the variable set; we must
run the full API package and fast shard to establish whether restoration is
already sound. Production fallback semantics should remain unchanged unless
the assignment explicitly requires a user-facing behavior change.

### Roll-up: revised plan after pass 1

Reproduce with the current environment, with and without an intentionally
invalid `ZCODE_NODE_BIN`, then run the API package and fast runner. Add a
regression assertion that the staged valid node remains selected when the
ambient override is stale, choosing test isolation or production behavior only
after observing the explicit-path contract. Review `git log` candidates
`af85466e7`, `ca347c082`, and the prior zcode load-sensitive fixes as history
evidence, not as assumed fixes.

### No-change decisions

- Keep the production explicit-path precedence unless a focused failing test
  proves it violates the documented runtime contract.
- Keep the readiness status taxonomy and Node floor unchanged.
- Keep the fix scoped to API readiness/test isolation; do not edit tmux adapter
  code solely because stale metadata names it.

## Planning pass 2 (counter 2)

### Critique of the revised plan, top-to-bottom

- Objective: now names the API test boundary and explicit Node precedence, but
  should say that the desired product truth is unchanged: an explicitly set
  unusable `ZCODE_NODE_BIN` must remain invalid.
- Tasks: reproduction succeeded with a stale absolute path; the plan should
  explicitly verify both inherited stale-path failure before the fixture fix
  and clean/stale-path success after it.
- Architectural changes: correctly rejects a production fallback. A helper-level
  test fixture change is the smallest owner for ambient test environment
  isolation.
- Test plan: package and fast-suite checks are still needed. Add a direct
  command using `env ZCODE_NODE_BIN=/definitely/not-a-node` so the regression
  proves the fixture, not the shell's usual empty environment.
- Support/evidence: the reproduction is real Go probe behavior plus process
  environment, while `CGO_ENABLED=0` is only a host build workaround because
  this checkout lacks ICU headers. Record that distinction.
- Docs: no docs change remains justified.
- Execution order: plan refinement and base focused evidence are complete;
  next is the minimal test-first fixture edit, then verification.
- Stability: clearing the override in the shared test helper is safer than
  fixing only one test, because every caller uses a temporary home and a
  curated search path; tests that intentionally exercise an override set it
  explicitly afterward.
- Blocker avoidance: the initial direct build failure from missing
  `unicode/regex.h` must not be misattributed to this issue or fixed in code.
- Parallel work: sequential execution is acceptable because no subagent tool
  is exposed; history already confirmed the relevant prior fixes are in the
  current base.
- Proxy audit: the test now reaches the real `zcodeNodeFloorDetail` command
  path, and the stale env is the required adversarial input. Fast-suite success
  remains stronger evidence than the focused test.
- No-change decisions: all remain valid; specifically do not make production
  code discard explicit env configuration.

### Critical evaluation of that critique

The helper-level clear is a test-only boundary correction, not a behavior
change: `zcodeNodeFloorDetail` must continue to select `ZCODE_NODE_BIN` when a
consumer intentionally provides it. The test's temporary `~/.local/bin/node`
is the intended discovery path, so a stale inherited override is unambiguously
ambient contamination. Setting the variable empty in `pinProbeSearchPath`
also leaves explicit-path coverage intact because the node-floor test sets it
per case and clears it for the missing-node case. The existing focused test is
the regression test once run with the adversarial inherited value; adding a
production test would encode the wrong contract.

### Roll-up: revised plan after pass 2

Make the one-line fixture isolation change in `pinProbeSearchPath`, rerun the
adversarial focused test with the stale override, rerun the full ZCode readiness
subset, then run the API package with `CGO_ENABLED=0` and the documented fast
suite. Confirm the diff has no adapter/API production changes and state the
missing-ICU limitation separately if the default CGO path still cannot build.

### No-change decisions

- Do not alter `zcodeNodeFloorDetail`, explicit `ZCODE_NODE_BIN` precedence,
  Node version parsing, or readiness statuses.
- Do not remove the node-floor test's explicit override cases.
- Do not treat a CGO/ICU host prerequisite failure as a product regression.

## Planning pass 3 (counter 3)

### Critique of the pass-2 plan, top-to-bottom

- Objective: precise and contract-safe. It should explicitly include the
  handoff limitation that passing the test cannot prove every CI host's Node
  environment, only that this test no longer depends on it.
- Tasks: complete and low risk. Add a final check that `git diff --check`
  and `go vet ./internal/api` observe only the intended changed surface.
- Architectural changes: correctly none; this is a test fixture boundary.
- Test plan: sufficient if it includes the before/after stale-env command,
  repeated focused runs, and the affected package. The whole fast suite may
  exceed the session budget, so keep its result explicit rather than silently
  substituting a proxy.
- Support/evidence: the agent log and plan are useful process artifacts but
  are not evidence that external CI scheduling is fixed; say so in the final
  handoff.
- Docs: no change.
- Execution order: implementation and verification order is correct; commit
  the required plan/log only if they are intended tracked deliverables, and do
  not leave untracked files that could be mistaken for implementation.
- Stability: helper-level `t.Setenv` uses Go's test cleanup and does not create
  production synchronization. Ensure no test becomes parallel or observes the
  temporary value after return.
- Blocker avoidance: use the configured `make` target where possible, but the
  known missing ICU header may require `CGO_ENABLED=0` focused/package runs.
- Parallel work: no additional parallel slice is needed after root cause is
  proven.
- Proxy audit: adversarial focused coverage is useful but insufficient for the
  original fast-suite claim; run the actual fast runner if time and host
  capacity permit.
- No-change decisions: unchanged and consistent with the evidence.

### Critical evaluation of that critique

The final plan is appropriately minimal. The source-of-truth for the defect is
the test's inherited process environment, and the required regression is the
same test under a stale override. Production code remains untouched, so the
readiness contract is tested rather than weakened. The plan/log files are
requested operational artifacts; they must be reviewed for accidental secrets
and either committed as scoped planning evidence or removed deliberately
before the clean-state gate. Since this assignment requires a reviewable
branch handoff, the branch and bead metadata checks are part of completion.

### Roll-up: final plan

1. Apply `t.Setenv("ZCODE_NODE_BIN", "")` in `pinProbeSearchPath`.
2. Verify stale ambient override no longer affects
   `TestProbeZCodeNeedsBundleAndKey`; verify explicit overrides still drive
   `TestProbeZCodeChecksTheNodeFloor`.
3. Run focused repetitions, `CGO_ENABLED=0 go test ./internal/api`,
   `go vet ./internal/api`, `make test-fast-parallel` if the host can build it,
   and inspect all diffs.
4. Commit cohesive changes, push/verify `polecat/sdk-nws`, record shipped
   outcome, and hand off to refinery. The evidence proves test-environment
   independence at this layer; it does not prove arbitrary external CI hosts.

### No-change decisions

- No production source change is required or authorized by the evidence.
- No test expectation is changed; only the test's ambient environment is
  explicitly normalized.
- No docs or architecture updates are required.
