# sdk-41b.2 plan

counter: 3
initial_counter: 0
work_bead: sdk-41b.2
base: origin/main

## 1. Full plan, tasks, and subtasks

Goal: publish controller-owned, scope-correct quota impairment evidence from
the existing passive Codex transcript extractor. The implementation must stay
inside existing session, worker, and recovery boundaries: no provider probe,
new event bus, new database, live City mutation, or role-specific decision
logic.

Tasks:

- T1 — Confirm the current contracts and baseline.
  - T1.1 Read the session Info codec, exact transcript resolver, worker
    transcript boundary, recovery responder, and current Wayfinder v3 types.
  - T1.2 Run the configured preflight (or the documented fast baseline when
    the formula has no configured command) from the clean fork.
  - T1.3 Record the layer each diagnostic observes and its limits.
- T2 — Define the typed controller projection at the worker boundary.
  - T2.1 Add the smallest worker-owned request/result API that accepts the
    already-loaded session identity and search roots, then delegates to the
    existing extractor without exposing sessionlog to cmd/gc.
  - T2.2 Preserve provider/account/model/scope, nullable window units,
    observation time, reset deadlines, and session-incarnation identity.
  - T2.3 Classify only a verified hard exhaustion (a reached window with
    sufficient scope and current identity); reminders, partial/unknown data,
    stale observations, and reset availability remain non-terminal evidence.
- T3 — Wire the controller projection and durable handoff.
  - T3.1 Resolve an exact current-session transcript through the existing
    session resolver; never use newest-file/workdir fallback for attribution.
  - T3.2 Project records through the existing session/work-store boundary,
    fencing writes by the current session identity/revision and preserving
    account/target isolation.
  - T3.3 Normalize verified hard upstream exhaustion into the existing typed
    Wayfinder v3 throttle evidence; keep auth/readiness and unknown values
    independent, and keep reset deadline separate from observation expiry.
  - T3.4 Ensure reminders do not quarantine, alter hard gates, or create
    recovery work by themselves. Do not implement successor original-bead
    continuation (sdk-41b.5).
- T4 — Prove the boundary with focused tests and fixtures.
  - T4.1 RED tests for hard exhaustion, reminder/non-terminal data, account and
    target isolation, missing scope, stale observations, changed incarnation,
    and recovery evidence.
  - T4.2 Replay the existing provider-free fixture through the worker seam and
    assert exact typed values and units.
  - T4.3 Add controller/store tests for idempotence and fencing. Do not change
    expectations unless the product contract changed; this task adds the
    projection contract.
- T5 — Self-review and delivery.
  - T5.1 Run focused tests, fast baseline, vet, and affected/full formula gate.
  - T5.2 Review imports and generated/schema references for boundary leakage;
    document exact source/fixture/schema references in the change.
  - T5.3 Commit cohesive source/tests/plan, push polecat/sdk-41b.2, verify the
    remote SHA, and hand the open bead to the refinery.

## 2. Architecture changes

The source parser remains `internal/sessionlog` and remains passive. A small
`internal/worker` adapter is the only production caller-facing boundary for
quota extraction. The controller (`cmd/gc`) consumes that typed adapter and
existing `session.Info`/store APIs. Exact transcript resolution remains in
`internal/session`, where session keys and provider-family rules already live.

The projection is an observation, not a new policy engine: it records the
provider evidence and maps only verified upstream hard exhaustion to the
existing Wayfinder v3 `Throttle=upstream` field. It does not infer token or
request rates from percentages, fabricate concurrency, rank targets, probe a
provider, or decide whether to retry/quarantine. Reset deadlines are evidence
fields; freshness/expiry controls whether evidence is usable, not whether the
provider recovered.

Durable writes use existing session/work-store mechanisms and the current
session-incarnation/revision fence. No new event registry, outbox, persistence
store, or role-specific dependency is introduced.

## 3. Test and evidence plan

Truth to prove: the controller publishes only attributable, scope-correct,
current hard-exhaustion observations and leaves non-terminal or ambiguous
evidence inert.

Required evidence layers:

- Parser/fixture tests prove the provider file can yield typed quota values;
  they do not prove controller attribution or durable publication.
- Worker-boundary tests prove cmd/gc can consume the typed extractor without a
  sessionlog import and that nullable units/scope are preserved.
- Controller/store tests prove exact session resolution, account/target
  isolation, revision/incarnation fencing, stale-record handling, and
  recovery evidence behavior.
- Existing Wayfinder request/response tests prove the v3 typed handoff uses
  upstream throttle evidence without changing auth/readiness/unknown fields.
- Static import/boundary tests prove no layering leak; `go vet` and the normal
  project test gates provide compile/regression coverage.

Cheaper but insufficient proxies: a passing extractor-only fixture, a parser
unit test with caller-supplied identity, a helper recovery task, a process
health check, a no-op Wayfinder request, or a screen/log preview. None proves
the controller emitted the right scoped durable record.

False-completion substitution to avoid: treating a 100% reminder as terminal,
treating a reset deadline as recovery, converting a weekly percentage into
tokens/minute, setting zero capacity when concurrency is unknown, quarantining
all accounts/targets, accepting a stale transcript after session incarnation
changes, or claiming the successor original-work recovery is complete.

Acceptance matrix:

| Case | Expected proof |
| --- | --- |
| verified hard limit | causal scoped impairment and typed upstream projection |
| reminder/partial/unknown | observation only; no quarantine or hard-gate change |
| account/target mismatch | excluded, never projected to the other target |
| missing scope | remains unknown/non-terminal |
| stale observation | retained with freshness metadata, not used as current recovery |
| changed incarnation | old evidence cannot mutate current session state |
| recovery evidence | existing verified recovery path remains authoritative |

## 4. Support structures and documentation

- Reuse `internal/sessionlog/testdata/codex/quota-refresh.jsonl`; add only
  minimal fixtures for cases that cannot be represented by the existing
  fixture.
- Add focused tests next to the worker/controller code. Avoid a second
  observation schema or duplicate registry.
- Update source comments and the relevant schema/fixture reference only if
  the typed handoff changes. Do not edit generated documentation by hand.
- Maintain temporary `agent-execution.log`; remove it before delivery if it is
  not an intentionally tracked artifact.

## 5. Execution order

1. Baseline/preflight on the clean branch.
2. Add failing worker/controller tests and run them to establish RED.
3. Implement the worker adapter and typed projection.
4. Implement controller wiring and durable fenced publication.
5. Run focused GREEN tests, then broader fast tests and vet.
6. Review diff/imports/contracts, format, commit, and run the final formula
   self-review/submit gates.

## 6. Stability and blocker avoidance

- Keep all changes in the per-bead worktree and branch.
- Do not touch the live shared City or runtime sessions.
- Do not restart Dolt unless its commands actually fail; if they do, collect
  the prescribed diagnostics before escalation.
- Do not add a second persistence or event path when an existing store/typed
  boundary can carry the record.
- If baseline fails, deduplicate in beads before filing and do not repair base
  failures in this branch. If a requirement remains genuinely ambiguous after
  source/history inspection, escalate to Witness with durable evidence.
- Push only after the affected/full tests, vet, clean-tree, branch-shape, and
  remote-SHA gates pass.

## 7. Candidate parallel work

The independent work slices are (a) typed worker adapter plus fixture replay
tests and (b) controller/store fencing tests. They converge on the same public
types and therefore must be integrated serially after contracts are fixed.
No subagent capability is available in this session, so the slices will be
handled in one worktree without speculative duplicate implementations.

## Planning pass 1 — initial plan (counter 0)

The initial plan identified the existing extractor as the source of truth,
placed provider-specific parsing behind `internal/worker`, and kept all
policy/continuation behavior outside this bead. It required both positive and
negative scope/freshness/incarnation evidence rather than parser-only proof.

## Planning pass 2 — critique of every major section (counter 1)

- Tasks: T2/T3 could accidentally duplicate existing recovery responder logic;
  constrain the implementation to observation projection and leave successor
  original-bead work to sdk-41b.5.
- Architecture: writing directly from cmd/gc would violate the worker boundary;
  require a worker adapter and exact session resolver.
- Tests: fixture success alone would not prove attribution, so add controller
  store/fence tests and explicit negative cases for reminders, stale data, and
  changed incarnation.
- Support/docs: new schemas or registries would create forbidden parallel
  infrastructure; reuse existing typed Wayfinder and store contracts.
- Execution: RED must precede production changes and baseline must precede
  implementation.
- Stability: avoid live runtime/provider access and preserve branch/worktree
  discipline.
- Parallelism: the slices share types; parallel implementation would increase
  merge risk, so only review/test thinking can be parallel in principle.

## Planning pass 3 — critical evaluation of the critique (counter 2)

The critique correctly narrows scope, but it must also guard against a second
false boundary: a worker adapter that merely aliases the parser could still
leave controller attribution and durable fencing unproved. The plan therefore
requires the adapter to accept verified session identity, the controller to use
exact keyed resolution, and persistence tests to observe the actual store
read/write layer. Wayfinder v3 has no new quota-window wire field in the
current SDK types; the handoff must preserve quota evidence in the existing
typed observation path or an already-reviewed typed source contract, not invent
an unreviewed v4-like schema. If the current types cannot carry the required
scope without loss, stop and escalate rather than silently flatten it.

## Roll-up: revised critique applied to the plan

T2.2/T3.1/T3.2/T3.3 and the evidence matrix were strengthened to require
current-session attribution, exact scope preservation, durable fencing, and
existing v3-only handoff. The plan explicitly names what each evidence layer
does not prove and forbids parser-only or helper-task false completion.

## Roll-up: revised critique applied to tasks/subtasks

T4 now requires actual controller/store observations, not only typed parser
output. T3.3 is conditional on the current v3 contract and cannot add a new
schema. T5.2 requires import and schema-reference review before push.

## Explicit no-change decisions

- No changes to the already-repaired Codex extractor unless a red integration
  test proves a contract defect; sdk-41b.7 owns that parser repair.
- No changes to provider runtime, account installation, live configuration,
  session creation, recovery policy, or successor original-work continuation.
- No new event framework, database, provider probe, ranking logic, mandatory
  crypto, or hardcoded role.
- No generated docs/schema edits unless the existing reviewed source contract
  actually changes and generation is required by the repository gate.

After this third pass, no material plan information was lost. `counter: 3`
records the completed refinement while `initial_counter: 0` preserves the
required starting state.
