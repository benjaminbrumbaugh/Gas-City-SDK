# sdk-8vo plan

counter: 0
status: draft

## Scope and target

Make the live API read sweep capability-aware. The fixture should explicitly
declare/configure the optional capabilities it intends to exercise, expect 200
only for those configured capabilities, and separately prove that unavailable
capabilities retain their typed 503 responses. Keep the change at the live
contract-test/fixture boundary; do not weaken production capability guards or
make the minimal fixture pretend to provide services it does not own.

## Pass 0 — full plan, tasks, and architecture

### Tasks and subtasks

1. Load current context and establish evidence baseline.
   - Inspect the live contract integration test and its fixture setup.
   - Identify the discovered GET sweep, optional capability routes, typed 503
     response contracts, and the earlier session-stop failure separately.
   - Search history for prior capability-aware sweep or fixture work before
     inventing a new mechanism.
2. Define the smallest boundary-owned test shape.
   - Represent capability availability from the fixture configuration or a
     typed expectation matrix, not from a broad "all GETs return 200" rule.
   - Keep routing and external-coordination capabilities distinct.
   - Ensure configured capability reads assert success and unavailable reads
     assert the typed 503 envelope/status.
3. Implement test-first.
   - Add/update tests that fail against the current unconditional sweep.
   - Implement only the fixture/sweep helpers needed to make those tests pass.
   - Preserve the existing session lifecycle coverage and investigate the
     process-already-finished stop error without masking it as capability data.
4. Verify and review.
   - Run focused integration tests with the fixture and owner-package tests.
   - Run the configured affected test command, then required broader quality
     gates in proportion to risk.
   - Review the diff for API-contract weakening, fixture leakage, and stale
     assumptions.
5. Submit through the feature-branch/refinery protocol.
   - Commit the coherent implementation and plan artifact.
   - Push `polecat/sdk-8vo`, record verification metadata, reassign to the
     refinery, and drain.

### Architectural changes

- No production API behavior change is intended.
- The live contract fixture/read-sweep boundary becomes capability-aware via
  explicit typed expectations. Capability ownership remains with the existing
  routing and external-coordination boundaries.
- Unavailable capabilities remain observable as typed 503 responses, so the
  test proves both the configured and unconfigured branches.

### Test plan and evidence discipline

- Target truth: the live API contract sweep must distinguish configured,
  reachable optional services from intentionally unavailable capabilities.
- Required evidence layer: the real live HTTP/API integration harness against
  the minimal fixture, including typed response decoding and status checks.
- Cheaper but insufficient proxies: owner-package unit tests, a static route
  list, a semantic fixture-only assertion, or a sweep that only checks status
  codes without decoding the typed 503 response.
- Tempting false-completion substitution: globally allowing 503 or removing
  optional endpoints from discovery. That would make the sweep green while
  failing to prove either configured capability success or the unavailable
  response contract.
- Residual-bug audit: even if the plan passes, the original bug could remain if
  the fixture declares capability state incorrectly, the sweep bypasses the
  live handler, the 503 assertion accepts the wrong payload, or session-stop
  failure is swallowed. Focused tests must guard each boundary.

### Supporting structures

- Prefer a small table/matrix of endpoint, capability, configured state,
  expected status, and typed response assertion over scattered conditionals.
- Use existing fixture configuration and response types; do not add a new
  generic capability framework for one integration test.
- Maintain `agent-execution.log` during execution and remove it before the
  final commit because it is temporary process evidence.

### Documentation

- Document the optional capability matrix in the integration test comments or
  test naming where future failures will be understandable.
- No user-facing documentation change is expected unless the current test
  contract reveals an inaccurate API contract statement.

### Execution order and stability strategy

Investigate -> write failing focused test -> implement boundary-owned matrix ->
run focused live test -> run affected tests/quality gates -> self-review ->
commit/push/refinery handoff. Keep the existing minimal fixture as the default
so tests remain deterministic; add only explicit, local capability setup where
200 responses are required. Avoid broad retries or sleeps. If the stop failure
is an independent pre-existing harness issue, preserve its diagnostic signal
and record it rather than changing expectations to conceal it.

### Candidate parallel work

No safe code-parallel split is planned: the live fixture, endpoint discovery,
and expectation matrix are one coupled boundary. Independent read-only history
search and package/test inspection may be done in parallel if tooling permits,
but all edits remain serialized in this worktree.

## Pass 1 — critique of every major section

- Scope: correct direction, but must distinguish a capability being configured
  from a service merely being reachable; configuration should be the source of
  truth and the live response should verify it.
- Tasks: history search and baseline need to happen before test edits; the
  session-stop failure must not be conflated with the sweep defect.
- Architecture: a test-only expectation matrix is preferable to production
  changes, but it must use existing typed contracts and avoid duplicating route
  ownership logic.
- Tests/evidence: status plus payload assertions are required; owner tests are
  supporting evidence only. The plan should require one explicit unavailable
  capability case even when configured cases pass.
