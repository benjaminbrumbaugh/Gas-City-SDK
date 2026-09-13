# sdk-zxp2 plan

counter: 0
status: proposed

## 1. Full plan, tasks, and subtasks

Objective: determine whether `TestE2E_SuspendResume_Agent` exposes a real
session lifecycle defect or a test/environment race, and deliver the smallest
reviewable correction without weakening the suspended-session contract.

1. Establish ownership and evidence boundaries.
   - Inspect the assigned bead, current branch, worktree status, and refs.
   - Read the repository testing guidance and the named integration test.
   - Inspect the suspend/resume production path and search history for prior
     fixes or diagnosis of this assertion.
2. Reproduce and classify the failure.
   - Run the focused integration test with uncached, bounded execution where
     the local harness supports it.
   - Capture the exact session state, timing, and cleanup result when it fails.
   - Compare the candidate bytes with `origin/main` and separate baseline
     evidence from causality evidence.
3. Choose the smallest boundary-correct deliverable.
   - If the test is independently wrong, add a failing regression and repair
     only its synchronization/fixture boundary while preserving real lifecycle
     assertions.
   - If production behavior violates the contract, add a focused regression
     first, then repair the session lifecycle at its owning boundary.
   - If the failure is environment-only or already present on the unchanged
     target, make no product change and record a durable baseline handoff.
4. Verify and hand off.
   - Run focused checks, affected tests, and proportional repository gates.
   - Review the final diff and ensure no pre-existing artifacts are staged.
   - Commit only scoped product changes when justified, push the bead branch,
     and hand the bead to the refinery without closing implementation work.

## 2. Architectural changes

No architectural change is presumed. The owning boundary is the session
manager/runtime lifecycle used by the integration harness; test-only setup
must remain in `test/integration`. Do not add role-specific logic, status
files, retries that mask a lifecycle race, or a second session abstraction.

## 3. Test plan and evidence discipline

Target truth: a session explicitly suspended by the supervisor remains
suspended and is not automatically restarted until an explicit resume action,
across the real supervisor/session composition.

Required evidence: the named integration test and inspection of the actual
session state transition and restart decision boundary. The integration test
observes real process/session lifecycle composition; it does not prove all
controller schedules, hosted environments, or unrelated convoy fan-out paths.

Useful but insufficient proxies include a passing unit test, a status snapshot,
an increased sleep/timeout, or a test run against a fake provider. The tempting
false-completion substitution is changing the expectation, skipping the test,
or adding retries until the assertion disappears. If this plan succeeds while
the original bug remains, a likely cause is that the test only observed a
stale status projection; verify the live restart path and process/session
identity before declaring completion.

## 4. Support structures

Use the existing integration harness and temporary `agent-execution.log`.
Preserve all existing untracked files and the unrelated branch commit. Add no
new helper unless two concrete lifecycle fixtures need the same tested
contract. Keep all temporary diagnostics on disk outside product paths.

## 5. Documentation

No user-facing documentation change is expected. Record the observed layer,
exact command/result, and residual uncertainty in the bead handoff. Add source
comments only if they explain a lasting lifecycle invariant rather than the
history of this incident.

## 6. Execution order and stability strategy

Read-only ownership/source inspection -> focused reproduction -> history and
causality classification -> test-first correction if justified -> focused and
affected verification -> scoped diff review -> branch handoff. Use the
documented integration shard command and avoid broad destructive cleanup,
shared cache changes, tmux-wide shutdown, or Dolt restarts.

## 7. Blocker avoidance

Use local history and existing diagnostics before escalating. If infrastructure
prevents reproduction, retain the exact failed layer and do not relabel it as
a product defect. If branch ownership or the required refinery handoff cannot
be reconciled safely, preserve the worktree and escalate rather than rewriting
refs or submitting unrelated commits.

## 8. Candidate subagent-parallel work

Read-only history archaeology and current lifecycle-source inspection are
independent candidates for parallel work. Test execution and any implementation
remain serial because they depend on the evidence classification. No subagent
is required for the initial small-surface diagnosis.

## 9. Planning pass 1 — critique of every major section

- Full plan: the decision gate is appropriate, but it must explicitly compare
  the current branch's committed bytes to the recorded baseline before taking
  ownership of the failure.
- Architecture: keeping the fix at the session boundary is sound, but the
  test may be observing a supervisor projection rather than a live runtime;
  inspect both sides before changing either.
- Test/evidence: the required integration test is correctly prioritized, but a
  single pass cannot distinguish an async wake race from deterministic restart
  policy.
- Support/docs: temporary logs and existing untracked files are protected;
  the bead note must state whether any product artifact was actually produced.
- Execution/stability: bounded, uncached focused runs are right; do not use a
  larger timeout as a substitute for synchronization.
- Blockers/parallelism: the branch mismatch may be a workflow boundary issue,
  not a reason to rename refs without inspecting ownership.
- Proxy audit: it names the main false completions, but must include process
  identity and the explicit resume event as the final lifecycle evidence.

## 10. Critical evaluation of that critique

The critique identifies the load-bearing risk: a failing assertion on a
baseline branch cannot justify a product patch without observing why restart
was requested. The investigation must therefore inspect the suspend command,
the supervisor's reconciliation/restart inputs, and the session provider's
actual process state. Reproduction under the documented shard is valuable,
but inability to reproduce does not prove correctness. The branch currently
contains a prior docs commit and is not named for this bead; that is a handoff
boundary to preserve, not evidence of causality.

