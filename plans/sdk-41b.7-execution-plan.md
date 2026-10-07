# sdk-41b.7 execution plan

counter: 3

## Scope and acceptance target

Repair the passive quota-path handoff in the existing implementation slice,
without adding lifecycle wiring or new frameworks. Preserve upstream alignment,
existing refs/claims, and foreign work. The required result is a source-only
branch with tests proving:

- resolver workdir equality follows canonical `pathutil.SamePath` semantics;
- root containment remains fail-closed, including symlink escapes;
- all independent guards reject otherwise-valid invalid fixtures;
- guard removal is detected by negative proofs rather than vacuous passing;
- turn-local model and effort state cannot leak from one turn context to the
  next, while supported reasoning-effort fallback remains covered;
- bounded-tail absence/truncation is reported honestly;
- token accounting and literal model/account/provider identity survive the
  handoff.

## Plan, tasks, and subtasks

1. Load exact current source and history.
   - Identify the four-file implementation slice and its existing tests.
   - Compare current `origin/main`, the referenced PR/commits, and relevant
     closed plans without copying unverified behavior.
   - Record baseline failures and current contracts before edits.
2. RED tests first.
   - Add focused resolver, containment, filename/session metadata, workdir,
     SessionID, replay, and per-turn evidence regressions.
   - Add scope-negative proofs that fail when each guard is removed.
   - Run the smallest owning package tests and preserve the expected failures.
3. GREEN implementation.
   - Apply the narrowest source changes at the existing resolver/evidence
     boundaries.
   - Keep provider-free replay fixtures and lifecycle authority unchanged.
4. Review/refactor.
   - Check exact-byte identities, path canonicalization, symlink behavior,
     field ownership, error truthfulness, and no new framework/crypto.
   - Remove duplication and keep tests at their owning layer.
5. Verify and hand off.
   - Run focused replay and scope-negative tests, affected tests, normal gates,
     and vet as practical.
   - Commit cohesive source/test changes, push, verify remote identity, record
     work outcome, and hand off to the refinery.

## Architectural changes

No new architecture is planned. Preserve the existing SDK lifecycle/authority/
delivery boundaries and Wayfinder-advisory role. Keep path checks in the
resolver boundary, evidence extraction in its existing boundary, and turn
state local to `turn_context`. Do not add registry/outbox/event frameworks,
mandatory crypto, lifecycle wiring, provider probes, or account/credential
mutation.

## Test and evidence plan

Target truth: the published quota handoff accepts only an isolated, correctly
bound source fixture and reports evidence whose identity, limits, and turn
state are truthful.

Required evidence layer: provider-free package replay tests plus focused unit
tests for path/guard and turn-state logic; retain one real source-boundary
proof if the current package has one. These observe source parsing, resolver
decisions, guard composition, and evidence state—not live provider behavior,
deployment, or end-user quota policy.

Cheaper but insufficient proxies: `stat`-only checks, a single happy-path
fixture, parent/worker gate success, an unchanged test expectation, or a
synthetic semantic preview. None proves replayed acceptance or independent
guard coverage.

Tempting false completion: making all tests green by weakening fixtures or
expectations; testing only the aggregate guard; claiming tail absence as
eligibility; using a model/effort default that hides cross-turn leakage; or
proving only file existence without replay.

If the plan succeeds but the bug remains, likely causes are a guard still
sharing the wrong canonical path helper, a symlink escape bypass, an untested
guard removal, or evidence state stored outside the turn context. The
scope-negative and replay proofs are specifically intended to expose those.

## Support structures and docs

- Keep `agent-execution.log` temporary and append one line after each completed
  subtask; remove it before final clean-state handoff if it is not a source
  artifact.
- Maintain this plan through three critique/rollup passes before implementation.
- No documentation change is expected unless current source comments or test
  names make the published contract materially misleading; if so, update the
  nearest owning documentation in the same cohesive change.

## Execution order and stability strategy

Use a fresh `origin/main` base and the per-bead worktree. Inspect history before
rebuilding missing behavior. Keep edits additive and localized, avoid broad
formatting, use deterministic `t.TempDir` fixtures, and avoid sleeps/open-coded
polling. Run focused tests after each RED/GREEN boundary, then affected and
normal gates. Preserve unrelated worktree bytes and do not force-push.

## Blocker avoidance and parallel candidates

Independent review candidates after implementation: (a) path/containment
semantics and symlink fixtures, (b) guard-removal negative proofs, (c)
turn-context/token identity semantics. Keep actual source edits serialized
around shared files; parallel review is useful only after the tested patch is
stable. If Dolt or external review is unavailable, use local exact-byte
history and package evidence, then escalate only for a true blocker.

## No-change decisions

- Do not alter lifecycle wiring or provider selection.
- Do not add a new registry, outbox, event framework, crypto requirement, or
  account/provider inference.
- Do not modify unrelated existing plans, refs, claims, or foreign files.
- Do not claim bounded-tail absence/truncation as current eligibility.

