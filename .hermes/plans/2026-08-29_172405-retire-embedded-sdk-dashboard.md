# Disable the Embedded SDK Dashboard UI Implementation Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** Stop Benjamin's deployed Gas City supervisor from serving the upstream embedded browser UI, preserve every API and run-projection capability consumed by the separately managed `Gas-City-Dashboard`, and make every operator and agent-facing dashboard path point to the correct product without deleting upstream dashboard source.

**Architecture:** Retain `dashboardspa` and `dashboardbff` as upstream-owned compatibility code. Add durable `[supervisor] embedded_dashboard = false` configuration that gates only SPA mounting and embedded deep-link publication while leaving the BFF/API plane running; add optional `[supervisor] dashboard_url` for the canonical external dashboard front door. Make Sling, `gc dashboard`, foreground/background startup output, shipped skills, and documentation consume the same ownership decision instead of inferring that a healthy API implies an embedded UI.

**Tech Stack:** Go 1.27, TOML supervisor configuration, `net/http`, Cobra CLI, Huma supervisor APIs, repository Markdown and generated CLI docs, Python `unittest` plus candidate integration checks for the external dashboard.

---

## Decision and scope

### Required outcomes

1. Benjamin's supervisor serves no embedded dashboard HTML or embedded SPA deep links.
2. The supervisor continues serving the BFF, typed API, and run-projection contracts used by `/Users/benjaminbrumbaugh/Documents/Gas City/Gas-City-Dashboard`.
3. `gc dashboard` opens the configured external dashboard, not the supervisor root, in Benjamin's posture.
4. Sling, supervisor startup, docs, and shipped agent skills never advertise a disabled embedded UI.
5. Future upstream pulls continue updating retained dashboard source normally; this fork avoids a permanent modify/delete conflict boundary.

### Configuration contract

Benjamin's durable machine configuration becomes:

```toml
[supervisor]
embedded_dashboard = false
dashboard_url = "http://localhost:8400/"
```

- `embedded_dashboard` is an optional boolean represented in Go as `*bool`, so absence preserves the upstream default of `true` and explicit `false` is durable.
- `dashboard_url` is an optional absolute HTTP(S) URL for the operator-facing dashboard front door. It does not imply that the external dashboard implements the embedded SPA's `/city/.../runs/...` routes and must never be used to synthesize Sling deep links.
- Existing `GC_SUPERVISOR_DASHBOARD=0` remains the stronger full-disable escape hatch. It continues removing the embedded UI and BFF `/api` plane; this plan does not change or replace it.
- Do not add `GC_SUPERVISOR_DASHBOARD_UI`. A new process environment variable would be lost when service definitions are regenerated from an unset shell and would require expanding the frozen `GC_*` vocabulary.

### Explicit no-change decisions

- Do **not** delete, tombstone, relocate, or rename `internal/api/dashboardspa` or `internal/api/dashboardbff`.
- Do **not** remove dashboard Make targets, npm manifests, generated clients, Playwright tests, hooks, CI jobs, or upstream regeneration machinery.
- Do **not** remove `dashboard_url` from API response types or OpenAPI. It remains an optional embedded-SPA deep link and is omitted when the SPA is not mounted.
- Do **not** hardcode `http://localhost:8400/` in generic SDK source or API contracts; it belongs in Benjamin's supervisor configuration and fork-facing documentation.
- Do **not** redirect Sling deep links to the external dashboard unless that dashboard later implements and tests the same route contract.
- Do **not** combine broader fork/upstream convergence or existing SPA cleanup with this change.

### Why this replaces the previous version

The deletion plan created a large permanent merge boundary. The first replacement correctly found the SPA composition seam but used a shell-derived environment variable and missed independent publication paths. This revision keeps the narrow seam while making the policy durable in `supervisor.toml`, fixing Sling and CLI inference, covering every consumed external-dashboard route, and reconciling all active agent guidance.

---

## Current verified context