- Supporting structures: a table-driven expectation helper is enough; avoid
  adding a new fixture abstraction unless existing setup cannot express state.
- Documentation: test-local contract documentation is sufficient unless a
  public contract is actually corrected.
- Execution/stability: the plan needs a clear criterion for whether the
  session-stop failure blocks submission: if reproduced on clean base and
  unrelated to the patch, preserve and report it; if caused by the patch, fix.
- Parallel work: no implementation split is safe; read-only investigation can
  be parallel but is not necessary to satisfy the scope.

## Pass 1 critique evaluation

The critique identifies the material risks: configuration must drive expected
success, the unavailable response needs independent proof, and the lifecycle
failure needs separate triage. It does not change the architecture. The
baseline and history steps already precede edits, but the plan should make the
failure classification an explicit decision gate and require response-body
type validation for both success and 503 paths where types exist.

## Pass 1 roll-up: revised critique

Retain the narrow test-boundary approach. Tighten the acceptance evidence to:
(a) configured routing/external-coordination reads prove live success,
(b) each unavailable counterpart proves the typed 503 response, and
(c) session-stop errors are either fixed when patch-caused or recorded as
independent baseline evidence, never hidden by a permissive sweep.

## Pass 1 roll-up: revised plan/tasks

Add an explicit pre-edit baseline comparison and a final matrix review. The
matrix must derive capability state from fixture setup, keep routing and
external coordination separate, and validate typed bodies rather than only
HTTP status. No production API change or broad capability abstraction is
authorized by this plan.

## Pass 1 no-change decisions

- No change to production 503 semantics.
- No change to the minimal fixture's default absence of optional services.
- No new generic capability interface.
- No parallel edits or broad test-expectation relaxation.

## Pass 2 — critical evaluation of the refined plan

- The target/evidence distinction is sound, but the live test must discover
  routes through the same mechanism as production; hard-coded route lists can
  miss future optional GETs.
- The expectation matrix should be keyed by a stable route identity or the
  existing route metadata, not fragile display text.
- Configured capability setup must register the real boundary adapter for
  external coordination and start/use the real routing service expected by the
  handler; a fake flag alone would be proxy evidence.
- The unavailable proof should exercise the minimal configuration in a
  separate subtest or fixture instance so configured state cannot leak.
- The plan mentions quality gates but should explicitly include the project
  required Go vet and focused integration command while honoring integration
  build tags and documented sharding.

## Pass 2 critique evaluation

These refinements protect against a second false completion: an expectation
matrix that is internally consistent but disconnected from live routing. The
implementation should reuse existing fixture builders and adapters, and only
introduce explicit setup if the current harness lacks a supported path. The
separate unavailable subtest is important for proving absence rather than
testing a configured service that happens to fail.

## Pass 2 roll-up: revised critique

Keep discovery-based sweep coverage, but attach expectations by the endpoint's
existing capability classification or typed route metadata. Require real
boundary setup for configured cases and a fresh minimal fixture for 503 cases.
Add a quality-gate checkpoint for focused integration coverage, `go vet ./...`,
and the repository's documented fast/sharded test command as appropriate.

## Pass 2 roll-up: revised plan/tasks

During investigation, identify whether the sweep already exposes capability
classification. If it does, extend that path; if not, add the smallest
test-only classification at the read-sweep boundary without changing the API.
Use separate fixture lifetimes for configured and unavailable assertions when
needed. Treat route discovery, real adapter/service ownership, typed payload
validation, and session lifecycle diagnostics as acceptance requirements.

## Pass 2 no-change decisions

- Do not remove optional endpoints from discovery.
- Do not convert intentional 503s to 200s by adding hidden defaults.
- Do not use a fake adapter/flag where the handler requires a registered
  adapter.
- Do not broaden the change into product dashboard or API production code.

## Pass 3 — lost-information check and final plan

The refinement preserved all original requirements: explicit optional-capability
configuration, 200 only when configured, typed 503 proof when unavailable,
separate handling of the session-stop baseline failure, history-first
investigation, test-first edits, and branch/refinery handoff. It added the
important layer checks: live route discovery, real boundary setup, fresh
fixtures for absence, and typed response-body validation. No task, risk, or
stability requirement was lost.

Final acceptance checklist:

- [ ] Current live sweep and fixture code/history inspected.
- [ ] Failing test captures configured-vs-unavailable expectation.
- [ ] Configured routing case uses the real required service boundary.
- [ ] Configured external-coordination case includes the real registered
      adapter/configuration required by the handler.
- [ ] Unavailable capability case uses minimal configuration and proves typed
      503 response separately.
- [ ] Session-stop failure classification is preserved and documented.
- [ ] Focused live integration tests pass.
- [ ] Affected tests and required quality gates pass; evidence layers are
      recorded in the handoff.
- [ ] Working tree is clean, commit is pushed, and work bead is handed to the
      refinery without closing it.

counter: 3
status: refined
