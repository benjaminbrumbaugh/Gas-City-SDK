# sdk-bcy.3 current-base delivery plan

counter: 3
status: broad-gates-recorded
base: origin/main @ da56f551b58

## Target truth and evidence boundaries

The target is a current-base, source-only repair of immutable routing delivery.
Lifecycle and launch-receipt facts must remain durable when optional producer
outcome projection is invalid; unrelated expiry/revocation must continue; a
read-only legacy ledger probe must accept historical buckets without mutating
them; delivery retries/unknown state must remain truthful and must not create a
second authoritative outbox. CLI/API status mapping must reflect the real
contract.

Required evidence layers are routingdecision unit tests for transaction and
upgrade semantics, cmd/gc and internal/api boundary tests for real mappings,
and the repository's affected/full quality gates. A passing candidate-only test
matrix, fake 200 client, compile, or semantic source review is only a proxy:
each can miss current-base drift, real status mapping, or accidental ledger
mutation. If this succeeds while the bug remains, the likely misses are a
launch-receipt path, a real handler status branch, or a probe fixture that
normalizes the legacy store before asserting it.

## Full plan / tasks / subtasks

1. Establish baseline and ownership.
   - Inspect current origin/main and the two candidate commits.
   - Run the focused baseline tests before candidate code is present.
   - Map lifecycle, delivery projection, expiry, receipt, probe, CLI, and API
     ownership; record exact commands and results.
2. Prove regressions before changing production code.
   - Use the candidate's existing RED/GREEN history where available; otherwise
     add the smallest tests first for valid decision persistence, poison plus
     later-valid expiry, revocation, launch receipts, and legacy read-only
     compatibility/corruption fail-closed behavior.
   - Cover no-authority and disabled-local-lane upgrades plus real API/CLI
     400/404/409/503 status paths promised by the task.
3. Port the minimal repair onto current origin/main.
   - Keep producer construction outside generic Transition.
   - Separate durable lifecycle commit from optional delivery projection with
     explicit causal errors and truthful pending/unknown state.
   - Isolate per-record expiry delivery failures and preserve unrelated work.
   - Preserve exact-byte replay/purge facts and do not reconstruct history from
     current state.
4. Review and verify.
   - Run focused GREEN tests, affected suites, documented sharded gates, vet,
     formatting/source checks, generated-wire checks if applicable, and the
     pre-commit hook.
   - Obtain an independent exact-byte review if an available review lane can
     provide it; otherwise record that limitation without claiming approval.
   - Commit and push the additive current-base branch, then update the bead and
     hand off without closing work that still needs owner review.

## Architectural changes

Keep internal/routingdecision as the owner of lifecycle, authority, delivery,
and durable launch facts. Keep API and CLI as projections over those contracts.
Do not add a registry, outbox/event framework, mandatory crypto, provider,
role, auth, pin, deployment, or live-state behavior. Add no interface unless
the existing types prove two concrete implementations are required.

## Test plan

Run the exact focused baseline first, then test-first regressions at the owning
domain package. Cover happy paths and edge cases: optional projection failure,
expiry isolation, revocation/receipt durability, legacy/no-authority/
disabled-local-lane upgrades, corruption and bound-session fail-closed checks,
exact bytes, replay/purge history, and real boundary status mapping. Use
provider-free tests and existing fixtures; integration tests only where the
contract requires real filesystem/process behavior.

## Support structures

Maintain this plan as the decision record and the temporary
`agent-execution.log` in this worktree. Each completed subtask gets one
timestamped PT line with overall percentage and task/subtask IDs. Use exact
SHAs, `git show`, and `git diff`; preserve the original dirty worktree and all
foreign refs.

## Docs

Update only narrow routing-delivery documentation or schemas if the current
consumer contract requires clarification. Do not touch the product dashboard
or retained embedded UI. Do not edit generated artifacts by hand; regenerate
only if a wire contract truly changes.

## Execution order

Baseline/source map -> RED proof -> minimal implementation -> boundary tests
and narrow docs -> focused/affected gates -> independent byte review ->
commit/push -> bead handoff.

## Stability strategy

