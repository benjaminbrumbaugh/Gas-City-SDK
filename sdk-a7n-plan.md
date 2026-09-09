# sdk-a7n implementation plan

counter=0

This is a continuation plan for the rescued implementation on `polecat/sdk-a7n`.
The implementation commit is already present; the remaining work is review,
hardening, evidence, and handoff.

## Full plan, tasks, and subtasks

1. Re-establish the boundary and current-state contract.
   - Read the external-coordination package, API control-plane guidance, and
     testing policy.
   - Inventory the existing implementation commit and the unstaged review fix.
   - Confirm the single configured target remains the only transport target.
2. Review opaque route identity.
   - Validate bounded, copied, replay-stable route data.
   - Reject URL-like and credential-like route keys/values.
   - Confirm route identity is carried as data and never used for target
     selection or authority.
3. Review durable callback outcomes.
   - Confirm the queued/submitted/uncertain/reconciled/responded/
     outcome-recorded/failed vocabulary and legal transitions.
   - Confirm idempotent replay uses immutable request/response bodies and
     persisted timestamps.
   - Confirm uncertain delivery cannot retry or fall back before reconciliation.
4. Review restart and stale-fence behavior.
   - Validate file-backed persistence and reload semantics.
   - Validate target rotation/removal fails closed without redirection.
   - Validate no credential or sensitive body leakage in durable state/logs.
5. Run focused tests, affected API tests, formatting, vet, build, and the
   repository's required pre-push gates as available.
6. Commit the final cohesive delta, push the polecat branch, and hand off to
   the refinery without closing the implementation bead.

## Architectural changes

- Keep route identity inside `internal/externalcoordination` as opaque,
  validated correlation data.
- Keep the configured target as the authorization and transport fence.
- Keep delivery state transitions and replay commitments in the domain package;
  API handlers remain projections and do not reimplement policy.
- Derive admission and delivery-time target snapshots through one API helper so
  configuration rotation cannot silently weaken the stale fence.

## Test plan and evidence layers

- Domain unit tests observe durable state-machine, sanitization, replay, and
  persistence behavior.
- API tests observe typed admission and detached delivery wiring, including
  target removal.
- Build/vet/gofmt observe compile and static correctness, not product
  semantics.
- Pre-push tests observe the repository's broader integration and ownership
  gates, not external recipient execution.

## Support structures

- `delivery_state.go` is the single transition vocabulary.
- `route_identity.go` is the single route-data validation/copy boundary.
- Existing file-backed service storage is the restart evidence seam.
- `agent-execution.log` records completed subtasks and remains temporary.

## Docs

- No product documentation change is required by this implementation review.
- This root plan records the architectural, evidence, and handoff decisions.

## Execution order and stability strategy

Review the domain boundary first, then API wiring, then tests and gates. Preserve
the existing passing implementation; make only narrow changes that close a
demonstrated gap. Never use credentials, callback URLs, or durable logs as test
fixtures. Use deterministic clocks/IDs where the existing seams permit them.

## Blocker avoidance

- Do not rebuild rescued code or touch another worktree.
- If lint/codegen contention occurs, wait for the lock and rerun; do not bypass
  hooks with `--no-verify`.
- If cgo-only tooling is unavailable, record the exact failing layer and run
  every non-cgo gate independently.
- Do not close the implementation bead; push and reassign it to the refinery.

## Candidate parallel work

- Independent review of route sanitization and credential leakage.
- Independent review of state transitions and restart/reconciliation semantics.
- Independent review of API target-fence wiring and generated-wire impact.

## Planning pass 1: boundary inventory and critique

The target truth is: an authorized opaque route can survive enqueue and
delivery while only the one configured target is used, and a callback outcome
can survive retries and restarts without ambiguity or secret persistence.

The required evidence layer is the domain package for state and persistence,
plus the API package for target-fence wiring. A compile pass or JSON preview is
not sufficient evidence of these behaviors.

Useful-but-insufficient proxies include a passing serialization test, a passing
adapter call count, and a semantic API response snapshot. The tempting false
completion is to treat `route_identity` being present on a wire struct as proof
that it is opaque and non-authoritative, or to treat `submitted` as proof that
the recipient completed work.

