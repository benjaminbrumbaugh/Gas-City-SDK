# sdk-fmw plan

initial counter: 0
counter: 3

## 1. Full plan, tasks, and subtasks

- Establish the per-bead worktree and `polecat/sdk-fmw` branch from `origin/main`.
- Reproduce the reported `go vet ./...` failure and capture the exact failing layer.
- Compare the failure against the clean base and the referenced source revision.
- Inspect repository history and dependency metadata for an actionable project-owned fix.
- Decide whether a source change is justified; if not, preserve a concise diagnostic-only handoff.
- Run the required quality checks, record evidence, commit only reviewable artifacts, and hand off to Refinery.

## 2. Architectural changes

Expected: none unless evidence identifies a Gas City-owned boundary defect. Do not patch vendored or external ICU behavior from SDK code. Any fix must keep host/toolchain assumptions out of generic SDK paths.

## 3. Test plan and evidence contract

- Target truth: the SDK's required quality gate can complete on the supported host/toolchain, or the failure is proven pre-existing and outside the SDK layer.
- Required evidence layer: command-level vet/build output plus clean-base comparison; source inspection and dependency metadata identify ownership.
- Useful but insufficient proxies: a passing package test, a clean `go test`, or a semantic dependency lookup does not prove `go vet ./...` works.
- False-completion risk: replacing or suppressing vet, adding an unrelated workaround, or changing expectations without fixing the host dependency would mask the failure.
- Run the affected/full test command from the formula, plus `go vet ./...` as the reported gate. Record failures with their layer and provenance.

## 4. Support structures

- Keep `agent-execution.log` as the temporary subtask ledger.
- Use command output and `git diff`/history as the evidence artifact; do not add a permanent harness unless a reproducible project-owned regression is found.

## 5. Documentation

- Update repository documentation only if the supported toolchain contract is already documented incorrectly.
- Otherwise record the diagnosis in bead notes and the commit/handoff summary, without adding speculative docs.

## 6. Execution order and stability strategy

1. Workspace identity/branch setup.
2. Base preflight and reported-failure reproduction.
3. History/dependency ownership investigation.
4. Minimal implementation or no-op diagnostic conclusion.
5. Self-review, tests, vet, clean-tree check.
6. Push, record artifact metadata, and reassign to Refinery.

Use the freshly fetched `origin/main`, preserve unrelated worktree state, avoid cache-clearing, and stop before any source edit not justified by evidence.

## 7. Blocker avoidance and candidate parallel work

- No subagent is needed: the investigation is a single failure boundary and shared git/Dolt state makes parallel edits counterproductive.
- If the external dependency failure cannot be reproduced, verify command/toolchain versions and compare the referenced revisions before escalating.
- If a real source fix emerges, parallel review can be considered only for independent test/doc checks; no parallel branch mutation.

## Planning pass critique

The plan correctly separates target behavior from proxy evidence and limits changes to project-owned code. It should explicitly require checking the worktree's bead redirect and recording the branch metadata before implementation; workspace setup already performs both, so no new task is needed. It should also treat the requested `go vet` result as a host/toolchain compatibility finding unless repository-owned configuration changes the failure; this is captured by the ownership investigation.

## Critical evaluation of the critique

The critique does not weaken the evidence requirements or imply that a no-op is automatically complete. It preserves the possibility of a minimal source/config fix and ensures metadata/redirect checks remain part of the setup boundary. No additional architectural work is justified before reproduction.

## Roll-up: applied evaluation

Keep the plan unchanged in scope. Add explicit setup verification to the execution sequence and retain the no-suppression rule for the vet gate.

## Roll-up: revised tasks/subtasks

- Verify `.beads/redirect`, worktree identity, branch shape, and `work_dir` metadata immediately after setup.
- Reproduce and classify the vet failure before deciding whether any implementation change exists.
- Preserve the original failure command in the handoff evidence.

## No-change decisions

- No SDK code change is planned until the failure crosses the Gas City ownership boundary.
- No new abstraction, test harness, dependency replacement, or documentation page is warranted from the current description alone.

## Proxy audit

- Target truth: `go vet ./...` is usable for this repository's supported environment, or a pre-existing external failure is unambiguously isolated.
- Required evidence layer: actual vet invocation on the clean base and source/dependency ownership comparison.
- Cheaper useful-but-insufficient proxies: individual package tests, `go test`, static source grep, or a passing build.
- Tempting false-completion substitution: mark the bead fixed because the failure is reproducible without proving it is outside project control.
- If the plan fully succeeds yet the original bug remains, the likely cause is a machine-specific ICU/toolchain condition not covered by the clean-base comparison; capture versions and exact compiler diagnostics to prevent that gap.

## Planning pass 2

### 1. Full plan, tasks, and subtasks

- Reconfirm the isolated branch and bead metadata.
- Run raw `go vet`, the supported `make vet`, and `go vet` with explicit ICU flags.
- Compare dependency/build wiring with `origin/main` and the referenced integration branch.
- Run the documented fast baseline and review the resulting diff for source changes.
- Hand off as no-op if all failures remain external and the supported path is green.

