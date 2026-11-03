# sdk-8fy waiver-clock renewal plan

counter=3

## Scope and target truth

The target truth is that the checked resource and runtime-provider ledgers
remain structurally synchronized and pass their strict wall-clock policy on
the clean `origin/main` baseline. The change is policy evidence maintenance:
renew expired, still-valid waiver claims with explicit dates and preserve their
existing owners/reasons. It does not claim that the underlying test debt is
fixed.

## Full plan, tasks, and subtasks

1. Reproduce and bound the baseline failure.
   - Run the two focused ledger tests with `GC_WAIVER_CLOCK=strict`.
   - Record expired rows, owner IDs, fleet-fatal dates, and any near-term
     provider warnings.
   - Inspect prior waiver-renewal commits for ownership, staggering, and
     generated-document conventions.
2. Renew the policy source of truth.
   - Update every expired resource-census date in
     `internal/testpolicy/resourcecensus/census.go` and
     `test/test-resources.toml` to a short future horizon.
   - Update only expired provider gaps in
     `internal/testutil/providerledger/ledger.go`, keeping one date per
     independent gap and preserving reasons/owners.
   - Do not weaken the clock, broaden the horizon, or delete ledger rows.
3. Regenerate checked documentation.
   - Run the resource-census `-update` path so its `TESTING.md` block matches
     the TOML ledger.
   - Run the provider-ledger `-update` path if needed and verify the runtime
     table matches the Go catalog.
4. Verify the change.
   - Focused strict ledger tests, then the documented fast suite, `go vet`, and
     the active pre-commit hook.
   - Review the diff for date-only policy changes, synchronized generated
     output, and no accidental census/provider behavior changes.
5. Commit, push, and hand off the per-bead branch to the refinery.

## Architectural changes

No runtime architecture changes. Ownership remains in the existing ledgers;
`census.go` and `ledger.go` stay the executable policy sources, TOML and
`TESTING.md` remain their checked projections, and the shared waiver clock is
unchanged.

## Test plan and evidence discipline

- Focused ledger tests observe ledger structure, live census/catalog wiring,
  generated documentation, and strict expiry classification. They prove the
  checked policy is internally consistent and current at test time; they do
  not prove the waived test debt has been eliminated.
- `scripts/waiver-clock-audit` observes the same strict clock boundary and
  owner-facing warnings; it is useful evidence but not a replacement for the
  focused tests.
- Fast suite and `go vet` provide repository-level regression evidence, not
  proof that each waiver's underlying contract is now covered.
- Do not replace the target strict-clock behavior with a grace-mode pass or a
  changed expectation. Dates change only because the policy is explicitly
  renewed, not because the test is made less strict.

## Support structures and docs

Use the existing `-update` generators and focused tests; add no new helper or
abstraction. Update only generated ledger blocks in `TESTING.md`. Keep a
temporary `agent-execution.log` during execution and remove it before commit.

## Execution order and stability strategy

Baseline -> history/policy review -> source edits -> generated docs -> focused
strict verification -> fast/vet/hook checks -> diff review -> commit/push.
Renewed dates stay within the validator's 90-day horizon, are explicit, and
remain staggered for independent provider gaps. Preserve owner IDs and reasons
so renewal does not silently transfer responsibility or erase the evidence
boundary.

## Blocker avoidance and parallel candidates

The current worktree and branch are isolated before edits. The focused resource
and provider tests can run independently in parallel after edits; source and
generated-document updates must remain serial because each generated block has
one source ledger. No independent subagent is available in this session, and
splitting source-of-truth edits would risk a mixed ledger.

## No-change decisions

- No new waiver-clock behavior or test expectation changes.
- No replacement proof is claimed because the underlying provider/resource
  debt is outside this repair's scope.
- No owner reassignment is made without durable owner approval; the existing
  owner references and reasons are retained while the dates are explicitly
  renewed in this bead's commit.

## Proxy-domain audit

- Target truth: strict mode passes for both ledgers on the reviewed baseline.
- Required evidence layer: the two focused tests with strict mode, plus the
  generated-document checks they contain.
- Cheaper but insufficient proxies: checking only text replacements, a grace
  mode run, or a passing generated preview.
