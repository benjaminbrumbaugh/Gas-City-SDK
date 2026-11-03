# sdk-h0s.6 convergence plan

counter: 3

## Initial full plan

### Target truth

Produce one exact, reviewable branch from current `origin/main` that contains
the still-missing Mayor communication candidate implementations, preserves
the existing `.5` lifecycle-callback bytes and all foreign/human fences, and
proves the real root CLI/API/durable-store boundaries. The target is source
readiness evidence, not deployment, callback submission, or hosted-service
authorization.

### Tasks and subtasks

1. Establish ownership and inventory.
   - Confirm current branch/worktree, clean state, and `origin/main`.
   - Read `sdk-h0s.6`, `gc-0s4t4`, repository instructions, API/Huma docs, and
     `TESTING.md`.
   - Inventory candidate refs `.1`, `.2`, `.3`, `.4`, `.5`, `.9`, and `.10`.
   - Classify already-landed bytes, remaining deltas, conflicts, and preserved
     fences without modifying foreign branches or city beads.
2. Add a failing integrated boundary regression before the fix.
   - Exercise the target layer that proves the root CLI/API registration and
     durable external-coordination path, not a mocked producer JSON shortcut.
   - Capture the baseline failure and name its observed layer and limits.
3. Integrate the smallest proven candidate slices.
   - Retain current-main `.5` and `.4` content by ancestry/content evidence.
   - Apply `.1` contract documentation, `.2` CLI/API projection, `.3`
     coordination claim/recovery, `.9` convoy subscription operations, and
     `.10` response composition/repair in dependency order.
   - Resolve conflicts semantically; do not union incompatible schemas or
     overwrite newer main changes.
4. Verify and refine.
   - Run focused package tests, root CLI/provider-free integration tests,
     targeted race tests, `go vet`, generated schema/client checks, and the
     applicable fast/broad sharded suites.
   - Regenerate only through established commands when API types change.
   - Inspect the final diff, provenance, exact SHA, and task-created residue.
5. Handoff.
   - Commit a cohesive branch with the plan and implementation evidence.
   - Push `polecat/sdk-h0s.6`, verify the remote SHA exactly, record metadata,
     and reassign to the refinery without closing the implementation bead.

### Architectural changes

- Keep contract prose in `engdocs/plans/` or the candidate-owned plan paths.
- Keep coordination state/recovery in `internal/externalcoordination` and its
  existing beads/extmsg adapters.
- Keep user-facing reply/transcript behavior in the `cmd/gc` and API projection
  layers; do not duplicate domain logic in either surface.
- Keep convoy subscription operation semantics in `internal/convoysubscription`
  and its existing convoy boundary.
- Keep response composition additive and typed; no arbitrary callback URL,
  callback endpoint, credential, or private-provider assumption.

### Test and evidence plan

- Baseline RED test: prove the missing integrated behavior on current main.
- Focused GREEN tests: candidate-owned unit and coordination tests, including
  concurrent claims, recovery, truthful failure/expiry, route/owner fencing,
  CLI attribution and ambiguity, subscription terminal-state recheck, and
  response handoff replay.
- Boundary evidence: run actual root CLI/API registration against durable test
  stores or provider-free adapters; do not treat mocked JSON as root proof.
- Generated evidence: `make spec-ci` and `make dashboard-ci` when API/schema
  surfaces are touched.
- Repository evidence: affected tests, `make test-fast-parallel`, targeted
  race packages, `go vet ./...`, `git diff --check`, and pre-commit/pre-push
  gates as available.

For every evidence artifact record: the truth it proves, the system layer it
observes, and what it does not prove. Passing tests do not certify live
external delivery, origin acknowledgment, deployment, or authorization.

### Support structures

- This plan is the durable convergence/evidence artifact.
- Use existing candidate tests and established generators; add only the one
  integrated regression needed to expose the pre-fix boundary gap.
- Use git refs, `git diff`, ancestry checks, and exact SHA records as
  provenance, not as behavioral substitutes.

### Documentation and integration plan

- Preserve and include `.1`'s response-to-origin contract if it remains the
  canonical contract for the implemented paths.
