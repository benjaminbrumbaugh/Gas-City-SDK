# sdk-bcy.1 implementation plan

counter: 0

## 1. Full plan, tasks, and subtasks

Goal: make the SDK the sole owner of local v3 admission and executable
candidate identity while preserving the legacy signed schema-1/v2 and signed
schema-2 entry points.

Tasks:

1. Establish the contract at the domain boundary.
   - Add a typed local-advice request/result and a redacted candidate-tuple
     projection.
   - Export only SDK-resolved model/serving/effort/account/provider/target and
     configuration/adapter/invocation digests; never executable paths, argv,
     environment, credentials, or inferred workload/evidence.
   - Define exact validation and identity rules for recommendation, work
     revision/claim fence/state digest, target, and candidate tuple.
2. Add local admission to the controller and durable routing ledger.
   - Add a local record marker and an idempotent local-admit write path that
     does not require an external authority file or signature.
   - Reuse the existing ready-work CAS, route metadata, compensation, lifecycle
     reconciliation, session authorization, and runtime-start receipt paths.
   - Keep external signature verification mandatory on `/routing/decisions` and
     all legacy/signed records.
3. Expose the typed contract through the control plane.
   - Add a city-scoped POST local-advice route behind the existing CSRF and
     city-write grant middleware; loopback is not sufficient authorization.
   - Add the generated Go client adapter and CLI command for exact JSON input,
     idempotency, and existing grant-command delivery.
   - Regenerate OpenAPI, schema mirrors, and generated clients.
4. Document and prove the boundary.
   - Update canonical routing/config documentation and bead notes with the
     minimal contract and legacy-compatibility rules.
   - Add RED/GREEN tests for exact candidate export, local admission without
     external keys, stale/changed tuple rejection, write-auth enforcement,
     idempotent replay, CAS compensation, receipt projection, and unchanged
     signed goldens.

## 2. Architectural changes

The local lane is advisory input, not trust or ranking authority. The SDK
re-resolves the selected target against root-owned `RoutingExecution` config
and validates the live work bead before it writes route metadata. The local
ledger record is explicitly marked local and carries no synthetic approval or
signature. It is still bound to the same immutable execution tuple and work
fence used by runtime authorization.

The existing signed decision route and verifier remain unchanged. The new
route uses normal city write authorization and CSRF handling. The loopback
perimeter remains only for the legacy signature-bypass route and must not be
reused as local-advice authorization.

No Wayfinder ranking, workload inference, account discovery, trust/key
provisioning, active-work migration, v4 behavior, or terminal-success claim is
added.

## 3. Test plan and evidence discipline

Target truth: an untrusted local caller cannot admit arbitrary work, while a
valid advisory selection matching fresh SDK configuration is admitted without
external signing keys and later produces only honest SDK lifecycle/runtime
facts.

Required evidence layers:

- domain/unit tests prove tuple validation, digest binding, local ledger
  idempotency, and legacy signature behavior;
- controller tests prove live config/work re-resolution, ready-work CAS,
  compensation, and runtime authorization at the controller boundary;
- API tests prove typed wire shape, city-write grant enforcement, and rejection
  of loopback-only local requests;
- CLI/client tests prove exact request serialization and no unsafe fallback;
- focused integration tests prove durable local admission-to-receipt/outcome
  wiring without treating preview/schema output as execution proof.

Cheaper but insufficient proxies: JSON schema validation alone, a passing
eligible snapshot, a fake provider, or a local route that only checks
loopback. Tempting false completion: generating a signed envelope inside the
SDK, trusting caller-supplied invocation digests, or reporting admission as a
successful provider execution. If this plan succeeds but the original bug
remains, the likely cause is a bypass path (direct worker creation, recovery,
or stale config mutation) that still accepts the advisory tuple without the
controller's local re-resolution; tests must cover those boundaries or record
the exact remaining gap.

## 4. Support structures, docs, and generated artifacts

- Domain types/tests: `internal/routingdecision`.
- Controller/config resolution: `cmd/gc/routing_decision_*`,
  `cmd/gc/routing_execution_*`, `internal/config/routing_execution.go`.
- Typed API/client/CLI: `internal/api`, `cmd/gc/cmd_routing.go`.
- Canonical docs: `docs/reference/config.md` and a focused routing contract
  section in `engdocs/architecture/durable-routing-intents.md` or the current
  canonical routing plan, with generated schema files updated only by their
  generators.
- Evidence artifact: focused local-admission test fixture using a temporary
  executable/config and no live provider or shared City writes.

## 5. Execution order

1. Finish context/worktree setup and base preflight.
2. Write this plan and refine it through three critical passes.
3. Add failing domain tests and implement typed candidate/local-admit seams.
4. Add failing controller/API/CLI tests and implement routing.
5. Regenerate wire artifacts and documentation from source.
6. Run focused tests, affected tests, vet, fast baseline, and schema/dashboard
   gates required by touched surfaces.
7. Self-review exact diff, commit, push, verify remote head, and hand off to the
   refinery without closing the implementation bead.

## 6. Stability and blocker avoidance