- `cmd/gc/supervisor_dashboard.go:54-91` currently couples the full environment gate, BFF/run plane, static SPA, and `WithDashboardBase` publication.
- `cmd/gc/cmd_supervisor.go:1324-1450` already loads `supervisor.toml` before dashboard composition and separately prints API and dashboard startup lines.
- `internal/supervisor/config.go:26-60` owns the durable `[supervisor]` schema and can distinguish absent/default from explicit false with a pointer boolean.
- `cmd/gc/sling_dashboard_link.go:43-110` independently treats `GET /api/health == 200` as proof that the SPA exists. That inference becomes false when the BFF remains mounted but the SPA does not.
- `cmd/gc/cmd_dashboard.go:90-160` currently opens any resolved API root without proving that a browser UI is served there.
- `cmd/gc/supervisor_dashboard.go:182-195` and its direct/systemd callers publish background-start hints independently from foreground startup state.
- The external dashboard directly consumes `/api/city/{city}/supervisor-status`, `/api/health/system`, and `/v0/city/{city}/agents`. Its direct-Beads fallback additionally consumes paginated `/v0/city/{city}/beads` and `/v0/city/{city}/beads/ready`, including `X-Gc-Index`, `partial`, `total`, cursor, and completeness invariants.
- Root instructions are not sufficient alone: `internal/bootstrap/packs/core/skills/gc-dashboard/SKILL.md`, `docs/getting-started/dashboard.md`, `CONTRIBUTING.md`, and generated CLI docs currently teach the embedded UI as the dashboard.
- The canonical SDK checkout is dirty. Implementation must use a clean task worktree and preserve all existing tracked and untracked work.

---

## Task 1: Establish a clean implementation boundary and baseline

**Objective:** Isolate implementation from unrelated canonical-checkout work and capture the current behavior before edits.

**Files:** None.

**Step 1: Capture exact repository state**

From `/Users/benjaminbrumbaugh/Documents/Gas City/Gas-City-SDK`:

```bash
git status --short --branch
git rev-parse HEAD origin/main upstream/main
git merge-base origin/main upstream/main
git worktree list --porcelain
```

Record the exact output in the implementation bead. Do not stash, reset, clean, or absorb the canonical checkout.

**Step 2: Create an isolated task worktree**

Create `temp/disable-embedded-dashboard-ui` from the intended fork base on a descriptive branch. Confirm `temp/` is ignored before use.

**Step 3: Run focused SDK baselines**

```bash
go test ./internal/supervisor -run 'Config' -count=1
go test ./cmd/gc -run 'Dashboard|SlingDashboard|Supervisor' -count=1
go test ./internal/api/dashboardbff -run 'Health|SupervisorStatus|RunCensus' -count=1
```

Expected: PASS, or record a pre-existing failure before source changes.

**Step 4: Run the external-dashboard baseline**

From `Gas-City-Dashboard/assets/gas-town-dashboard`:

```bash
python3 -m unittest test_server.py
```

Expected: PASS. This proves only the external dashboard's current unit/wire fixtures, not candidate SDK compatibility.

---

## Task 2: Add durable supervisor dashboard policy with RED tests

**Objective:** Define a backward-compatible persistent configuration contract before changing runtime composition.

**Files:**

- Modify: `internal/supervisor/config.go`
- Modify: `internal/supervisor/config_test.go`
- Modify if seeded examples require it: `test/acceptance/helpers/env.go`

**Step 1: Write configuration tests**

Add tests proving:

- absent `embedded_dashboard` means enabled;
- explicit `embedded_dashboard = true` means enabled;
- explicit `embedded_dashboard = false` means disabled;
- `dashboard_url` parses from TOML without normalization loss;
- existing supervisor config with neither field preserves current behavior.

Run:

```bash
go test ./internal/supervisor -run 'Dashboard|Config' -count=1
```

Expected: FAIL because the fields and helper do not exist.

