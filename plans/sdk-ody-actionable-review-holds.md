# Actionable candidate-review holds

counter=0

## Full plan and execution contract

### Ownership and boundary

The generic SDK remains responsible for typed review/acceptance evidence and
durable bead metadata, but does not classify prose, infer intent, choose an
owner, or create a role-specific repair. The Gas Town pack owns the policy and
mechanical workflow because the requested behavior is a Gas Town operating
loop, not a new SDK primitive. The pack contract is opt-in: only an explicit
`gc.candidate_review_*` metadata set with `hold_class=mechanical` is eligible.

### Tasks and subtasks

1. Add the pack contract and durable state machine.
   - Define exact correction, owner, target/source, route, review route,
     explicitly-owned residue/landed paths, and attempt-limit metadata.
   - Define queued, active, failed, exhausted, published, and resubmitted
     lifecycle evidence; preserve human/external/foreign/uncertain holds.
2. Add the pack order and repair formula.
   - Atomically claim only the source bead snapshot that was inspected.
   - Route one bounded repair formula to the configured owner/route.
   - Run current-target rebase, explicit cleanup, configured gates, durable
     push, and review resubmission in one writer's worktree.
3. Add deterministic regression coverage.
   - RED: prove actionable holds route once while non-mechanical holds do not.
   - GREEN: prove the worker repairs only declared paths, rebases against the
     current target, records evidence, and resubmits review.
   - Include target movement, duplicate-claim, exhausted-attempt, and foreign
     dirty-worktree cases.
4. Review the complete surface and run quality gates, then commit and hand off
   to Refinery without closing the implementation bead.

### Architectural changes

- New files live under `examples/gastown/`, the fork-owned Gas Town pack
  surface; no new SDK primitive, Go role branch, or core metadata constant.
- The order is a city/rig pack mechanism and uses `gc bd update` preconditions
  as the single-writer claim. It does not use PID/lock/status files.
- The repair worker is a pack asset. It accepts only validated relative paths,
  requires a clean isolated worktree before agent changes, checks the fetched
  target again before publication, and records lifecycle evidence on the
  source bead.

### Test and evidence plan

- Pack parser test: proves the order/formula are discoverable and the contract
  is represented at the pack layer; it does not prove a live controller runs
  them.
- Script harness with fake `gc` and real temporary git repositories: proves
  deterministic metadata CAS, routing, path allowlisting, target rebase,
  force-with-lease publication, and review handoff; it does not prove provider
  sessions or a live Dolt/controller reconcile.
- Negative cases prove fail-closed preservation for human/external and
  uncertain/foreign work, duplicate writers, target movement, and exhausted
  retries.
- Run targeted `go test ./examples/gastown`, then documented fast tests,
  `go vet ./...`, and the active pre-commit hook.

### Support structures and documentation

- The pack asset comments document the metadata contract, state transitions,
  security/path boundary, and evidence semantics.
- The formula description documents the exact correction/owner requirement and
  the configured gate commands. The plan file is the implementation decision
  record; no generic SDK documentation changes are needed.
- `agent-execution.log` records each completed subtask while this work runs and
  is removed before handoff because it is temporary execution evidence.

### Execution order

1. Finish preflight control step and record plan.
2. Add the failing harness and contract assertions; run to RED.
3. Add order, formula, worker, and supporting pack tests; run to GREEN.
4. Review the diff and run targeted/broader gates.
5. Commit, push, verify remote identity, update the implementation bead, and
   reassign it to Refinery.

### Stability and blocker avoidance

- All claims use `--if-status` and `--if-assignee`; a lost race is a no-op.
- Every repair has a finite attempt budget and a durable failure state.
- The target ref is fetched before repair and compared again before push; a
  moving target prevents publication rather than creating a stale candidate.
- Human/external and uncertain/foreign metadata is never auto-routed.
- No broad filesystem cleanup, branch deletion, bare tmux cleanup, or cache
  invalidation is permitted.

### Candidate parallel work

- A separate agent could review the metadata contract against existing
  `beadmeta`/acceptance boundaries without editing the implementation surface.
- A separate agent could inspect pack-loading/order discovery and test fixture
  conventions. These are independent review tasks; no parallel writer is
  needed for the small implementation and this session has no delegated
  subagent capability exposed.

## Pass 1 — top-to-bottom critique (counter=1)

- Ownership: correct and aligned with the no-hardcoded-role rule; the pack
  must not leak assumptions into core Go.
- Contract: explicit fields prevent prose heuristics, but the worker must
  reject malformed JSON/path values before any mutation.
- State machine: CAS protects one writer, but publication and review handoff
  need distinct durable states so a handoff failure is retryable without
  repeating git repair.
- Tests: static pack tests alone would be false completion; the real git
  harness must exercise the worker and all fail-closed branches.
- Support/docs: comments need to say what the harness does not prove.
- Execution/stability: temporary execution logs must not ship; target movement
  and remote lease checks must be explicit.
- Parallel work: review-only candidates are useful, but implementation stays
  serialized at the worktree and bead boundaries.

## Critical evaluation of Pass 1 critique (counter=1)