- Tempting false completion: deleting waiver rows, changing strict to grace,
  or updating only `TESTING.md`.
- If this plan fully succeeds, the original underlying debt can still remain;
  the result must therefore state renewal, not proof of the waived contracts.

## Planning pass 1: critique and roll-up

### 1. Full plan/tasks/subtasks review

The baseline is now concrete: every production resource-census row is dated
2026-10-01, while provider gaps are independently dated. The plan correctly
keeps source ledgers and generated documentation separate, but it needs an
explicit treatment for provider waivers that are still valid yet already in
the 14-day warning window. Leaving those warnings would technically pass
strict mode but would not restore a clean waiver-clock audit.

### 2. Critique of each major section

- Scope: correct target, but the description omitted provider owner
  `ga-80po0c.3` because the issue description focused on the resource owners.
  The provider ledger failure is part of acceptance and must be included.
- Tasks: “short future horizon” was underspecified. A reviewer needs exact,
  bounded dates and a check that no expired or warning-window finding remains.
- Architecture: correct; no new runtime policy is justified.
- Test plan: focused strict tests are necessary but should be supplemented by
  `scripts/waiver-clock-audit`, whose output exposes warning findings hidden by
  ordinary passing tests.
- Support/docs: generator usage is correct, but both marked `TESTING.md`
  blocks need regeneration, not hand edits.
- Execution/stability: resource rows may share one census renewal date, while
  provider dates must remain staggered per independent gap and grouped for the
  two t3bridge routes.
- Blockers/parallelism: source edits and generators are serial; post-edit
  focused tests may run in parallel.
- Proxy audit: correctly distinguishes renewed policy from fixed debt; retain
  that distinction in the handoff.

### 3. Critical evaluation of that critique

The critique is sound. A strict test with warning logs is an incomplete
operational result because the scheduled audit treats those logs as actionable
findings. Updating every provider waiver in or near the warning window is a
small, explicit policy renewal and preserves the stagger invariant. The lack
of local records for historical owner IDs is not a reason to invent new owners;
the ledger's durable owner references and reasons remain authoritative.

### 4. Roll-up: revised critique

Renew all resource rows to `2027-01-01`. Renew provider gaps to staggered dates:
ACP `2026-12-01`, t3bridge (both routes) `2026-12-08`, K8s `2026-12-15`,
Herdr `2026-12-22`, and hybrid `2026-12-29`; retain the independently dated
SSH row unless the audit proves it needs adjustment. These dates remain under
the 90-day validator horizon and remove current warning findings while keeping
each gap answerable separately.

### 5. Roll-up: apply to plan/tasks/subtasks

Add the exact dates above to the implementation task, require both focused
strict tests and `scripts/waiver-clock-audit`, and verify the provider stagger
test. Treat the generated `TESTING.md` blocks as derived outputs of the two
source ledgers.

### 6. No-change decisions

Do not change owners, reasons, expiry semantics, validator horizons, or unit
test expectations. Do not file additional owner alert beads from the audit
script; this assigned repair bead is the durable work record and creating
duplicates would add coordination noise.

## Planning pass 2: critique and roll-up

### 1. Full plan/tasks/subtasks review

The revised plan now covers both ledgers, exact renewal dates, generated output,
and the audit. It still needs a precise edit boundary: production policy dates
must change, but fixture dates in unit tests intentionally exercise historical
date parsing and must not be mass-replaced. The plan also needs an explicit
post-generation diff check so a generator cannot hide an unrelated document
change.

### 2. Critique of each major section

- Scope: exact and appropriately distinguishes policy renewal from proof.
- Tasks: source files are named, but the resource generator's bootstrap policy
  in `census.go` and TOML must stay byte-for-byte aligned; fixture dates in
  `census_test.go` and `medium_test.go` are outside the production policy.
- Architecture: correct and minimal.
- Test plan: add the provider stagger test and the structural/documentation
  portions of both focused tests; audit output must be captured as evidence.
- Support/docs: `providerledger -update` and `resourcecensus -update` are
  separate commands; run them in an order that leaves both marked blocks
  regenerated before the final tests.
- Execution/stability: exact dates satisfy the horizon and stagger rules, but
  the final diff must prove only intended date/projection changes occurred.
