# sdk-h0s.8 bounded proof plan

counter: 3
counter history: 0 -> 1 -> 2 -> 3

## Objective and scope

Prove, from current Gas City source, the existing trusted-local caller/session
authority used by convoy self-registration and its notification path. Publish
the exact boundary, the minimal operation/result contract, and explicit trust
limits. Work only in the exclusive task tree on `proof/mayor-session-authority`
from base `faefda46ea6ab3563cc3f9ad9ee3b02e1824c54d`.

In scope: source-cited audit prose under `docs/audits/`, provider-free tests at
the existing handler/runtime resolution boundary, and evidence artifacts that
exercise positive and negative ownership cases. Out of scope: production code,
registration implementation, CLI/API schema changes, generated files, live
registration, provider turns, deployment, profile/session discovery, private
state, authentication or consent frameworks, and mutation of held consumers.

## Tasks and subtasks

1. Establish provenance and baseline.
   1. Read repository instructions and testing guidance.
   2. Record the PR68 plan source as Hermes-Extensions `origin/main` at the
      fetched plan commit, and retain the exact pinned SDK base.
   3. Confirm the task tree remains clean before edits and preserve all other
      worktrees, branches, and untracked bytes.
2. Map the existing boundary.
   1. Trace `internal/launchorigin/capture.go` and city-write/read middleware.
   2. Trace session/runtime claim resolution and caller/session lookup.
   3. Trace convoy subscription, terminal notification admission, and native
      versus external recipient routing.
   4. Identify whether the existing seam proves self-service authority or
      exposes a precise missing surface.
3. Define evidence before implementation.
   1. Target truth: a caller can act only on its proven session and convoy
      scope, and a notification reaches only an authorized exact recipient.
   2. Required layer: provider-free source-level handler/runtime tests against
      the real resolution and admission functions, plus source citations.
   3. Negative cases: false owner, cross-session selector, ambiguous selector,
      stale generation/fence, and route text or ambient actor spoofing.
   4. State trusted-local same-user assumptions; do not claim hostile same-UID
      isolation or treat environment strings as authentication.
4. Implement the smallest evidence slice.
   1. Write failing focused tests first, without modifying production code.
   2. Add only test helpers/fixtures needed to exercise the existing seam.
   3. Write the audit only after test results and source reads are known.
5. Verify and hand off.
   1. Run affected provider-free tests and applicable docs checks.
   2. Run the required repository quality gates proportionate to touched files.
   3. Record evidence boundaries and any unresolved blocker in the audit.
   4. Commit on the task branch, push and verify the remote SHA, then notify
      Mayor `gc-0s4t4` and return exact next-action/cleanup ownership.

## Architectural changes

No production architectural change is authorized. The audit is a boundary
artifact; tests are consumers of existing seams and must not introduce a new
authority abstraction. If no existing seam proves the contract, the result is
a precise missing-surface blocker, not a replacement security design.

## Test plan and evidence discipline

The tests observe provider-free caller/session resolution and notification
admission. They prove the behavior of those existing source-layer boundaries;
they do not prove provider delivery, live chat registration, deployment, or
hostile same-UID isolation. An updated passing test is not product acceptance.

Cheaper but insufficient proxies include string matching, environment-only
selectors, route parsing, a passing semantic fixture disconnected from the real
resolver, and a mock that bypasses admission. The false-completion risk is
mistaking a selector for authenticated identity or a queued notification for a
delivered callback. If the proof fully succeeds while the original bug remains,
the likely cause is an unexercised production composition path, an external
transport mismatch, or a stale/generated consumer; the audit must call those
limits out rather than imply acceptance.

## Support structures

`agent-execution.log` records each completed subtask with timestamp, overall
percentage, task id, and subtask id. The plan file records all planning passes.
No new harness, private-state scraper, provider fixture, or runtime state file
will be created.

## Documentation

Own `docs/audits/mayor-session-registration-boundary.md`. Cite exact files,
symbols, and test names. Explain the minimal operation/result interface,
trusted-local threat model, selector-versus-authority distinction, notification
path, negative cases, and the precise blocker/next action if proof is
incomplete. Do not edit generated docs or product dashboard surfaces.

## Execution order and stability strategy

Read provenance and code first; record baseline; write failing tests; make only
test/audit edits; run focused tests; write and lint the audit; run docs and
quality gates; review the diff for scope leaks; commit, push, verify, notify
Mayor, and preserve the provided task tree until accepted owner cleanup.

Stability comes from one writer, a clean isolated branch, provider-free tests,
no external mutations, exact source citations, and explicit evidence limits.

## Blocker avoidance and parallelization

If the existing boundary is insufficient, stop at the exact missing public
surface and document it. Do not add authentication, consent, callback,
registration, or remediation machinery. No subagent write split is safe here:
the audit and tests share one trust-boundary interpretation. Independent
read-only source inspection may be parallelized conceptually, but edits remain
serial in this task tree.

## Planning pass 1: critique

The objective and scope are appropriately bounded, but the final operation and
result names must come from existing conventions rather than this plan. The
source map names likely entry points but must follow actual imports and call
sites. The negative cases are required, but stale-fence behavior may be absent;
the test must distinguish an existing rejection from an unproved seam. The
quality gates must be narrowed after the final diff is known. The handoff must
not call the source artifact feature acceptance.

