# sdk-bfd — deterministic tmux model-switch-modal precondition

Planning counter: 0 (initialized before implementation)

## Objective and target truth

The real-tmux integration test must exercise the product behavior it claims to
cover: when the adapter is asked to dismiss a model-switch modal, the modal is
present in a real tmux-backed session, dismissal clears that modal, and the
assertion is meaningful rather than skipped because setup failed to create the
precondition. Preserve the dismissal assertion. Do not weaken the test to make
the precondition disappear.

## Full plan / tasks / subtasks

1. Load current context and establish a baseline.
   - Inspect `internal/runtime/tmux/adapter.go`, `startup_test.go`, helpers, and
     nearby real-tmux tests.
   - Reproduce the exact targeted test on `origin/main` and capture the raw
     result, timing, tmux/socket/session setup, and relevant logs.
   - Compare the test and adapter history with the candidate commit named in
     the bead before inventing a new setup mechanism.
2. Diagnose the precondition boundary.
   - Identify how the model-switch modal is created, observed, and dismissed.
   - Determine whether the failure is caused by timing, startup ordering,
     provider state, tmux target selection, or an accidental test double.
   - Separate a real adapter regression from an environment/provider setup
     failure using baseline and candidate runs.
3. Implement the smallest deterministic fix.
   - Make only the setup/adapter/test changes needed to create and observe the
     modal through the real tmux/provider boundary.
   - Keep production adapter behavior generic; do not encode role names,
     model-specific judgment, or test-only assumptions in shared runtime code.
   - Add or update focused tests for the discovered edge cases.
4. Review and verify.
   - Run the focused real-tmux test repeatedly enough to distinguish a stable
     fix from a timing accident.
   - Run affected package tests, `go vet ./...`, and required project gates as
     practical; record failures with their observed layer and disposition.
   - Review the diff for boundary leakage, cleanup, race potential, and test
     expectation changes. Expectations may change only if independently wrong
     or if the product contract changed; record the reason.
5. Handoff.
   - Commit the cohesive change on `polecat/sdk-bfd`.
   - Push and verify the remote branch tip, record producer metadata, reassign
     the open work bead to the refinery, and drain the session.

## Architectural changes

Prefer no architectural change. If the diagnosis requires a change, keep it at
the existing tmux runtime/provider boundary and preserve the adapter's generic
contract. Test setup belongs in `internal/runtime/tmux` test support or the
integration test, not in unrelated API, CLI, dashboard, or role configuration
paths.

## Test and evidence plan

- Targeted test: `TestDismissModelSwitchModalIfPresentClearsModalOnRealTmux`.
- Baseline evidence: exact `origin/main` run, including whether the modal is
  created and observed before the assertion.
- Product-layer evidence: real tmux session/pane state and adapter behavior;
  this proves the provider boundary was exercised, not merely a semantic mock.
- Package evidence: focused `go test` for `internal/runtime/tmux`, then the
  configured affected-test command or equivalent integration shard.
- Repository evidence: `go vet ./...`, relevant build/test gates, clean diff.
- Repeat evidence: several serial targeted executions under fresh sockets or
  equivalent isolated tmux state to check determinism.

## Support structures and docs

- Use existing tmux test helpers, socket isolation, and startup fixtures where
  history shows they are the established boundary.
- Add a narrowly scoped helper only if it centralizes real provider setup and
  avoids repeated timing-sensitive code.
- Update docs only if the supported test/provider contract is clarified; no
  unrelated documentation work is in scope.
- Maintain temporary `agent-execution.log` in the worktree with one line after
  each completed subtask.

## Execution order and stability strategy

Read and reproduce first, inspect history second, write a failing focused test
or fixture if needed, then implement, repeat, run affected checks, vet, review,
commit, push, and hand off. Use fresh isolated tmux sockets and bounded
commands. Never use the default tmux server or a broad kill command. Treat a
passing semantic/unit test as insufficient until real-tmux evidence confirms
the target behavior.

## Blocker avoidance

Use `git show`/`git log` for archaeology and existing test helpers before
creating new machinery. If tmux or the test harness is unavailable, capture
the exact diagnostic and escalate to the witness rather than weakening the
assertion. If the result remains nondeterministic after two or three focused
attempts, preserve evidence and escalate with the layer observed.

## Candidate subagent-parallel work

No subagent tool is available in this session. Parallelize independent
read-only checks where useful: one command path for history/candidate diff and
one for test/helper inventory, then combine before editing. Keep all edits in
this worktree.

## Proxy audit

- Target truth: the real tmux/provider can reliably present the model-switch
  modal and the adapter dismissal clears it.
- Required evidence layer: real tmux integration behavior plus the adapter
  boundary, with direct observation of modal-present then modal-cleared state.
- Useful but insufficient proxies: mocked adapter tests, static code review,
  one semantic preview, or a single passing run without precondition evidence.
- Tempting false completion: skip the test when the modal is absent, remove the
  dismissal assertion, or update expectations to accept absence.
- Residual-bug question: if this plan succeeds, the original bug could remain
  only if the harness's “real tmux” path still uses a fake/incorrect target, or
  if repeated fresh-socket runs are not actually exercised; verification must
  explicitly rule both out.