Use a dedicated worktree, t.TempDir fixtures, injected clocks, bounded
context-aware waits, and existing sharded runners. Do not restart services,
mutate live settings, touch credentials, or use shared-cache cleanup. Keep the
patch additive and upstream-friendly.

## Blocker avoidance

Do not mutate the existing sdk-4rm checkout or candidate branch. If current
base cherry-picking conflicts, resolve only in this branch using source
ownership and current tests; do not force-push or rewrite existing refs. If an
independent review lane is unavailable, document that as a limitation and use
local exact-byte/diff evidence without overstating it.

## Candidate subagent-parallel work

Independent read-only work can map routingdecision ownership, API/CLI status
paths, and history/docs. No subagent lane is exposed in this session, so these
checks remain coordinated here; production edits and assertions stay in one
branch.

## Pass 0 critique, top to bottom

- Scope/evidence names the target layer and rejects the main false-completion
  proxies; it must keep candidate evidence distinct from current-base proof.
- Tasks cover baseline, RED, minimal repair, and delivery; exact commit ancestry
  and branch-preservation checks must happen before cherry-picking.
- Architecture protects ownership and upstream mergeability; no new framework
  should appear through a convenience helper.
- Tests cover semantic and boundary layers; generated-wire checks are
  conditional on an actual wire change.
- Support, docs, execution order, and stability are sufficient for a source-
  only handoff; the temporary log must be appended after every subtask.
- Blocker handling preserves safety and does not turn unavailable review into
  false approval.

## Critical evaluation of the critique

The critique is sound but must not require reimplementing already-proven
candidate behavior. The candidate's two commits are a source artifact to
review and port, not authority: current-base tests and the final diff decide.
The status matrix should remain proportional to existing contracts, and any
failure to obtain another model family is a reportable evidence gap rather than
a reason to invent a review mechanism.

## Pass 0 roll-up

Add a mandatory ancestry/diff check before cherry-pick, keep tests layered by
what they observe, and treat the candidate plan/review as historical evidence.
Use the smallest source-compatible patch that passes current-base tests and
preserve all original refs and unrelated worktrees.

## No-change decisions

- No changes to the original dirty checkout, existing candidate branch, or
  unrelated generated/dashboard files.
- No live service/provider/deployment/auth/credential mutation.
- No new generic registry/outbox/event framework or speculative abstraction.
- No re-opening or stealing the prior closed work item.

## Pass 1 — baseline/source-map results

The current-base `internal/routingdecision` baseline passed with:
`go test ./internal/routingdecision -count=1`.
The focused `cmd/gc` baseline could not compile because the host lacks ICU's
`unicode/regex.h`; this is an environment gate failure, not product evidence.
The candidate diff is two commits based directly on `faefda46`, while
`origin/main` is a merge of that base plus `sdk-a89`; the original checkout is
on unrelated `sdk-4rm` work and remains untouched.

Source ownership is concentrated in `internal/routingdecision` for lifecycle,
delivery, receipts, and the session probe, with `cmd/gc` and `internal/api`
projecting boundary behavior. Candidate-only generated dashboard artifacts are
not evidence of product-dashboard work and will be excluded unless a current
wire contract proves they are required.

### Pass 1 critique

- The baseline result distinguishes a passing domain layer from an ICU-host
  blocker at the command layer; that distinction must remain explicit in the
  final handoff.
- The candidate contains both predecessor feature code and the repair, so a
  blind cherry-pick could import unrelated generated bytes; the next step must
  compare the actual current-base patch and retain only owned source/tests.
- The source map is sufficient to keep lifecycle ownership in routingdecision,
  but boundary tests must be run where the environment permits.
- The plan still needs a concrete test-only RED observation before production
  repair; use the predecessor commit's focused tests or add a narrow equivalent.

### Critical evaluation of Pass 1 critique

The critique correctly prioritizes semantic ownership and environment truth.
The candidate's generated files are part of its historical diff, not an
automatic requirement; current generated checks decide. Existing candidate
tests are valuable regression evidence, but they cannot replace a current-base
RED/GREEN observation. No production cherry-pick should proceed until the test
surface is identified.