- Preserve `.9`'s subscription-operation design and `.10`'s repair plan only
  when their implemented behavior remains accurate after integration.
- Update docs only for the final current behavior; do not copy historical
  status claims or readiness assertions into source documentation.

### Execution order

1. Worktree/branch and assignment verification.
2. Read architecture/testing/coordination context and inventory refs.
3. Write and run the RED integrated regression.
4. Apply `.1`, `.2`, `.3`, `.9`, `.10` candidate commits with focused checks
   after each semantic group; retain current-main `.4`/`.5`.
5. Resolve generated artifacts, run focused and broad checks, review exact diff.
6. Commit, push, verify, update metadata, hand off to refinery, drain.

### Stability strategy

- Work only in the bead-scoped branch/worktree.
- Never mutate or force-push candidate branches, old worktrees, city/root
  checkouts, held beads, or callback routes.
- Keep each candidate application independently identifiable and use commits
  that can be reverted without losing unrelated main changes.
- Treat test failures as defects until proven pre-existing with package/layer
  evidence; do not weaken expectations.

### Blocker avoidance

- Use `gc bd` for the city coordination bead; do not use a failing direct
  project-store path or repair/reinitialize Dolt.
- If a candidate cannot be safely reconciled, record the exact file/semantic
  boundary and preserve it for the owner rather than guessing.
- Do not run deployment, installation, service restart, live requests,
  credentials, or private Hermes-state inspection.

### Candidate parallel work

Independent read-only inventory of each candidate and package-level test
mapping can be done in parallel by separate reviewers. The shared integration
branch, generated artifacts, final root checks, and handoff remain serial and
owned by this task. No subagent capability is available in this session, so
the read-only inventory is performed directly and recorded here.

### Proxy audit

- Target truth: all intended candidate implementations coexist at the current
  root/API/domain boundaries and preserve ownership/fencing semantics.
- Required evidence layer: real root CLI/API registration plus durable-store
  and adapter-boundary tests, complemented by focused unit/race tests.
- Useful but insufficient proxies: candidate ancestry, clean diffs, PRs,
  package-only tests, generated-spec equality, or callback admission.
- Tempting false completion: treating a cherry-pick, passing fixture test, or
  queued external request as proof of correct end-to-end behavior.
- If the plan succeeds but the original bug remains, it would most likely be
  due to a root-construction omission, a generated contract mismatch, a
  cross-conversation ownership leak, or a durable replay path not exercised by
  the proxy tests.

## Planning pass 1: critique of the initial plan

### Critique, top to bottom

- Target truth is separated from deployment, but it needs an explicit
  candidate-to-contract matrix so no candidate is silently dropped.
- Tasks are complete at workflow level, but the RED test target is too broad;
  it must name the first missing root behavior and remain merge-stable.
- Architectural boundaries are correct, but `.1` is documentation-only and
  `.10` may overlap `.4`; provenance and semantic ownership need a conflict
  rule.
- The test plan names the right layers, but it must distinguish generated
  contract equality from behavior and require causal replay/ownership proofs.
- Support structures are intentionally small, but the final plan must include
  an evidence ledger rather than relying on terminal output.
- Documentation scope is sound, but historical plans must not be copied merely
  because a candidate contains them.
- Execution order needs candidate dependency ordering and a stop condition for
  a held or unavailable candidate.
- Stability and blocker avoidance should explicitly ban broad cleanup and
  mutation of preserved refs.
- Parallel work is bounded, but the inventory must be mechanically reproducible
  with git commands because no subagent is available here.
- Proxy audit should explicitly reject callback-admission/queued-state as
  completion evidence.

### Critical evaluation of the critique

The critique identifies the main integration risk: a clean cherry-pick can
still leave an incomplete root. A candidate matrix, owned RED test, and
evidence ledger improve reviewability without adding production abstractions.
The stop condition must not turn historical fences into a new blocker: only a
current source absence or an unresolvable semantic conflict stops safe work.
Candidates `.4` and `.5` are already present by exact content/ancestry and must
be retained, not reapplied.

### Roll-up: apply evaluation to the critique

