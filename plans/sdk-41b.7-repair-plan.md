# sdk-41b.7 repair plan

counter: 0

## Full plan, tasks, and subtasks

Repair the existing Codex passive-quota source slice on the preserved
`polecat/sdk-41b.7` branch, rebased onto current `origin/main`.

1. Load the current source, rejection evidence, and exact branch/base
   identities; preserve the prior implementation and unrelated files.
2. Add focused RED regressions before production edits for any current-base
   defect: canonical workdir equivalence, physical search-root containment,
   independently valid isolation guards, provider-free replay execution, and
   per-turn model/effort reset with fallback.
3. Apply the smallest source repair at the existing `internal/sessionlog`
   resolver/parser boundary. Do not add lifecycle wiring, provider probes,
   policy, or a new framework.
4. Run focused package tests, scope-negative checks, affected tests, and
   normal quality gates available in this checkout; inspect exact diff and
   remote identity.
5. Commit source/test/required-plan changes, push and verify the branch, record
   producer metadata, and hand the open work bead to the refinery.

## Architectural changes

No new architecture. Keep session-log path binding and Codex evidence
extraction in `internal/sessionlog`; keep caller-supplied provider, account,
scope, and token identities literal. Shared path behavior may change only as a
minimal fail-closed containment repair with its owning tests.

## Test plan and proxy audit

Target truth: a provider-free replay is accepted only when the resolved file,
session filename, session metadata, workdir, and SessionID are independently
bound, and the returned observation preserves literal identity and turn-local
state.

Required evidence layer: executed `internal/sessionlog` replay and unit tests,
plus pathutil/shared-tail tests when that boundary is touched. These prove
parser and filesystem-boundary behavior, not live Codex/provider behavior,
deployment, or quota eligibility policy.

Useful but insufficient proxies: `stat`-only fixture checks, one happy-path
test, aggregate guard coverage, parent/worker gate success, or semantic
preview. False completion would be a green suite that still permits symlink
escapes, a removed guard, cross-turn effort inheritance, or stale-tail claims.

## Support structures and docs

Append one concise completion line to `agent-execution.log` after each
subtask. Keep it temporary and remove it before final clean-state handoff.
No documentation change is planned unless a touched source comment makes the
bounded-tail contract false or ambiguous.

## Execution order and stability strategy

Use the owner-approved worktree, fetch current origin, preserve the baseline
commits, write/run RED tests first, then make localized GREEN changes. Use
deterministic `t.TempDir` fixtures, do not mutate provider/config state, and do
not force-push. Re-run focused tests after each boundary, then broader gates.

## Blocker avoidance and candidate parallel work

Source edits remain serialized because the parser and tests share files.
Independent exact-byte review can be split conceptually across (a) path and
symlink containment, (b) guard-removal proofs, and (c) turn/identity semantics;
no extra framework or external probe is needed. Escalate only if current-base
reconciliation, required tests, or authenticated push/review evidence is
actually blocked.

## Critique of major sections and critical evaluation

- Scope is narrow, but the prior branch must remain traceable; do not rewrite
  its implementation commit or treat old review evidence as approval.
- The test order is correct, but each invalid fixture must keep all unrelated
  inputs valid or a guard-removal proof is vacuous.
- The architecture preserves boundaries, but physical containment must not be
  confused with canonical workdir equality.
- The evidence plan correctly excludes live provider truth; final handoff must
  state that limitation and must not call bounded-tail absence eligibility.
- Cleanup and handoff are part of correctness: the temporary log must not ride
  into the source branch, and only the refinery closes the work bead.

Critical evaluation: the highest-risk false completion is a passing aggregate
test whose fixture is invalid for multiple reasons. Therefore the repair is
accepted only with independently valid guard fixtures and an executed replay,
plus direct inspection of path canonicalization and turn reset. If current
`origin/main` already contains the needed behavior, record no production
change and preserve the evidence-only repair rather than inventing a second
mechanism.

## Roll-up and explicit no-change decisions

Apply the critique by keeping the prior implementation commits intact,
rebasing onto current `origin/main`, and adding only demonstrated repair/test
evidence. No lifecycle wiring, recovery policy, inference, credential/account
mutation, mandatory crypto, registry/outbox/event framework, deployment, or
unrelated cleanup will be added. No additional planning rounds are needed for
this rejected-branch repair.

## Independent review follow-up

The exact-byte review of the first published repair found two evidence gaps:
the provider rejection was not exercised with otherwise-valid evidence, and
`session_meta.payload.id` was not bound to the filename/session identity.
Added direct regressions for both cases. The parser now carries the metadata
ID to the quota boundary, which rejects a missing or mismatched ID before
tail extraction. A subsequent review also identified a check-then-reopen
symlink race; quota parsing now retains the validated file descriptor for
metadata and tail reads, with a path-swap regression. This remains a
source-only `internal/sessionlog` repair; no provider lifecycle, probing,
policy, or orchestration behavior is added.
