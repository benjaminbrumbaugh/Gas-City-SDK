# sdk-wx8 Launch-Origin Implementation Plan

counter: 3
status: planning

## Pass 0: full plan

### Objective

Implement the accepted launch-origin contract for `gc sling` and convoy
persistence. A normal agent launch captures an opaque, harness-neutral actor
route by default; explicit registration or prompt instructions are not
required. Missing or untrustworthy routes preserve the existing behavior.
Convoy lifecycle identity must survive persistence and remain usable by the
CLI/API without leaking provider-specific semantics.

### Tasks and subtasks

1. Reconstruct the current contract and boundaries.
   - Read the accepted contract/design and inspect current sling, convoy, CLI,
     API, and persistence paths.
   - Identify all launch entry points and legacy no-route behavior.
   - Search history for prior working implementation before inventing APIs.
2. Define the capture boundary.
   - Derive origin only from trustworthy runtime/session context available to
     `gc sling`.
   - Keep actor identity opaque and do not parse or persist credentials,
     callback URLs, harness names, or prompt-derived flags.
   - Make ordinary agents eligible by default while keeping explicit opt-out
     and absent-route compatibility behavior clear.
3. Persist convoy lifecycle identity.
   - Add the smallest typed/domain representation compatible with the accepted
     contract and existing convoy storage adapters.
   - Preserve old records and old create paths when origin is absent.
   - Ensure clone, serialization, API projection, and CLI output agree.
4. Add focused evidence.
   - Test automatic capture through CLI/API launch paths.
   - Test opaque identity round-trip and lifecycle persistence.
   - Test absent/untrustworthy actor routes retain legacy behavior.
   - Run race-safe persistence tests where shared state is involved.
5. Review, document, and hand off.
   - Update only source-of-truth contract docs if implementation changes the
     accepted wording.
   - Run affected tests, broader required gates, format/vet checks, and
     pre-commit checks.
   - Commit cohesive changes, push `polecat/sdk-wx8`, and hand off to refinery.

### Architectural changes

- Keep automatic origin derivation in a small launch/runtime boundary, not in
  generic formula or role logic.
- Keep convoy identity in the convoy domain/persistence boundary; CLI and API
  remain projections and must not reimplement lifecycle decisions.
- Represent actor routes as opaque typed data. Provider-specific parsing is
  forbidden in generic SDK code.
- Preserve compatibility by treating an unavailable or failed trust check as
  no origin rather than synthesizing an identity.

### Test plan

- Unit tests for origin capture: ordinary agent default, trustworthy route,
  absent route, malformed/untrusted route, and opaque round-trip.
- Persistence tests for convoy creation/update/load and lifecycle identity
  preservation across legacy records.
- CLI/API tests proving the same behavior through public launch projections.
- Focused package tests first, then affected test command, `go vet`, and the
  repository gates required by the formula.

### Support structures and evidence

- Use the existing typed convoy and sling abstractions; add no speculative
  interface until a second implementation requires it.
- Keep tests as the primary evidence artifact. A semantic CLI/API response
  proves projection behavior only; it does not prove an actual harness
  callback. Persistence round-trip proves storage behavior only; it does not
  prove runtime actor trust. Both are required and neither substitutes for the
  other.
- Maintain `agent-execution.log` for completed subtasks.

### Documentation

- Read `engdocs/plans/convoy-callback-contract-v1.md` and update it only if
  current implementation semantics make an accepted contract statement stale.
- Do not touch product dashboard code or the retained embedded dashboard.

### Execution order

1. Load context and verify branch/worktree.
2. Run baseline/preflight checks.
3. Audit inherited diff, contract, current code, and history.
4. Write/adjust tests first, then implement the minimal boundary changes.
5. Run focused and affected tests; self-review all touched surfaces.
6. Commit, push, reassign to refinery, and drain.

### Stability strategy

- Never overwrite inherited changes blindly.
- Avoid global state and test-order dependence; use `t.TempDir` and isolated
  fakes.
- Keep legacy records readable and absent-origin operations idempotent.
- Use atomic persistence conventions already present in the repository.
- Do not alter shared tmux/Dolt state as part of tests.

### Blocker avoidance

- Use git history and existing contract tests before designing replacements.
- Keep all work in the recorded per-bead worktree and branch.
- If a baseline failure is pre-existing, deduplicate in beads and continue;
  do not fix unrelated base failures.
- If Dolt is slow, collect the prescribed diagnostics before escalation.

### Candidate parallel work

- Independent history/contract archaeology and API/CLI call-site inventory can
  be inspected in parallel, but shared worktree edits must remain serialized.
- Persistence and projection tests may be designed independently, then merged
  into one cohesive implementation after the boundary is confirmed.

### Proxy-domain audit

- Target truth: `gc sling` automatically records the launching actor route on
  a convoy when a trustworthy route exists, and convoy lifecycle identity is
  persisted and projected without provider coupling.
- Required evidence layers: runtime/launch capture tests, domain persistence
  round-trip tests, and CLI/API projection tests.
- Cheaper but insufficient proxies: helper-only unit tests, JSON shape checks,
  or a semantic preview of one response.