The critique correctly identifies the two main risks: accidentally making the
SDK a policy engine and claiming completion from a static formula test. It does
not require a new generic API because existing bead CAS, order dispatch, and
formula routing already provide the needed infrastructure. It also correctly
separates repair failure from review-handoff failure, which is necessary for
bounded convergence rather than a repeated multi-hour stall.

## Roll-up: apply evaluation to critique (counter=1)

Treat pack metadata validation, CAS, target identity, remote lease, and state
transitions as load-bearing implementation requirements. Treat live controller
and provider execution as explicitly unproved by the local harness and report
that boundary rather than substituting a proxy claim.

## Roll-up: revised plan/tasks/subtasks (counter=1)

Add a real-git worker harness before implementation, make publication and
review handoff separate states, and require malformed/foreign/uncertain input
to remain untouched. Keep the core SDK unchanged unless a test proves an
existing boundary is insufficient.

## No-change decisions (Pass 1)

- Do not add candidate-review fields to `internal/beadmeta`; they are pack
  policy, not a core wire contract.
- Do not modify convoy acceptance or generic dispatch; neither owns repair
  judgment.
- Do not copy or fork the large upstream refinery formula; add a small,
  explicitly invoked pack repair workflow.

## Pass 2 — top-to-bottom critique (counter=2)

- Ownership remains appropriately local, but the local example must actually
  discover the new order/formula through its existing import/layout rules.
- The task contract needs a clear distinction between correction owner,
  repair route, and review route so no route is guessed.
- The worker must not stage arbitrary foreign changes; requiring a clean
  pre-agent worktree and committing the agent correction before mechanics is
  the simplest safe boundary.
- Gate evidence must identify configured commands and results, while commands
  remain configuration/agent-owned rather than bead-authored shell injection.
- Tests need to assert one writer wins and that a failed review handoff does
  not trigger a second git mutation.

## Critical evaluation of Pass 2 critique (counter=2)

These refinements improve operability without adding abstractions. Discovery
is a concrete integration risk because the example imports its public pack;
the local root order/formula must be tested directly. Requiring separate route
fields prevents the repair loop from silently sending work back to the actor
that supplied the hold. Gate execution belongs to the configured formula
surface, with the worker recording results, not to untrusted bead metadata.

## Roll-up: apply evaluation to critique (counter=2)

The implementation will use a local root order and formula, explicit route
metadata, a clean-worktree precondition, and formula-provided gate commands.
The harness will include a published-pending-review retry case and verify no
second rebase/push occurs.

## Roll-up: revised plan/tasks/subtasks (counter=2)

Before coding, confirm local root discovery and formula variable injection.
Write RED tests around the order contract and worker state transitions, then
implement only the pack files needed to make those tests pass.

## No-change decisions (Pass 2)

- Keep the order rig-scoped so it reads and routes within the owning work
  store; do not invent a city-wide federated scanner.
- Keep exact paths JSON arrays and reject traversal/absolute paths; do not
  accept globbing or inferred files.
- Keep human/external/foreign/uncertain holds fail-closed; do not add a timeout
  that converts an unresolved human decision into automation.

## Pass 3 — top-to-bottom critique (counter=3)

- The plan now covers policy ownership, durable state, one-writer claims,
  target freshness, explicit cleanup, gate evidence, and handoff recovery.
- The main remaining risk is an overlarge shell implementation; keep helpers
  small, avoid eval except for trusted formula-configured gate commands, and
  make all error transitions observable.
- The local harness must prove the target lifecycle without pretending to prove
  a live model's interpretation of the correction. The formula prompt is the
  judgment boundary; the worker only performs declared mechanics.
- Final verification must include the repository's documented test tiers and
  branch/push identity, not only the focused test.

## Critical evaluation of Pass 3 critique (counter=3)

The remaining risks are implementation risks, not reasons to widen scope. A
small shell state machine is justified because the behavior is pack-owned and
the project already uses executable pack assets for mechanical order work. The
worker can avoid arbitrary staging and shell injection by accepting only
formula-configured commands and validated path lists. The final handoff checks
prove artifact identity but do not prove Refinery merge success; Refinery owns
that final truth.

## Roll-up: apply evaluation to critique (counter=3)

Proceed with the pack-only implementation, preserving strict evidence labels:
local harness = pack/worker behavior; focused Go tests = source-level fixture
behavior; remote SHA = publication identity; Refinery = merge truth.

## Roll-up: revised plan/tasks/subtasks (counter=3)

Implement and test the contract now. Keep the code small and reviewable,
record all failures durably, run the required gates, and hand off only after
remote SHA verification.

## No-change decisions (Pass 3)

- No Go changes unless the pack cannot reach an existing canonical routing or
  CAS operation; current inspection shows it can.
- No external repository edits or dependency pin changes; this fork owns the
  local pack overlay.
- No live-controller claim from unit/static tests; report that as an explicit
  residual verification boundary.

## Lost-information check after three passes

The refinement preserved the original required behavior: exact correction and
owner persistence, one-writer ownership, bounded current-target repair,
explicit-only cleanup, gate rerun, durable remote publication, review
resubmission, fail-closed human/external holds, foreign-work preservation, and
durable lifecycle evidence. It also preserved the decision that the correct
owner is the user-supplied Gas Town pack/formula/prompt rather than generic Go.
