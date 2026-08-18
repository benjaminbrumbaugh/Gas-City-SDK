---
title: SDK Contract Gap Audit
description: Evidence map for typed execution and lifecycle facts, redacted export, provenance, and advisory consumers.
---

## Status and scope

This is the Gas City SDK migration-wave audit for `sdk-8ps`, captured from
commit `4e451c8de` before this documentation-only commit. It records the
contracts that already exist, the layer that owns each fact, and the limits a
future Wayfinder advisory consumer must preserve.

The audit is an evidence artifact. It does not create a new runtime contract,
route work, choose a provider, admit work, or cut over any consumer. Gas City
remains the authority for work admission and lifecycle mutation. A Wayfinder
consumer may compare and advise from versioned inputs; it must not read Gas
City stores or infer a mutation authority from an advisory result.

The current repository has two different kinds of contract:

1. `pkg/eventexport` is a public, versioned, redacted event wire contract.
2. The execution, session-lifecycle, API-run, and worker-history types are
   committed implementation contracts under `internal/`. They are authoritative
   inside Gas City, but they are not yet one published cross-repository
   Wayfinder input contract.

That distinction is the primary contract gap. This audit documents it rather
than inventing a new package or silently treating internal structs as a stable
wire format.

## Source-of-truth chain

Gas City has a deliberate chain from durable facts to consumer projections:

```text
graph/work stores and provider transcripts
        │
        ├─ executionevent: authoritative graph.v2 facts
        ├─ session: persisted metadata + observed runtime/config facts
        ├─ worker: normalized transcript evidence + provenance
        └─ events: append-only activity record
                 │
                 ├─ api: typed local run/session projections
                 └─ eventfeed → eventexport: redacted remote event batches
```

The branches are not interchangeable. A run projection is not a live provider
observation; a lifecycle view is not a transcript; and a redacted event batch
is not a screen capture or proof that a consumer's route would be correct.

## Committed type inventory

| Surface and types | Authoritative input and truth | Safe interpretation | It does not prove |
| --- | --- | --- | --- |
| `internal/events.Event`, `internal/events.TaggedEvent` | The append-only city event provider; `Event` carries sequence, type, timestamp, actor, subject, optional message/payload, and typed run/session/step fields. | An activity record. `RunID`, `SessionID`, `StepID`, and topology are typed correlation/fact fields stamped at the record site. | That `Message` or `Payload` is safe to export. They can contain titles, paths, metadata, or other free-form data. |
| `internal/executionevent.WorkAssociation`, `StepDefinition`, `Projection` | `ProjectCurrent` reads the graph store for the graph.v2 root and physical steps, and the work store for the root's input-convoy tracks. | The deterministic current-store execution topology: physical work bead → run root, physical step bead → semantic step ID, and validated native dependencies. | Provider liveness, successful event delivery, or the eventual outcome of a step. |
| `internal/executionevent.LifecycleEvent` and `EmitCompletedFromClosedNotification` | A physical bead-close notification plus a graph-store lookup validates graph.v2 root, step ownership, session identity, and native step metadata. `ReconcileCompleted` repairs a missing close fact from durable closed steps. | A typed `execution.step_started` or `execution.step_completed` fact for a native graph step. | A completion event for a non-native bead, a guessed dependency transition, or a transcript-level assertion about what the provider did. |
| `internal/session.BaseState`, `DesiredState`, `RuntimeProjection`, `IdentityProjection`, `LifecycleBlocker`, `WakeCause`, `RuntimeFacts`, `LifecycleInput`, `LifecycleView` | `ProjectLifecycle` consumes persisted session metadata plus separately observed runtime, named-identity, config, wake-cause, and clock facts. The projection itself performs no runtime I/O. | A typed local lifecycle interpretation, including terminality, blockers, identity posture, desired posture, and whether runtime facts were observed. | A command to wake, stop, route, or retry. `RuntimeProjectionUnknown` is not proof that a runtime is absent. |
| `internal/worker.Provenance`, `HistoryEntry`, `HistoryBlock` | Provider-native transcript records normalized by the worker/session-log adapter. `Provenance.Derived` distinguishes provider identity from a Gas City synthesized identity. | Evidence about transcript normalization, source provider, stream, and entry identity. | Orchestration truth. `TranscriptPath`, `Raw`, raw IDs, file paths, tool input, and transcript text are not a remote advisory contract. |
| `internal/api.RunStatus`, `RunStepStatus`, `Run`, `RunStep`, `RunStatusCounts` | The API run projection folds `.gc/events.jsonl` through `runproj`; list/census responses also expose whether the projection is partial or warming. | A typed local API view of run and child-bead status. Closed enums are suitable for exhaustive client branching within that API version. | A complete global census when `Partial` is true, provider liveness, or a stable cross-repository Go import. |
| `internal/api.RunsListOutput`, `RunsCensusOutput` | The run projection source and its bounded fold. `PartialErrors` are sanitized public reasons, not the local error detail. | Counts plus an explicit completeness signal. A consumer must preserve `Partial` and avoid treating an omitted row as non-existent. | That an empty or limited row set means no work exists. Counts are independent of the list row limit, but a partial projection remains incomplete. |
| `eventexport.TaggedEvent`, `Envelope`, `Batch`, `Options`, `Exporter` | `eventfeed.toExport` copies only typed event fields into `TaggedEvent`; `ProjectEvent` validates the closed set; `Exporter` sends acknowledged per-city batches. | The public redacted event wire. `SchemaVersion` is 5, `CityHash` is a salted partition key, and optional correlation fields join opaque event facts. | A lifecycle snapshot, transcript, route recommendation, or live screen/provider observation. |