Critique: the existing implementation has the right package boundaries and
dedicated tests, but the delivery-time target derivation must be checked for
nil/disabled configuration and exact parity with admission. Persisted times
must be asserted across reload, not inferred from process memory.

Critical evaluation of that critique: the nil configuration edge is a real API
boundary because detached dispatch can outlive admission. The domain tests
already own restart and stale-fence semantics, so adding duplicate broad tests
would not improve evidence. The review should therefore focus on the API
derivation helper and any missing exact assertions.

Roll-up: retain the existing domain evidence; add/keep one API regression for
removed configuration and verify the helper is the sole target projection.

Roll-up into tasks: inspect the current diff and run the focused package tests
before making further changes. No new abstraction is justified.

No-change decisions: do not add a second target, URL parser, provider-specific
route type, or API wire shape; do not alter existing terminal-state semantics.

## Planning pass 2: adversarial security and recovery critique

The target truth remains opaque routing plus durable, replay-stable outcomes.
Evidence must observe the durable record and reload path, while security tests
must inspect both durable serialization and diagnostic/log surfaces where the
package emits them.

Cheaper proxies are map-copy tests and substring checks. They are useful but do
not prove that an adapter ignores route identity for target selection. The
false-completion substitution is to accept arbitrary route values because they
are called opaque, or to retry an uncertain submission because a test double
returned no error.

Critique: bounded validation, deep-copying, and a single configured target are
necessary but not sufficient unless transitions make `uncertain` single-successor
and reconciliation preserve the original idempotency key. Response commitments
must reject divergent bodies rather than overwrite history.

Critical evaluation: these are independent obligations owned by domain tests;
the implementation already has named tests for restart and stale fences. The
remaining review must ensure tests assert the exact negative behavior, not just
the happy path.

Roll-up: preserve the strict route policy and transition table; inspect tests
for divergent replay, uncertain restart, credential absence, and target
rotation/removal. Change code only if a test exposes a gap.

Roll-up into tasks: run route/outcome tests and read their failures as the
authoritative next action; do not broaden the API surface.

No-change decisions: no credential-bearing fixtures, no callback URL storage,
no fallback after an unknown reconciliation, and no provider-specific logs.

## Planning pass 3: delivery and maintainability critique

The target truth is not merely green tests; it is a reviewable branch whose
implementation can track upstream with small ownership boundaries. Required
evidence includes diff inspection, formatting, vet/build, affected tests, and
the real pre-push hook where infrastructure permits.

Useful-but-insufficient proxies include `go test ./...` alone, a clean diff
without generated-artifact checks, or a test that only exercises the fake
adapter. The false completion is claiming external recipient behavior from an
in-process transport test.

Critique: the rescued implementation is intentionally concentrated in
`internal/externalcoordination`, but the current API hardening should remain a
small helper extraction plus one regression test. Generated wire artifacts must
remain unchanged unless the typed contract actually changed.

Critical evaluation: because `route_identity` already exists on the wire,
generated artifacts need verification rather than modification. The branch
handoff is part of correctness in this workflow, so branch metadata and push
verification are required evidence.

Roll-up: finish the two-file API hardening only if focused tests pass, then run
the affected and repository gates, record limitations, commit, push, and hand
off. Remove only temporary planning/execution artifacts if they are not meant
to ship; retain this plan because the task explicitly requires a plan file.

Roll-up into tasks: complete the test/evidence matrix, inspect secrets and
generated-file drift, then perform the mandated refinery handoff.

No-change decisions: do not alter unrelated worktrees, do not close the bead,
do not bypass pre-commit, and do not claim that in-process tests prove a live
external provider completed the requested work.

## Lost-information check after three passes

The refinement retained all original requirements: opaque route identity,
single-target authorization, explicit delivery states, replay-stable bodies,
persisted timestamps, no credentials, restart evidence, stale-fence evidence,
upstream-friendly boundaries, and refinery handoff. It did not lose the
distinction between domain persistence evidence and API wiring evidence.

## Approval checkpoint

Plan is complete and presented in this file for review. Under the assigned
polecat workflow, execution continues autonomously after this checkpoint.

counter=3
