# sdk-bcy.3 plan

counter: 3
status: implementation-complete-pending-handoff
base: origin/main @ faefda46ea6ab3563cc3f9ad9ee3b02e1824c54d
worktree: /Users/benjaminbrumbaugh/Documents/Gas City/Gas-City/.gc/worktrees/Gas-City-SDK/work/sdk-bcy.3

## Scope and evidence

The source-only delivery task repairs the current immutable routing-delivery
candidate while preserving the existing lifecycle, authority, exact-byte, and
legacy interfaces. The independent review `gc-wisp-ikyfy` is evidence of the
candidate behavior, not product acceptance: it observes the routingdecision
domain and API/CLI source on exact candidate SHAs, and does not prove the
current-base implementation or live deployment behavior.

Target truth:

- Lifecycle transitions and launch receipts remain durable when optional
  producer outcome projection is invalid; required lifecycle persistence is
  never silently dropped and producer identity validation is not relaxed.
- Expiry/revocation continue for valid decisions when another decision has an
  invalid delivery projection; errors remain explicit and causal.
- A read-only `ExecutionSessionBound` probe accepts a pre-delivery legacy
  ledger without mutating it or requiring optional buckets, while still
  failing closed for actual corruption and bound-session evidence.
- Existing decisions remain usable after upgrade, including record/revoke/
  expire paths, and durable delivery remains truthful: pending/ack state,
  exact bytes, retries/unknown outcomes, and no duplicate authoritative
  outbox.
- CLI/service/API boundaries expose the documented 400/404/409/503 outcomes,
  including non-success ack paths, rather than proving only a fake 200 client.

Required evidence layers:

- `internal/routingdecision` unit tests for transaction ownership, expiry
  isolation, receipt persistence, legacy bucket compatibility, corruption,
  exact bytes, and replay/purge semantics.
- `cmd/gc` and `internal/api` boundary tests for CLI/service/controller
  mapping and real response status behavior.
- Existing provider-free and relevant integration/convergence gates for the
  current base; these prove composition/wiring, not the lower-level semantic
  branches by themselves.

Cheaper but insufficient proxies include a green candidate-only matrix, a
  fake 200 HTTP client, a writable open that self-heals the ledger, or a clean
  compile. The tempting false completion is to move all validation to creation
  or to ignore delivery errors; both would hide the ownership bug and violate
  legacy and truthful-delivery contracts. If this plan fully succeeds yet the
  original bug remains, the likely causes are a missed launch-receipt path, a
  real handler status mapping not covered by the fake client, or a probe test
  that accidentally mutates/normalizes the legacy ledger before reading it.

## Pass 1 — initial plan, tasks, and critique

### Full plan / tasks / subtasks

1. Establish the current-base baseline and source ownership.
   - Confirm `origin/main`, worktree/branch metadata, clean baseline, and
     current test/lint environment.
   - Read the exact current implementations in `store.go`, `operations.go`,
     `delivery.go`, `execution_receipt.go`, `execution_session_probe.go`,
     outcome projection files, and their existing tests.
   - Map API/service/controller/CLI delivery entry points and generated-wire
     implications before editing.
2. Write proving RED regressions first.
   - Valid `DecisionPayload` with `work_bead_id=sk-1234` and target
     `demo/code reviewer` must still persist lifecycle state.
   - Poison plus later-valid expiry must expire the valid record and return a
     bounded, explicit poison error rather than wedge the sweep.
   - Revoke and launch receipt persistence must survive a delivery projection
     validation failure without duplicate authoritative delivery.
   - Read-only legacy ledger probe must accept only the historical bucket set,
     not create missing delivery buckets, while genuine corruption and an
     actual bound session remain fail-closed.
   - Add the smallest real CLI/service/API cases for delivery pending/ack and
     ack statuses 400/404/409/503 promised by the current contract.