### Execution truth in detail

`executionevent.ProjectCurrent` is the authoritative graph execution
projection for a graph.v2 workflow root. It requires the root to have the
workflow kind and graph.v2 formula contract, then reads physical steps from
the graph store. If the root names an input convoy, work associations come
from that convoy's tracking dependencies in the work store. It sorts physical
IDs and validates native dependencies before producing repeatable facts.

`StepDefinition.DependsOnStepIDs` has an intentional three-state contract:

- `nil` means native topology is unknown or could not be represented;
- a present empty slice means a known root step with no dependencies; and
- a non-empty slice is known, strictly sorted, unique topology.

The same distinction is carried by `events.Event` and `eventexport.Envelope`.
It must survive serialization; collapsing `nil` and `[]` changes execution
truth.

Step lifecycle facts are narrower than topology. `LifecycleEvent` accepts only
`execution.step_started` and `execution.step_completed` for a physical step
owned by an authoritative graph.v2 root. The close-side producer consumes the
physical snapshot from `bead.closed`; it does not infer completion from a
dependency graph. The reconciliation pass is an idempotent repair for a
durable close whose best-effort event append was lost, not a second source of
step policy.

### Session lifecycle truth

`LifecycleInput` is the typed input boundary for lifecycle projection. Its
metadata-derived fields are deliberately limited to the persisted keys the
projection reads. Runtime and configuration observations remain explicit
external facts. `LifecycleView` is therefore a derived interpretation, not
another durable state store.

Consumers must preserve these distinctions:

- `BaseState` describes persisted/compatibility state, while `DesiredState`
  describes what the orchestrator wants for an identity.
- `RuntimeProjectionUnknown` means no runtime fact was observed. It must not be
  converted into `missing` or `dead`.
- `IdentityConflict` and lifecycle blockers are diagnostics that explain why a
  desired posture may not be realizable; they are not route instructions.
- `Terminal`, `CountsAgainstCap`, and `ReconciledState` are projection outputs
  useful for local display and diagnosis, not authorization to mutate beads.

### Run and transcript projections

The canonical API `Run` is folded from the append-only event log. `RunStatus`
and `RunStepStatus` are closed projection enums; they are not a raw provider
state vocabulary. For point reads, an incomplete projection returns a service
unavailable response rather than a false not-found. For step reads, a terminal
run clamps stale child activity so a lost close event cannot leave a completed
run displaying an active child forever.

`HistoryEntry` is a separate evidence surface. It normalizes provider-native
transcripts and preserves `Provenance` for diagnosis. A synthesized entry or
block is marked `Derived`; that flag must not be discarded when comparing
provider evidence. Transcript content can contain secrets, paths, prompts, and
tool arguments, so it is not a substitute for the redacted event contract.

## Redaction, omission, and provenance contract