## Pass 1 — critique

The plan correctly identifies the precondition as the central failure and
preserves the assertion, but it does not yet name the exact source-of-truth
helper or the candidate patch's intended contract. It also leaves the
repeat-count and integration command somewhat open, which could permit a weak
verification claim. The docs/support section is appropriately conservative.

## Pass 1 — critical evaluation of critique

The critique is valid: without inspecting current code and history, naming
exact helper boundaries would be speculation. The plan should retain discovery
before commitment, but it can make the acceptance gate concrete: no skip,
explicit precondition observation, and repeated isolated real-tmux runs. The
lack of a fixed repeat count is a stability decision that should be resolved by
the test's existing conventions rather than an arbitrary number.

## Pass 1 — roll-up to revised critique

Revise the implementation gate to require an observable modal-present state in
the real provider path, not merely a non-error setup call. Use repository
conventions/history to choose repeat coverage and record exact commands/results.
Do not force a new helper or docs change before understanding ownership.

## Pass 1 — roll-up to plan/tasks/subtasks

Add explicit inspection of the modal-present observation and test target/socket
identity to diagnosis and verification. Keep all other plan scope unchanged.

## Pass 1 — no-change decisions

- No production architecture change is assumed.
- No test expectation change is authorized without an independent correctness
  finding or a documented contract change.
- No role-specific or model-specific judgment belongs in Go.
- No docs change is needed unless implementation changes the supported contract.

## Pass 2 — revised full plan / tasks / subtasks (counter 2)

1. Establish ownership and baseline on the bead-scoped `origin/main` branch:
   inspect the tmux adapter, startup helper, real-tmux test, test socket
   lifecycle, and relevant history/candidate diff; run the exact failing test.
2. Trace the precondition end to end: identify the input/event that causes the
   model-switch modal, the real tmux pane/window target, the observation used by
   the test, and the dismissal assertion. Capture state before and after each
   transition so setup failures cannot masquerade as product behavior.
3. Write the smallest failing/diagnostic test adjustment at the correct
   boundary if the current test cannot prove modal creation. Implement the
   deterministic setup or minimal adapter fix, retaining the clear assertion.
4. Verify with fresh isolated tmux sockets and repeated targeted runs, then
   run package/affected tests, `go vet ./...`, and relevant build gates. Review
   every touched surface and document any pre-existing failures.
5. Commit, push, verify origin identity, record producer metadata, reassign to
   the refinery, and drain.

### Pass 2 — critique of each major section

- Objective: precise about target behavior and false completion; good.
- Tasks: now trace modal creation and target identity, but “minimal adapter
  fix” could still invite a production change without evidence.
- Architecture: correctly keeps ownership at tmux/provider boundary; should
  explicitly reject test-only state injection into production APIs.
- Tests/evidence: distinguishes real provider from mock, but should require
  both modal-present and modal-cleared observations in the test's own path.
- Support/docs: suitably conservative; `agent-execution.log` itself is
  process evidence, not product evidence.
- Stability/blockers: safe socket discipline is clear; bounded cleanup needs
  to avoid masking failures if setup cleanup itself fails.
- Handoff: complete and respects the refinery contract.
- Proxy audit: correctly warns about semantic/mock passes; residual risk should
  include a modal appearing by unrelated startup timing.

### Pass 2 — critical evaluation of critique

The critique usefully separates test harness ownership from production runtime
ownership. A deterministic test may legitimately use a provider-supported input
or fixture, but must not mutate private state merely to satisfy the assertion.
The evidence requirement should be expressed as observable provider state or
the public adapter operation's direct result, depending on what the existing
contract exposes. Cleanup must be scoped and failure-visible, but a cleanup
failure after the assertion should not erase the primary result.

### Pass 2 — roll-up to revised critique

Require the implementation to prove the modal is caused by the intended test
stimulus and observed on the intended real tmux target. Prefer existing public
test seams; if none exist, add the smallest test-only seam rather than leaking
test controls into production. Record cleanup behavior separately from the
product assertion.

### Pass 2 — roll-up to plan/tasks/subtasks

Update diagnosis and implementation tasks with causal stimulus/target checks,
and update evidence with both state transitions plus isolated cleanup failure
visibility. No broad redesign is justified.

### Pass 2 — no-change decisions

- Keep the assertion that dismissal clears the modal.
- Do not convert the integration test into a unit test or allow absent-modal
  skips.
- Do not use default tmux server state or destructive global cleanup.
- Do not add a new abstraction until current helpers and history prove a second
  implementation would benefit from it.

## Pass 3 — final revised full plan / tasks / subtasks (counter 3)

1. Baseline and archaeology: run `git log`/`git show` for the affected files
   and candidate commit; inspect current real-tmux setup, modal stimulus,
   target selection, and dismissal path; run the exact test on `origin/main`.
2. Causal diagnosis: capture fresh isolated socket/session evidence showing
   the intended stimulus, modal present on the selected target, dismissal, and
   modal cleared. Determine whether the defect is setup timing/order, target
   mismatch, provider startup behavior, or adapter behavior. Do not infer from
   a mock or a pass without precondition observation.
