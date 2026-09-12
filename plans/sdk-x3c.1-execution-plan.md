# sdk-x3c.1 execution plan

counter=0

## Full plan and tasks

Task 1 — reproduce the child failure and capture the right evidence layer.

- Re-run `TestGCLiveContract_BeadsAndEvents` with the repository's Darwin
  integration environment.
- Compare the current branch with `origin/main` where useful.
- Capture the session start result, worker/session phase, process liveness,
  transcript presence, and raw stream precondition at the failure point.

Task 2 — identify the owning boundary.

- Trace the API stream handler through worker state and transcript/live-output
  discovery.
- Decide whether the 404 is caused by product behavior, provider lifecycle,
  test timing, or an invalid test expectation.
- Keep T3/provider assumptions out of generic session code.

Task 3 — make the smallest evidence-backed repair.

- Write a failing test at the boundary that owns the defect before changing
  implementation code.
- If the defect is not in this repository's product contract, make only a
  narrowly scoped evidence/test correction and record why.
- Do not weaken a real live-stream contract to make the integration shard
  green.

Task 4 — verify and hand off.

- Run focused unit/integration tests, the affected shard, fast baseline, and
  `go vet ./...` in documented environments as applicable.
- Update `sdk-x3c` and `sdk-x3c.1` with evidence limits and status.
- Commit and push only scoped changes; preserve unrelated worktree files.

## Architecture and boundary changes

No architecture change is planned initially. The observed boundary is
API session stream → worker session state → runtime/transcript provider. Any
repair must stay within the owning API/session/runtime boundary and preserve
the worker boundary and provider-neutral SDK contract.

## Test plan and evidence discipline

The target truth is that a successfully started live session exposes the raw
session stream with the documented response status. Integration tests observe
the real supervisor/API/session/provider composition; they do not by
themselves prove the exact root cause. Focused unit tests observe stream
preconditions and state projection only. A semantic path assertion proves
filesystem identity, not byte-for-byte wire spelling. The tempting false
completion is changing 404 to success or loosening the test without proving
live output exists. If this plan succeeds while the original bug remains, the
most likely failure is an unobserved race between subprocess startup,
transcript creation, and worker phase projection.

## Support structures, docs, and stability

- Use this plan and the ignored temporary `agent-execution.log` for durable
  execution trace.
- Add no documentation unless the public contract changes.
- Use bounded integration timeouts, isolated temporary directories, and no
  destructive tmux/cache cleanup.
- Candidate parallel work: one read-only trace of stream/state code and one
  independent reproduction against `origin/main`; keep implementation and
  shared integration runs serialized.

## Execution order and blocker avoidance

Reproduce → inspect boundary → write RED test if a repair is owned → implement
minimal GREEN change or record non-code disposition → verify → update beads →
commit/push. Avoid rebuilding missing mechanisms before checking git history.
The user's explicit request authorizes immediate execution, so this plan is
visible and approved for execution without a pause.

## Planning pass 1 — critique

The plan covers reproduction, ownership, TDD, verification, bead updates, and
push. It names the stream boundary and limits claims about integration
evidence. It could over-focus on a code repair before proving whether the
failure is a startup race.

### Critical evaluation of the critique

That concern is addressed by making root-cause classification an explicit
Task 2 gate and by allowing a no-code disposition. The plan must not promote
an integration symptom into a generic API contract change.

### Roll-up: revised critique

No structural change is needed. The plan should explicitly preserve the
existing successful provider-env and path fixes while isolating this child.

### Roll-up: revised plan/tasks/subtasks

Keep the plan unchanged in execution order, and evaluate the child only after
the parent fixes are present on the pushed branch.

### No-change decisions

Do not retarget the parent, duplicate the child, touch the dashboard, broaden
the REST shard, or alter unrelated tmux/refinery behavior.

counter=1

## Planning pass 2 — critique

The revised plan correctly isolates the child, but “session start result” and
“live output” could still be conflated. The evidence checklist must distinguish
runtime process liveness, worker phase, and transcript bytes.

### Critical evaluation of the critique

The test plan already identifies the startup/transcript/phase race, but the
capture list should be operationally explicit so a passing retry is not
mistaken for proof of a deterministic fix.

### Roll-up: revised critique

Add an explicit A/B comparison of process liveness, worker phase, and output
availability at the stream request. No implementation should proceed on a
single retry alone.

### Roll-up: revised plan/tasks/subtasks

Task 1 now requires at least one controlled repeat or code-level test that
separates those three observations; all other tasks remain unchanged.

### No-change decisions

Do not increase stream timeouts or replace live API evidence with a unit-only
proxy. Do not claim the issue fixed merely because the endpoint eventually
returns a different status.

counter=2

## Planning pass 3 — critique

The plan is sufficiently specific and keeps the evidence layers separate. It
does not prescribe a speculative fix, and it protects the provider-neutral
boundary. The remaining risk is losing the distinction between the original
parent failures and this newly exposed child.

### Critical evaluation of the critique

The parent bead notes already record that provider Env and macOS path failures
are resolved, while this child records the raw-stream 404. Repeating that
separation in the final bead update prevents scope drift.

### Roll-up: revised critique

No additional tasks are needed. The plan retains the target truth, required
evidence layer, useful-but-insufficient proxies, false-completion trap, and
the way the bug could remain after an apparently green run.

### Roll-up: revised plan/tasks/subtasks

Proceed with the evidence pass immediately under the existing assignment.

### No-change decisions

No changes to architecture, public docs, unrelated tests, or assignment
routing. Do not force-close the parent while this child remains open.

### Lost-information check after three passes

No material information was lost. The plan preserves the target behavior,
layer-specific evidence, race risk, TDD gate, stability rules, scoped commit
and push requirement, and explicit no-change decisions. It is approved for
immediate execution by the user's request.