- False-completion substitution to avoid: treating an explicit registration
  flag, a prompt instruction, or a test fixture that directly injects origin
  as proof that ordinary launch capture works.
- Even if this plan succeeds, the original bug could remain if the production
  `gc sling` path bypasses the helper, if trust checks accept stale/forged
  context, or if legacy storage adapters drop the field; the execution must
  exercise those concrete paths.

## Pass 1: critique and roll-up

### Critique

- The plan correctly isolates runtime capture, domain persistence, and
  projections, but it does not yet name the exact current entry points or the
  accepted field names. The archaeology task must resolve those before tests
  are finalized.
- “Explicit opt-out” is not in the assigned contract; adding behavior beyond
  absent/untrustworthy compatibility risks scope creep. Remove that wording
  unless current contract evidence requires it.
- The plan says “provider-specific parsing is forbidden” but should identify
  the one trust boundary that can inspect process/session context.
- The test plan must distinguish a route that is present from one that is
  trustworthy, and must test no-registration ordinary-agent behavior directly.
- A plan file is a required artifact, not a task ledger; beads remain the
  durable work tracker.

### Critical evaluation of critique

- Naming entry points before archaeology would guess; retaining a discovery
  gate is safer than inventing names.
- Removing unconfirmed opt-out behavior narrows the implementation to the
  accepted request and avoids a new compatibility surface.
- The trust-boundary concern is valid, but the exact boundary should be
  selected from current architecture/history rather than prescribed here.
- The evidence distinction is actionable and should be rolled into the test
  matrix.

### Revised roll-up

- Keep archaeology first and make exact call-site/field discovery a hard
  prerequisite for implementation.
- Remove “explicit opt-out” from the objective and test plan; only absent or
  untrustworthy route compatibility is assumed until the contract says more.
- Require one production-path test that invokes sling without registration or
  prompt flags, plus separate trust-failure tests.
- Keep the plan file separate from bead tracking and log execution milestones
  in `agent-execution.log`.

### No-change decisions

- Keep the typed-domain boundary and no-upward-dependency constraints.
- Keep separate runtime, persistence, and projection evidence layers.
- Keep history search before rebuilding missing behavior.

## Pass 2: critique and roll-up

### Critique

- The revised plan still leaves “trustworthy runtime/session context” vague;
  this could invite a provider-specific implementation in generic code.
- It does not explicitly require checking whether the inherited diff is
  already a complete implementation or contains wrong assumptions.
- It does not state how to handle branch divergence (`origin/main` is ahead).
- It should require testing legacy convoy records with no new field, not just
  absent-origin create calls.

### Critical evaluation of critique

- Trust context must be derived from an existing opaque route/capability at the
  sling boundary; the implementation can only settle the exact source after
  reading current code and contract.
- Auditing inherited bytes before TDD edits is necessary because this session
  resumed with uncommitted changes.
- Branch setup must follow formula metadata and rebase only through the
  formula’s safe workflow; local divergence should not be “fixed” by reset.
- Legacy read compatibility is a concrete acceptance condition and belongs in
  both persistence tests and review.

### Revised roll-up

- Add an explicit inherited-diff audit and contract-to-code matrix before
  changing tests.
- Select the trust source only from existing opaque route data or a narrowly
  scoped launch boundary; reject prompt/role/harness inference.
- Verify/reconcile branch state using the recorded `polecat/sdk-wx8` branch and
  origin base without destructive checkout/reset.
- Add legacy-record decode/load tests and ensure round-trip tests observe the
  storage layer, not only in-memory structs.

### No-change decisions

- No new role names, harness names, credential fields, callback URL fields, or
  dashboard changes.
- No broad refactor of upstream-owned packages.
- No replacement persistence backend or speculative abstraction.

## Pass 3: critique and roll-up

### Critique

- The plan is now appropriately constrained, but “run broader required gates”
  must acknowledge known repository/environment failures without weakening
  the affected-test gate.
- The handoff step must preserve the implementation bead open and reassign it
  to refinery, per polecat protocol.
- The plan should explicitly verify generated/API contracts only if touched,
  avoiding an unnecessary dashboard gate.

### Critical evaluation of critique

- Recording layer-specific failures is compatible with the quality-gate
  requirement; affected tests caused by this change still must pass.
- Handoff semantics are mandatory and should be stated in execution order.
- Conditional generated-artifact verification prevents both omission and
  scope creep.

### Final roll-up

- Affected tests and focused tests are hard gates. Broader gate outcomes will
  be reported with the exact failing layer and whether pre-existing.
- Submit by pushing `polecat/sdk-wx8`, updating artifact metadata, reassigning
  the still-open work bead to refinery, and draining the session.
- Run API/OpenAPI/dashboard verification only when those generated surfaces
  are actually modified.

### No-change decisions

- No further architectural expansion is justified before the code/history
  audit.
- No subagent edits are needed until independent boundaries are confirmed;
  the shared worktree and inherited diff make serialized review safer.

## Approval and execution

The assigned implementation bead and polecat formula authorize execution after
this three-pass plan. No external approval wait is introduced; implementation
starts after the required preflight and audit gates.