**Step 2: Add the minimal schema**

Add to `supervisor.Section`:

```go
EmbeddedDashboard *bool  `toml:"embedded_dashboard,omitempty"`
DashboardURL      string `toml:"dashboard_url,omitempty"`
```

Add `EmbeddedDashboardEnabled() bool`, returning true when the pointer is nil and otherwise returning the explicit value.

Do not add a new environment variable or service allowlist entry.

**Step 3: Validate canonical dashboard URLs at the consumer boundary**

Add one shared helper in `cmd/gc` that trims whitespace and accepts only an absolute `http` or `https` URL with a non-empty host and no userinfo. Invalid configured URLs must produce an actionable CLI/config error and must never be passed to the OS browser opener.

**Step 4: Run GREEN**

```bash
go test ./internal/supervisor -run 'Dashboard|Config' -count=1
go test ./internal/testenv -run TestGCEnvReadBaseline -count=1
```

Expected: PASS with no environment-vocabulary golden change.

**Step 5: Commit**

```bash
git add internal/supervisor/config.go internal/supervisor/config_test.go test/acceptance/helpers/env.go
git commit -m "feat(supervisor): add durable dashboard policy"
```

Stage only files actually changed.

---

## Task 3: Split SPA mounting from the retained BFF/run plane

**Objective:** Make `embedded_dashboard = false` remove only browser-facing embedded behavior.

**Files:**

- Modify: `cmd/gc/supervisor_dashboard.go`
- Modify: `cmd/gc/cmd_supervisor.go`
- Modify: `cmd/gc/supervisor_dashboard_test.go`

**State matrix:**

| Full gate | Config | Plane/run projection | `/api` BFF | SPA `/` | embedded base/link |
|---|---|---|---|---|---|
| default | absent/true | present | present | present | present |
| default | false | present | present | 404 | absent |
| `GC_SUPERVISOR_DASHBOARD=0` | any | fallback run plane only | absent | 404 | absent |

**Step 1: Write failing composition tests**

Test the three rows above at the mux layer. In the UI-disabled row require:

- returned plane is non-nil;
- `uiMounted` is false;
- `GET /api/health` is 200;
- `GET /` is 404 and not HTML;
- no dashboard base is installed;
- the run-census source remains attached.

Also prove the full environment gate retains its current no-BFF behavior.

**Step 2: Make mounted state explicit**

Change the local composition seam to return:

```go
(*dashboardbff.Plane, bool, error)
```

The boolean means **embedded SPA mounted**. Pass `supCfg.Supervisor.EmbeddedDashboardEnabled()` into this seam rather than rereading configuration or process environment in multiple places.

**Step 3: Preserve the full-disable branch**

When `dashboardEnabled()` is false, return `(nil, false, nil)`. Keep the existing unmounted run-census fallback in `cmd_supervisor.go`.

**Step 4: Always compose the retained plane when the full gate is enabled**

Create `dashboardbff.Plane` and always install `WithRunCensusSource(plane)` and `WithAPIPlane(plane.Handler())`.

Only when `embedded_dashboard` is enabled:

- construct `dashboardspa.NewStaticHandler()`;
- install `WithStaticHandler(spa)`;
- install `WithDashboardBase(...)` for non-wildcard binds;
- return `uiMounted=true`.

**Step 5: Use explicit state for foreground output**

Pass the returned mounted state and configured canonical dashboard URL into the startup renderer. If `dashboard_url` is configured, print that URL as the external dashboard. Otherwise print the supervisor root only when the SPA is actually mounted. Never label an API-only root as a dashboard.

**Step 6: Run focused tests**

```bash
go test ./cmd/gc -run 'Dashboard|Supervisor' -count=1
go test ./internal/api/dashboardbff -run 'Health|SupervisorStatus|RunCensus' -count=1
```

Expected: PASS.

**Step 7: Commit**

