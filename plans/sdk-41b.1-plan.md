# sdk-41b.1 implementation plan

counter: 3

## Full plan

Implement only the passive Codex quota extraction boundary in
`internal/sessionlog/`. The helper will consume one already-resolved,
managed-session JSONL path, validate that path against the supplied Codex
search roots, and bind the result to caller-supplied verified session/provider
context. It will return typed observation data and never decide whether work
may execute or whether quota is exhausted.

### Tasks and subtasks

1. Workspace and baseline
   - Keep the work on `polecat/sdk-41b.1`, rooted at fresh `origin/main`.
   - Record the worktree and branch metadata on `sdk-41b.1`.
   - Run the sessionlog baseline before source changes.
2. Typed extraction API
   - Add a small Codex-only observation type with provider/session identity,
     optional account/scope, observed timestamp, known model/effort, limit ID,
     and primary/secondary rate windows.
   - Represent provider-null or absent reset/reached values as nullable typed
     fields; preserve window units and percentages without converting them.
   - Expose only the search-root-validated entry point for an already-resolved
     path; do not discover auth files or scan unrelated session history.
3. Parser
   - Parse `event_msg`/`token_count` `rate_limits` even when `info` is null.
   - Track the latest preceding `turn_context` model and effort when present.
   - Select the newest valid structured quota snapshot in the bounded tail,
     ignore malformed/unrelated lines, and preserve its source timestamp.
   - Do not change `TailUsage`, token totals, context accounting, or execution
     / hard-exhaustion policy.
4. Evidence tests and fixtures
   - Add labelled synthetic JSONL controls for reminder-only input, explicit
     reached evidence, null-info refreshes, primary/secondary window lengths,
     null/unknown/reset fields, stale superseded snapshots, malformed input,
     and path/session/scope isolation.
   - Keep existing Codex token/model attribution tests unchanged and passing.
5. Review and handoff
   - Run focused `internal/sessionlog` tests, affected tests, vet, and the
     configured fast baseline as practical.
   - Review the diff for scope leakage and document exact evidence in the
     bead before pushing `polecat/sdk-41b.1` to the refinery.

## Architectural changes

One new parser surface is added below the controller: sessionlog observes a
resolved Codex transcript and emits passive typed evidence. The boundary
accepts verified session/provider context from its caller, leaves unknown
account/scope/model/effort fields empty, and does not infer permission,
recovery eligibility, reset availability, or hard exhaustion. Existing token
accounting remains on its current path. No controller, recovery adapter,
Wayfinder, City, wire schema, or deployment code is touched.

## Test plan

- First run the existing `internal/sessionlog` package tests to establish the
  base result and layer observed.
- Write failing parser tests before implementation for each acceptance vector.
- Run `go test ./internal/sessionlog` and the affected-test command, then
  `go vet ./...` and the repository fast baseline required by the formula.
- Tests prove parser behavior over synthetic JSONL at the sessionlog layer;
  they do not prove controller projection, live Codex behavior, account
  identity, or quota recovery.

## Support structures, docs, and execution order

The support artifact is a labelled synthetic replay fixture/helper next to the
sessionlog tests plus this plan and the temporary `agent-execution.log`.
No product or architecture documentation change is expected because this is a
new internal extraction seam, not a public contract. Execute workspace setup,
baseline, red tests, implementation, focused checks, review, commit, and
handoff in that order.

## Stability strategy and blocker avoidance

Preserve the existing polecat-home untracked plan and unrelated commit; all
edits stay in the per-bead worktree. Use `t.TempDir()`, no network/provider
calls, no auth-file scans, no live inference, no deployment/reload/restart,
and never clear the shared Go build cache. If the existing source contract is
insufficient, stop at the sessionlog boundary and escalate rather than adding
controller policy. If tests reveal a pre-existing failure, record the exact
package/layer and do not weaken expectations.

## Candidate subagent-parallel work

No subagent split is justified: the API shape, parser, and fixtures are one
small coupled boundary, and parallel edits would increase overlap risk. Read-
only history/source inspection may be parallelized, but implementation and
tests remain serialized in this worktree.

## Proxy-domain audit

- Target truth: a resolved managed Codex session log yields accurate passive
  rate-limit evidence with honest scope and nullability, without changing
  token accounting or granting execution permission.
- Required evidence layer: provider-free `internal/sessionlog` parser tests
  over literal JSONL, plus existing token/model tests and static checks.
- Useful but insufficient proxies: a compiling type, a passing fixture helper,
  a path-containment check, or a parsed `used_percent` alone.
- Tempting false completion: treating a reset hint, high percentage, or
  available reset count as hard exhaustion, or proving only a helper task
  completed.
- If this plan succeeds while the bug remains, the likely gap is above this
  layer: controller scope binding or recovery policy could still misclassify
  or launch work. This task explicitly does not claim that behavior.

## Planning pass 1 — initial critique

Critique: The plan correctly limits ownership to `internal/sessionlog`, but
the API must make the already-resolved-path boundary explicit and avoid
silently treating an empty context as verified. The test list must distinguish
an older snapshot being superseded from an observation being safe to act on.

Critical evaluation of the critique: Both concerns are material. Requiring a
non-empty provider/session identity and root validation makes the parser
boundary fail closed; returning the latest observation still provides data
without embedding freshness or recovery policy. The plan should also state
that nullable fields preserve unknown rather than synthesize zero values.

Roll-up into the plan: Add context validation, root containment, latest-valid
snapshot semantics, and explicit unknown/null preservation to the API and
tests. Keep freshness/eligibility decisions outside this package.

No-change decisions: Do not add a new event bus, persistence store, recovery
adapter, account credential reader, provider probe, or hard-exhaustion
classifier. Do not change existing token accounting expectations.

## Planning pass 2 — refined critique

Critique: The revised plan still needs to avoid coupling account/scope to
filesystem discovery and should capture effort only when the transcript
actually supplies it. It should identify how malformed current lines behave
without discarding the last valid observation.

Critical evaluation of the critique: These are boundary and evidence issues,
not polish. The context is caller-owned metadata, and the parser should retain
the last valid structured snapshot when later input is malformed while
preserving the valid snapshot timestamp. Effort must remain empty when absent.

Roll-up into the plan: Keep account/scope solely in the verified context,
track model/effort from in-log turn context, and test malformed trailing and
unrelated records explicitly.

No-change decisions: Do not infer account, scope, model, effort, reset time,
or reached state from percentages, plan names, auth files, or path names.

## Planning pass 3 — final critique and roll-up

Critique: The plan is complete for the assigned slice, but the handoff must
name the exact artifact and distinguish parser evidence from controller
integration evidence. Temporary execution logging must not become product
code or an accidental committed artifact.

Critical evaluation of the critique: The handoff distinction is required by
the parent coordinator, and the temporary log is operational evidence only.
The implementation can be accepted without claiming the dependent controller
lane or live quota behavior.

Roll-up into the plan: Record source commit, branch, focused test commands,
and the exact unproven higher-layer boundaries in the bead/refinery handoff;
keep `agent-execution.log` temporary and excluded from the source change.

No-change decisions: No docs update, no cross-repository edits, no live
verification, no deployment, and no changes to the parent/coordinator or
dependent beads beyond the normal handoff metadata.

## Lost-information check

The final plan retains the required scope, typed boundary, TDD order, fixture
coverage, evidence-layer limits, no-policy-in-Go decision, stability rules,
branch/handoff contract, and all no-change decisions. Counter increments below
record the three planning passes.
