# Verdict-aware convoy acceptance

## Scope

Convoy acceptance is opt-in. A convoy without `gc.convoy_acceptance.v1` keeps the legacy rule: administrative completion (`total > 0 && terminal == total`) is also accepted completion.

No reviewer, gate, candidate, or remediation is inferred from a bead title, assignee, label, or configured role name.

## Acceptance contract

The convoy stores one strict, bounded JSON object under `gc.convoy_acceptance.v1`:

```json
{
  "contract_version": 1,
  "candidate_work_id": "gc-candidate",
  "candidate_commit": "0123456789abcdef0123456789abcdef01234567",
  "review_gate_ids": ["gc-review"]
}
```

- `candidate_work_id` must name a resolved tracked member.
- `candidate_commit` must be a full lowercase 40- or 64-hex Git object ID. It must equal the candidate member's current `gc.work_commit` value.
- `review_gate_ids` contains 1–64 unique, bounded IDs. Every ID must name a resolved tracked member.

The convoy contract is the canonical candidate identity. Every required review must bind to those exact candidate bytes. Contract and envelope JSON are bounded to 32 KiB, reject duplicate or unknown keys, and do not normalize identity or verdict values.

## Review evidence

A terminal review gate uses the existing strict `gc.coordinator_outcome.producer_disposition` typed-close envelope. Contract version 1 now permits these optional fields for verdict-aware consumers:

```json
{
  "contract_version": 1,
  "disposition": "deliverable",
  "work_id": "gc-review",
  "recorded_by": "configured-producer",
  "reason": "review complete",
  "producer": "configured-producer",
  "passing_verdict": "review_verdict",
  "candidate_work_id": "gc-candidate",
  "candidate_commit": "0123456789abcdef0123456789abcdef01234567",
  "remediation_ids": ["gc-fix"]
}
```

`work_id` must still identify the review bead itself. `passing_verdict` remains limited to the existing `review_verdict` and `evidence.reviewer_verdict` keys, and `gc.review_gate` must be `consumed`. The published value is `pass` or `block`; any other value fails closed. Producer identities remain open-world configuration.

`remediation_ids` is bounded to 64 unique IDs per envelope. Every referenced remediation must already be a resolved tracked member of the convoy. Gas City does not parse findings prose or synthesize repair beads.

Linking members and writing metadata are separate Store operations. The SDK does not claim they are atomic. A missing member, dangling track, malformed envelope, partial remediation linkage, or store resolution failure withholds acceptance.

## Lifecycle states

The CLI and API expose administrative completion separately from acceptance:

- `complete`: legacy administrative terminality; this field retains its existing meaning.
- `accepted_complete`: administrative completion plus every opted-in acceptance gate passing against the anchored candidate.
- `acceptance_gated`: whether the opt-in contract is present.
- `acceptance_state`:
  - `in-progress`: required work or a required review is still non-terminal;
  - `complete`: accepted completion;
  - `blocked-remediation`: a typed BLOCK (or a PASS carrying follow-up work) has an open, explicitly linked remediation bead;
  - `stranded`: terminal administrative work cannot be accepted because evidence is BLOCK without open remediation, missing, malformed, unsupported, unlinked, unresolved, or bound to different candidate bytes.
- `acceptance_issues` and `remediation_ids`: bounded diagnostics and discoverable follow-up IDs.

Auto-close and explicit close refuse an opted-in convoy unless `accepted_complete` is true. `gc convoy land --force` preserves legacy behavior for non-gated owned convoys, but cannot bypass an acceptance contract. Deletion remains an administrative teardown operation and does not assert acceptance.