Keep all edits in this bead worktree and preserve the dirty agent-home work.
Use short owned `TMPDIR`/ICU settings for SDK gates and pin `PYTHONPATH` only
for Python gates. Do not deploy, install binaries, reload runtimes, change
trust keys/pins, touch the live shared City, or run live-provider smoke.
If the baseline is broken, record the exact existing symptom rather than
repairing unrelated code. If a controller boundary remains ambiguous after
focused archaeology, escalate with the evidence instead of inventing a second
authority path.

## 7. Candidate subagent-parallel work

No independent source lane is safe to delegate before the typed contract is
frozen: domain wire names, durable local-record semantics, and generated API
shape are coupled. After the contract is fixed, an independent reviewer can
audit exact-byte controller admission and a second reviewer can audit legacy
signed/schema artifacts in parallel with implementation. Their evidence must
be read as layer-specific review, not substituted for focused tests.

## Planning pass 1: critique of every major section

counter: 1

- Plan/tasks: the scope is correctly decomposed, but “local record” could
  accidentally become a second lifecycle model; it must share the existing
  `Record`, state transitions, receipts, and work metadata fence.
- Architecture: the local marker needs to be durable and explicit, while its
  wire exposure should be additive/omittable so signed legacy JSON remains
  byte-compatible.
- Tests/evidence: local admission without an authority file must be tested at
  both controller and HTTP layers; a direct method test alone would miss the
  loopback-only authorization bug.
- Support/docs: generated schema/client edits must be source-generated and the
  contract must state which fields are intentionally absent.
- Execution/stability: plan-file and temporary-log work must not contaminate
  the implementation commit; foreign dirty changes remain outside this branch.
- Parallel work: exact-byte review is useful only after implementation and
  cannot decide the API contract independently.

## Planning pass 2: critical evaluation of pass 1

counter: 2

- The shared-record decision is sound, but local records cannot flow through
  `ActiveApproved` or any verifier-required signed path; dedicated local store
  creation/admission must be explicit and fail closed when the local registry
  is disabled.
- The HTTP route should use normal city POST/write-auth middleware, not the
  signed ingest's loopback exception. This directly proves “loopback alone is
  not authorization.”
- Candidate export must be computed from the same resolver used by admission,
  not copied from TOML or accepted from the request. The request must include
  enough identity to fence the work but no workload/evidence fields.
- Existing outcome projection is useful only after a real launch receipt; no
  local-admit success may set actual/session/execution fields.
- The CLI should preserve exact JSON and grant binding, but its output must
  expose only typed SDK receipt/record facts.

## Roll-up 1: apply evaluation to critique

The implementation will centralize candidate construction in one resolver
used by `eligible`, local admission, and final launch. Local ledger creation
will be a distinct store method with the same durable state/audit/index
structures and idempotency semantics, while signed methods stay untouched.
The API route will be ordinary write-auth protected; tests will include a
loopback request with no grant and a valid grant request. Outcome tests will
assert unknown terminal disposition and absence of fabricated actual facts.

## Planning pass 3: revised plan critique

counter: 3

- The contract now has a clear owner, but names must remain intuitive and
  additive: `ExecutionCandidateSnapshot`, `LocalAdmissionRequest`, and
  `LocalAdmissionResult` should live in `routingdecision` rather than API-only
  structs so CLI/API/downstream consumers share one typed shape.
- Opening the routing ledger without an authority file is safe only when local
  execution is explicitly enabled. Initialization must preserve the old
  denied/nil behavior for disabled cities and never create trust material.
- `Local` must not be accepted merely because it is in a JSON record; store
  validation and controller launch checks must ensure its payload matches the
  live registry and work fence.
- Generated API artifacts and schema docs are part of the public surface and
  require `spec-ci`, dashboard generation, and OpenAPI sync checks.
- The plan still cannot prove every worker/recovery path without broadening
  scope; record any remaining path as an explicit non-claim, not a hidden
  shortcut.

## Roll-up 2: apply revised critique to plan/tasks/subtasks

Before implementation, freeze these decisions: shared typed domain structs;
one SDK candidate resolver; local records only when config enables the lane;
normal write-auth on the local route; signed route unchanged; local success
means admission only; actual execution remains receipt-backed and terminal
status remains unknown. Add invariant tests for disabled initialization,
local-record tamper/replay, resolver reuse, and signed golden stability.

## No-change decisions

- Do not add a new ranking abstraction or provider interface.
- Do not remove or weaken authority-file loading for legacy signed ingest.
- Do not alter schema-1/schema-2 canonical signing bytes or existing signed
  endpoint names.
- Do not add workload inference, evidence fabrication, v4 support, cross-session
  migration, trust/key/pin changes, deployment, or live provider tests.
- Do not touch the unrelated dirty agent-home files or the live shared City.

## Lost-information check after three passes

Retained: local versus signed trust boundary; exact tuple/work fences; CAS and
capacity path; durable lifecycle/receipt/outcome semantics; HTTP/CLI/generated
wire ownership; authorization and proxy-domain evidence requirements; legacy
compatibility and explicit exclusions. No critical requirement was dropped.

## Approved working contract

The minimal local lane is a typed advisory selection carrying a producer
recommendation identity, one exact fresh work snapshot, and one exact
SDK-exported execution candidate. The SDK re-resolves the candidate and live
work, performs the existing route metadata CAS, and records a durable local
admission receipt. External signatures remain required only on the existing
signed ingest route. The implementation starts after this contract freeze.