## Planning pass 1 — source-grounded refinement

### 1. Full plan/tasks/subtasks

The current base is `faefda46e`, and the prior implementation to repair is
the exact four-file `cfb565658` slice: `internal/sessionlog/codex_quota.go`,
its test, its replay fixture, and its plan. The production parser currently
uses raw WorkDir equality, validates only lexical search-root containment, and
retains effort across turns. The current tests use an invalidly named outside
fixture and only `os.Stat` the replay fixture. The repair therefore needs to
reuse the four-file slice, then add tests that exercise each guard in isolation
before changing the parser.

Concrete order: cherry-pick the known four-file implementation; add RED tests
for SamePath/trailing-slash/symlink equivalence, valid-name outside-root,
wrong filename session, missing session metadata, WorkDir mismatch, invalid
SessionID, supported reasoning-effort fallback, per-turn effort reset, replay
execution, and bounded-tail truthfulness; run them and capture the expected
failures; make the smallest production changes; run focused tests and mutation
or equivalent source-negative proofs; then run affected/normal gates.

### 2. Critique of every major section, top-to-bottom

- Scope is concrete but must distinguish parser-layer truth from live Codex
  provider truth; no live inference is authorized.
- Tasks correctly put tests before source edits, but cherry-picking the prior
  implementation must be treated as baseline provenance rather than a new
  implementation action.
- Architecture preserves the existing boundary, but root containment must be
  upgraded at the shared `validateSearchPathFile` boundary only if that change
  does not broaden unrelated providers; otherwise keep the quota boundary
  local. Current acceptance explicitly requires this parser's search-root
  symlink behavior, so tests must expose the chosen ownership.
- Evidence plan names the right layer and false proxies; it must assert that
  replay calls extraction, not merely reads fixture bytes.
- Support/log plan needs a precise cleanup rule so the temporary log cannot
  accidentally become a source artifact.
- Execution order must preserve exact reviewed identities and avoid rebasing
  or force-pushing the source branch.
- Parallel candidates are useful only for review; shared-source edits stay
  serialized.

### 3. Critical evaluation of that critique

The most important risk is not just raw WorkDir equality: `validateSearchPathFile`
currently allows a symlinked path outside the lexical root, so proving
`SamePath` alone does not establish containment. The correct fix belongs in the
shared path validation because all session-log callers rely on fail-closed
root checks, but the change must be covered by existing path tests and quota
tests. The filename/session and session_meta guards must be tested with all
other inputs valid, otherwise mutation survival remains possible. Turn state
must be reset explicitly on every `turn_context`, including empty fields.
Tail truncation must be observed and described, not promoted into an eligibility
decision. The plan also needs exact identities for model/account/provider and
token accounting; this task must not infer or rewrite them.

### 4. Roll up: apply evaluation to the critique

Add shared `validateSearchPathFile` symlink containment tests if the existing
function owns the defect, and keep the quota API test as the end-to-end parser
proof. Add a `turn_context` fixture where model changes and effort is absent,
then assert model changes and effort clears; separately assert
`reasoning_effort` fallback. Add explicit fixture assertions for provider,
account/scope, observed timestamp, and nullable window fields. Treat the
`readTailWindow` truncation boolean as the only available bounded-window fact;
if exposing it would change the public API beyond scope, record the limitation
truthfully in tests/comments rather than fabricate eligibility.

### 5. Roll up: revised plan/tasks/subtasks

1. Cherry-pick `cfb565658` as the known baseline and verify its four files.
2. Add tests first for each independent guard and turn transition; execute RED.
3. Fix canonical WorkDir comparison, fail-closed symlink containment, and
   per-turn effort reset; preserve supported fallback and identities.
4. Run provider-free replay plus focused package tests, inspect mutation or
   source-negative outcomes, then affected tests and ordinary quality gates.
5. Commit only the repair slice and temporary evidence cleanup; push and hand
   off with verified remote identity.

### 6. Explicit no-change decisions

No production caller, quota policy, retry logic, lifecycle wiring, account
resolution, or provider probe will be added. The prior commit's established
nullable model and token-count parsing remain unchanged unless a test proves
otherwise.

## Planning pass 2 — boundary and evidence challenge

### 1. Full plan/tasks/subtasks

The owning source boundary is `internal/sessionlog`: quota extraction must
consume only a caller-resolved file, `validateSearchPathFile` must reject paths
outside physical roots, and `extractCodexTailQuota` must emit descriptive
snapshots. Tests will construct real temporary roots and symlinks, write valid
Codex JSONL, and execute the extractor. Negative proofs will mutate or disable
one guard at a time and must fail; if an automated mutation tool is unavailable,
equivalent table-driven tests will make each guard's fixture independently
valid except for the targeted violation and source review will record the
negative result.

### 2. Critique of every major section, top-to-bottom

- The scope now names exact files, but shared tail validation is a cross-file
  surface and needs regression coverage beyond quota extraction.
