counter: 3

# Plan: enforce store affinity during `gc hook --claim`

## Pass 0 — initial plan

### 1. Full plan, tasks, and subtasks

Goal: ensure a rig-scoped `gc hook --claim` only offers and claims beads from
the store it can mutate, while making cross-store skips observable and keeping
empty `gc.routed_to` behavior explicit and consistent with an absent key.

Tasks:

1. Reconstruct the claim-path boundary.
   - Trace federated ready discovery, store selection, candidate decoding, and
     claim mutation.
   - Identify the smallest ownership boundary that can retain candidate store
     provenance without changing generic bead wire types.
   - Inspect history and existing cross-store/class-route behavior for
     compatibility constraints.
2. Define the candidate-affinity and route-metadata contracts.
   - State which hook scopes may claim which store candidates.
   - Decide whether an empty `gc.routed_to` is unrouted or rejected, matching
     absent-key behavior.
   - Define an explicit diagnostic for an excluded cross-store candidate.
3. Add focused regression tests first.
   - Verify cross-store candidates are excluded before claim mutation.
   - Verify the diagnostic is visible through the relevant hook output path.
   - Verify absent and empty `gc.routed_to` metadata have the same semantics.
   - Preserve assigned-work, class-route, and single-store behavior.
4. Implement the smallest maintainable fix.
   - Keep store-specific logic in `cmd/gc` hook claim orchestration.
   - Avoid adding residency fields to the canonical `beads.Bead` wire model
     unless provenance cannot be preserved at the hook boundary.
   - Keep upstream-owned changes minimal and isolated.
5. Verify and hand off.
   - Run focused tests, affected tests, `go vet ./...`, and the configured fast
     baseline as practical.
   - Review the diff, generated/surface impact, and test-layer limitations.
   - Commit to `polecat/sdk-8xp`, push, verify the remote ref, update metadata,
     and reassign the implementation bead to the refinery without closing it.

### 2. Architectural changes

Preferred shape: preserve candidate store provenance at the hook claim
boundary, using an internal candidate/store association or a scoped query
adapter. Do not make the generic `beads.Bead` aware of physical store origin:
IDs and configured prefixes are not a complete residency authority, and the
canonical bead model is shared by CLI/API/domain code. The claim mutation must
receive the same store identity that produced the candidate, and candidates
without affinity evidence must not be served to a rig-scoped pool.

If current federated shell output cannot carry provenance safely, use explicit
per-store discovery for rig-scoped claims rather than infer origin from IDs.
Retain class-routing behavior only where its existing in-process route proves
the destination store. No role-specific behavior or new primitive is needed.

### 3. Test plan and evidence discipline

Target truth: a rig-scoped hook claim cannot mutate or serve a bead from a
different physical beads store; excluded candidates produce an actionable
diagnostic; route metadata matching does not accidentally distinguish an empty
value from an absent value.

Required evidence layer: `cmd/gc` hook-claim orchestration tests that observe
the candidate source store, claim destination, returned work, and diagnostic
output. Add a process/integration test only if the existing harness can
exercise real isolated stores without depending on shared Dolt state.

Useful but insufficient proxies: pure route-matching tests, ID-prefix tests,
and tests that only assert a claim command was not called. They do not prove
that federated discovery retained the source-store identity. The tempting
false-completion substitution is changing expected output or adding a helper
test while the actual claim still runs against the wrong store. The original
bug could remain if the implementation uses bead ID prefixes as physical
residency or if diagnostics are emitted only to an unobserved logger.

### 4. Support structures

- Use the existing hook runner fakes and store-command seams for deterministic
  source/destination assertions.
- Keep `agent-execution.log` temporary and ignored; append one bounded record
  after each completed formula subtask.
- Use git history and `git diff upstream/main` to guard the fork boundary.
- No subagent work is split: this is one tightly coupled claim-path change,
  and no callable subagent capability is exposed in this session.

### 5. Documentation

No user-facing documentation change is expected. If the final contract changes
the `gc hook --claim` CLI diagnostic or ready output, update the nearest CLI
reference/test fixture and document only the externally observable behavior.

### 6. Execution order

Inspect and record contracts → write failing focused tests → implement the
affinity boundary → run focused and affected checks → review surface and
upstream diff → commit/push/refinery handoff.

### 7. Stability and blocker avoidance