```bash
git add cmd/gc/supervisor_dashboard.go cmd/gc/supervisor_dashboard_test.go cmd/gc/cmd_supervisor.go
git commit -m "fix(supervisor): detach embedded UI from dashboard APIs"
```

---

## Task 4: Correct every independent dashboard publication path

**Objective:** Ensure no CLI or background path infers an embedded UI from API health.

**Files:**

- Modify: `cmd/gc/sling_dashboard_link.go`
- Modify: `cmd/gc/sling_dashboard_link_test.go`
- Modify: `cmd/gc/cmd_sling.go` tests covering human and JSON output
- Modify: `cmd/gc/cmd_dashboard.go`
- Modify: `cmd/gc/cmd_dashboard_test.go`
- Modify: `cmd/gc/supervisor_dashboard.go`
- Modify: focused tests for `cmd/gc/cmd_supervisor_lifecycle.go` and `cmd/gc/supervisor_systemd_delegate.go`

**Step 1: Replace the Sling health inference with a browser-surface probe**

Rename the probe/hook so its contract is UI availability, not BFF health. Require `GET /` to return HTTP 200 with an HTML content type before generating an embedded `/city/...` link. Keep the existing tight timeout and silent degradation.

Do not use configured `dashboard_url` for Sling deep links.

**Step 2: Add the adversarial Sling regression**

Serve `/api/health` as 200 and `/` as 404. Assert:

- `slingDashboardURL` returns empty;
- human Sling output contains no dashboard link or lag warning;
- JSON Sling output omits/empties `dashboard_url`;
- the API Sling path also omits its link because `WithDashboardBase` was not installed.

Keep positive tests where root returns HTML and the generated run detail/list URL is correct.

**Step 3: Give `gc dashboard` an explicit destination policy**

Resolve in this order:

1. a valid configured `[supervisor] dashboard_url`;
2. an explicitly supplied `--api` origin only when its root passes the SPA probe;
3. a live supervisor origin only when its root passes the SPA probe;
4. a standalone API origin only when its root passes the SPA probe;
5. otherwise no dashboard destination.

When no destination exists, do not open a browser. Print that the embedded dashboard is disabled/unavailable and name the `dashboard_url` configuration field for an external front door. Preserve the command's informational exit behavior unless existing command contracts require an error for an invalid configured URL.

**Step 4: Correct foreground and background startup hints**

Use a shared renderer/resolver for:

- foreground `supervisor run`;
- direct background start;
- launchd-managed start;
- delegated systemd start.

The background paths execute after readiness. They must load the durable supervisor config, print configured `dashboard_url` when present, and otherwise probe the served root before advertising the embedded URL. The process environment alone is not authoritative.

**Step 5: Run focused RED/GREEN loops**

```bash
go test ./cmd/gc -run 'SlingDashboard|DashboardNotice|DashboardStart|Supervisor.*Dashboard' -count=1
```

Expected final result: PASS, including `/api/health = 200` with root 404.

**Step 6: Commit**

```bash
git add cmd/gc/sling_dashboard_link.go cmd/gc/sling_dashboard_link_test.go cmd/gc/cmd_sling.go cmd/gc/*sling*test.go cmd/gc/cmd_dashboard.go cmd/gc/cmd_dashboard_test.go cmd/gc/supervisor_dashboard.go cmd/gc/*supervisor*test.go
git commit -m "fix(cli): stop publishing disabled dashboard links"
```

Review the staged path list before committing; do not stage unrelated wildcard matches.

---

## Task 5: Make the ownership boundary consistent for agents and users

**Objective:** Remove contradictory instructions that can route generic dashboard work into the retained SPA.

**Files:**

- Modify: `AGENTS.md`
- Modify: `READ_ME_FIRST.md`
- Modify: `README.md`
- Modify: `internal/api/dashboardspa/READ_ME_FIRST.md`
- Modify: `internal/bootstrap/packs/core/skills/gc-dashboard/SKILL.md`
- Modify: `docs/getting-started/dashboard.md`
- Modify: `CONTRIBUTING.md`
- Regenerate: `docs/reference/cli.md`
- Verify only: `CLAUDE.md` continues to include root `AGENTS.md`