## Planning pass 1: critique of the critique

The critique correctly prevents inventing an interface, but it could still hide
the crucial distinction between city request authority and session authority.
The final audit must show both layers and prove that one does not substitute for
the other. It must also distinguish native subscription admission from external
transport delivery. The plan should preserve negative evidence instead of
silently omitting a case the code cannot exercise.

## Planning pass 1: roll-up

Add explicit city-write-versus-session-authority and native-versus-external
evidence boundaries to the audit and test matrix. Record honest failing probes
when no seam exists. Use the repository's existing operation/result names only.

## Planning pass 1: no-change decisions

No production code, new interface, runtime state, generated contract, or live
consumer change is added. No test is weakened to make a proxy pass.

## Planning pass 2: full plan/tasks/subtasks

1. Use the fetched Hermes-Extensions PR68 plan as scope authority and inspect
   the current SDK source from the clean task base.
2. Follow the caller from request admission through session/claim resolution,
   then follow convoy subscription and notification admission for native and
   external recipients.
3. Add provider-free tests first at the nearest existing boundary. Cover the
   accepted caller/session case and each negative selector/owner/generation
   case that the current seam can express; preserve honest unsupported probes.
4. Publish the audit with source citations, exact existing names, trust model,
   evidence-layer limits, and a precise missing-surface blocker if needed.
5. Run focused tests and docs checks, review the scope, commit/push the branch,
   verify its remote SHA, and nudge the Mayor with the evidence and ownership
   handoff. Do not close the implementation bead.

## Planning pass 2: critique

The revised sequence now separates admission, session resolution, and delivery,
which avoids treating a queued callback as proof of delivery. It still needs a
concrete stopping rule for source-only work: no test may require a live store,
tmux, provider, external process, network callback, or current-chat state. The
plan also needs an explicit audit table mapping each claimed truth to its
observed layer and non-proven layers so future readers cannot overread a pass.

## Planning pass 2: critique of the critique

The stopping rule is necessary and should be applied before writing fixtures.
The evidence table is stronger than prose alone, but it must remain concise and
source-cited. A test that only rejects malformed strings is not a session
authority proof; the positive path must cross the real resolution seam. The
negative matrix must report whether each case is rejected, unavailable, or
uncovered rather than collapsing those states.

## Planning pass 2: roll-up

Before implementation, classify every planned test as provider-free and record
its boundary. Add an audit evidence matrix with columns for target truth,
observed layer, result, and non-proof. Use three outcomes for negatives:
rejected by the existing seam, not expressible at this seam, or not proven.

## Planning pass 2: no-change decisions

Do not add a fake in-memory authority service, an alternate session identity,
or a test-only authentication bypass. Do not broaden the task to resolve gaps
owned by convoy producer, bridge, or host-boundary work.

## Planning pass 3: full plan/tasks/subtasks

1. Preserve the verified exclusive task tree and provenance in the audit and
   execution log.
2. Read the exact current symbols before choosing test files; use existing
   package seams and names.
3. Implement only focused regression/proof tests and the audit. If a required
   proof cannot be exercised without production changes, state the missing
   surface and leave consumers blocked.
4. Verify with affected tests, `go vet` for touched packages where practical,
   and `make check-docs` for the audit path; state any gate that is not relevant
   or cannot run without prohibited infrastructure.
5. Commit and push the exact branch, verify origin matches, update bead
   metadata/handoff without closing the implementation bead, and notify
   `gc-0s4t4` of acceptance evidence or the precise blocker.

## Planning pass 3: critique

This final plan is maintainable and reviewable, but `make check-docs` may treat
an audit outside the Mintlify navigation as an orphan. That outcome must be
reported, not fixed by adding product navigation for a contributor artifact.
The branch handoff must preserve the provided task tree until the accepted
owner confirms cleanup. The plan must also include an execution log entry after
each completed subtask, including the plan-writing subtask itself.

## Planning pass 3: critique of the critique

The orphan possibility is a docs-boundary issue, not evidence that the audit
belongs in user navigation. Check existing `docs/audits` conventions and the
repository gate before deciding. The cleanup ownership is already explicit in
the bead instructions; no task-owned deletion is allowed before remote
verification and handoff. Logging is operational support and must not become a
second source of truth for the proof.

## Planning pass 3: roll-up

Inspect existing audit conventions and docs gate behavior first. Keep the audit
where the bead requires it, cite the exact gate result, and avoid nav changes
unless existing convention requires them. Append concise execution-log entries
after each completed subtask. Treat the audit as the durable evidence source;
the log only records progress and provenance.

## Planning pass 3: no-change decisions

No generated file, production implementation, provider integration, live
callback, or private-state inspection is authorized. The audit's two minimal
hand-maintained navigation/index entries are required by the repository's
docs gate to publish the requested durable evidence; they do not change the
product dashboard or any generated contract. The plan is complete when the
exact boundary is proven or the missing surface is precisely reported; it does
not require feature acceptance or rollout.

## Lost-information check after three passes

The refinements preserve the original objective, source-only scope, required
negative cases, city-write/session distinction, native/external distinction,
provider-free evidence layer, exact provenance, handoff requirements, and
cleanup ownership. No architectural or security requirement was dropped.