Prefer additive internal seams and deterministic fakes. Do not restart Dolt or
touch shared stores unless an integration test requires it. Do not infer
physical residency from IDs when a query can retain provenance. If a test
reveals Dolt instability, collect the prescribed diagnostics before any
escalation or restart. Keep the implementation bead open for refinery review.

## Pass 0 critique

1. The plan correctly identifies provenance as the key boundary, but it leaves
   the exact source of provenance open and could overfit to a shell-output
   change. The implementation choice must be made after the complete call graph
   and history review.
2. The route contract is named but not yet tied to the precise existing helper;
   tests must distinguish ordinary routed work, workflow fallback, assigned
   work, and an empty key.
3. The evidence section appropriately rejects ID-prefix-only proof, but it must
   require at least one assertion connecting discovery store to claim store.
4. The handoff steps are complete, though baseline commands must follow
   `TESTING.md` rather than defaulting to a monolithic sweep.

## Pass 0 critique of critique

The critique catches the central uncertainty but could be clearer that the
contract is not “all federated rows are invalid”: class-resident and explicitly
assigned routes may be valid when their destination is proven. It also should
require checking whether the existing runner abstraction can carry provenance
without altering `gc ready`’s public JSON shape. The verification section
should name changed-test rationale: expectations may change only for a changed
contract, never merely to make the regression pass.

## Pass 0 roll-up: applied evaluation to critique

Refine the implementation investigation around three questions: (a) where
source-store identity is already known, (b) whether a candidate can carry that
identity privately through `tryHookClaim`, and (c) whether class/assigned
exceptions are already represented by route metadata. Require a failing test
that observes both sides of the discovery/mutation pair. Treat any changed
expectation as justified only by the store-affinity contract.

## Pass 0 roll-up: applied revised critique to plan

The next pass will choose between a private candidate wrapper and per-store
queries only after confirming existing seams and history. It will explicitly
preserve proven class and assigned routes, reject unproven cross-store rows,
and keep public ready JSON unchanged unless no safe internal route exists.

## Pass 0 no-change decisions

- No new primitive, role, beads wire field, or generic store abstraction.
- No product dashboard or docs-site changes.
- No broad refactor of `gc ready`, `internal/beads`, or `internal/storeref`.
- No test expectation changes before the contract is demonstrated.

## Pass 1 — call-graph findings and design narrowing

### 1. Full plan, tasks, and subtasks

The call graph confirms the failure boundary: split-city `gc ready --json`
returns a bare, first-leg-wins bead array, while `tryHookClaim` receives only
the decoded beads and the selected `hookStore`. The public ready row has no
store-origin field. `scopeFederatedHookStores` intentionally pins the
federated reader to the primary leg, so the claim loop can select a row from a
different physical store and then run `Claim` against the primary store.

The implementation must therefore retain origin privately or make the query
store-scoped before candidate selection. It must preserve three existing
contracts: city-scoped agents may serve across stores, class-route operations
may claim a binding-resident row after all work legs prove absence, and
co-resident IDs follow the ready reader's first-leg order. ID prefixes alone
cannot prove any of these.

### 2. Architectural changes

Options considered:

- Add store origin to the public `gc ready` JSON. Rejected unless necessary:
  it widens a compatibility wire contract and leaks an internal physical-store
  detail to every ready consumer.
- Remove federated discovery for all rig-scoped hooks. Rejected: it loses
  relocated-class visibility and risks regressing the existing class-route and
  co-resident ordering contracts.
- Probe only after a federated candidate is selected. Rejected as the sole
  fix: a probe can prove absence in the claim store, but cannot establish which
  of several stores produced a co-resident row and would make a valid class
  exception indistinguishable from an invalid ordinary row.
- Carry a private source-store association through the hook reader and claim
  loop, with a fail-closed skip and diagnostic for ordinary candidates whose
  source does not match the claimant's store. This is the preferred boundary;
  it keeps the public ready schema unchanged and lets class routing remain an
  explicit, separately proven exception.

The selected design will use a small `cmd/gc`-local representation for a
candidate plus source store, populated at the hook boundary. If the existing
shell runner cannot provide that association without changing its public
output, the fallback is a private store-scoped query mode used only by hook
claim, with a dedicated class-route read path retained. Any fallback must
leave the generic `beads.Bead`, `gc ready` JSON, and role configuration model
untouched.

### 3. Test plan and evidence discipline

