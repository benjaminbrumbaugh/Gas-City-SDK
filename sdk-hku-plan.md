# sdk-hku investigation plan

counter: 3
status: executed-no-source-change

## Target truth and evidence discipline

Target truth: under the documented concurrent integration runner, a suspended
agent remains suspended and is not restarted, even when other REST-full shards
share the host and supervisor resources.

Required evidence layer: the real integration test's REST request path,
supervisor state, owned agent process/session state, and runner/shard resource
allocation. A unit test or semantic test preview cannot establish this.

Useful-but-insufficient proxies: an isolated `TestE2E_SuspendResume_Agent`
pass; repeated exact tests; concurrent exact tests with independent homes;
individual shard logs; host load/process census; and unrelated shard failures.
These identify controls and contention, but do not prove the loaded matrix's
restart assertion.

False-completion substitution to avoid: weakening/removing the assertion,
changing retry timing until the test passes, or treating an isolated pass as
proof that the parallel failure is fixed.

## Full plan, tasks, and subtasks

1. Load assignment and workflow context.
   - Confirm the claimed bead, formula, current branch, and mail context.
   - Run `gc bd prime` and preserve existing evidence notes.
2. Establish a bead-scoped workspace.
   - Fetch `origin/main` and create/reuse the worktree for `sdk-hku`.
   - Write `.beads/redirect`, record `metadata.work_dir`, `metadata.branch`,
     and `fork_sha`; verify branch shape and ancestry.
3. Map the failure boundary.
   - Inspect the suspend/resume test, shard runner, supervisor lifecycle, and
     integration-home/tmux/Dolt allocation.
   - Check git history and existing diagnostics before inventing changes.
4. Reproduce with bounded, owned experiments.
   - Run an exact control if needed.
   - Run the smallest loaded cohort that can exercise the shared boundary,
     with separate logs and process-group cleanup; record layer and limits.
   - Compare process, supervisor, tmux, REST, and test evidence.
5. Decide whether a product/test/runner fix is justified.
   - If the cause is identified, write TDD evidence first, implement the
     narrow boundary fix, and preserve the assertion.
   - If no cause is identified, make no source/test-expectation change and
     record the strongest evidence plus the remaining uncertainty.
6. Verify and hand off through the formula.
   - Run affected checks or the configured suite, vet as proportionate, review
     the diff, commit only reviewable changes, push, and reassign to refinery.

## Architectural changes

Default: none. Any change must remain at the concurrent runner/resource
boundary and must not leak host-specific or provider-specific assumptions into
generic SDK lifecycle code. The assertion and REST/process/supervisor
ownership boundaries remain intact.

## Test plan

- Existing exact integration test: real REST/process/supervisor lifecycle;
  useful control only.
- Existing documented parallel runner: real loaded matrix and its shard
  resource behavior; target evidence when the assigned assertion fires.
- Diagnostic process and resource census: observes host contention and owned
  cleanup only, never semantic suspend correctness by itself.
- If code changes are warranted, add the smallest deterministic regression
  test at the layer where the identified race exists before changing code.

## Support structures and artifacts

- `sdk-hku-plan.md`: this plan and three refinement passes.
- `agent-execution.log`: temporary subtask completion log.
- `/var/tmp/gc-sdk-hku-*`: bounded experiment logs only when experiments run;
  never use them as a substitute for target assertions.

## Docs

No documentation change is expected unless runner behavior or supported
resource-isolation semantics change. If docs are touched, apply the Gas City
docs skill and update source-of-truth docs only.

## Execution order

Load context -> plan refinement -> workspace setup -> boundary inspection ->
bounded experiments -> evidence-based change/no-change decision -> tests and
self-review -> commit -> push -> refinery handoff.

## Stability strategy and blocker avoidance

Use only owned process groups, unique integration homes/log directories, and
bounded commands. Do not kill shared tmux servers, restart Dolt without the
required diagnostics, or clean unrelated test processes. Treat timeouts and
cleanup failures as evidence of the observed layer, not as target-test passes.

## Candidate subagent-parallel work

If subagents are available, parallelize read-only inspection of (a) the
integration runner/shard resource allocation and (b) the suspend/resume test
and supervisor lifecycle. Keep experiments serialized or independently scoped
because shared-host load is itself the variable under investigation.

## Planning pass 0: initial review

The plan separates target behavior from proxies, preserves the assertion, and
keeps a no-change outcome valid. The primary risk is repeating a broad matrix
that is already known to be noisy without increasing observability. Mitigate
that by inspecting allocation code first and using bounded, uniquely owned
experiments only when they answer a specific boundary question.

## No-change decisions

- Do not alter `TestE2E_SuspendResume_Agent` expectations without a proven
  product-contract change or independently wrong test.
- Do not call broad host instability a fix for the restart race.
- Do not add a new abstraction until two concrete implementations require it.

## Planning pass 1 (counter 1): boundary-first refinement