3. Implement the minimal ownership repair.
   - Keep producer construction outside generic lifecycle `Transition` as the
     canonical design requires.
   - Separate durable lifecycle commit from optional delivery projection;
     report projection failure explicitly with causal state that permits a
     truthful retry/unknown path.
   - Make `ExpireDue` isolate per-record delivery errors while preserving
     expiry/revocation of unrelated valid decisions.
   - Make launch receipt recording persist its causal receipt independently of
     projection validation, without fabricating outcomes or writing a second
     authoritative outbox.
   - Treat delivery buckets as optional for read-only legacy probes; preserve
     fail-closed checks for malformed legacy buckets, invalid data, and bound
     session evidence.
4. Resolve historical/restart semantics without reconstruction from current
   state.
   - Preserve exact-byte delivery replay and distinguish purged decisions from
     missing launch history.
   - Document or minimally correct empty pending pages/cursor restart and
     retention behavior only where current consumer contract is violated.
5. Verify and hand off.
   - Run focused RED/GREEN tests, affected tests, required full/sharded gates,
     `go vet`, and source-format checks with the documented ICU environment.
   - Independently re-review exact changed bytes in another model family or
     authenticated review lane if available; record what that evidence does
     and does not prove.
   - Commit, push `polecat/sdk-bcy.3`, record work outcome/metadata, and route
     the open implementation bead to the refinery without closing it.

### Architectural changes

- Preserve `internal/routingdecision` as the owner of lifecycle, authority,
  delivery, and durable launch facts; do not add a new registry, outbox, event
  framework, or mandatory crypto layer.
- Keep producer/runtime construction at the controller edge. Generic domain
  transition code records its own durable facts and does not make producer
  validation a prerequisite for those facts.
- Model missing delivery buckets as an upgrade-compatible optional projection
  for read-only probes; do not have a read-only function repair stores.
- Keep API and CLI as projections over routingdecision contracts. Modify wire
  specs/generated files only if an actual contract change requires it; avoid
  dashboard/product UI changes.

### Test plan

- First run the baseline focused suites on exact `origin/main` before adding
  tests; capture failures separately from candidate regressions.
- Add tests beside the owning domain code and observe each new regression fail
  before implementation.
- Cover successful lifecycle persistence, poison isolation, revocation,
  launch receipts, legacy/no-authority/disabled-local-lane upgrades,
  corruption and bound-session fail-closed behavior, replay/purge facts, and
  exact pending/ack bytes.
- Add one focused real API/CLI boundary proof for status/error mapping and
  retain lower-level tests for all branch details.
- Run affected tests, then relevant process/integration shards; no sleeps or
  open-coded polling in new tests.

### Support structures

- Keep this plan as the decision record; do not create a second ad hoc TODO
  list.
- Maintain temporary `agent-execution.log` in the task worktree, appending one
  concise line after each completed formula subtask.
- Use exact candidate/base SHAs and `git diff`/`git show` for archaeology;
  preserve unrelated worktree files and foreign branches.

### Docs

- Update only the narrow routing-delivery design/API documentation needed to
  explain lifecycle ownership, legacy probe behavior, and pending cursor/
  retention semantics. Do not use historical plans as authoritative code
  documentation.
- If the existing spec already states the intended statuses, align code/tests
  to it rather than changing the public contract. Regenerate typed artifacts
  only when a wire schema changes.

### Execution order

Baseline/source map → RED tests → minimal domain repair → boundary tests and
any narrow docs → focused GREEN → affected and broader gates → independent
exact-byte review → commit/push/refinery handoff.

### Stability strategy

- Use `t.TempDir`, existing store fixtures, injected clocks/helpers, and
  bounded context-aware waits. Do not restart services, deploy, mutate live
  settings, or invoke providers.
- Keep changes additive/minimal to preserve upstream mergeability and avoid
  unrelated scale refactors.
- Treat durable failure as a first-class observable: no swallowed errors,
  fabricated outcomes, or silent drops of required persistence.

### Blocker avoidance

- No live City or provider work is needed; all source and tests stay in this
  task worktree.
- If Dolt is slow, collect the mandated diagnostics before escalation; do not
  restart it. If exact review infrastructure is unavailable, record that
  limitation and rely on local evidence without claiming independent review.