The first regression will drive the complete injected claim loop with a
candidate observed from store A and a claimant whose mutation context is store
B. It must prove that no claim mutation or work result is emitted, and that
stderr contains the candidate ID and both store identities. A control will
prove that a same-store candidate claims normally. Separate table cases will
assert absent and explicitly empty `gc.routed_to` take the same route state;
workflow `gc.run_target` remains the only documented fallback.

The tests observe the hook-claim layer, not an actual Dolt process. They prove
source-to-destination affinity and diagnostic emission in deterministic runner
seams, but do not prove a live backend's physical topology. Existing split-city
integration/conformance tests remain required for that backend layer.

### 4. Support structures

Prefer extending the existing runner/result seams over a second parser. Keep
source association immutable while ranking and claiming. Reuse
`sameHookStore`/store identity helpers if they express physical identity; do
not compare only display labels when environments distinguish stores.

### 5. Documentation

The external contract is a stderr skip diagnostic and unchanged JSON. Update
CLI help or docs only if implementation shows that the diagnostic wording is
operator-facing enough to warrant a reference entry; no dashboard surface is
in scope.

### 6. Execution order

Read `TESTING.md` → run preflight focused tests → add failing affinity and
empty-route tests → implement private provenance/affinity guard → run affected
and broader checks → review and hand off.

### 7. Stability and blocker avoidance

Do not modify shared Dolt state or restart Dolt. Keep the change confined to
`cmd/gc` unless a narrowly required private ready reader seam is proven. If
source identity cannot be retained safely, fail closed with a diagnostic
rather than silently claim an unproven row.

## Pass 1 critique

1. The private-association design is directionally correct but still assumes
   the shell query can expose origin; the code currently returns one string.
   The next pass must select a concrete mechanism rather than leave a fallback
   as an implementation escape hatch.
2. The test description must avoid asserting only a skipped mutation: it must
   exercise candidate ranking, source identity, and the final stderr path.
3. The plan must state whether “same store” means the selected `hookStore` or
   the claimant's configured rig. Those are equivalent only for rig-scoped
   production stores, not for city-scoped agents or class bindings.

## Pass 1 critique of critique

The critique is valid. A shell command's JSON cannot be trusted to carry
private state unless the runner is changed. The most maintainable concrete
route is to represent source at the point where `gc ready` federates its own
legs, then let the hook invoke that typed path in-process when it recognizes
the built-in federated query. Custom work queries must retain their existing
shell behavior and cannot receive inferred provenance. The affinity predicate
must be scoped to rig-backed claim stores; city-scoped agents remain wildcard,
and class-route binding claims are handled by `hookClaimClassRoute`.

## Pass 1 roll-up: applied evaluation to critique

Before editing, inspect whether `cmdHookWithOptions` already has enough city
and topology context to invoke a typed ready reader, and whether its shell
runner abstraction is used by custom-query tests that must remain unchanged.
Define a candidate's physical source as the ready leg selected by the same
first-leg-wins merge used by `gc ready`; define a claim destination as the
`hookStore` passed to `tryHookClaim`, with the class binding explicitly outside
that comparison.

## Pass 1 roll-up: applied revised critique to plan

The implementation task is narrowed to a private typed federated-read seam or
an equivalent hook-only query result that carries `readyLeg` ownership. It
must be opt-in for built-in federated reads, preserve custom commands, and
filter/diagnose before any claim CAS. Empty route handling will be expressed
through a named route-state helper so map absence and `""` cannot diverge.

## Pass 1 no-change decisions

- Keep `gc ready`'s public array and schema unchanged.
- Keep city-scoped cross-store eligibility unchanged.
- Keep class-route and co-resident first-leg-wins behavior unchanged.
- Do not use bead ID prefixes as residency authority.

## Pass 2 — final architecture and lost-information audit

### 1. Full plan, tasks, and subtasks

The final implementation slice is:

1. Add a private claim candidate/source model and a source-aware hook query
   result path, or the smallest equivalent adapter at the existing
   `bestStoreWithWork`/`tryHookClaim` boundary.
2. Enforce the guard only for rig-scoped ordinary work: source and claim
   destination must be the same physical store; otherwise skip the candidate,
   emit a clear diagnostic, and continue looking for eligible same-store work.
3. Leave assigned/self-resume and proven class-binding claims on their current
   routes, while ensuring an unproven ordinary row cannot reach `Claim`.