### Full plan/tasks/subtasks

1. Load and verify the assignment, formula, bead ownership, and existing
   evidence; do not infer a source regression from the prompt alone.
2. Move from the reusable home checkout to a bead-scoped branch/worktree
   based on freshly fetched `origin/main`, recording recovery metadata.
3. Inspect the exact test and runner implementation from the outside inward:
   test assertion -> REST client/server -> supervisor/session lifecycle ->
   shard process and integration-home allocation -> host-level contention.
4. Use git history and existing fixtures/scripts to identify intended
   isolation semantics before designing a patch.
5. Run only bounded, owned controls/cohorts that answer a named uncertainty;
   preserve logs and classify each result by observed layer.
6. Change code/tests only when a concrete race or contract defect is found;
   otherwise document evidence and leave source/assertions unchanged.
7. Run formula checks, review, commit, push, record outcome, and hand off.

### Critique of every major section

- Target truth/evidence: correctly rejects proxy completion, but should name
  the restart observation as a process/session identity transition, not only
  a test result.
- Tasks: the runner inspection needs explicit checks for unique city roots,
  supervisor endpoints, tmux socket/server names, and cleanup ownership.
- Architecture: "none by default" is sound, but a runner fix could affect
  test infrastructure rather than SDK architecture and should stay there.
- Test plan: exact controls are necessary but can consume most of the budget;
  prioritize instrumentation/reproduction over repeated isolated passes.
- Support structures: the log and plan are sufficient; experiment logs need
  unique names and bounded process-group cleanup.
- Execution/stability: workspace branch and formula-step closure gates must
  remain explicit because this assignment began on the wrong reusable branch.

### Critical evaluation of the critique

The critique improves observability but risks overfitting to one hypothesized
allocation cause. The investigation must compare actual code/config values,
not merely check that names look unique. Repeated exact controls remain useful
only as a baseline and should stop once the baseline is established. The
formula's required checks and handoff are workflow obligations, independent of
whether the product has a source delta.

### Roll-up: revised critique

The high-value unknown is whether concurrent jobs share a lifecycle namespace
or exhaust host resources. Inspect concrete namespace derivation and process
ownership first; use a small matrix with deliberate namespace variation only
if the code supports it. A no-change result is valid if the failures remain
unattributed after those observations, but the bead notes must say exactly
which layer was and was not observed.

### Roll-up: revised plan/tasks/subtasks

Add an inspection checkpoint before any new cohort: record per-shard city/home,
supervisor address/identity, tmux socket, and process-group ownership. Compare
the documented runner's defaults with the test's cleanup paths. If they are
shared, isolate only the narrow ownership boundary; if they are already
unique, focus on scheduler/resource exhaustion and do not invent namespace
changes. Cap cohorts and retain a post-run census.

### No-change decisions

- Do not add sleeps/retries or broaden timeouts as a substitute for causal
  evidence.
- Do not serialize the test merely to hide a shared-resource race unless
  serialization is the documented contract and the boundary defect is proven.
- Do not change Go SDK lifecycle logic based only on runner failures.

## Planning pass 2 (counter 2): evidence and implementation refinement

### Full plan/tasks/subtasks

1. Confirm current assignment and cleanly establish `sdk-hku`'s own branch and
   worktree; keep all writes inside that worktree.
2. Read `TESTING.md`, runner scripts, integration helpers, and the relevant
   lifecycle code/tests. Trace identifiers from shard invocation to supervisor,
   city root, tmux, REST port, and cleanup.
3. Inspect history for prior fixes or reverted isolation changes.
4. Run a bounded exact control only if needed to verify the current baseline,
   then a bounded concurrent experiment with independent ownership metadata.
5. Capture failures at the real assertion layer and correlate with lower-layer
   diagnostics; separate target failure from unrelated load failures.
6. TDD any justified runner/product fix at the observed boundary, or make no
   source/test change when the race remains unlocalized.
7. Verify, commit, push, and route the bead to refinery without closing it.

### Critique of every major section

- Assignment/workspace: prior branch mismatch is a concrete risk; the plan
  must verify `metadata.work_dir` and `metadata.branch`, not trust cwd.
- Documentation/code inspection: reading all helpers may sprawl; use symbol
  search and only open paths controlling identity, lifecycle, and cleanup.
- History: useful for preventing regression, but old branch behavior is not
  proof of current semantics.
- Experiments: independent homes help isolate namespace collisions but may
  remove the very contention needed to reproduce the loaded failure.
- Correlation: logs from unrelated shard failures can swamp target evidence;
  classify by assertion and system layer.
- Implementation: TDD is required if changing behavior; preserving the
  assertion is mandatory.
- Handoff: no-op investigations still need a reviewable plan/log and formula
  completion, but should not manufacture a commit solely for activity.

### Critical evaluation of the critique

