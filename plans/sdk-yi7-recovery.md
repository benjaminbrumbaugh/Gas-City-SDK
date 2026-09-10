# sdk-yi7 recovery plan

counter: 3

## Objective

Complete the already-produced `sdk-yi7` verification handoff without
self-attesting a closed implementation step. Preserve the candidate's exact
branch and evidence, obtain principal-separated producer-artifact recovery for
the blocked self-review step, then run the self-review against the recorded
worktree before any refinery handoff.

## Plan, tasks, and subtasks

1. Establish durable identity and scope.
   - Treat `sdk-yi7` as the work bead and `sdk-2tp` as the self-review step.
   - Use `/Users/benjaminbrumbaugh/Documents/Gas City/Gas-City/.gc/worktrees/Gas-City-SDK/work/sdk-yi7` as the only producer checkout.
   - Preserve `polecat/sdk-yi7` and its current-target ancestry.
2. Repair the workflow evidence boundary.
   - Have a witness or mayor run `self-review-guard.sh recover-producer` for
     closed implement step `sdk-8pu` from the recorded clean checkout.
   - Retain explicit reconstructed-artifact metadata and the existing hold
     until the guard confirms readback.
3. Review and verify.
   - Run the guard's `where` resolution and review the exact candidate diff.
   - Run focused candidate-review tests, required project checks, and the
     live/disposable replay required by `sdk-yi7`.
   - Record what each evidence layer proves and cannot prove.
4. Submit only after all gates pass.
   - Commit only review-derived touch-ups and keep the work bead open.
   - Push and verify the remote head, record producer outcome, set target, and
     reassign to the refinery.

## Architectural changes

No source architecture change is planned. The relevant boundary is between
the pack-owned self-review workflow, the durable beads ledger, and the
recorded producer Git worktree. Do not move verification logic into generic
SDK code, add role-specific behavior, or treat work-bead notes as a substitute
for the self-review guard's producer triple.

## Test and evidence plan

- Durable ledger inspection proves assignment, branch metadata, prior outcome,
  and the principal-separated recovery record; it does not prove the current
  installed binary or controller/provider behavior.
- Git ancestry, clean status, exact commit, and tree identity prove the source
  artifact being reviewed; they do not prove the candidate was installed or
  exercised by a live controller.
- Focused tests prove the assertions covered by those tests at their package
  layer; they do not prove all runtime integrations or the shared city state.
- The disposable `gc --city` replay proves the deterministic lifecycle
  transition and hold preservation in an isolated fixture; it does not prove
  the real shared city's pinned pack publication/config activation.
- The installed binary/version check proves provenance of that executable; it
  does not prove a running long-lived controller has adopted it.

## Support structures

- Existing guard scripts remain the attestation and close boundary.
- The recorded per-bead worktree and branch remain the sole producer artifact.
- This plan is the temporary coordination artifact; no status/PID/lock file or
  second worktree is introduced.

## Documentation

Keep the existing task notes as the durable record of merge SHA, installed
runtime SHA, focused results, replay evidence, and known shared-city limits.
Add only the recovery metadata and concise handoff note required by the guard;
do not rewrite accepted design or product documentation.

## Execution order

Durable identity -> principal-separated recovery -> guard readback -> exact
diff review -> focused and live/disposable verification -> clean commit ->
push/remote verification -> producer outcome -> refinery handoff.

## Stability strategy and blocker avoidance

Fail closed on missing or mismatched producer artifacts, dirty trees, branch
shape errors, uncertain ownership, human/external holds, and failed tests.
Escalate the existing `no-producer-artifact` hold to the witness rather than
running recovery as the polecat under review. Never use the unrelated reusable
polecat checkout or mutate unrelated repositories.

## Candidate parallel work

After producer recovery, exact diff review and read-only ledger evidence can be
prepared independently, but guard recovery must precede self-review and all
source-changing touch-ups must remain serialized on `polecat/sdk-yi7`.

## Planning pass 1: initial critique

- Scope is correctly limited to the blocked workflow and the existing
  candidate; no implementation rewrite is justified.
- The evidence layers are named, but a successful test run alone must not be
  mistaken for installed/live proof.
- The recovery action is correctly assigned to a different principal; the
  plan must not instruct this polecat to run `recover-producer`.
- The branch and worktree are explicit, avoiding the known agent-home branch
  mismatch.

## Planning pass 2: critique of pass 1

- The plan needs an explicit no-change decision for source, config, generated
  artifacts, and product expectations; add that decision below.
- The handoff must preserve the work bead's open state and must not close the
  implementation bead or work bead from this session.
- A replay remains a proxy for shared-city behavior; its limitation is already
  stated and must remain visible in the final handoff.

## Planning pass 3: critical evaluation of pass 2 and roll-up

- The revised plan now separates producer-artifact recovery from review pass,
  so recovered metadata cannot be mistaken for approval.
- The plan retains a single-writer branch rule and permits only serialized
  touch-ups after review.
- No plan item depends on a human-only physical action; the only external
  dependency is the authenticated witness/mayor ledger action already raised.

## No-change decisions

- No Go, shell, config, generated, or product-contract source changes are
  planned in this recovery pass.
- Do not change test expectations: the product contract has not changed and
  the existing fixture adjustment is independently correct.
- Do not remove the `hold:mayor` label or close around a guard refusal.
- Do not treat the prior work-bead notes as permission to skip the missing
  self-review artifact or the required live verification.

## Proxy-domain audit

- Target truth: the shipped self-healing candidate is reviewed from its exact
  producer artifact and the required lifecycle is verified before handoff.
- Required evidence layer: guard-attested producer metadata plus focused tests,
  installed provenance, and isolated live/disposable lifecycle replay.
- Useful but insufficient proxies: bead notes, Git ancestry, package tests, and
  semantic replay each cover only their stated layer.
- Tempting false completion: accepting `gc.work_commit` on the work bead as a
  replacement for the missing implement-step producer triple.
- If this succeeds, the original bug could still remain if the installed
  runtime differs from the reviewed commit or if the real city's pinned pack
  never activates; those limits must remain explicit.