The producer and receiver have different responsibilities. The producer
(`eventfeed` plus `eventexport`) must project from a closed typed set and fail
closed. A receiver must run `ValidateEnvelope`/`ValidateBatch` against the
wire-authoritative rules without access to producer options.

| Source value | Wire representation | Omission / provenance rule |
| --- | --- | --- |
| City name | `Batch.CityHash` | Salted, non-reversible 16-hex partition key. The cleartext city name never crosses the boundary. |
| Actor | `Envelope.ActorHash` | Salted 16-hex correlation token. It is not an anonymity guarantee; a weak/known salt makes a small actor namespace brute-forceable. A blank actor stays absent. |
| Event subject | `Envelope.Ref` only for allowlisted ref-bearing types | `safeRef` accepts only an opaque lowercase ID/slug shape and a 64-byte maximum. Paths, hosts, names, and free text are omitted. `ExportRef` is a producer option and is not on the wire. |
| Run/session identity | `run_id`, `session_id` | Typed fields are emitted only when `EmitCorrelation` is enabled and the value is an opaque ref. Invalid values are dropped; they are never truncated or copied from `Payload`. |
| Native step identity | `step_id` | Nonblank valid UTF-8 up to 256 bytes. This domain is intentionally broader than opaque run/session refs because it preserves native semantic step IDs. |
| Native dependencies | `depends_on_step_ids` | `nil` is unknown; `[]` is a known root; non-empty values are sorted, unique, valid, and self-dependency-free. Malformed topology fails closed. |
| Title/formula | `title`, `formula` | Free-form content is the explicit exception to the envelope-only default. The content gate is package-private and unreachable through the current `Exporter`; current end-to-end export cannot emit it. If a future producer makes it reachable, it must own the schema-version and receiver-coordination decision. |
| Mail event | `mail.sent` envelope | Reduced to `seq`, `type`, and `ts`; addressing, actor, subject, body, and payload are omitted. |
| Event `Message`/`Payload` | No wire field | `eventfeed.toExport` never reads them. A run ID hidden in bead metadata or a title hidden in a payload remains absent. |
| API partial reason | Sanitized `partial_errors` reason | Internal paths, raw errors, and error prose stay server-side. `Partial` is the completeness fact; a reason is explanatory, not a route recommendation. |
| Worker provenance | Not part of `eventexport` | Provider, transcript path, raw entry ID, raw type, and `Raw` remain local evidence. `RawRecordID` is explicitly excluded from JSON. |

The allowlist is default-deny and is mirrored by
`internal/eventfeed/allowlist_drift_test.go`. The event-export package does not
import `internal/events`, preserving the package boundary; the adapter test is
the drift proof between canonical event constants and the exported wire list.

The practical rule for future consumers is: an omitted field is an explicit
absence/unknown state unless the owning contract says otherwise. Do not turn a
missing `run_id`, `session_id`, `step_id`, topology pointer, provenance field,
or partial row into a negative fact about execution.

## Wayfinder advisory inputs

The future Wayfinder integration should consume only a versioned adapter over
these evidence classes:

| Advisory input class | Use | Required guard |
| --- | --- | --- |
| Validated `eventexport.Batch` | Compare observed event activity, opaque run/session joins, native step topology, and export freshness/cursor behavior. | Validate schema version, city hash, event type, opaque refs, lifecycle-field combinations, and topology before use. Preserve omission states. |
| A future versioned subset of run projection fields | Display run status, step status, counts, and projection completeness. | Carry `Partial`/warming explicitly. Do not import `internal/api` or treat a missing row as a negative fact. |
| A future versioned lifecycle-advisory subset | Explain persisted session posture, observed liveness, blockers, identity conflicts, and wake causes. | Carry observation provenance and distinguish unknown from missing; treat the result as advisory only. |
| Explicitly redacted transcript evidence, if later approved | Explain why a run/session projection is degraded or compare evidence quality. | Exclude paths, raw provider payloads, prompts, secrets, and tool arguments; preserve `Derived`/unknown markers. |

Wayfinder may use these inputs for advisory recommendations, evidence views,
usage/cost context, trends, simulations, or shadow comparisons. It must not
consume Gas City stores or provider internals, invent a target, choose routing
policy, admit fresh work, spawn/stop a session, or execute a step. Unsupported
readiness, capability, policy, or evidence input is fail-closed. Any future
mutation path requires a separately versioned contract, current-manifest
approval, and evidence gates.

