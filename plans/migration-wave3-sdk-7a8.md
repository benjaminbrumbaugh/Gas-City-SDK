# Migration Wave 3: redacted execution-truth fixture contract

Counter: 3

## Full plan, tasks, and subtasks

1. Establish the fixture boundary and baseline.
   - Keep the work isolated to branch `migration/wave3-sdk-7a8` and the
     requested worktree.
   - Re-read the execution-truth audit and existing owners before adding any
     type or fixture.
   - Run focused baseline tests for `pkg/eventexport`,
     `internal/executionevent`, `internal/session` lifecycle projection, and
     `internal/worker` provenance normalization.
2. Define a deterministic internal shadow-replay fixture corpus.
   - Add a small owner-local package under `internal/testfixtures` rather than
     extending the public event-export or internal production contracts.
   - Model only typed, redacted execution envelopes plus explicit observation
     provenance and freshness/unknown state; never include event payloads,
     messages, transcript text, paths, raw provider IDs, or credentials.
   - Preserve the execution topology tri-state (`nil` unknown versus present
     empty root versus populated dependencies) and lifecycle/provenance
     distinctions in the serialized fixture.
   - Use fixed timestamps and stable opaque references so replay is byte and
     order deterministic.
3. Add RED/GREEN contract tests and fixture goldens.
   - Validate every redacted batch with `eventexport.ValidateBatch` and assert
     the fixture corpus is free of forbidden free-form fields.
   - Cover authoritative/fresh execution facts, derived provenance, unknown
     runtime/topology, and stale observations without converting absence into
     a negative execution fact.
   - Assert JSON round trips preserve pointers, ordering, provenance, and stale
     timestamps; assert repeated loads produce independent values.
   - Keep tests at the fixture boundary; do not duplicate provider, database,
     or live runtime coverage.
4. Document usage and evidence limits, then verify and hand off.
   - Document what the fixture proves, which layer it observes, and what it
     cannot prove about live providers, consumers, or routing authority.
   - Run focused tests, affected package tests, `go vet` on affected packages,
     `git diff --check`, and inspect the staged file set.
   - Commit only worktree-owned source/tests/docs; record the commit and exact
     tests on the bead. Do not push, fetch, or edit another repository.

## Architectural changes and support structures

The fixture package is internal test support. It consumes the existing public
`pkg/eventexport` envelope/batch types and represents cross-layer provenance as
fixture metadata, without changing production event, session, worker, or API
schemas. The package is not a producer, receiver, router, admission path, or
provider adapter. A fixed corpus plus a loader/test oracle is the support
structure needed for deterministic shadow replay; no new abstraction is
justified outside that test boundary.

## Test and evidence plan

The fixture contract tests prove that a known, redacted, typed scenario can be
loaded and replayed deterministically, that wire envelopes remain receiver-
valid, and that unknown/stale/provenance markers survive serialization. They
observe the fixture serialization and redaction boundary only. They do not
prove graph-store projection, lifecycle mutation, provider liveness, event
delivery, screen state, or any downstream advisory decision. Existing focused
tests remain the owners of those lower-level contracts.

The proxy audit is explicit: a passing fixture test is useful-but-insufficient
evidence. The tempting false completion is to treat a valid fixture as proof
that a live provider emitted the same facts or that a shadow consumer would
choose a correct action. If this plan succeeds while the original bug remains,
the likely cause is a producer stamping the wrong identity, a consumer
collapsing unknown/stale into absent/fresh, or a future adapter bypassing the
redacted boundary.

## Execution order and stability strategy

Plan → baseline → RED fixture tests → smallest fixture/schema implementation →
golden/round-trip tests → affected package tests → vet/diff review → local
commit → bead comment/close. All timestamps, IDs, ordering, and expected JSON
are fixed. No sleeps, polling, live provider calls, network calls, or generated
dashboard/API changes are needed.

## Documentation plan

Add concise package documentation and a migration note describing the fixture
schema, its provenance and unknown/stale states, the redaction rules, and the
truth/evidence limits. Keep it under the SDK repository and outside the public
API navigation unless an existing docs owner requires otherwise.

## Blocker avoidance and candidate parallel work

The implementation is small and boundary-sensitive, so concurrent edits are
not useful. A read-only review can independently inspect eventexport validation
and existing lifecycle/provenance enums while the fixture is built. If the
desired fixture requires a new public cross-repository type, live provider
capture, or an unapproved freshness policy, stop at the boundary and report it
instead of widening scope.

## Planning pass 1 — initial critique

The plan must avoid inventing a second execution-truth source. Reusing
`eventexport.Envelope`/`Batch` keeps the redaction rules owned by the existing
wire package, while fixture-only provenance/freshness metadata supplies the
shadow comparator's missing evidence without changing production contracts.
The tri-state topology and unknown/stale cases are the highest-risk omissions;
they are explicit test obligations, not incidental examples. A fixture loader
must deep-copy or decode fresh values so one replay cannot mutate the next.

## Planning pass 2 — critical evaluation of the critique

The plan still needs to distinguish provenance from freshness: `derived` is a
source/evidence property, while `unknown` and `stale` describe observation
quality. They must not be represented by one overloaded string. The fixture
should therefore use typed fields with closed values and optional timestamps,
and should reject malformed combinations at load/validation time. The redacted
batch itself must remain validated by `eventexport.ValidateBatch`; fixture
metadata must never be mistaken for wire fields.

## Planning pass 3 — roll-up and no-change decisions

Apply the evaluation: use a fixture-local typed provenance enum and a separate
freshness enum, preserve `nil` versus empty topology through pointer fields,
validate fixed timestamps and opaque IDs through the existing eventexport
receiver validator, and include only safe references in JSON. Do not add a new
public package, modify schema version, alter event producers, add live/E2E
coverage, or touch another repository. No-change decision: the existing
production contracts and routing/admission behavior remain unchanged.

After three passes, no material plan information was lost: scope, ownership,
proxy limits, failure edges, redaction boundary, test order, and handoff
constraints remain explicit. The assigned worker may execute without an
interactive approval pause.