4. Make route metadata state explicit: absent and empty `gc.routed_to` are
   both `unrouted`; only a workflow's `gc.run_target` supplies its existing
   fallback. Add focused tests for each branch.

### 2. Architectural changes

The guard belongs in `cmd/gc` because it governs the CLI's read-to-write
boundary. It must not move into `internal/beads` or generic store APIs: those
layers cannot know the claiming rig, ready-leg order, or class-route exception.
The source association is invocation-local and never serialized into a bead,
event, API response, or worktree state.

If the current built-in command is inseparable from a shell-only runner, the
safe implementation is to query each physical store separately for ordinary
rig-scoped candidates and retain the existing typed class route as a separate
claim source. Do not accept a best-effort provenance guess. The code review
must explicitly record which route was chosen and why it preserves public
wire compatibility.

### 3. Test plan and evidence discipline

Run the focused `cmd/gc` tests first, then the affected package command from
`TESTING.md`, `go vet ./...`, and the fast baseline. Test expectations change
only when the store-affinity contract requires exclusion; existing class,
city-scoped, assigned, and co-resident expectations are controls, not to be
weakened.

Proxy audit: target truth is “a rig-scoped claim is mutated only in its
permitted physical store.” Required evidence is a hook-layer test that ties
source, destination, mutation, returned bead, and diagnostic together. A
route helper unit test, an ID-prefix test, or a passing parser test alone is
insufficient. If all planned tests pass while the original bug remains, the
likely cause is that tests bypass the federated reader, infer source from the
ID, or assert stderr without proving which claim destination was used; review
must reject that result.

### 4. Support structures

Use existing fakes and store identity helpers. Keep `agent-execution.log`
temporary and append one bounded record after each completed formula subtask.
No parallel subtask is safe because the source model, claim loop, and tests
share one boundary.

### 5. Documentation

No docs change unless the final stderr diagnostic is documented as a CLI
operator contract. The public `gc ready` schema and dashboard contracts remain
unchanged.

### 6. Execution order

Finalize plan → read `TESTING.md` → preflight → failing tests → implementation
→ focused/affected/full verification → diff review → commit, push, metadata,
refinery handoff, and drain.

### 7. Stability and blocker avoidance

Use normal Go cache; never clear it. Avoid live Dolt integration unless an
existing tagged test is part of the affected suite. Preserve the implementation
bead as open and leave no known cleanup beyond the handoff.

## Pass 2 critique

The final plan now has a concrete enforcement boundary and proxy audit, but it
still presents two implementation mechanisms. That is acceptable only as a
fail-closed engineering decision: the code must choose the mechanism the
current architecture can prove, and tests must reject any loss of source
identity. The phrase “permitted physical store” also needs to account for the
existing leading/city co-resident and class-binding exceptions without making
them accidental bypasses.

## Pass 2 critique of critique

The remaining risk is not ambiguity in the product contract but accidental
scope widening. The implementation should begin with the narrowest existing
source fact: per-store runner invocations already know their `hookStore`; a
federated primary invocation does not. If no private source can be carried,
the guard must reject candidates from that unproven invocation or route the
query through per-store reads. Existing class tests are the evidence that any
binding exception is explicit, not a reason to disable the guard globally.

## Pass 2 roll-up: applied evaluation to critique

The implementation will choose the smallest source-preserving seam discovered
in preflight. It will not widen `hookStore` semantics, public ready output, or
city-scoped eligibility. The tests will include a control for the federated
reader's selected source and a negative control for an unproven foreign row.

## Pass 2 roll-up: applied revised critique to plan

Execution may now begin. The plan's non-negotiable acceptance checks are:
source/destination affinity before CAS, explicit skip diagnostics, empty/absent
route equivalence, preserved class/city/assigned behavior, and no public wire
change absent a separately justified contract update.

## Pass 2 no-change decisions

- No new exported API or generic store abstraction.
- No changes to Wayfinder, dashboard code, or shared rig checkout.
- No implementation-bead closure; Refinery owns verification and closure.

## Lost-information check after three passes

Retained: the exact federated-reader failure mode; the distinction between
physical source, claim destination, route target, city-scoped wildcard, and
class-binding exception; the empty-vs-absent route contract; TDD and evidence
requirements; public-wire and upstream-boundary constraints; verification and
handoff obligations. Nothing from Pass 0 was discarded; the later passes made
the source-provenance decision conditional on what the current runner can
actually prove and require fail-closed behavior when it cannot.