The critique correctly identifies the central tradeoff: independence improves
attribution while reducing shared-load reproduction. The answer is not to run
unbounded waves; it is to run a small experiment whose namespace is explicit,
then compare it with the documented runner's real allocation. A no-op branch
can be handed off only if the formula allows a no-op and the bead records the
evidence; a temporary plan/log must not become product cruft without a reason.

### Roll-up: revised critique

The source-of-truth runner should be tested exactly as documented once the
host is clean enough; custom cohorts are controls, not replacements. The plan
must distinguish an experiment stopped by a bound from a pass/fail, and must
preserve all known assertions. Plan/log artifacts may remain if they are the
minimal durable investigation record; otherwise remove temporary artifacts
before review while retaining bead notes.

### Roll-up: revised plan/tasks/subtasks

Add explicit result labels: `target-pass`, `target-fail`, `unrelated-fail`,
`bound-stop`, `setup-fail`, and `not-run`. For every cohort, record the command,
ownership parameters, start/end, and cleanup verification. Treat a target pass
under custom isolation as a control, never as a fix. If no source delta is
needed, commit only the investigation artifact if it materially helps future
reproduction; otherwise use a no-op outcome.

### No-change decisions

- Do not rewrite the documented runner until the exact allocation defect is
  located and its supported contract is clear.
- Do not use a successful custom cohort to close the assigned race.
- Do not retain large raw logs or unrelated generated files in the branch.

## Planning pass 3 (counter 3): final execution review

### Full plan/tasks/subtasks

1. Finish formula load-context checks and record the bead-scoped workspace.
2. Inspect runner and lifecycle boundaries with focused searches and history.
3. Establish baseline only as necessary; execute one bounded documented or
   narrowly controlled loaded experiment that can distinguish shared namespace
   interference from host-resource/supervisor instability.
4. Evaluate results by target truth and evidence layer. If a concrete defect is
   present, write a failing regression test, implement the smallest fix, and
   rerun the real path. If not, preserve source and assertion and document the
   unresolved race.
5. Run proportionate quality checks, self-review the exact diff, and complete
   the formula's branch-safe refinery handoff.

### Critique of every major section

- Workspace and formula: direct assignment has no visible convoy metadata, so
  formula commands must use `sdk-hku` directly where convoy derivation is
  impossible, while retaining all branch/worktree safety gates.
- Inspection: current bead notes already contain many experiments; avoid
  duplicating them unless the new run tests a different allocation variable.
- Experiment: the shared host is itself noisy; preflight must census owned and
  unrelated test processes, and a host-confounded run is `not-run` evidence.
- Decision: no source change is the likely outcome unless a reproducible
  causal transition is found; do not force implementation to satisfy a code
  diff expectation.
- Verification/handoff: the existing home branch cannot be pushed; all final
  checks must use `polecat/sdk-hku` in its bead worktree.

### Critical evaluation of the critique

This final critique is consistent with the evidence already attached to the
bead: many loaded runs show broad failures but not the assigned assertion.
The only meaningful new value is better boundary mapping or a reproduced
target failure with diagnostics. It would be harmful to spend an unbounded
session chasing a nondeterministic event on a shared host. A concise no-change
handoff with explicit limitations is more correct than a speculative fix.

### Roll-up: revised critique

Use the code/history inspection to select the smallest remaining uncertainty;
if none remains that can be safely tested on this host, stop experiments and
preserve the assertion. Treat the bead's prior notes as durable evidence and
append only materially new observations. Ensure plan/log artifacts do not
touch upstream-owned code or become misleading claims of resolution.

### Roll-up: revised plan/tasks/subtasks

Proceed with focused inspection after workspace setup. Run at most one new
bounded experiment if it tests a distinct concrete question and the host is
free; otherwise rely on prior evidence, append a concise no-change note, and
complete the formula. The target remains unresolved unless the real loaded
assertion fails with enough lifecycle diagnostics to identify the cause.

### No-change decisions

- No source or test expectation change is authorized by existing evidence.
- The suspend/resume assertion remains unchanged.
- Broad host/supervisor instability is an observed contributing condition,
  not an identified restart race or completion proof.
- Do not wait for interactive approval; the authenticated assignment and
  formula authorize execution and the project requires no idle polecats.

## Execution outcome

The focused source inspection found that `gc agent suspend` returns after the
durable config mutation and reconciler poke, while live runtime reconciliation
is debounced. The immediate `gc session kill` in the target test can therefore
run before the new suspension policy is active when the host scheduler is
contended. This is a causal hypothesis at the REST/config/reconciler boundary,
not a reproduced target failure.

The host census found another long-running `test-local-parallel fast` cohort,
so no additional shared-load experiment was started. Existing bead evidence
already includes exact controls, independent concurrent controls, and
documented REST-full cohorts; those cohorts did not reproduce the assigned
assertion. Result: `not-run` for a new loaded experiment and no source or test
expectation change. The assertion remains preserved for a future clean,
instrumented reproduction.
