# sdk-zbe plan

initial_counter: 0
counter: 3

## Objective

Repair the rejected `sdk-zbe` resource-census ratchet branch so its three
synchronized subprocess baselines match the source census on the current
`origin/main` lineage. Preserve the anti-growth policy and generated-document
agreement; do not add subprocess exemptions or alter product behavior.

## Full plan, tasks, and subtasks

1. Establish the rejection and source-of-truth evidence.
   - Confirm the bead, recorded worktree, required `polecat/sdk-zbe` branch,
     rejection reason, and current `origin/main`.
   - Reproduce `TestRepositoryLedgerMatchesCensusAndDocumentation` on the
     candidate before editing so the stale-baseline failure is observed.
   - Compare the three relevant values in the Go policy, TOML ledger, and
     generated `TESTING.md` block with the live census and current base.
2. Apply the smallest synchronized correction.
   - Update only the all-source audit, untagged debt, and untagged Small-debt
     subprocess call/file pairs from the measured current counts.
   - Regenerate the checked `TESTING.md` block through its owning test helper;
     do not hand-edit unrelated rows.
   - Keep the existing scanner, ratchet semantics, historical reported counts,
     owners, expiry, and migration targets unchanged.
3. Verify at the owning evidence layer and repository boundaries.
   - Run the focused census test after the change and verify it proves parsed
     tracked-source counts plus Go/TOML/Markdown synchronization.
   - Run the affected test command, `go vet ./...`, and the documented fast
     baseline as practical, recording any unrelated/environment failures.
   - Review diff identity, generated output, branch provenance, and hook state.
4. Hand off without closing the implementation bead.
   - Append completed-subtask entries to `agent-execution.log`.
   - Commit the cohesive ratchet correction and plan artifact on
     `polecat/sdk-zbe`.
   - Push and verify the remote head, set branch/target metadata, reassign the
     open bead to the refinery, wake it, and drain this session.

## Architectural changes

No production architecture changes. The owning boundary is the
`internal/testpolicy/resourcecensus` source scanner and its checked policy
projection in `test/test-resources.toml` and `TESTING.md`. The correction must
stay at that policy/documentation boundary and must not leak into generic test
helpers, command behavior, or provider code.

## Test plan and evidence discipline

Target truth: the checked subprocess ceilings equal the current syntax-aware
census for all tracked test source, untagged source, and the derived Small-debt
scope, while the Go bootstrap policy, TOML ledger, and rendered Markdown block
remain byte-for-byte semantically synchronized.

Required evidence layer: the focused repository census test, which observes
tracked files through the scanner and validates policy ceilings and generated
documentation. The pre-edit failure is RED evidence that the candidate
baselines are stale; the post-edit pass is GREEN evidence for this policy
boundary, not proof of runtime subprocess behavior or overall CI health.

Useful but insufficient proxies include matching literal numbers with `rg`,
comparing only `origin/main`, or passing a TOML parse without running the live
repository scan. The tempting false completion is raising ceilings until the
test passes, weakening the anti-growth validation, or changing the scanner to
report the stale candidate values. If this plan fully succeeds, the original
bug could remain if a different generated ledger block is stale or if the
scanner's scope semantics regress; the focused test's three-way validation and
final generated-block diff constrain those risks, while broader tests cover
scanner compilation and repository integration only.

## Support structures

Use the existing census scanner, `TestRepositoryLedgerMatchesCensusAndDocumentation`
update flag, git history, and temporary `agent-execution.log`. No new
production seam, fixture, dependency, database mutation, or subprocess
helper is justified. Work only in the bead-recorded worktree.

## Docs

`TESTING.md` is a checked normative policy projection and must be regenerated
with the ledger change. No user-facing conceptual or API documentation is
needed; the plan records the rejection diagnosis and synchronization evidence.

## Execution order and stability strategy

Read assignment and formula -> verify branch/base -> run RED focused test ->
inspect current-base source truth -> patch Go/TOML -> regenerate Markdown ->
run GREEN focused test -> affected tests/vet/fast baseline -> diff and hook
review -> commit/push/refinery handoff. Do not clean the shared build cache,
run destructive git commands, or treat a passing proxy as product evidence.
If a broad command fails outside the touched package, preserve the focused
result and identify the failure layer rather than changing policy values to
compensate.

## Blocker avoidance and candidate subagent-parallel work

Independent read-only checks may run in parallel: compare current-base values,
inspect the generator contract, and inspect affected-test/hook configuration.
The RED reproduction, synchronized edit, generated output, and final handoff
remain sequential. No subagent is required for this small correction, but an
independent review candidate should verify that only three subprocess pairs
changed and that all three projections agree.

## Pass 1 critique (top-to-bottom)

- The objective correctly treats the refinery report as a stale candidate
  baseline, but it must explicitly distinguish current `origin/main` source
  truth from the old branch's measured counts.