- If a boundary is unclear after source/history inspection, escalate once to
  the Witness with the exact question and continue with non-blocked work.

### Candidate subagent-parallel work

- Parallelizable read-only work: one reviewer maps domain transaction paths;
  another maps API/CLI status paths; another checks historical commits and
  docs for prior implementations. All results must be reconciled by the main
  agent before edits.
- Not parallelized here unless an authenticated subagent lane is available:
  RED test design and production ownership changes must remain coordinated to
  prevent conflicting assertions or duplicate delivery mechanisms.

### Pass 1 critique, top to bottom

- Scope/evidence is appropriately explicit about target truth and proxy
  limits, but the exact durable error shape is not yet selected.
- Task 1 is complete in intent, but the baseline command and exact file map
  need to be recorded after inspection.
- Task 2 covers the review's core regressions, but the no-authority and
  disabled-local-lane upgrade cases need named test entry points.
- Task 3 could accidentally imply optional delivery is non-authoritative even
  when the contract requires durable delivery; it needs a clear distinction
  between lifecycle commit, delivery projection, and truthful pending state.
- Task 4 correctly avoids current-state reconstruction but needs explicit
  launch-history behavior for purged decisions.
- Task 5 includes gates but not the repository's affected-test command,
  branch-shape gate, or pre-commit hook verification.
- Architecture preserves ownership but should state whether any new error
  sentinel/type is justified; no interface should be added prematurely.
- Tests name layers well, but generated OpenAPI drift must be checked if
  boundary handlers change.
- Support/docs/stability are sufficient; execution order should insert a
  deliberate baseline RED observation before any production edit.

### Critical evaluation of the critique

The critique is useful but must not turn the plan into a speculative API
redesign. The durable error shape can remain an existing typed/sentinel error
or a wrapped causal error if current consumers already distinguish it. The
ownership distinction must be tested from observable state, not solved by
renaming functions. Upgrade cases should exercise the same read-only probe
used by session authorization, not a new helper-only simulation. Boundary
coverage must be proportional: a focused real handler test can prove status
mapping while domain tests own the matrix. The formula's branch/worktree
verification and pre-commit gates are operational requirements and belong in
the final pass, not source design.

### Pass 1 roll-up: revised plan changes

- Add an explicit baseline result table and exact test command to the plan.
- Name upgrade coverage as probe tests plus controller/session authorization
  cases for no authority and disabled local lane.
- Define delivery states by observable contract: lifecycle receipt committed,
  delivery pending/unknown on projection failure, ack idempotency preserved,
  and no second authoritative record.
- Require launch replay tests to prove durable launch-time facts or an honest
  historical-unavailable error; never derive them from current state.
- Add branch-shape, hook, pre-commit, and generated-wire checks to the final
  gate list; do not invent a new interface or framework.

### Pass 1 no-change decisions

- No new registry/outbox/event framework.
- No role/provider/auth/key/pin/live-state changes.
- No dashboard UI work or unrelated scale refactor.
- No relaxation of producer identity safety and no retroactive tightening of
  legacy creation.

## Pass 2 — source-informed refinement

### Full plan / tasks / subtasks

1. Confirm source map and baseline: inspect `requiredBucketNames`, write
   transaction call sites, delivery projection validators, `ExpireDue`, launch
   receipt transactions, `ExecutionSessionBound`, and concrete Huma/CLI
   handlers. Record exact baseline command/results in this file.
2. Add RED tests at those owners, including a legacy bucket fixture that is
   opened read-only and compared byte-for-byte/bucket-for-bucket before and
   after the probe. Ensure poison records are ordered before valid later
   records so the old `ExpireDue` early return is observed.
3. Implement only the smallest transaction/projection split and per-record
   expiry isolation supported by current types. Preserve error wrapping and
   state transitions, and ensure retries expose pending/unknown rather than a
   false success.