Add a matrix keyed by candidate, owned surface, expected files, and proof.
Define the RED test as an integrated root command/API registration assertion
that fails on current main. Add an evidence table with truth/layer/limits.
Apply commits in dependency order, stopping only on an unresolvable semantic
conflict or missing required candidate bytes; preserve all other candidates and
record the boundary.

### Revised plan and tasks/subtasks

1. Inventory exact refs and classify `.1` contract, `.2` CLI/API, `.3`
   coordination recovery, retained `.4`/`.5`, `.9` subscription operations,
   and `.10` response composition/repair.
2. Add one RED root-boundary regression whose failure is observable on current
   main and whose fix is expected from the candidate set.
3. Integrate `.1`, `.2`, `.3`, `.9`, and `.10`; resolve only semantic
   conflicts in this branch and retain commit provenance.
4. Run focused, root/API/provider-free, race, generated, vet, fast-sharded,
   and final diff checks.
5. Commit/push the exact candidate and hand off; do not close the work bead.

### Explicit no-change decisions

- Do not reapply `.4` or `.5`; current main contains their implementation
  content and reapplying them would create duplicate/conflicting history.
- Do not mutate `gc-0s4t4`, held beads, candidate refs, or other owners.
- Do not add a general callback/transport abstraction or new harness.
- Do not claim live external receipt, origin application, rebuild, or deploy.

### Proxy audit for pass 1

- Target truth: one integrated root with all required candidate behavior and
  ownership fences intact.
- Required layer: root CLI/API and actual durable coordination adapters, plus
  package tests for branch matrices.
- Cheaper proxies: commit ancestry, patch stats, generated specs, and mocks;
  useful for inventory only.
- False completion: a cherry-pick or queued callback request.
- Remaining-bug scenario: symbols compile but root command/API registration is
  omitted, leaving the user-facing path broken.

## Planning pass 2: critique of the revised plan

### Full plan/tasks/subtasks

The revised plan keeps the serial integration owner, applies only missing
candidate slices, and verifies both package semantics and root projections.
The next pass will add exact candidate parent/base checks, an explicit RED
test location, and a generated-artifact ownership rule before implementation.

### Critique, top to bottom

- The candidate matrix lacks exact parent/base SHAs, so a reviewer cannot tell
  whether a patch was replayed from the intended historical bytes.
- The RED test is still underspecified: a test can pass at package level while
  missing root command registration. It needs a concrete package and assertion.
- The integration order may be wrong for `.10`, which depends on the existing
  `.4` handoff and should follow `.3` semantics, while `.9` is independent.
- Generated artifacts are named but ownership is not: API source, OpenAPI,
  generated client, dashboard types, and metrics census must move together.
- The evidence ledger needs explicit results for baseline failure, each
  candidate application, focused tests, and final limits.
- The plan does not yet say how to handle a pre-existing test failure without
  weakening the new contract.

### Critical evaluation of the critique

These are correctness and provenance gaps, not reasons to broaden the design.
Exact parent/base checks are cheap and protect against applying a similarly
named but wrong branch. A root CLI test should assert command discovery and
the real adapter call boundary; it can remain small and deterministic. `.9`
can be applied before `.10`, while `.10` must be reviewed against current `.4`
and `.3` behavior. Generated outputs are repository-owned consequences of API
source changes and must be regenerated once after all API edits. Pre-existing
failures should be recorded with package/layer evidence and escalated only if
they block the required gates.

### Roll-up: apply evaluation to the critique

Extend the matrix with candidate SHA, parent/base, touched surfaces, and
verification. Make the RED proof a root `cmd/gc` test that discovers the
conversation command and exercises an injected recording API adapter, then
record that it does not prove a live provider. Apply `.1`, `.2`, `.3`, `.9`,
then `.10`; regenerate API artifacts once after source convergence. Require a
result table with command, layer, pass/fail, and limits.

### Revised plan and tasks/subtasks

1. Before edits, record exact candidate heads and parent/base ancestry; verify
   `.4`/`.5` content on current main and preserve all foreign refs.
