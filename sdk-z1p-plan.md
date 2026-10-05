# sdk-z1p plan: integration cleanup idempotency

counter: 0

## Target and scope

Fix the pre-existing integration cleanup failure reported by
`make test-integration-shards-parallel`: after functional REST shards finish,
cleanup must accept an already-stopped supervisor and must treat an owned tmux
socket whose server is already gone as successfully cleaned. Preserve the
ownership fences: cleanup may act only on the current run's supervisor and
owned tmux socket root.

## Full plan, tasks, and subtasks

1. Establish the owning boundaries and reproduce the failure.
   - Review `test/integration/integration_test.go` lifecycle cleanup and
     `test/tmuxtest/guard.go` owned-socket cleanup.
   - Run focused existing tests and, where available, a minimal reproduction
     for the two terminal states: supervisor already stopped; tmux server
     already absent behind a discovered socket path.
2. Add RED tests at the smallest owning layers.
   - Add a tmuxtest unit test proving a missing tmux server is idempotent.
   - Add a tmuxtest unit test proving unexpected tmux cleanup errors still
     surface.
   - Add integration cleanup classification tests proving
     `supervisor is not running` is benign while timeout/other failures remain
     diagnostic.
3. Implement the narrowest fixes.
   - Make owned tmux socket shutdown classify the documented absent-server
     result as success, retaining context for real errors.
   - Make the integration supervisor-stop wrapper classify the documented
     already-stopped result as success without weakening timeout handling.
4. Refactor only if needed for clear names and reusable error classification.
5. Run focused tests, the affected integration shard, `go vet`, and the fast
   project baseline as practical; record any environmental limitation.
6. Self-review the diff, commit only scoped changes plus this plan, push the
   per-bead branch, and hand the open work bead to the refinery.

## Architectural changes

No production SDK architecture changes. This is test-harness lifecycle policy:
cleanup is an idempotent terminal-state boundary, while ownership and failure
reporting remain strict. No new abstraction or interface is planned.

## Test plan and evidence discipline

Target truth: a real integration run exits cleanly after functional tests when
its isolated supervisor has already stopped and/or its owned tmux server is
already absent; unrelated cleanup failures still fail the run.

Required evidence layer: integration harness lifecycle tests plus the focused
tmuxtest helper tests. A full integration shard is the real process/tmux
composition proof.

Useful but insufficient proxies: unit tests of string classification, fake
tmux scripts, and the existing timeout helper test. They prove classification
and bounded control flow only; they do not prove a real supervisor/tmux run
reaches cleanup cleanly.

Tempting false completion: changing cleanup to ignore every non-zero exit,
removing the owned-socket cleanup, or relying only on passing functional REST
tests. Those would hide real leaks and would not prove the cleanup boundary.

If the plan succeeds but the original bug remains, the likely cause is a
different supervisor lifecycle path or tmux error text. The full integration
shard and retained error diagnostics must therefore remain part of validation.

## Support structures

Use the existing command runner and tmuxtest fake-executable pattern. No new
library or persistent runtime state is needed. Keep the bead-scoped worktree
and branch metadata authoritative; leave the stale polecat home untouched.

## Documentation

No user documentation change is needed. Update code comments only where they
describe the idempotent terminal-state contract or preserve the ownership
boundary.

## Execution order

Read-only archaeology and baseline checks -> RED tests -> focused
implementation -> focused GREEN checks -> integration shard -> vet/fast gate
-> self-review/commit -> push and refinery handoff.

## Stability strategy

Do not add sleeps or polling. Match the existing bounded command context.
Classify only explicit already-stopped/absent-server outcomes; preserve
timeouts, permission failures, malformed paths, and unexpected command errors.
Keep cleanup diagnostics on stderr and retain command output for real failures.

## Blocker avoidance