**Step 1: Define one root ownership rule**

State plainly:

- Benjamin's dashboard product is `/Users/benjaminbrumbaugh/Documents/Gas City/Gas-City-Dashboard`, configured at `http://localhost:8400/`.
- `internal/api/dashboardspa` is retained upstream compatibility code, not the destination for generic dashboard features or fixes.
- UI layout, copy, interaction, and operations work belongs in `Gas-City-Dashboard`.
- SDK API/run-projection changes remain here when independently justified.
- Mechanical bundle/client regeneration remains allowed for upstream merges or intentional schema changes.
- Editing the retained embedded UI requires an explicit task naming it; a generic “dashboard” request does not authorize that work.

**Step 2: Reconcile the shipped dashboard skill**

Rewrite its opening and command guidance so an agent first checks `dashboard_url`, treats the external dashboard as the configured product, and treats the embedded SPA as optional upstream compatibility. It must not instruct agents to infer a UI from `/api` availability.

**Step 3: Reconcile user and contributor docs**

Document both supported modes without ambiguity:

- upstream default: embedded dashboard enabled;
- Benjamin/fork posture: retained APIs, embedded UI disabled, canonical external URL configured.

Label dashboard build/test steps in CONTRIBUTING as embedded-upstream compatibility work rather than this fork's product UI workflow.

**Step 4: Regenerate CLI reference from corrected command behavior**

Use the repository's documented generator. Do not hand-edit generated prose if a generator owns it.

**Step 5: Verify consistency**

```bash
make check-docs
git diff --check
```

Search the changed and active guidance surfaces for claims that `gc dashboard` always opens the supervisor or that API health proves the SPA is present. Expected: no contradictory fork-facing instructions.

**Step 6: Commit**

```bash
git add AGENTS.md READ_ME_FIRST.md README.md internal/api/dashboardspa/READ_ME_FIRST.md internal/bootstrap/packs/core/skills/gc-dashboard/SKILL.md docs/getting-started/dashboard.md CONTRIBUTING.md docs/reference/cli.md
git commit -m "docs(fork): establish the authoritative dashboard boundary"
```

---

## Task 6: Prove the complete external-dashboard contract

**Objective:** Verify the real external consumer, including the path that bypasses direct `bd` and depends on supervisor APIs.

**Files:**

- Modify if needed for a durable regression: `Gas-City-Dashboard/assets/gas-town-dashboard/test_server.py`
- Otherwise use only a disposable harness under the SDK task worktree's ignored `temp/`

**Required routes and invariants:**

1. `GET /api/city/{city}/supervisor-status`
2. `GET /api/health/system`
3. `GET /v0/city/{city}/agents`
4. `GET /v0/city/{city}/beads?limit=500&rig={city}[&cursor=…]`
5. `GET /v0/city/{city}/beads/ready`

For Beads responses, verify stable `X-Gc-Index` across active, continuation, and ready responses; exact `partial`, `total`, `next_cursor`, item-count, and bounded pagination semantics; and fail-closed behavior on drift or truncation.

**Step 1: Build and start an isolated candidate**

Use an isolated `GC_HOME`, test city, and non-production ports. Write:

```toml
[supervisor]
embedded_dashboard = false
```

Start the candidate with no new dashboard environment variable.

**Step 2: Verify the HTTP boundary directly**

Require:

- `/` returns 404 and not HTML;
- `/health`, `/openapi.json`, and `/v0/cities` return 200;
- `/api/health` and `/api/health/system` retain their contracts;
- all city routes above retain status, content type, body shape, headers, pagination, and completeness semantics;
- no API or CLI Sling response publishes an embedded dashboard URL.

**Step 3: Force the external dashboard onto its supervisor fallback**

