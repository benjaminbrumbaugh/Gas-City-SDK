# Convoy callback contract v1 implementation plan

counter=0

## Scope and target truth

Define the stable Gas City-owned contract for lifecycle notifications about a
launched convoy. The contract must make launch origin opaque, identify the
convoy and (when applicable) a task, distinguish lifecycle phases, and remain
usable by any external harness without naming a provider, role, runtime, URL,
credential, or conversation implementation.

The contract is not a delivery implementation. It does not decide when to
retry, how to authenticate a recipient, how to discover a private runtime, or
how to deploy a rebuilt artifact. Those concerns belong to the owning
subscription, fan-out, transport, and deployment boundaries.

## Tasks and subtasks

1. Specify the versioned wire vocabulary.
   - Define the contract version and lifecycle event kinds.
   - Define required identity, timestamp, correlation, and opaque-origin
     fields.
   - Define which fields are required by each lifecycle phase.
2. Add contract tests first.
   - Pin JSON names and version values.
   - Cover each valid phase and phase-specific required identity.
   - Reject missing identity, unknown versions/kinds, malformed timestamps,
     embedded credentials/URLs, and non-opaque origin assumptions.
   - Prove old unversioned convoy events remain readable through an explicit
     compatibility adapter, while new callback records are versioned.
3. Implement the smallest pure validation/normalization package.
   - Keep it free of stores, event buses, transports, and runtime discovery.
   - Preserve unknown optional JSON fields for forward-compatible readers only
     at the wire boundary; do not make validation permissive on required data.
4. Document ownership and compatibility rules beside the contract.
5. Run focused tests, formatting, vet for the package, and inspect the diff for
   accidental role/provider/private-runtime leakage.

## Architectural changes

Add one internal contract package for callback lifecycle records. The package
owns only schema constants, typed records, phase-specific validation, and the
legacy-event compatibility adapter. Sling, convoy, events, API, and external
coordination code may depend on the contract, but the contract imports none of
those layers.

The launch-origin field is an opaque value captured at ordinary `gc sling`
launch time by a later implementation task. This contract does not parse or
derive it. Convoy creation, task acceptance, closure, deployment, and
ready-for-rebuild are separate phases; no phase is inferred from another.

## Test plan and evidence boundaries

The unit contract tests prove only schema shape, validation, compatibility, and
normalization at the contract layer. They do not prove ordinary sling captures
origin, that convoy lifecycle emitters fire, that a recipient is authorized,
that a callback is delivered, or that a rebuild/deployment succeeds. Those
truths require later coordination, event, transport, and deployment evidence.

Run the package tests and `go vet` for the package. Run the repository fast
test target after implementation if the shared worktree permits it.

## Support structures

Use typed constants and a single validation entry point. Keep phase-specific
requirements in a table or switch owned by the package so downstream fan-out
does not duplicate string comparisons. Use deterministic timestamps and IDs in
fixtures. Do not add a fake transport or callback server to this contract
package.

## Documentation

This plan is also the contributor-facing design record. The package doc must
state the version, compatibility behavior, and non-goals. Public wire fields
must have comments explaining whether they are durable identity, lifecycle
state, or opaque route data.

## Execution order

Write RED tests → observe the focused failure → add the pure contract types and
validator → make tests GREEN → refactor names/comments → run focused gates →
review the boundary and diff → commit only task-owned files.

## Stability and blocker avoidance

Do not touch the already-dirty unrelated files in the worktree. Do not change
existing `convoy.created` or `convoy.closed` event payloads in this task.
Represent legacy compatibility explicitly rather than silently treating a
missing version as a current callback record. If a downstream expected name is
unclear, prefer names derived from the neutral lifecycle vocabulary in this
document and record the decision in the package doc.

## Candidate parallel work

The contract tests and package design are coupled and should stay together.
After this contract lands, subscription ownership/fencing, sling-origin
capture, and lifecycle fan-out can proceed independently against it. An
independent review can audit security leakage and backward compatibility after
the focused tests pass.

## Pass 1 — initial plan review

The scope is intentionally limited to a pure contract boundary. The main risk
is under-specifying the phase distinctions, so the test matrix must assert each
phase independently. The legacy adapter must be explicit because accepting
missing versions as v1 would make a compatibility path indistinguishable from
new callback data. The plan correctly separates target truth from cheaper
contract evidence.

Proxy audit: target truth is that later producers and consumers can exchange a
stable lifecycle record. Required evidence here is typed validation and JSON
round trips. A passing unit test cannot prove sling capture or delivery. The
tempting false completion is to add only constants or a passing parser. If this
plan fully succeeds while the original bug remains, the missing bug would be
in producer wiring or recipient fan-out, which this task explicitly leaves to
their owning beads.

## Pass 2 — critique of the critique

The first review is sound but misses one boundary: route identity must be
opaque without becoming an unbounded arbitrary string that can carry unsafe
wire material. Add bounded, non-empty opaque-token validation while avoiding
semantic parsing. It also needs a precise forward-compatibility rule: unknown
optional fields are tolerated by JSON decoding, but unknown lifecycle kinds and
versions are rejected by the v1 validator. Add both to tests and docs.

No-change decisions: do not add callback URLs, authorization fields, provider
names, role names, deployment actions, or a transport interface. Do not reuse
the HCA request queue; its durable request lifecycle is a different boundary.

## Pass 3 — critical evaluation of the revised plan

The revised plan now covers identity boundedness and forward compatibility. A
remaining risk is conflating a route identity with launch origin. Keep them as
separate opaque fields: origin identifies the launch actor/context, while route
identity is optional opaque data for an authorized downstream route. Neither
may be interpreted by the contract package. Require event identity,
correlation, convoy identity, origin, and occurrence time for callback records;
require task/deployment identity only for their corresponding phases.

Proxy audit: a validator can prove the record is safe and complete at its
boundary, not that the opaque values are truthful or that a caller is
authorized. The plan must retain this limitation in the final package docs so
future tests do not claim more than they observe.

## Roll-up: applied evaluation

The implementation will use distinct `Origin` and `RouteIdentity` fields,
bounded opaque-token checks, and an explicit `DecodeLegacyEvent` adapter. The
v1 validator will reject unsupported versions and event kinds, require
phase-specific IDs, and ignore unknown JSON fields only through standard
forward-compatible decoding. No producer or delivery behavior will be added.

## Roll-up: final tasks and subtasks

1. Add RED contract tests for constants, JSON shape, phase requirements,
   bounded opaque fields, legacy adaptation, and forward-compatible unknown
   fields.
2. Add the pure versioned contract package and validator.
3. Add package documentation covering ownership, compatibility, and evidence
   limits.
4. Run focused tests and vet, inspect the task-owned diff, commit, and record
   verification in the bead.

## Explicit no-change decisions

- No edits to existing convoy event payloads or event registration.
- No sling, convoy, API, transport, HCA, or deployment implementation.
- No Hermes, Gas Town role, provider, URL, credential, or private-runtime
  identifier in code or contract fields.
- No claim that contract tests prove launch capture, authorization, delivery,
  rebuild, or deployment.

## Lost-information check after three passes

The refinement retained all requested concerns: versioning, lifecycle phase
distinctions, ordinary sling-origin capture as a downstream producer contract,
legacy compatibility, validation, support structures, execution order,
stability, blocker avoidance, and candidate parallel work. It added bounded
opaque identity handling and a clear forward-compatibility rule without
expanding into delivery or deployment implementation.

counter=3