### Pass 1 roll-up and no-change decisions

Before implementation, isolate the candidate's test surface and run it against
the current base where possible. Do not alter the current checkout, candidate
branch, generated dashboard output, or any live state. Keep the ICU failure as
an explicit limitation rather than masking it with a proxy.

## Pass 2 — current-base candidate and focused evidence

The two source commits cherry-picked cleanly onto current `origin/main` as
`4ad889b81` and `a406de19`. The focused routingdecision, internal/api, and
cmd/gc routing/delivery suites pass with Homebrew ICU include/link flags and an
on-disk temp directory. The no-flags baseline API/cmd attempts failed only at
`unicode/regex.h` discovery, so both outcomes are retained. The new repair
tests directly observe lifecycle persistence, poison isolation, launch receipt
durability, legacy read-only probe compatibility, and corrupt-ledger rejection;
the real API boundary test covers pending/ack 400/404/409/503 mapping.

The API spec, generated Go/TS clients, CLI reference, and embedded dist are
contract-generated outputs of the routing endpoint change. No product dashboard
repository or UI source was modified.

### Pass 2 critique

- Focused GREEN evidence now covers the named semantic and boundary owners, but
  broad gates and generated freshness remain outstanding.
- The candidate's historical review record is request-changes evidence, not
  independent approval of this current-base head; exact changed bytes still
  need a separate review pass.
- The current branch includes the candidate's generated API outputs as required
  by the repository freshness gates; their scope must stay limited to the
  changed contract.
- The ICU environment is resolved for local verification, but the unconfigured
  baseline failure must remain visible in handoff diagnostics.

### Critical evaluation of Pass 2 critique

The critique preserves the distinction between semantic proof, generated
freshness, and independent review. The current-base cherry-pick is valid because
both commits have the original candidate parent and no conflict, but that is
not proof of review quality. The next pass must run the repository gates and
compare the final diff for ownership leaks before delivery.

### Pass 2 roll-up and no-change decisions

Proceed to broad gates and exact diff review. Keep the implementation and
generated contract outputs unchanged unless a failing current-base test or
ownership audit identifies a concrete defect. Do not add a new abstraction,
rewrite history, or touch unrelated UI/source branches.

## Pass 3 — broad-gate results and delivery review

`go vet ./...` and `make check-docs` passed. All six `cmd/gc` fast shards and
the other fast jobs passed. The fast lane's `unit-core` job had one unrelated
host-environment failure in `internal/gitcred/TestStatOwnerReportsRealOwnership`
(observed gid 0, fixed expected gid 20); all other packages, including
`internal/routingdecision`, passed. The candidate diff has no changes under
`internal/gitcred`, so this is not change-specific evidence and is retained as
a release-gate limitation rather than patched here.

The exact changed source remains within routingdecision lifecycle/delivery,
typed API/client/CLI projections, generated contract outputs, narrow docs, and
tests. No new role-specific Go logic, untyped wire payload, or dashboard source
ownership leak was found in the review pass. Another model-family review lane
is not exposed in this session; local exact-byte/diff review is evidence of
what was inspected, not independent approval.

### Pass 3 critique

- The broad result distinguishes a real unrelated baseline failure from the
  repaired package and avoids changing a foreign test contract.
- Vet/docs and command shards establish broad compilation and boundary health,
  but generated API freshness and the pre-commit hook are still outstanding.
- The ownership review is explicit about its model-family limitation and must
  not be described as an independent review.
- Delivery still needs generated gates, final status, commit, push, and bead
  handoff metadata.

### Critical evaluation of Pass 3 critique

This is sufficient evidence to proceed because the failed test is isolated to
an untouched package and the change-specific suites are green. It is not
sufficient to claim every repository gate passed. Finish the generated and hook
checks, preserve the failure in the handoff, and route the pushed branch for
independent review rather than closing the work as semantically approved.

### Pass 3 roll-up and no-change decisions

Run generated contract freshness and final hooks without altering the
unrelated gitcred test. If a generated gate changes files, inspect and commit
only deterministic outputs caused by this API contract. Do not claim full
green until the gitcred environment limitation is separately resolved.