Run a disposable external-dashboard instance against the candidate while deliberately making direct `bd` collection unavailable through the existing test seam. Do not merely use mocks for supervisor responses: the five requests must reach the candidate listener.

Verify the fallback returns a complete snapshot, the dashboard health endpoint passes, and a rendered Gas City Ops snapshot contains current candidate data. Record request paths and `X-Gc-Index` values without logging private Beads fields.

**Step 4: Run the full external suite**

```bash
python3 -m unittest test_server.py
```

If a new cross-repository regression test is needed, commit it in `Gas-City-Dashboard` as its own reviewed deliverable; do not strand it as an ignored fixture.

**Evidence boundary:** Unit tests prove fixture behavior. The forced-fallback candidate run proves SDK wire compatibility. Neither proves launchd deployment until Task 8.

---

## Task 7: Run repository gates and upstream portability review

**Objective:** Prove release quality and keep the fork delta narrow.

**Step 1: Run SDK gates**

```bash
make fmt
make test-fast-parallel
go vet ./...
make lint
make check
make build
git diff --check
```

Expected: PASS. Do not weaken tests or update unrelated expectations.

**Step 2: Audit the final diff**

Allowed product changes are limited to:

- supervisor configuration and focused tests;
- dashboard composition and publication paths;
- focused Sling/dashboard/startup tests;
- fork ownership guidance and generated CLI docs;
- a justified external-dashboard contract test.

The diff must not contain SPA source/bundle deletion, BFF relocation, OpenAPI contract removal, CI removal, or unrelated canonical-worktree changes.

**Step 3: Replay the exact task commits onto refreshed upstream**

Use a disposable worktree under `temp/`. Record conflicts by path and resolve only semantic conflicts within this feature. Do not merge broad upstream divergence into the dirty canonical checkout.

**Step 4: Obtain independent review of final bytes**

Review specifically for:

- API plane accidentally disabled with `embedded_dashboard=false`;
- Sling links surviving through either CLI or API output;
- `gc dashboard` or start output opening/advertising a 404;
- invalid/untrusted `dashboard_url` reaching the browser opener;
- config-default compatibility with upstream;
- contradictory agent guidance;
- tests that only mock the consumer instead of reaching the candidate.

Any byte change after review invalidates that review.

---

## Task 8: Deliver and deploy through the normal fork boundary

**Objective:** Land the reviewed SDK change, configure Benjamin's machine durably, and prove the live external dashboard remains authoritative.

**Step 1: Deliver reviewed commits**

Follow the repository's branch/PR process. Verify remote commit identity before building/installing the candidate. Deliver a separate external-dashboard test commit if Task 6 modified that repository.

**Step 2: Install the reviewed SDK binary**

Verify executable provenance before replacing the installed binary. Do not deploy an unreviewed worktree build.

**Step 3: Apply durable supervisor configuration**

Target only the existing `[supervisor]` table in Benjamin's active `supervisor.toml`, preserving every unrelated field:

```toml
embedded_dashboard = false
dashboard_url = "http://localhost:8400/"
```

Do not hand-edit launchd plist environment blocks. Reinstalling the service must not affect these file-backed settings.

**Step 4: Perform one controlled launchd handoff/restart**

Use the supported service-manager lifecycle. Verify launchd's managed PID equals the API PID and the served build equals the reviewed commit.

**Step 5: Verify live behavior and persistence**

Require all of the following:

- supervisor `/` is 404 and not HTML;
- required `/api` and `/v0` routes are healthy;
- API and CLI Sling omit embedded dashboard links;
- `gc dashboard --no-open` prints `http://localhost:8400/`;
- `gc dashboard` opens only that configured URL;
- foreground/background start paths do not advertise the supervisor root as a dashboard;
- `http://localhost:8400/healthz` is healthy and the visible dashboard renders current Gas City data;
- a controlled service reinstall followed by restart leaves both config fields intact and behavior unchanged.