3. TDD implementation: encode the missing deterministic precondition at the
   narrowest existing test/provider boundary. If production code must change,
   keep it generic and minimal, with a focused regression test. Never weaken,
   skip, or invert the dismissal assertion.
4. Evidence review: run the focused test repeatedly with fresh scoped tmux
   state, inspect raw logs, run the affected package/integration shard and
   `go vet ./...`; use the project's documented sharded targets for broader
   coverage. Confirm the diff does not touch unrelated surfaces, and record
   why each changed expectation is correct (or avoid changing expectations).
5. Delivery: append completed-subtask entries to `agent-execution.log`, format
   and review the diff, commit on `polecat/sdk-bfd`, push and verify the exact
   remote tip, set `gc.work_*` and target metadata, reassign the still-open
   work bead to `Gas-City-SDK/gastown.refinery`, wake/nudge as prescribed, and
   drain-ack.

### Pass 3 — critique of each major section

- Objective: defines the product and evidence truth without presupposing the
  implementation; sufficient.
- Tasks: TDD and archaeology are ordered correctly. “Repeatedly” must be
  judged from observed stability, not used as an unbounded loop.
- Architecture: protects generic runtime layering and test-only ownership;
  the fallback production-change clause remains appropriately constrained.
- Tests/evidence: correctly distinguishes real tmux from proxies and names
  sharded coverage. It must also preserve the baseline failure evidence so a
  candidate-vs-base comparison remains reviewable.
- Support/docs: execution log is required; no docs are needed for an internal
  test precondition unless a supported contract changes.
- Stability/blockers: bounded commands and scoped cleanup prevent collateral
  damage; escalation is required if infrastructure prevents proving truth.
- Handoff: branch/metadata/push/refinery sequence is explicit; implementation
  bead must remain open.
- Proxy audit: covers false completion and remaining target mismatch/timing
  risk; should explicitly state that passing tests do not prove all tmux hosts,
  only the configured real-tmux integration boundary.

### Pass 3 — critical evaluation of critique

The final critique identifies the only material residual uncertainty: this test
can establish deterministic behavior for the configured tmux/provider boundary,
not every host or terminal environment. That is the correct scope. Preserve
baseline and candidate raw results, because a green candidate alone cannot
prove it fixed the pre-existing failure. “Repeatedly” should mean the existing
integration conventions or a small documented repeat count sufficient to show
the precondition is not a one-shot race; stop when evidence is adequate rather
than adding an unbounded harness.

### Pass 3 — roll-up to revised critique

The plan is ready to execute. Acceptance requires: causal modal creation,
modal-present observation, dismissal, modal-cleared observation, fresh scoped
tmux state, baseline/candidate comparison, focused and affected checks, and a
clean refinery handoff. Any infrastructure-only failure is reported with its
observed layer and is not “fixed” by weakening the assertion.

### Pass 3 — roll-up to plan/tasks/subtasks

No new implementation task is needed. Add baseline/candidate artifact
retention and scoped-boundary qualification to verification. Keep the narrow
fix and existing ownership boundaries.

### Pass 3 — no-change decisions

- No role names, model judgment, API/dashboard code, or unrelated runtime
  abstractions enter the patch.
- No broad tmux cleanup, global server restart, or state-file workaround.
- No test expectation is changed merely to match the flaky baseline.
- No docs are changed unless the final implementation changes a user-facing
  or contributor-facing contract.

## Lost-information check after three passes

Retained from the initial plan: target truth, required real-provider evidence,
proxy limitations, upstream/history-first workflow, branch and refinery
handoff, scoped tmux safety, escalation path, test-expectation discipline,
temporary execution log, and candidate parallel-read work. Refinement added
causal stimulus/target checks, explicit before/after modal evidence, raw
baseline/candidate comparison, cleanup-failure visibility, and the scope limit
of configured-provider evidence. No critical information was lost.

## Approval decision

Plan is approved for execution under the autonomous polecat workflow. Proceed
with discovery and implementation in this worktree; no human approval pause is
needed because the assignment and constraints are durable in `sdk-bfd` and the
change remains within the existing tmux test/provider boundary.

## Execution findings

- The current candidate path includes the real-tmux modal test and the
  production dismissal adapter; the test uses a fixed 500 ms startup sleep.
- With Homebrew ICU flags supplied, the focused test passed once, then failed
  once in ten fresh test iterations because `CapturePaneAll` returned an empty
  pane after the fixed sleep, then passed the remaining iterations.
- The existing `b290985c9` history contains a narrowly scoped, proven polling
  helper for this exact test family. Port only that readiness pattern; do not
  import its unrelated production/input-fence changes.
- Planned patch: poll the real pane until the modal marker is observed and poll
  until `MODEL_KEPT` is observed after dismissal, retaining both assertions and
  the existing isolated tmux lifecycle.
- Verification: the corrected target passed 10/10 and 20/20 repeated runs; the
  documented `packages-runtime-tmux-3-of-3` shard passed all 132 selected tests.