### 2. Architectural changes

No architectural change. The ownership boundary is the host ICU installation and the Makefile/CI environment setup, not SDK domain code.

### 3. Test plan and evidence contract

The target truth is unchanged: supported repository checks must be green, and raw failures must be classified by layer. The required evidence is the three-way vet comparison plus fast-suite result. Package tests alone remain insufficient.

### 4. Support structures

Retain the plan and bead notes as durable evidence; keep `agent-execution.log` temporary. Do not add a workaround that changes compiler inputs globally.

### 5. Documentation

Existing Makefile comments and CI setup already document the ICU requirement. No documentation delta is justified.

### 6. Execution order and stability strategy

Verify setup, run the three vet modes, inspect history, run fast tests, review/clean the branch, then push and reassign. Do not clear caches or mutate the shared root.

### 7. Blocker avoidance and candidate parallel work

Keep this single-threaded because all evidence touches one branch and one host environment. If supported `make vet` failed, escalate with exact compiler output rather than guessing at a dependency edit.

### Critique of pass 2

This pass makes the evidence comparison explicit and avoids treating raw `go vet` as the only contract when the repository provides `make vet`. It still permits a project-owned fix if the supported path fails.

### Critical evaluation of that critique

The critique is sound: the wrapper is part of the repository's supported quality boundary, while raw vet is a diagnostic of ambient environment. The plan does not overclaim that a passing wrapper proves all hosts are configured.

### Roll-up: applied evaluation

Keep the three-way comparison and fast-suite requirement. Record the host/toolchain facts and exact exit codes in the handoff.

### Roll-up: revised plan/tasks/subtasks

Add explicit evidence recording for raw vet failure, `make vet` success, ICU-flagged vet success, and the clean-base/reference-branch parity check.

### No-change decisions

No dependency pin, build-tag change, vet suppression, or CI change is warranted.

### Proxy audit

Target truth: the supported gate is green and the reported raw failure is environmental. Required layer: actual commands plus repository wiring. Insufficient proxies: dependency graph alone or one passing test. False completion: declaring the problem solved solely because `make vet` passes. Residual risk: a different macOS ICU layout could fail despite this host's Homebrew installation; CI's Linux/macOS setup remains the authoritative cross-host mitigation.

## Planning pass 3

### 1. Full plan, tasks, and subtasks

- Validate branch/worktree identity and metadata.
- Reproduce and classify the reported vet failure.
- Verify the supported wrapper and explicit ICU flags.
- Confirm no relevant delta from the referenced branch to `origin/main`.
- Run the fast suite, review the plan/log/diff, and submit the no-op result with durable evidence.

### 2. Architectural changes

None. The clean boundary is repository quality wiring versus host-provided ICU headers; introducing SDK code for the latter would leak toolchain policy into the product layer.

### 3. Test plan and evidence contract

Target truth, required evidence, and insufficient proxies remain as stated in passes 1 and 2. The final evidence must state that no source behavior changed and that the baseline suite passed.

### 4. Support structures

Plan file is the durable planning artifact. Execution log is removed before handoff. Bead metadata carries branch/worktree and outcome fields.

### 5. Documentation

No docs update. Existing comments already explain the Homebrew ICU path and CI installs native dependencies before vet.

### 6. Execution order and stability strategy

All required execution steps have now run on `origin/main`; only cleanup, commit/push, metadata, and refinery handoff remain. Keep the branch limited to the plan artifact, with no production source edits.

### 7. Blocker avoidance and candidate parallel work

No parallel work is useful. A failure in cleanup or remote verification is a handoff blocker to report, not a reason to alter the diagnosis.

### Critique of pass 3

The final pass is complete and preserves the distinction between a diagnostic artifact and a product fix. It explicitly identifies the remaining actions and prevents accidental source churn.

### Critical evaluation of that critique

No missing architectural or evidence requirement remains. The plan would fail only if the exact command results or remote branch verification were omitted, both of which are scheduled as mandatory handoff checks.

### Roll-up: applied evaluation

Proceed with no source change, keep the plan, remove the temporary log, and record the verified no-op outcome before reassigning the open work bead.

### Roll-up: revised plan/tasks/subtasks

Final tasks are: review `git diff`, remove temporary log, commit the plan artifact, run clean-tree checks, push and verify `origin/polecat/sdk-fmw`, set `gc.work_outcome=no-op` or `shipped` according to the actual branch delta, set target, and reassign to Refinery.

### No-change decisions

The original `go vet ./...` failure remains a pre-existing host invocation issue; changing SDK code, dependency versions, or CI is explicitly declined based on the passing supported gate and existing CI coverage.

### Proxy audit

If this plan succeeds while the original issue remains, the remaining failure would be a host-specific ICU/toolchain configuration outside the tested Homebrew path. The handoff must preserve that limitation and the exact raw failure so a future owner can address host provisioning rather than misdiagnosing SDK behavior.
