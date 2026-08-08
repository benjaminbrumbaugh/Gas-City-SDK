# Durable routing decisions

## Status and boundary

Gas City stores signed route decisions in a local, schema-versioned bbolt
ledger:

```text
<city-root>/.gc/routing-decisions.db
```

The controller is default-deny. Its verifier is an explicitly injected
dependency whose production value remains nil in this integration. Neither a
ledger record nor an authority file enables routing by itself. Enabling a
production signer and verifier remains a separate key-custody decision.

An admitted decision only adds the existing route metadata to fresh, ready
work. It does not launch or stop an Agent, invoke Sling, change capacity,
select an alternative, migrate active work, or contact a provider. Normal
controller demand reconciliation remains the only downstream session-launch
path.

## Trust inputs

The ledger is an owner-only regular file with exact mode `0600`. Opening uses a
bounded lock wait and a no-follow file opener. A missing ledger may be created;
an unsafe file, future schema, malformed record, corrupt index, or failed
signature is refused.

Public verification keys are separately managed at:

```text
<city-root>/.gc/routing-decision-authorities.json
```

The authority file must be an owner-only regular file (`0400` or `0600`) and is
opened without following symlinks. Its strict schema accepts only an exact
schema version and a bounded list of canonical lowercase authority IDs with
base64-encoded Ed25519 public keys. Unknown, duplicate, case-variant, malformed,
or trailing JSON is rejected. Gas City never creates this file and never loads
it into the running controller automatically.

## Signed contract

A decision payload binds the following admission facts:

- caller decision ID and a distinct SHA-256 binding ID;
- work bead ID, exact revision, claim fence, and ownership-state digest;
- City, Rig, canonical static target, and full resolved target-config digest;
- policy and observation digests;
- model, source, account, serve-as, provider, endpoint, reason, evidence,
  alternatives, and typed options used for audit;
- creation and expiry times; and
- `no_migration=true`.

The binding ID hashes only the immutable work/scope/target admission binding,
independently of the caller decision ID and explanatory audit fields. The
signed decision still includes both IDs and every audit field.

Canonical schema structs fix field order, normalize nil collections to empty,
sort typed options by key, preserve alternative order, encode full lowercase
SHA-256 values, and render timestamps as fixed-width UTC nanoseconds. Signing
bytes use separate versioned domains and length framing for the canonical
decision and approval payloads. The approval binds the decision ID, binding
ID, authority ID, and approval time. Only Ed25519 is accepted.

## Ledger model

The database contains schema metadata, immutable decisions, a state/expiry/ID
index, request-bound idempotency receipts, transition audit records, and legacy
import receipts. It maintains a global store revision and an independent
record revision used for lifecycle compare-and-swap.

The exact lifecycle graph is:

```text
proposed ──> approved ──> admitted ──> claimed
    │           │             └──────> outcome_recorded
    │           ├────────────> refused_after_race
    │           ├────────────> revoked
    │           └────────────> expired
    ├──────────> revoked
    └──────────> expired
```

All other edges are invalid. `claimed`, `outcome_recorded`,
`refused_after_race`, `revoked`, and `expired` are terminal in this slice. The
store exposes `admitted -> claimed` and `admitted -> outcome_recorded`, but the
controller does not infer either fact without an unambiguous owning event.

Active-approved queries seek directly into the state/expiry index, return a
bounded deterministic result, and reverify every signature. A bounded expiry
sweep moves due proposed or approved records to `expired`. Payload bytes never
change during a lifecycle transition.

## Controller admission

With an injected verifier, each controller pass first expires due records, then
considers at most 256 active approved decisions. Immediately before mutation it
rechecks:

1. signature, lifecycle revision, and half-open validity window;
2. exact City and Rig scope;
3. canonical enabled static target identity and its current full config digest;
4. live work revision, claim fence, state digest, open/unassigned state, and
   absence of route, session, execution, deferred, continuation, or structural
   ownership metadata; and
5. raw readiness, including dependency, block, and defer state.

The decision store holds its writer transaction across one bounded bead
callback. This serializes approval recheck and the final lifecycle transition
against revoke or expire. The bead store must expose the existing
readiness-and-revision conditional writer; unsupported stores fail closed.

One successful bead compare-and-swap writes exactly:

```text
gc.routed_to
gc.run_target
gc.routing_decision_id
gc.routing_decision_claim_fence
```

The ledger then records `admitted`. A proved freshness/readiness race records
`refused_after_race`. Integrity, verifier, or storage failures do not get
converted into lifecycle refusals, and controller logs contain only fixed,
sanitized summaries.

The ledger and bead store cannot share a physical transaction. After a
successful stamp, the controller records the exact post-write bead revision.
If the ledger commit fails, it clears only those four values through another
readiness/revision CAS when the bead still has that exact revision, marker,
target, and fence. If exact compensation cannot be proved, the marker remains;
the controller never guesses at a newer revision.

## Restart recovery

An exact `approved` plus already stamped marker/target/fence is the recognized
crash window. After rechecking the signature, scope, time, target-config digest,
and unchanged ownership projection, the next admission pass records `admitted`
without rewriting the bead.

If an admitted carrier later has only `gc.routed_to` missing, recovery may copy
the exact `gc.run_target` back through the readiness/revision conditional
writer. It requires the same unexpired authentic admitted record, decision
marker, target, original claim fence, scope, target-config digest, and otherwise
empty ownership projection. Missing, proposed, approved-but-unrepaired,
refused, revoked, expired, unverifiable, changed, owned, or unready work remains
untouched.

Legacy unmarked carried-route recovery retains its established compatibility
behavior. It never grants authority to a decision-marked carrier.

## Explicit legacy import

The retired flat file is recognized only by the offline importer at its exact
old path:

```text
<city-root>/.gc/routing-intents.json
```

The controller never reads that file. The strict legacy parser is reachable
only through:

```text
gc route-decision import-legacy --city <city>
```

The hidden local command requires the controller lock, rejects remote or Rig
selection, and reads current config/work only to enrich every candidate with an
exact revision, claim fence, work-state digest, canonical target, and target
config digest. All records must still be fresh, open, unassigned, unowned, and
ready; otherwise nothing imports.

The import transaction creates every record as unsigned `proposed` and stores a
receipt keyed by the exact source digest. A legacy `approval_id` is audit text,
never an approval or signature. After commit, the exact source is renamed
without replacement to a digest-bearing archive. A retry after an archive
failure uses the committed receipt and performs only the missing archive step.
There is no startup import and no active-work migration.

## Offline operations

The same hidden group exposes:

```text
gc route-decision backup --city <city> --output <new-file>
gc route-decision export --city <city> [--output <new-file>]
gc route-decision verify --city <city>
```

Every operation refuses a running controller and remote selection. Backup uses
a live consistent bbolt read transaction and creates a new `0600` file without
overwrite. Export emits deterministic typed records and transition audits; it
omits idempotency tokens, import paths, authority keys, and credentials. Verify
checks bbolt pages, schema, revisions, every record, index membership,
transition chain, receipt shape, binding, and every stored signature against the
separate authority file.

These commands do not enable the production verifier, approve a record, mutate
route metadata, launch an Agent, or touch live provider configuration.