There is no committed SDK type today that combines all four classes into a
published Wayfinder contract. In particular, `internal/session.LifecycleView`,
`internal/api.Run`, and `internal/worker.HistoryEntry` are useful owners inside
Gas City but are not cross-repository imports. `pkg/eventexport` is public and
versioned, but intentionally contains redacted events rather than a lifecycle
or transcript model. This is the bounded exposure gap for the next migration
slice; this bead does not resolve it by adding a speculative public API.

## Evidence ownership and false-completion checks

The existing tests provide useful layer-specific evidence:

| Evidence artifact | Truth it proves | Layer observed | It does not prove |
| --- | --- | --- | --- |
| `internal/executionevent/projector_test.go`, `lifecycle_test.go` | Store projection and native graph-step lifecycle gates are deterministic, ordered, and reject invalid/non-native facts. | Graph/work store and event-fact projection. | Every live claim/close producer, provider behavior, or remote transport. |
| `pkg/eventexport/project_test.go`, `golden_test.go`, `validate_test.go` | Closed-field projection, omission, topology tri-state, redaction, receiver validation, schema version, and allowlist behavior. | Dependency-light projection and wire validation. | The supervisor adapter, endpoint authorization, receiver implementation, or a screen capture. |
| `internal/eventfeed/muxsource_test.go`, `allowlist_drift_test.go` | The adapter forwards typed correlation/topology, clones topology, refuses payload/message promotion, and tracks event constants. | Supervisor event-to-export boundary. | A production sink's policy or a provider's event-recording call site not exercised by the fixture. |
| `internal/session/lifecycle_projection_test.go` and session manager tests | Persisted metadata, observed runtime facts, blockers, and desired state project into the typed lifecycle view. | Local session lifecycle projection and runtime seam. | A live machine probe in every deployment or any Wayfinder decision. |
| `internal/worker/normalize_entry_invariants_test.go`, history/conformance tests | Provider identity, synthesized identity, derived blocks, and transcript continuity are retained in normalized history. | Provider transcript normalization. | Orchestration completion or safe remote redaction. |
| `internal/api/huma_handlers_runs_test.go` and run launch/cancel tests | Run status, partial/warming behavior, point-read semantics, terminal clamps, and cancellation outcomes are exposed through the typed API. | Local API projection and store composition. | The public cross-repository compatibility of internal Go types or live provider liveness. |

The focused audit runs passed for `./pkg/eventexport`, `./internal/eventfeed`,
`./internal/executionevent`, `./internal/session` lifecycle tests, and the
selected `./internal/api` run/provenance/history tests. The CGO-backed packages
required the repository-documented ICU include/library paths. The successful
tests are package-level evidence, not proof of a live Wayfinder consumer or
screen capture.

The original integration bug could still remain after every listed test passes
if a producer stamps the wrong typed run/session ID, if a new producer bypasses
`eventfeed.toExport`, if a receiver collapses unknown and absent values, or if a
future adapter treats an advisory result as routing authority. Those risks are
why this audit records source ownership and explicitly declines a cutover.

## Gap verdict and no-change decisions

| Finding | Verdict | Action in `sdk-8ps` |
| --- | --- | --- |
| Execution topology and native step lifecycle have typed owners and bounded tests. | No objective local gap found. | No code or test change. |
| Redacted export has a closed source set, default-deny allowlist, topology validation, schema version, and adapter drift/leak tests. | No objective local gap found. | No code or test change. |
| Session and transcript projections preserve unknown/derived/provenance distinctions. | No objective local gap found in the audited owners. | No code or test change. |
| Wayfinder lacks one published, versioned, typed lifecycle/advisory contract spanning these surfaces. | Objective exposure gap, but cross-repository API design is not settled by this bead. | Document the gap; do not invent a public type or adapter. |
| Routing, admission, provider credentials, spawning, and cutover remain outside the SDK audit. | Explicit boundary. | No routing policy, no other-repository edit, no cutover. |

No bounded contract test was added. Adding a test for the absent Wayfinder
contract would manufacture field semantics before the public versioned boundary
and receiver behavior are approved. The current tests already own the settled
SDK invariants at their smallest layers.