**Step 6: Close tracking and clean task-owned residue**

Close the implementation bead only after live verification and remote readback. Remove only merged task worktrees/branches and disposable harnesses. Preserve all pre-existing canonical dirty and untracked work.

---

## Likely final file disposition

### SDK modifications

- `internal/supervisor/config.go`
- `internal/supervisor/config_test.go`
- `cmd/gc/supervisor_dashboard.go`
- `cmd/gc/supervisor_dashboard_test.go`
- `cmd/gc/cmd_supervisor.go`
- focused supervisor lifecycle/delegation tests
- `cmd/gc/sling_dashboard_link.go`
- `cmd/gc/sling_dashboard_link_test.go`
- focused `cmd_sling` output tests
- `cmd/gc/cmd_dashboard.go`
- `cmd/gc/cmd_dashboard_test.go`
- `AGENTS.md`, `READ_ME_FIRST.md`, `README.md`
- `internal/api/dashboardspa/READ_ME_FIRST.md`
- `internal/bootstrap/packs/core/skills/gc-dashboard/SKILL.md`
- `docs/getting-started/dashboard.md`, `CONTRIBUTING.md`, generated `docs/reference/cli.md`

### Possible external-dashboard modification

- `Gas-City-Dashboard/assets/gas-town-dashboard/test_server.py`, only if the candidate fallback proof reveals a durable missing regression.

### Preserve unchanged

- retained SPA/BFF implementation and generated bundles;
- OpenAPI and generated clients unless an independently required API contract change is discovered;
- dashboard Make, hook, CI, npm, and Playwright machinery;
- launchd/systemd environment allowlists;
- `internal/testenv/testdata/gc_env_read_baseline.golden`.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| API health is mistaken for SPA availability | Probe the actual root HTML surface and test `/api/health=200` with `/=404`. |
| A later service install re-enables the UI | Store policy in `supervisor.toml`, not the invoking shell or generated service environment. |
| External URL becomes an unsafe browser target | Accept only absolute HTTP(S), require host, reject userinfo, and test the opener boundary. |
| External URL is incorrectly used for embedded deep links | Keep canonical front-door resolution separate from Sling route generation. |
| External dashboard tests pass without using candidate APIs | Force direct `bd` unavailable and record all five requests reaching the candidate listener. |
| Agents still follow contradictory embedded guidance | Update root instructions, shipped skill, user docs, contributor docs, and generated CLI docs together. |
| Upstream compatibility regresses | Pointer boolean preserves default-on behavior; retained trees/build machinery remain untouched; replay exact commits onto refreshed upstream. |

## Acceptance criteria

- `[supervisor] embedded_dashboard = false` leaves the BFF and run plane active while supervisor `/` returns 404/non-HTML.
- Absence of `embedded_dashboard` preserves the current upstream embedded-UI default.
- Existing `GC_SUPERVISOR_DASHBOARD=0` full-disable behavior is unchanged.
- `dashboard_url` accepts only safe absolute HTTP(S) front doors and is never used to mint embedded Sling paths.
- CLI and API Sling omit embedded links whenever the root SPA is unavailable, even while `/api/health` is 200.
- `gc dashboard`, foreground start, direct background start, launchd start, and delegated systemd start never advertise a disabled supervisor UI; Benjamin's configuration resolves to `http://localhost:8400/`.
- All five external-dashboard route families and Beads pagination/index invariants pass against a real isolated candidate with direct `bd` unavailable.
- Root instructions, shipped skill, getting-started docs, contributor docs, and generated CLI docs agree on the dashboard ownership boundary.
- Service reinstall and restart preserve file-backed policy and live behavior.
- No SPA/BFF deletion, OpenAPI removal, generated-client removal, CI removal, or environment-vocabulary expansion is included.
- Exact task commits pass repository gates, independent review, and refreshed-upstream replay while the canonical dirty checkout remains untouched.