Use the fresh bead worktree, not the dirty reusable polecat home. Avoid broad
tmux cleanup and never kill a default/shared tmux server. If Dolt or the
integration infrastructure is unhealthy, collect the prescribed diagnostics
before escalation. If a test fails for an unrelated pre-existing reason,
record it and do not weaken the new assertion.

## Candidate parallel work

Potentially independent reviews are: (a) supervisor-stop terminal-state
semantics in the integration harness, and (b) tmux absent-server error
classification in tmuxtest. No subagent surface is available in this session,
and the changes share one cleanup contract, so they will be handled in one
cohesive sequence to avoid conflicting edits.

## Planning passes

Pass 1, critique, critical evaluation, and rollups will be appended before
implementation. The plan must reach counter 3 and preserve any information
lost during refinement.

## Planning pass 1 (counter 1)

### 1. Full plan/tasks/subtasks

Retain the plan above. Add an explicit evidence step before RED: run a
minimal `tmux -S <owned-path> kill-server` probe against an absent server and
capture its stderr on this host, so classification is based on observed
behavior rather than only the issue text. Add an explicit check of supervisor
stop output from `cmd_supervisor.go` so the benign phrase is anchored to the
current CLI contract.

### 2. Critique of every major section, top-to-bottom

- Target/scope: correctly isolates cleanup, but the exact absent-server output
  and whether a stale socket file remains are not yet evidenced.
- Tasks: good lifecycle order; the RED step needs tests that fail before either
  implementation change, not just post-hoc helper tests.
- Architecture: correctly avoids SDK changes; the shared tmuxtest helper has
  multiple callers, so the contract impact must be reviewed.
- Test/evidence: correctly distinguishes proxies from the real shard, but must
  retain a negative-path assertion so cleanup does not swallow permission or
  malformed-command failures.
- Support structures: fake executables are appropriate and deterministic, but
  the fake must match actual stderr sufficiently to test the intended class.
- Documentation: no docs needed; comments should avoid claiming all tmux
  failures are benign.
- Execution/stability: correct ordering and no sleeps; add the host's focused
  test command before broad tests.
- Blocker avoidance: correct, including no shared tmux server; the plan should
  explicitly avoid deleting stale socket files as a substitute for stopping a
  server.
- Parallel work: the two reviews are independent in theory but share the
  cleanup acceptance; serial implementation is safer without subagents.

### 3. Critical evaluation of that critique, top-to-bottom

The added evidence step is necessary because tmux wording varies by platform.
The RED requirement is already mandated by TESTING.md and must be enforced by
running the focused tests before editing production code. Shared-helper impact
is real: idempotent absent-server behavior is valid for runtime/tmux and
integration cleanup, while unexpected errors must remain visible. A fake that
only returns exit 1 would make the negative case impossible to distinguish;
the fake needs distinct stderr fixtures. Deleting socket files would bypass the
owned-server contract and is not acceptable. No additional abstraction is
justified.

### 4. Roll-up: apply evaluation to the critique

The implementation will use a small, named classifier around captured tmux
stderr, matching only absent-server forms observed locally and known tmux
forms. Error wrapping will preserve command context and output for all other
failures. The integration wrapper will similarly classify only the exact
already-stopped supervisor message. Tests will be authored and run RED before
these changes.

### 5. Roll-up: apply revised critique to plan/tasks/subtasks

Update task 1 to capture local tmux output and supervisor CLI wording. Update
task 2 to include positive and negative fake-command fixtures. Update task 3
to forbid broad non-zero suppression and socket deletion. Update stability to
require platform-safe matching and retained diagnostics.

### 6. No-change decisions

No change to the architectural boundary, public CLI behavior, ownership root,
timeout budget, or docs scope. No subagent split is added.

## Planning pass 2 (counter 2)

### 1. Full plan/tasks/subtasks

The revised work is: observe exact terminal outputs; write RED unit tests in
`test/tmuxtest/guard_test.go` and an integration cleanup classification test;
implement narrow classifiers in `test/tmuxtest/guard.go` and
`test/integration/integration_test.go`; run focused and real-boundary checks;
then hand off the committed branch.