2. Add a RED test in the smallest owning `cmd/gc`/API test package that fails
   because current main lacks the conversation projection; use recording
   collaborators rather than hand-built JSON or a live provider.
3. Apply candidate groups in this order:
   - `.1` contract docs;
   - `.2` conversation CLI/API projection and its typed generated impacts;
   - `.3` durable coordination claims/recovery;
   - `.9` subscription operations;
   - `.10` response composition/replay repair, reviewed against `.3`/`.4`.
4. Run focused tests after each group, then regenerate and verify all affected
   specs/client/metrics artifacts once.
5. Run root provider-free integration, targeted races, vet, affected/full
   sharded tests, inspect diff/residue, and record evidence limits.
6. Commit, push, verify exact remote head, update bead metadata, and hand off.

### Explicit no-change decisions

- Do not manufacture a live Hermes route or submit the readiness callback.
- Do not introduce a new test harness when existing command/API test seams can
  observe the required root behavior.
- Do not regenerate artifacts per commit; one final generation avoids churn
  while preserving the source-generated contract.
- Do not treat a pre-existing failure as permission to alter expectations;
  record it and file/escalate only if it blocks handoff.

### Proxy audit for pass 2

- Target truth: the root command/API projection reaches the correct typed
  domain/adaptor boundary and the durable coordination paths preserve fences.
- Required evidence layer: `cmd/gc` root tests plus API registration and
  durable-store adapter tests; unit tests cover branch decisions.
- Cheaper proxies: command source presence, generated OpenAPI equality, and
  candidate ancestry; none proves invocation or persistence.
- False completion: a package test or spec regeneration standing in for root
  execution, or a provider admission standing in for delivery.
- Remaining-bug scenario: the command is present but wired to stale selection,
  wrong conversation attribution, or a non-durable/mock-only path.

## Planning pass 3: critique of the second revision

### Full plan/tasks/subtasks

The plan now has exact-ref provenance, an owning RED boundary, dependency
ordered candidate application, one generated-artifact pass, and an evidence
ledger. Implementation remains additive and serial on this branch.

### Critique, top to bottom

- Target truth is clear, but the candidate matrix still needs to be populated
  with observed current values rather than remaining abstract.
- The RED test may not be independently valuable if candidate `.2` already
  supplies equivalent tests; it must assert integrated behavior not covered by
  those tests and remain a narrow regression.
- The architecture correctly separates projections and domain, but applying
  historical commits may import stale documentation or metrics snapshots.
  Review each non-source file against current main before retaining it.
- The test plan is strong, but the final gate must include race tests for the
  actual changed packages and a diff check for generated/schema drift.
- The evidence ledger must record what was not run when environment limits
  prevent a full gate, without claiming completion.
- Handoff is correct, but branch metadata and remote SHA verification need to
  occur before any local branch cleanup.

### Critical evaluation of the critique

The remaining issues are execution details that can be handled without design
expansion. Populating the matrix from read-only git output makes provenance
auditable. The RED test should cover root command registration plus one typed
adapter call, not duplicate `.2`'s selection matrix. Historical docs/metrics
must be kept only when generated or still normatively accurate. Race and diff
checks are proportionate to the concurrency/durable-state risks. Every
unavailable gate is a stated verification limit, not a pass. Handoff ordering
already follows the refinery contract and must be preserved exactly.

### Roll-up: apply evaluation to the critique

Populate the final plan's evidence section during execution with exact refs,
test outputs, and limits. Add a review checkpoint for every non-source file.
Run changed-package races and `git diff --check`; after all checks, update
metadata, push/verify, and only then reassign/drain.

### Revised plan and tasks/subtasks

1. Record the observed candidate matrix and current-main retained-content
   proofs in this plan before implementation.
2. Add/run the narrow root regression; preserve its RED output in the evidence
   ledger, then apply candidate commits and resolve conflicts semantically.
3. Review each docs/generated/metrics file for current ownership and source of
   truth; remove stale historical output only if it is task-owned and safe,
   otherwise preserve and record the boundary.
4. Run focused and changed-package race tests, root provider-free checks,
   `go vet ./...`, generated checks, sharded fast coverage, and `git diff
   --check`; record truth/layer/limits.