- The task sequence follows TDD by reproducing the existing regression before
  editing, and it names all synchronized projections.
- The architecture boundary is narrow and appropriate; it should explicitly
  exclude changing scanner semantics merely to satisfy the ratchet.
- The evidence section distinguishes source-policy evidence from runtime
  subprocess behavior, but must state that a broad suite cannot validate the
  exact ledger agreement by itself.
- The handoff obeys the formula by leaving closure to the refinery and
  preserving branch identity.

## Pass 1 critique evaluation

Add a three-way comparison requirement to task 1: live candidate census,
current-base checked values, and branch values. Add an explicit no-scanner-
semantics-change decision and name the focused test as the unique ledger owner.

## Roll-up: revised critique applied

Task 1 now requires measured candidate/current-base/branch comparison. The
architecture and no-proxy rules explicitly prohibit changing scanner semantics
or using broad tests as a substitute for the owning census test.

## Roll-up: revised critique applied to tasks/subtasks

The final diff review must verify the corrected pairs are exactly the live
counts and that no scope, matcher, historical count, or ownership metadata
changed.

## Explicit no-change decisions

- Do not modify `internal/testpolicy/resourcecensus` scanner logic.
- Do not alter resource scope definitions, ratchet validation, historical
  `reported_*` counts, owners, expirations, or migration targets.
- Do not add or remove subprocess tests, exemptions, waivers, or fixtures.
- Do not weaken the focused test or replace its live scan with literals.
- Do not touch the shared rig checkout, another polecat branch, or live Dolt.

## Pass 2 critique (top-to-bottom)

- The plan now identifies the exact data lineage and guards against carrying
  forward the rejected branch's stale values.
- The implementation is intentionally mechanical and keeps all synchronized
  copies under their existing owners.
- The evidence boundaries are clear: the focused census test is authoritative
  for ledger agreement, while vet and broad tests are supporting evidence.
- The support and stability sections avoid new abstractions and forbidden
  cache/git operations.
- The handoff captures the required branch, push, metadata, and refinery
  ownership contract.

## Pass 2 critique evaluation

No material architecture gap remains. The main residual risk is regenerating
`TESTING.md` from an incorrect ledger or accidentally preserving a mixed set of
counts. Require a post-generation exact-value check and a clean three-file
projection diff before commit.

## Roll-up: revised critique applied

Task 3 and the final diff review now require post-generation exact-value and
three-projection checks. The plan retains the no-change decisions for scanner
and policy semantics.

## Roll-up: revised critique applied to tasks/subtasks

The execution record must state the observed pre-edit failure, measured post-
edit counts, and the exact files changed. It must not claim more than the
focused policy layer proves.

## Pass 3 critique (top-to-bottom)

- The objective is limited to restoring a rejected ratchet after base drift.
- Tasks are ordered RED -> smallest synchronized edit -> owning verification
  -> refinery handoff, with no skipped critical evaluation.
- Architectural ownership, evidence limitations, and explicit no-change
  decisions prevent a convenient but unsafe baseline relaxation.
- The support and stability strategy is proportionate to a three-file policy
  synchronization and includes repository-wide checks without conflating them.
- The handoff is complete and does not close the implementation bead.

## Pass 3 critique evaluation

The plan preserves all required information. Before source edits, run the
focused RED test and confirm the current base publishes 652/189, 439/127, and
432/123 for the three subprocess rows. After editing, regenerate the Markdown
projection and verify exactly those values in all three surfaces.

## Final roll-up and approval state

The plan is approved for autonomous execution under `mol-polecat-work` after
the required three planning passes. Proceed with the evidence-first repair;
the implementation bead remains open for refinery verification.

## Verification record

- `git rebase origin/main`: PASS; clean rebase to `origin/main` at
  `4da0a10e2`, exposing the refinery-reported drift.
- Pre-edit `go test -count=1 ./internal/testpolicy/resourcecensus -run
  '^TestRepositoryLedgerMatchesCensusAndDocumentation$'`: RED after rebase;
  live 652/189, 439/127, and 432/123 versus candidate 657/190, 444/128,
  and 437/124.
- Corrected `internal/testpolicy/resourcecensus/census.go` and
  `test/test-resources.toml`, then regenerated `TESTING.md`; all three
  projections now carry 652/189, 439/127, and 432/123.
- Focused census owner test and the full
  `./internal/testpolicy/resourcecensus/...` package suite: PASS. This proves
  the checked source-policy/documentation boundary, not runtime subprocess
  behavior.
- `CGO_ENABLED=0 go vet ./...`: PASS; `go vet ./...`: environment-layer
  failure because the host lacks `unicode/regex.h` for go-icu-regex.
- `EXTRA_TEST_ENV='CGO_ENABLED=0' make test-fast-parallel`: PASS (`All fast
  jobs passed`), including all six cmd/gc shards and unit-core.
- `git diff --check` and `.githooks` configuration: PASS.
