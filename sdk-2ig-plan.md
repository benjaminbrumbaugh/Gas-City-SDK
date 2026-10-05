# sdk-2ig plan: idempotent REST integration cleanup

counter: 0

## Target and scope

Fix the pre-existing REST integration cleanup failure: after functional REST
tests finish, cleanup must accept an integration supervisor that is already
stopped and an owned tmux server that is already absent. Unexpected cleanup
errors, ownership violations, and timeouts must remain visible. Keep the
change in the integration/tmux test harness; do not alter production
supervisor lifecycle semantics.

## Full plan, tasks, and subtasks

1. Establish boundaries and evidence.
   - Read TESTING.md and the integration/tmuxtest cleanup paths.
   - Inspect the current supervisor-stop output and local tmux absent-server
     output without touching any shared/default tmux server.
   - Confirm the sibling historical fix applies cleanly to this base only at
     the intended files.
2. Add RED tests before implementation.
   - Test owned tmux cleanup treats an absent server as a successful terminal
     state.
   - Test owned tmux cleanup still reports an unrelated command failure.
   - Test integration supervisor cleanup classifies only the already-stopped
     message as complete; timeout and unrelated failures remain diagnostic.
3. Implement the narrowest fix.
   - Capture tmux cleanup output and classify only known absent-server forms.
   - Classify only the current supervisor CLI's already-stopped result.
   - Preserve command context, ownership checks, timeout behavior, and real
     cleanup calls.
4. Verify in increasing evidence layers.
   - Run focused unit tests and package tests.
   - Run the affected REST integration shard or the smallest real shard that
     exercises TestMain cleanup.
   - Run gofmt, go vet, and the documented fast baseline where practical.
5. Self-review, commit, push, and hand the open bead to the refinery.

## Architectural changes

No SDK architecture or public API change. This is a test-harness lifecycle
boundary: cleanup becomes idempotent for explicit terminal states while
retaining strict ownership and diagnostics for all other failures.

## Test plan and evidence discipline

Target truth: a real REST integration process exits through cleanup cleanly
when its own supervisor/tmux server has already terminated, while unrelated
cleanup failures remain observable and actionable.

Required evidence layer: `test/integration` lifecycle cleanup plus
`test/tmuxtest` owned-socket helper tests. A real REST integration shard is
the process/tmux composition proof.

Useful but insufficient proxies: pure string-classifier tests, fake tmux
executables, and the existing timeout test. They prove classification or
bounded control flow only, not the real cleanup boundary.

Tempting false completion: ignoring every non-zero cleanup exit, deleting
owned socket paths instead of stopping servers, or accepting only functional
REST test passes. None proves clean resource teardown.

If the plan succeeds but the bug remains, another cleanup path or a different
platform-specific terminal error is likely. Keep real shard execution and
negative-path diagnostics in the gate so that failure is not hidden.

## Support structures

Reuse the existing command runner and fake executable pattern. Add no new
dependency, retry loop, polling, persistent state, or broad tmux cleanup.
Keep all edits in the bead worktree and leave the dirty polecat home alone.

## Documentation

No user documentation change. Add only concise code comments if needed to
explain the explicit idempotent terminal-state contract and ownership fence.
This plan is the reviewable design/evidence artifact.

## Execution order

Read-only archaeology and baseline -> RED tests -> narrow implementation ->
focused GREEN tests -> real REST cleanup evidence -> vet/fast checks ->
self-review/commit -> push and refinery handoff.

## Stability strategy

Do not add sleeps or polling. Preserve the existing timeout budget. Match only
observed/known absent-server and already-stopped forms; permission failures,
malformed paths, timeouts, and unrelated command errors remain failures with
their output attached.

## Blocker avoidance

Use the fresh per-bead worktree. Never run bare `tmux kill-server` or touch a
personal/default server. If integration infrastructure fails for an unrelated
reason, record that evidence rather than weakening assertions. If Dolt is
unhealthy, collect the prescribed diagnostics before escalation.

## Candidate subagent-parallel work

The supervisor classifier and tmux classifier are logically independent, but
they share the cleanup contract and final integration proof. No subagent
surface is available here; serial changes keep ownership and review cohesive.

## Planning passes

### Pass 1 (counter 1): initial critique

The scope is correctly isolated, but terminal output must be observed locally
and the RED tests must precede implementation. The tmux helper is shared, so
unexpected errors need explicit negative coverage. The real REST shard must
remain required because fake commands cannot prove process cleanup.

### Pass 2 (counter 2): critical evaluation and roll-up

A broad matcher would hide platform and permission failures. Use pure named
classifiers around unchanged command invocation, with captured output on every
non-benign error. Keep supervisor timeout handling before classification. The
real shard is the required boundary proof; focused tests only establish the
classification contract. No architecture, API, or documentation expansion.

### Pass 3 (counter 3): final refinement and lost-information check

The final sequence preserves ownership checks, timeouts, diagnostics, and the
actual cleanup calls while adding only explicit idempotent terminal states.
Review confirms no plan information was lost: target truth, evidence layers,
negative cases, stability constraints, and handoff requirements remain above.

No-change decisions: no production supervisor changes, no retries/polling,
no socket deletion workaround, no public API, no user docs, no new dependency,
no broad cleanup matcher, and no parallel conflicting edits.

Planning is implementation-ready; proceed without an approval wait because
this is an assigned polecat task and the handoff sequence is mandatory.

## Validation record

- Observed local tmux terminal output: `error connecting to ... (No such
  file or directory)` for an owned absent socket.
- RED: new tmux tests failed before implementation; the integration package
  could not compile with default CGO because ICU headers are unavailable.
- GREEN: `go test ./test/tmuxtest -count=1`, integration classifier test and
  `go vet` with `CGO_ENABLED=0`, and the real
  `TestGastown_ControllerStartStop` lifecycle test passed.
- `LOCAL_TEST_JOBS=2 make test-fast-parallel` passed all 10 jobs.
- The six-test REST smoke composition ran and reached the real graph workflow;
  it failed only because `TestGraphWorkflowSuccessPath` timed out waiting for
  a workflow bead, an unrelated pre-existing orchestration failure. The
  supervisor lifecycle test independently passed cleanup.
- Default `go vet ./...` and default integration compilation are blocked by
  the host missing `unicode/regex.h`; pure-Go vet and integration checks pass.
