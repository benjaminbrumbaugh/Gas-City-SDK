# sdk-h0s.4 implementation plan

counter: 0

## 1. Full plan, tasks, and subtasks

Goal: make accepted external-coordination answers reach their registered
origin durably and idempotently, using the existing typed destination and
session/extmsg boundaries without introducing arbitrary callback execution.

Tasks:

1. Establish the current contract and ownership boundaries.
   - Inspect the parent coordination contract and the existing
     `internal/externalcoordination`, `internal/extmsg`, session, and API
     surfaces plus relevant history.
   - Identify the canonical typed result-destination/route identity and the
     durable request/response state already available to this lane.
   - Preserve the existing single configured target, opaque credentials, and
     conversation registration fence.
2. Add a failing target-layer regression for response-to-origin handoff.
   - Prove the correct registered conversation receives the exact answer.
   - Prove lost response acknowledgement and lost origin-delivery acknowledgement
     replay safely without emitting a duplicate turn.
   - Cover cross-conversation/owner isolation, stale or conflicting replay,
     refusal/failure truthfulness, and retention policy.
3. Implement the smallest recoverable handoff seam.
   - Consume canonical destination/route identity from the durable request and
     authenticate attempt, correlation, registration generation, and owner.
   - Persist enough authorized handoff state to retry independently of the
     coordinator execution while keeping credentials out of durable records.
   - Route through existing session/extmsg interfaces; do not add arbitrary
     URLs, commands, or a general remediation engine.
4. Verify and document evidence boundaries.
   - Run focused package tests, affected tests, vet, and required repository
     gates for the touched surface.
   - Record what component/in-process evidence proves and what live bridge or
     external-origin behavior remains outside this task's verification.

## 2. Architectural changes

The external-coordination service remains the authority for request admission,
claiming, and outcome truth. This lane owns only the response-to-origin
handoff after an admitted request has a typed, registered destination. The
handoff is a durable, replayable state transition bound to the original
request/correlation/attempt and registration fence; it is not a new request,
turn, authorization mechanism, or callback endpoint.

The implementation must reuse existing typed destination semantics and the
session/extmsg delivery boundary. Durable state contains identifiers and
redacted content/commitment fields required by retention policy, never bearer
credentials or private provider state. Replay is idempotent after ambiguous
acknowledgement, and stale, cross-owner, cross-conversation, or conflicting
replay is rejected. Terminal request outcomes remain truthful when delivery is
refused or fails; a recorded response hash alone is not delivery success.

## 3. Test plan and evidence discipline

Target truth: the actual registered origin receives exactly one correct answer
for an accepted request, including after coordinator/ack loss, while an
unauthorized or stale replay cannot deliver to another conversation or owner.

Required evidence layers:

- handoff/service tests prove durable state transitions, replay fencing,
  idempotency, retention, and truthful refusal/failure;
- session/extmsg coordination tests prove typed route selection and one-turn
  delivery through the existing boundary;
- API/CLI tests, only if their wire projections change, prove exact typed
  request/result serialization and unchanged generated schema contracts;
- a focused composition test proves request outcome to origin handoff without
  treating a schema preview, response hash, callback admission, or in-process
  fake as proof of live external receipt.

Cheaper but insufficient proxies: a recorded response hash, a queued request,
an admitted callback, a passing JSON/schema test, or a fake delivery method
that is not bound to destination and registration identity. Tempting false
completion: marking a request completed when only response persistence
succeeded, replaying a new turn after lost acknowledgement, accepting a
caller-supplied arbitrary URL/command, or inferring live origin receipt from an
in-process test. If this plan succeeds but the original bug remains, a likely
cause is an untested recovery/reconciliation path that reconstructs delivery
without the same identity fence; tests and review must inspect those paths or
record the exact gap.

## 4. Support structures, docs, and generated artifacts

- Domain/service boundary: `internal/externalcoordination/` and any focused
  handoff package that already owns durable coordination state.
- Delivery boundary: `internal/extmsg/` and existing session/registration
  interfaces; do not add a parallel transport.
- API/client surface: `internal/api/` and `cmd/gc/` only if the typed contract
  requires a projection change; read the control-plane architecture docs first.
- Tests: package-local regression fixtures and one composition proof at the
  smallest real boundary.
- Evidence artifact: `agent-execution.log`, recording completed subtasks and
  layer-specific verification without committing raw temporary dumps.

## 5. Execution order

1. Complete context, bead-scoped branch/worktree setup, and base preflight.
2. Refine this plan through three critical passes and freeze no-change
   decisions.
3. Perform history/code archaeology and add the smallest failing regression.
4. Implement the recoverable handoff and refactor only as needed for a clear
   boundary.
5. Run focused and affected verification, then required quality gates.
6. Review exact diff, commit, push, verify the remote branch, and hand the
   open implementation bead to the refinery without closing it.

## 6. Stability and blocker strategy

Keep all edits in this bead worktree and preserve the dirty reusable polecat
home. Do not touch the shared City, deploy/install/restart services, change
credentials, provider accounts, routes, or protected historical work. Do not
weaken tests or broaden the scope into a general callback/remediation engine.
Use existing temp directories and bounded commands; never use `go clean
-cache` or place caches in `/tmp`. If a dependency or live registration is
unavailable, capture the exact layer and escalate rather than substituting
private runtime state or claiming live success.

## 7. Candidate subagent-parallel work

Before the typed handoff contract is confirmed, source edits are sequential.
After the seam is fixed, an independent review can inspect replay/identity
fences while another review checks extmsg/session boundary use and retention.
Parallel verification is useful for independent package tests, but neither
review nor a proxy test can substitute for the target-layer composition proof.