4. Add real boundary mapping tests for pending and ack, including invalid,
   missing, conflict, and unavailable store cases. Use generated clients only
   as wire consumers; assert handler status/body at the Huma boundary.
5. Verify replay/purge and cursor semantics against existing retention; change
   docs/tests only if the actual contract is violated.

### Architectural changes

The repair remains inside existing routingdecision and boundary layers. The
key boundary is the commit point: lifecycle state/launch receipt is durable
before an optional delivery projection is attempted or reported. The read-only
legacy probe recognizes the historical bucket set and treats absent delivery
storage as empty without opening a writer. No new abstraction is added unless
the current code has two existing implementations that require a stable port.

### Test plan

Use `internal/routingdecision` for transaction and probe semantics; `cmd/gc`
for controller/CLI behavior; `internal/api` for Huma status mapping; and one
integration/provider-free convergence proof for composition. Keep independent
review evidence separate from tests: passing tests prove only their observed
layer.

### Support structures / docs / execution order / stability / blockers

Reuse existing test helpers and constants. Update the plan and execution log
after each formula subtask. Execute baseline → RED → minimal fix → focused
GREEN → boundary/spec checks → affected/full gates → review → handoff. Keep
all temp caches under `/var/tmp` only when isolation is essential, never clear
the shared Go cache, and preserve unrelated files.

### Candidate parallel work

Read-only history and boundary maps can run in parallel; implementation and
tests remain serial because the transaction contract is shared.

### Pass 2 critique, top to bottom

- The source-informed plan now names the correct ownership seams, but it must
  not assume the current public delivery API already exposes a pending error
  state; verify existing types before selecting representation.
- The legacy fixture comparison is strong evidence, but bucket ordering and
  Bolt transaction semantics must be handled through existing helpers rather
  than brittle raw-byte snapshots.
- Boundary status tests need to distinguish API handler mapping from client
  decode behavior and must not change OpenAPI for implementation-only errors.
- Cursor/retention work is likely independent and should remain unchanged if
  no consumer-visible violation is reproduced.

### Critical evaluation of the critique

These constraints reduce overreach. The implementation must be driven by
current structs and callers: if there is no new public error variant, preserve
existing error contracts and test pending state through durable records. The
legacy proof should inspect the exact read-only behavior and absence of writes,
not rely on a broad byte dump that couples tests to Bolt internals. API tests
should call handlers/server routing directly. Cursor changes require a
reproduction against the documented consumer contract, otherwise remain a
no-change decision.

### Pass 2 roll-up: revised plan changes

- Select the current error/state representation after source inspection;
  avoid adding a public type for an internal projection failure.
- Prove legacy non-mutation with a store reopen and durable bucket existence/
  content checks using existing store APIs or narrowly owned test helpers.
- Keep OpenAPI/generated files unchanged unless the public response schema
  actually changes; run drift checks regardless for boundary edits.
- Do not touch cursor/retention code without a reproduced contract failure.

### Pass 2 no-change decisions

- No speculative pending/outbox schema or new public error type.
- No direct raw Bolt test coupling when existing domain accessors suffice.
- No cursor/retention refactor without a consumer-contract reproduction.

## Pass 3 — final plan and lost-information audit

### Full plan / tasks / subtasks

1. Baseline and source map: record exact SHAs, affected package tests,
   preflight result, and current branch/worktree metadata; preserve the
   independent review as a separate semantic warning.
2. RED: add focused tests for optional outcome validation not aborting
   lifecycle/launch receipt, poison-isolated expiry/revocation, legacy probe
   compatibility with no authority/disabled lane, corruption/bound-session
   fail-closed behavior, and real 400/404/409/503 boundary mapping.
3. GREEN: make the smallest existing-layer changes that commit lifecycle facts
   independently, isolate expiry errors, keep producer construction outside
   generic transition, and let read-only legacy probes tolerate absent
   delivery buckets without mutation. Preserve exact bytes, identity safety,
   honest unknown/retry behavior, and historical launch semantics.