5. Commit and run the pre-push gate; push and verify exact remote SHA before
   recording `gc.work_*`, target, and refinery assignment.

### Explicit no-change decisions

- No deployment, service restart, live external request, callback submission,
  credential handling, private runtime inspection, or broad cleanup.
- No edits to foreign candidate branches, old worktrees, held beads, or the
  city coordination bead.
- No test expectation changes unless a current contract is independently
  proven wrong; candidate integration alone is not a contract change.
- No assertion that source evidence equals live external delivery or origin
  acknowledgment.

### Proxy audit for pass 3

- Target truth: exact current branch bytes implement the candidate contract at
  root CLI/API, durable state, adapter, and concurrency boundaries.
- Required evidence layer: changed-package tests/races plus root provider-free
  execution and generated contract checks; final git provenance proves which
  bytes were reviewed.
- Cheaper proxies: clean tree, pushed branch, PR existence, passing unit tests,
  or callback queue state; these are necessary workflow facts only.
- False completion: declaring readiness because the branch is pushed or a
  callback is admitted.
- Remaining-bug scenario: all local tests pass while an untested root wiring
  path, stale generated client, or live-provider route/ack semantics remains
  wrong.

## Lost-information check after three passes

- Preserved: exact target truth, candidate ownership, `.4`/`.5` retained-byte
  rule, TDD RED/GREEN order, root/durable evidence boundary, security fences,
  generated-artifact ownership, race/vet/sharded gates, and refinery handoff.
- Added: candidate provenance matrix, concrete root regression requirement,
  stale historical-artifact review, explicit verification limits, and push
  ordering.
- Intentionally unchanged: no new production abstraction, no live callback or
  deployment activity, no foreign cleanup, and no work-bead closure.

## Execution evidence ledger

This section is populated during execution. Each row must state the truth it
proves, observed layer, and what it does not prove.

| Evidence | Result | Truth proved | Layer observed | Limits |
| --- | --- | --- | --- | --- |
| Candidate inventory | pass | exact heads/parents and retained `.4`/`.5` bytes are recorded | git refs/history | no behavior |
| RED regression | fail as intended | root cannot find `extmsg reply` on current main | root CLI command tree | CGO-disabled build; no live provider |
| Focused tests | pending | pending | changed packages | no deployment |
| Race tests | pending | pending | concurrent durable paths | no external receipt |
| Generated checks | pending | pending | API/schema/client artifacts | no runtime invocation |
| Broad gates | pending | pending | repository policy | environment-dependent |
| Final provenance | pending | pending | git/refinery handoff | no merge until refinery |

### Observed candidate matrix (pre-implementation)

| Candidate | Exact head | Parent/base evidence | Owned surface | Current-main status |
| --- | --- | --- | --- | --- |
| `.1` | `ba381178d273766a49564060c8ccf9ddb33ad35b` | parent `e1d820ab4`; old fork | response-origin contract | absent from current main |
| `.2` | `35f96e64f3d2ec932806f65997f1540d82bd3be9` | parent `e1d820ab4`; old fork | conversation CLI/API | absent from current main |
| `.3` | `0d38d4534e2a4639c413678cb911ad1bab9c029c` | parent `e1d820ab4`; old fork | claim/recovery adapters | absent from current main |
| `.4` | `ce999b1e45645dd4daf71b2f7f8cf4a82d8234b1` | parent `e1d820ab4`; ancestor | response-to-origin delivery | retained on current main |
| `.5` | `7cdf68104d83e84ee9344ab7339e1d04d9e2b7d3` | same content as main `3a2df8ab3` | lifecycle callback delivery | retained by exact content |
| `.9` | `33218fcb09c272f8ea2521181182ff1246cfa82d` | `2c786ad4`; base `c5411795` | subscription operations | absent from current main |
| `.10` | `dde6cf5275410a0eb316df803042bf99c1d5d0e9` | `d7992834a`; base `c5411795` | response composition/replay repair | absent from current main |

The matrix is inventory evidence only. It proves ref identity and retained
content, not semantic correctness, root wiring, generated-contract health, or
live external delivery.