## Planning pass 1: critique of every major section

counter: 1

- Plan/tasks: the scope correctly separates admission/outcome ownership from
  origin delivery, but “existing typed destination” must be located before any
  new type is proposed; otherwise a duplicate route identity could leak in.
- Architecture: durable handoff state must be explicitly tied to one accepted
  attempt and registration generation, not only the request ID or response
  hash. Credentials and provider state must remain outside the record.
- Tests/evidence: in-process delivery proves only the SDK/session boundary;
  the plan must label it as insufficient for external receipt and retain an
  exact-byte answer assertion.
- Support/docs: API/generated artifacts should remain untouched unless the
  current public contract actually changes; the handoff should prefer an
  internal service seam over wire expansion.
- Execution/stability: history archaeology must precede implementation because
  prior callback-delivery work exists and may be the smallest proven slice.
- Parallel work: independent review is safe only after the producer commit and
  identity fence are frozen; no parallel edits to the same boundary.

## Planning pass 2: critical evaluation of pass 1

counter: 2

- The shared-type requirement is sound, but typed destination semantics may
  be represented by an existing route-registration key rather than a new
  public result type. Search the current service and extmsg constructors
  before adding fields or persistence.
- A response acknowledgement and an origin-delivery acknowledgement are
  different facts; the state machine must retain both and make the retry
  decision from durable state, not from a process-local callback.
- Cross-conversation isolation needs a test that registers two origins with
  the same provider/account and distinct conversation IDs, not only different
  provider names.
- Retention tests must distinguish durable identifiers/commitments from
  retained answer content and assert the configured policy at the write/read
  boundary.
- “Failure reaches origin truthfully” requires an explicit refusal/error
  envelope or existing extmsg failure path; a Go error alone is not sufficient
  if the origin never receives it.

## Roll-up 1: apply evaluation to critique

Use the existing route-registration identity and service response records as
the contract source. Model the handoff as a durable transition keyed by
request, correlation, accepted attempt, owner, and registration generation.
Add two-origin same-provider tests, separate response-persist and origin-ack
loss tests, and assert that failure/refusal is delivered through the existing
typed extmsg path. Do not enlarge the wire surface unless current types cannot
carry the required facts.

## Planning pass 3: revised plan critique

counter: 3

- The revised plan is appropriately additive, but the current source may have
  a partially implemented response replay fence. The first RED test must prove
  the exact missing behavior rather than restating an already-passing path.
- Recovery must not resend after possible remote acceptance without a durable
  reconciliation signal; “idempotent replay” means one origin turn, not merely
  one local state row.
- Any response content persisted for retry must use the existing retention
  policy and must not persist a bearer or provider-private object. A digest
  alone is insufficient if retry needs the answer bytes; this tradeoff must be
  resolved from the current contract, not guessed.
- The plan should explicitly record the component/live verification limit in
  the bead handoff and final summary, even if no docs or API files change.

## Roll-up 2: apply revised critique to plan/tasks/subtasks

Before coding, freeze the existing route-registration and extmsg/session
interfaces as the only delivery boundary, locate any current replay fence,
and add a RED regression only where the current bytes fail. The state machine
must separate response persistence, attempted delivery, delivery acknowledgement,
and terminal request outcome. Recovery may retry only when durable state proves
the prior attempt was not accepted; otherwise it must reconcile through the
existing identity-aware path. Retention and redaction are tested at the durable
writer boundary. The final handoff records component evidence separately from
live-origin receipt, which is not authorized or available here.

## No-change decisions

- Do not add arbitrary callback URLs, commands, MCP/tool registration, or a
  general remediation engine.
- Do not create a second coordination transport or bypass the existing
  extmsg/session registration fence.
- Do not treat a response hash, queue/admission state, schema preview, or
  in-process fake as live origin receipt.
- Do not persist bearer credentials, private provider runtime state, or
  inferred authorization/owner data.
- Do not alter unrelated dirty home-worktree files, protected historical
  branches, deployment/runtime configuration, or human-held beads.

## Lost-information check after three passes

Retained: exact origin-delivery target, typed route identity, accepted attempt
and registration fences, idempotent replay after acknowledgement loss,
cross-conversation isolation, truthful refusal/failure, retention, TDD and
affected verification, upstream-safe boundaries, and component-versus-live
evidence limits. No critical acceptance requirement was dropped.

## Approved working contract

The implementation will consume the current typed destination/registration
identity from durable external-coordination state and deliver one exact
response or truthful failure through the existing extmsg/session boundary.
Durable handoff state will make response-ack and origin-ack loss recoverable,
with replay fenced by request/correlation/attempt/owner/registration identity
and no duplicate turn. The implementation starts after base preflight and
history confirmation; no human approval wait is introduced by the polecat
workflow.

## Execution findings and evidence boundary

- The first RED run reached the intended undefined handoff symbols under
  `CGO_ENABLED=0`; the default build path was separately blocked by the host's
  missing `unicode/regex.h` ICU header. The repository fast runner later passed
  all configured jobs.
- The implementation uses beads deterministic create for the request/attempt/
  correlation identity, stores a destination-qualified outbound idempotency
  key, and rejects a changed destination or response commitment. Conditional
  transitions fence claims and ambiguity; reconciliation never calls publish.
- Focused tests prove durable handoff state, destination isolation, concurrent
  enqueue idempotence, ambiguity reconciliation, recovery fencing, retention,
  and sanitized failure persistence. They observe the SDK/beads/extmsg seam,
  not a live provider receipt or origin application acknowledgement.
- No API/schema, CLI, deployment, provider-account, credential, or unrelated
  worktree changes were made.