4. Review: inspect every diff for authority leakage, silent drops, duplicate
   delivery, generated wire changes, and upstream-unfriendly edits; run
   focused/affected/full gates and independent exact-byte review.
5. Handoff: commit on `polecat/sdk-bcy.3`, verify hooks and clean tree, push
   and verify remote SHA, record shipped outcome and target, reassign the open
   implementation bead to the refinery, signal it, and drain.

### Architectural changes

Only existing lifecycle/delivery/probe ownership boundaries change. Durable
lifecycle and launch receipt persistence are authoritative SDK facts; producer
outcome projection is advisory/optional until it has a valid causal receipt.
Legacy read-only safety probes remain non-mutating and backward-compatible.

### Test plan and evidence discipline

RED must fail on the exact baseline before production edits. Domain tests prove
state/transaction semantics; API/CLI tests prove real response mapping; the
read-only probe tests prove no mutation and fail-closed safety; broader gates
prove compilation/composition only. Passing a fake client or writable self-
healing fixture is explicitly insufficient.

### Support structures / docs / execution order / stability / blockers

Maintain `agent-execution.log` and this plan. Update docs only for verified
consumer-visible behavior. Run in the order above, using existing helpers and
bounded test synchronization. Escalate only durable blockers; do not mutate
live state or restart shared services.

### Candidate parallel work

Parallel read-only source/history/review checks are useful; all edits and
semantic test decisions remain in one serial branch to preserve ownership.

### Pass 3 critique, top to bottom

- The target truth and evidence layers are now explicit and guard against the
  known false completions.
- The tasks cover every blocker from the independent review without requiring
  unrelated cursor/retention work.
- The architecture is minimal and preserves the existing boundary model.
- The test plan separates domain, real handler, probe, and composition proofs;
  it still must record any unavailable independent review honestly.
- The handoff includes the required branch shape and refinery-only closure.

### Critical evaluation of the critique

No material gap remains in the plan. The remaining uncertainty is an
implementation detail intentionally deferred to source inspection: whether
projection errors are represented as returned errors, durable pending records,
or a combination already present in the current types. Deferring that choice
prevents a speculative public contract. The plan does not promise a docs or
cursor change unless a current reproduction proves one is needed.

### Pass 3 roll-up: final plan

Proceed with the minimal source-owned repair and tests described above. The
acceptance bar is the target truth section plus the exact evidence-layer
checks; a green compile or fake 200 is not completion. Keep all changes on the
per-bead branch and hand off only after remote SHA verification.

### Pass 3 no-change decisions

- Leave producer construction outside generic `Transition`.
- Leave producer identity safety and legacy creation semantics unchanged.
- Add no new registry/outbox/event framework, mandatory crypto, public error
  schema, dashboard UI, provider/live-state mutation, or unrelated refactor.
- Leave cursor/retention behavior unchanged unless reproduced as a contract
  failure.

### Lost-information audit after three passes

Retained from the initial task: exact poison identifiers, expiry/revocation,
launch receipt, legacy/no-authority/disabled-lane upgrade, corruption and
bound-session fail-closed behavior, API/CLI 400/404/409/503 mappings, exact
bytes, replay/purge historical facts, and the no-fabrication/no-duplicate
delivery constraints. Refinement removed only speculative public error,
cursor/retention, raw Bolt, and new-framework work that was not reproduced.

counter: 3
status: implementation-complete-pending-handoff

## Execution evidence

- RED regressions failed on the candidate for lifecycle rollback, expiry
  short-circuiting, launch-receipt rollback, and legacy probe rejection.
- GREEN: `CGO_ENABLED=0 go test -count=1 ./internal/routingdecision` passed.
- GREEN: full `internal/api` suite passed; routing/JSON-schema controller
  coverage and the no-authority/local-lane service tests passed.
- `CGO_ENABLED=0 go vet ./...` and `make dashboard-ci` passed.
- The documented `make test-fast-parallel` gate was attempted twice but its
  push-gate waited on two pre-existing shared fast-test slots; the invocation
  was stopped without claiming a result. No unrelated slot process was
  terminated.