- Blockers/parallelism: no blocker; running the two focused tests in parallel
  is safe after all source and generated changes are complete.
- Proxy audit: correctly rejects text-only and grace-mode completion; add the
  generated-block comparison as the required wire/document evidence.

### 3. Critical evaluation of that critique

This refinement catches the main maintenance risk: a global date replacement
could rewrite deterministic fixtures and weaken test intent. The production
policy has two executable representations (`census.go` and TOML), while
`TESTING.md` is generated from the TOML/provider catalog. Explicit diff review
and focused tests are enough to detect drift without adding new tooling.

### 4. Roll-up: revised critique

The implementation must edit only production expiry literals and the generated
ledger projections. It must leave fixture dates and clock tests unchanged. The
verification evidence must include a clean `git diff --check`, the generator
tests in `-update` mode, strict ledger tests, the provider stagger test, and an
audit run with no waiver findings.

### 5. Roll-up: apply to plan/tasks/subtasks

Add a source-boundary subtask: enumerate production date occurrences before
editing, then inspect `git diff --word-diff`/`git diff --check` after generation.
Add a no-fixture-change assertion to self-review. Keep the plan file and
temporary execution log out of the shipped source delta except for the plan
itself if it remains a useful review artifact.

### 6. No-change decisions

No new tests are needed because the existing strict-clock and stagger tests
observe the target layers directly. Do not alter fixture dates, clock helper
tests, or waiver validation semantics. Do not hand-edit the generated
`TESTING.md` blocks.

## Planning pass 3: final critique and roll-up

### 1. Full plan/tasks/subtasks review

The plan now has a bounded source edit, exact dates, generator commands,
strict-clock evidence, and a diff boundary. The final risk is provenance: a
renewal should be explicit in the commit/bead record while preserving the
historical owner IDs, and the handoff must say that the underlying gaps remain.

### 2. Critique of each major section

- Scope: target truth and limits are clear; the final summary must name both
  ledger tests and distinguish policy health from contract proof.
- Tasks: complete, with production-vs-fixture boundaries and exact dates.
- Architecture: no leakage or new abstraction.
- Test plan: evidence layers are correctly named; the audit's no-findings
  result is stronger than a passing focused test with warning logs.
- Support/docs: existing generators preserve source ownership and avoid manual
  table drift.
- Execution/stability: staggered provider dates and 90-day bound are explicit;
  resource-census renewal is one coherent snapshot policy.
- Blockers/parallelism: safe and operationally simple.
- Proxy audit: explicitly guards against falsely claiming debt removal.
- Handoff: must include the producer commit, strict results, audit result, and
  refinery ownership; the implementation bead must remain open.

### 3. Critical evaluation of that critique

No further design change is warranted. The remaining concern is communication,
not code: the commit message and bead note should identify this as an explicit
renewal of expired/warning-window policy evidence, not a silent clock bump.
The plan's date choices are safe on the stated 2026-11-02 clock and remain
inside the validator horizon even if the run occurs a few days later.

### 4. Roll-up: revised critique

Proceed with the planned dates and generated projections. Before submission,
record the renewal rationale in the bead notes, run all required gates, and
report any unrelated pre-existing failures separately instead of changing
expectations.

### 5. Roll-up: apply to plan/tasks/subtasks

Implementation is approved by the assigned-work protocol: edit the two source
ledgers, regenerate both documentation blocks, run the focused and repository
checks, review, commit, push, and hand the branch to refinery. The plan itself
is the requested planning artifact; `agent-execution.log` remains temporary.

### 6. No-change decisions

Do not replace the waivers with unverified proof, do not widen the 90-day
horizon, do not suppress strict findings, do not reassign historical owners,
and do not close the implementation bead. No subagent split is introduced
because the source/projection boundary is intentionally serial.

### 7. Final proxy-domain audit

Target truth: both ledgers and their checked projections pass strict clock
validation now. Required evidence: focused strict tests, no-findings audit,
generator synchronization, diff review, and repository gates. Insufficient
proxies: date grep, grace-mode tests, or unchanged warning output. Residual
risk: the waived resource/provider debt still exists and must continue under
the original owners' migration targets.