- The task sequence is correct, though cherry-pick plus added tests can leave
  an intermediate commit; final history should remain cohesive and explain the
  repaired handoff.
- The architectural boundary is appropriate, but changing shared validation
  can affect Claude/other providers; run their focused path tests.
- The test plan proves replay and guard isolation, but it must cover filename
  session mismatch and missing session metadata with an otherwise valid path and
  valid content, not merely expected errors.
- Tail absence/truncation remains an information limit, not a parser failure;
  tests must avoid asserting a value unavailable from the bounded window.
- Parallel review must inspect semantic parity and resource policy separately.

### 3. Critical evaluation of that critique

The cross-provider impact is real: `validateSearchPathFile` is shared by the
generic and Codex extraction paths. A physical containment change is a security
boundary improvement, but it may reject previously accepted symlinked roots;
that is intended fail-closed behavior and needs explicit pathutil semantics.
The prior implementation's use of `mergeCodexSearchPaths` also means tests must
avoid accidental default-home roots and must use absolute temp paths. The replay
test should use the same public extractor as production would, with valid
context, so it catches parser wiring rather than fixture existence.

### 4. Roll up: apply evaluation to the critique

Add a focused `validateSearchPathFile` test in `tail_test.go` (or the nearest
existing owner) for a symlinked root/path escape and preserve lexical sibling
rejection. Add quota tests for all six independent guards and ensure the valid
outside-root filename is named with the expected rollout grammar. Run
`go test ./internal/sessionlog` after the parser fix and include any shared-path
regression tests in the affected set.

### 5. Roll up: revised plan/tasks/subtasks

1. Baseline the old slice and inspect shared path/tail owners.
2. RED: add independent guard tests, same-path tests, per-turn tests, replay
   execution, and shared containment tests.
3. GREEN: use `pathutil.SamePath` for WorkDir, resolve roots/candidate before
   `Rel` and reject physical escapes, reset model/effort per turn.
4. Verify semantic limits (tail boolean, nullable fields, identity/account
   preservation), run source-negative proofs, format, affected tests, vet.
5. Record exact test evidence and hand off the clean per-bead branch.

### 6. Explicit no-change decisions

Do not expose new bounded-tail metadata or add a policy decision based on
absence. Do not change search-path discovery, session filename grammar, token
accounting, or the public context type beyond what tests require.

## Planning pass 3 — final implementation and handoff audit

### 1. Full plan/tasks/subtasks

Implement only the reviewed repair on `polecat/sdk-41b.7`: add/adjust focused
tests first and observe RED; apply path and turn fixes; run package replay and
guard-negative evidence; run the rig's affected test command if available,
`go vet` and the documented fast baseline as time permits; inspect diff for
foreign bytes; commit, push, verify `origin/polecat/sdk-41b.7`, record metadata,
and reassign to refinery without closing the work bead.

### 2. Critique of every major section, top-to-bottom

- Scope is now limited to source/test repair and does not accidentally promise
  production quota recovery.
- Tasks include RED/GREEN and evidence, but exact test names and changed files
  must be captured in the execution log and final handoff.
- Architecture is stable and shared-path ownership is explicit.
- Evidence covers parser and filesystem boundary truth; it does not prove live
  provider emission, so final reporting must say so.
- Support artifacts are temporary and must be removed before the clean-state
  guard unless intentionally committed as a plan artifact.
- Handoff follows the branch contract and refinery ownership rules.

### 3. Critical evaluation of that critique

The only remaining ambiguity is whether the task expects a standalone repair
commit or a branch containing the published implementation plus repair. The
bead explicitly says repair the published PR66 handoff and reuse its four-file
implementation, so retaining the baseline commit and adding a repair commit is
the traceable choice. The plan itself is an execution artifact but should not
be included unless repository policy expects it; because this task requires a
plan file and the prior slice includes a plan, keep the new plan in the branch.
The temporary execution log should be removed before final commit.

### 4. Roll up: apply evaluation to the critique

Use two commits if needed: the exact prior four-file baseline, then one
repair-focused commit containing tests, source, and the required plan. Do not
rewrite the baseline. Final report will distinguish parser-layer evidence from
live-provider semantics and note any unavailable full gate.

### 5. Roll up: revised plan/tasks/subtasks

Proceed with the five-step implementation/handoff sequence above, with the
additional hard checks that each guard test is independently valid, replay
executes extraction, and no temporary log remains in the final tree.

### 6. Explicit no-change decisions

No lifecycle wiring, no production caller, no recovery policy, no live probe,
no broad refactor, and no changes to unrelated existing untracked files.

## Three-pass loss check

No required information was lost: the final plan retains the original
acceptance requirements, exact reviewed source provenance, architectural
boundary, proxy audit, TDD order, cross-provider risk, tail limitation, no-
change decisions, and refinery handoff contract. The only deliberate narrowing
is to keep bounded-tail absence descriptive rather than treating it as
eligibility.