## 11. Roll-up: apply evaluation to critique

Add an explicit pre-edit provenance check and require that any proposed
regression test observe the live session/process identity before and after
suspend. Treat the baseline failure as test/integration evidence until source
inspection proves a production defect. Make the no-change baseline handoff a
valid result, but do not fabricate a source commit merely to satisfy the
deliverable filename.

## 12. Roll-up: apply revised critique to plan/tasks/subtasks

Task 1 now includes branch/diff provenance and both supervisor and runtime
boundaries. Task 2 requires uncached focused reproduction plus process/session
identity evidence. Task 3 forbids expectation weakening and allows no product
change when the unchanged target reproduces the failure. Task 4 requires a
truthful, scoped handoff and preserves the implementation-bead closure rule.

## 13. Explicit no-change decisions

- Do not change suspend/resume behavior or test expectations from the bead text
  alone.
- Do not add retries, fixed sleeps, broad timeouts, or skip directives to hide
  an asynchronous lifecycle defect.
- Do not clean, overwrite, stage, or commit the existing untracked artifacts.
- Do not rename or rewrite the current branch until ownership and handoff
  safety are established.

counter: 1

## 14. Planning pass 2 — critique of the refined plan

- Full plan/tasks: the evidence and no-change paths are complete, but the
  focused command must be selected from `TESTING.md`, not improvised.
- Architecture: the two-sided supervisor/runtime inspection is enough for
  diagnosis; avoid introducing a new lifecycle interface for one failure.
- Test plan: process identity is necessary, but termination/restart evidence
  must distinguish “suspended” from “not yet reconciled.”
- Support/docs: the execution log should record completed subtasks only and
  must not become an accidental product deliverable.
- Execution/stability: a baseline failure should be reproduced at least twice
  when safe, with each run's environment limitation recorded separately.
- Blockers/parallelism: Dolt or tmux failures need their own diagnostics and
  must not be folded into the lifecycle result.
- Proxy audit: the explicit resume event is named, but residual risk should
  include a controller restart or cross-process race outside this test.

## 15. Critical evaluation of that critique

The focused integration command and harness lifecycle define the evidence
layer. Repeating the test can classify scheduler sensitivity, but repetition
does not replace source inspection or prove the absence of a race. The key
contract is temporal: suspend must prevent the restart reconciler from
starting a session, while resume must be the causative transition. Therefore,
the final decision must include the observed transition sequence, not merely
the terminal agent status.

## 16. Roll-up: apply evaluation to critique

Require a transition-sequence record for every reproduction attempt and a
separate classification for host/infrastructure errors. Add a bounded repeat
only where the documented harness allows it. Keep controller restart and
other out-of-scope races as residual risk rather than expanding the patch.

## 17. Roll-up: apply revised critique to plan/tasks/subtasks

Task 2 now records suspend -> reconcile/restart attempt -> resume (or the
absence of the expected transition) and repeats only as a diagnostic. Task 4
reports the evidence layer and residual risk explicitly. No new runtime
surface is added solely for observability.

## 18. Explicit no-change decisions

- Do not treat a repeated failure as permission to weaken the contract; it
  strengthens only the baseline diagnosis.
- Do not restart Dolt or tmux broadly for convenience.
- Do not broaden the fix to controller restart semantics without evidence from
  the named target and an owning code boundary.

counter: 2

## 19. Planning pass 3 — critique of the refined plan

- Full plan/tasks: complete, with a clear no-change outcome and scoped
  implementation path; final execution must check for information loss.
- Architecture: boundaries are explicit and avoid hardcoded roles or upward
  leakage.
- Test/evidence: the target truth, required layer, insufficient proxies, and
  residual risk are stated; the exact test command remains environment-bound.
- Support/docs: temporary artifacts are protected and the durable handoff is
  correctly the bead/refinery record.
- Execution/stability/blockers: ordering and safety rules are sufficient;
  tests must not use cached success as proof.
- Parallel work: parallelism is optional and should not risk the dirty
  worktree.
- No-change decisions: they preserve the product contract and branch safety.

## 20. Critical evaluation of that critique

No material planning gap remains. The plan distinguishes a real lifecycle
defect from a pre-existing integration baseline and prevents a green test
proxy from replacing the target behavior. The existing branch mismatch and
untracked files remain the main operational risks; both are handled by the
required provenance and scoped-diff checks. If the unchanged target still
fails and no independent test defect is found, the correct output is an
evidence-only handoff, not an invented implementation.

## 21. Roll-up: apply evaluation to critique

Proceed with the plan using the repository's documented test commands,
preserve unrelated worktree state, and make the final handoff truthful about
whether a source diff exists and what layer the evidence covers.

## 22. Roll-up: apply revised critique to plan/tasks/subtasks

The task sequence is final: provenance and source inspection, bounded
reproduction, test-first scoped correction only if justified, proportional
verification, then refinery handoff. After the three passes, no information
was lost; the target contract, evidence limits, branch boundary, and no-change
path all remain represented.

## 23. Explicit no-change decisions

- No product-code or test-expectation change without independent causal proof.
- No staging or cleanup of pre-existing files or unrelated commits.
- No implementation-bead closure; the refinery remains responsible for
  verification and closure after handoff.

counter: 3