### 2. Critique of every major section, top-to-bottom

- Target/scope: now precise, but “real-boundary check” must name the relevant
  integration shard and not be replaced by a fake-only pass.
- Tasks: complete, but adding an integration test under the integration build
  tag may invoke expensive TestMain setup; keep the pure classifier test small
  and still run one real shard.
- Architecture: shared test helper change is the only cross-package surface;
  confirm its error message remains actionable.
- Test/evidence: positive/negative fake coverage is good, but a fake cannot
  prove the supervisor's actual output; retain the existing command path and
  run the affected shard.
- Support/docs/execution: coherent; the plan artifact itself is a reviewable
  change and should be included in the branch.
- Stability/blockers: good; avoid changing timeout semantics while classifying
  terminal state.
- Parallel work: still not worth splitting because the final acceptance is
  one cleanup sequence.

### 3. Critical evaluation of that critique, top-to-bottom

The shard requirement prevents false completion. Integration TestMain overhead
is acceptable for a package-level test, but the classification function should
be pure and independently testable. Error context is part of the diagnostic
contract and must include the socket path and captured output. Supervisor
timeouts must continue to log as timeouts, not be mistaken for already-stopped
state. Including the plan is required by the workspace instructions and gives
the refinery the reasoning/evidence record.

### 4. Roll-up: apply evaluation to the critique

Use pure helpers for classification and keep process invocation unchanged.
Add tests for absent-server, benign supervisor-stop, and unexpected errors.
Run `go test ./test/tmuxtest` plus the integration package's focused test, then
the configured integration shard. Do not weaken or remove any cleanup call.

### 5. Roll-up: apply revised critique to plan/tasks/subtasks

The final implementation plan explicitly preserves: command timeouts,
ownership checks, real cleanup calls, diagnostic output, and a full integration
composition proof. Focused unit tests are evidence of classification only;
the shard is the evidence of the target behavior.

### 6. No-change decisions

No change to production supervisor stop semantics, no retry loop, no polling,
no new dependency, and no deletion of stale socket files.

## Planning pass 3 (counter 3)

### 1. Full plan/tasks/subtasks

Final sequence: (1) verify current branch/worktree and captured terminal
outputs; (2) write and run RED tests; (3) add narrowly scoped idempotence
classification with preserved diagnostics; (4) run focused GREEN tests and the
affected integration boundary; (5) run vet/fast checks available in the rig;
(6) review, commit, push, update metadata, reassign to refinery, and drain.

### 2. Critique of every major section, top-to-bottom

The target, architecture, evidence, support, docs, execution, stability,
blocker, and handoff sections are now mutually consistent. The only residual
risk is platform-specific tmux wording, which is addressed by observed local
output plus conservative known variants and a negative test.

### 3. Critical evaluation of that critique, top-to-bottom

The residual risk cannot be eliminated with a broader matcher without hiding
real failures. The correct boundary is a small explicit set of absent-server
phrases, with all other errors retained. The real integration shard remains
mandatory evidence. The dirty reusable home remains outside the patch.

### 4. Roll-up: apply evaluation to the critique

Proceed with no architectural expansion. If the host's tmux output differs,
add only the observed absent-server form and its test fixture; do not broaden
matching speculatively.

### 5. Roll-up: apply revised critique to plan/tasks/subtasks

The plan is implementation-ready. Acceptance is satisfied only when the
focused tests, affected integration evidence, and quality gates support the
same conclusion: already-complete cleanup succeeds, unexpected cleanup still
fails, and ownership boundaries remain unchanged.

### 6. No-change decisions

No changes to user docs, public APIs, production supervisor lifecycle, test
timeouts, process ownership, or the stale home worktree. No parallel edits.

Planning complete at counter 3. Proceed without an approval wait because this
is an assigned polecat implementation and the done sequence is mandatory.
