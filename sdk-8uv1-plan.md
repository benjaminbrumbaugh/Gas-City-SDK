# sdk-8uv1 plan

counter=3

## 1. Full plan, tasks, and subtasks

Target truth: a real `gc bd <command> --json` process emits one parseable JSON
value on stdout; scope/routing/status diagnostics are on stderr. The proof must
observe the CLI process boundary, not only the `doBd` writer seam.

Tasks:

1. Establish the branch and baseline.
   - Verify the claimed bead, isolated worktree, `polecat/sdk-8uv1` branch,
     and `origin/main` ancestry.
   - Run the documented base/preflight checks and record failures separately
     from task behavior.
2. Map the JSON boundary.
   - Trace `gc bd` dispatch, JSON buffering, scope diagnostics, bd passthrough,
     and existing orphan-recovery consumers.
   - Reproduce the reported stdout pollution with city and rig routing when
     an executable path permits it.
3. Add RED evidence at the smallest owning layer.
   - Add process-level regression coverage for city-scoped and rig-routed
     `list`/`query` JSON invocations.
   - Assert stdout parses as exactly one JSON value and diagnostics are absent
     from stdout but may be present on stderr.
   - Exercise the recovery parsing path without line-stripping workarounds.
4. Implement the narrow boundary fix.
   - Keep JSON payload ownership with the bd passthrough and route human
     diagnostics through the injected stderr writer.
   - Avoid changing domain/store routing semantics or adding a second parser.
5. Verify and hand off.
   - Run focused tests, affected tests, `go vet ./...`, and the documented
     fast baseline as feasible.
   - Inspect the final diff, commit only task-owned files, push the required
     branch, record metadata, and reassign to the refinery.

Architectural changes: only the CLI machine-readable output boundary is in
scope. No domain, store, API, dashboard, or recovery policy changes are
planned. If the defect is in shared command execution, centralize the fix at
that execution seam so all `gc bd` JSON subcommands inherit it.

Test plan:

- RED/GREEN focused `cmd/gc` tests for city and explicit `--rig` invocations.
- Exact JSON decoding (including rejection of multiple values/trailing
  non-whitespace) on captured stdout.
- Stderr assertions for scope/routing diagnostics.
- Existing orphan-recovery consumer test or a focused consumer harness proving
  it can decode the command output directly.
- Affected fast suite, `go vet ./...`, and broader sharded checks required by
  `TESTING.md`/the formula when the environment supports them.

Support structures: `sdk-8uv1-plan.md` records decisions and evidence;
`agent-execution.log` records completed formula subtasks. No production
abstraction or new long-lived fixture is justified until the owning seam is
confirmed.

Docs: no user-facing docs change is expected unless command output behavior is
currently documented incorrectly. If docs need updating, use the Gas City docs
skill and update only the source page, not generated output.

Execution order: load context → workspace/branch setup → baseline preflight →
planning passes → RED test → implementation → focused/affected verification →
self-review/commit → refinery handoff.

Stability strategy: use the existing injected writers and test helpers; avoid
live Dolt restarts, tmux cleanup, sleeps, polling, or `/tmp` build caches.
Keep all diagnostics in bounded test buffers and use `t.TempDir()` for any
filesystem fixture.

Blocker avoidance: distinguish a missing native dependency or unhealthy Dolt
from a product failure; collect evidence and escalate only after focused
alternatives are exhausted. Do not modify the dirty home worktree. Do not
rewrite or discard unrelated changes.

Candidate parallel work: an independent reviewer can inspect existing CLI JSON
buffering and recovery consumers while the focused test fixture is prepared;
another can audit history for a prior clean-output fix. Keep edits serialized
at the shared CLI boundary until ownership is clear.

## 2. Critique of major sections (pass 1)

- Scope is appropriately limited to CLI stdout/stderr, but “all commands” is
  not yet enumerated. Add a command inventory from `gc bd --help`/source and
  test representative passthroughs that share the seam.
- The test plan risks proving only `run` with synthetic writers. Retain one
  process-level fixture or executable harness so root-level output cannot leak
  around `doBd`.
- The implementation section must not assume the existing stderr disclosure is
  sufficient; confirm where `named_session` and routing text originate.
- The recovery-consumer proof needs a named consumer and exact decode contract,
  otherwise it could accidentally validate a lossy cleanup filter.

## 3. Critical evaluation of the critique (pass 1)

The critique correctly identifies the main false-completion risk: a unit test
of `doBd` can pass while Cobra/root initialization writes to the real stdout.
However, requiring every bd verb in a full matrix would duplicate a shared
passthrough contract and increase test cost without new risk. Enumerate the
documented `--json` forms, then cover the shared execution seam plus one city
and one rig route for each behaviorally distinct output shape (`list` and
`query`). Treat `show`/`ready` as existing seam coverage unless their route
differs. Search history before inventing a harness.

## 4. Roll-up: apply evaluation to critique (pass 1)

The plan will use a shared exact-decoder helper in tests, a process/root
invocation fixture where available, and representative city/rig routes rather
than a Cartesian verb matrix. The implementation will first identify the
polluter; no output redirection change is made based on the reported symptom
alone. The recovery proof will name the existing consumer and assert direct
decode.

No-change decisions: do not change domain routing, do not parse or strip
arbitrary stdout lines, do not move JSON payloads to stderr, and do not add a
new public abstraction.

## 5. Roll-up: apply revised critique to tasks (pass 1)

Add an explicit command/output inventory and an exact single-value decoder to
the RED test. Include a process/root execution path and the existing recovery
consumer. Preserve current stderr scope disclosures. Search history for prior
output-boundary fixes before implementation.

## 6. Proxy-domain audit (pass 1)

- Target truth: machine consumers receive exactly one JSON value on stdout.
- Required evidence layer: actual `gc` CLI process/root execution with city and
  rig scope routing, plus direct recovery-consumer decoding.
- Cheaper but insufficient proxies: `doBd` buffer tests, semantic command
  output snapshots, or a test that strips non-JSON lines.
- Tempting false completion: moving only the known scope line to stderr while
  another startup/status writer still targets stdout.
- If the plan succeeds but the bug remains: a root hook or spawned helper can
  still write named-session/routing text to process stdout outside the tested
  writer, and recovery code could still hide it by trimming lines.

## 7. No-change decisions and next pass

No production edit is authorized yet. Complete baseline preflight and history
inspection, then increment the counter and refine only with new evidence.

## 8. Critique of major sections (pass 2)

- The baseline command-package shards passed so far, but the broad core shard
  is still running; do not treat the partial baseline as product evidence.
- Source and history show that `gc bd` is intentionally a raw passthrough and
  that root execution currently streams its stdout. The plan must distinguish
  preserving bd's payload bytes from permitting framework diagnostics on that
  same writer.
- The current installed binary puts named-session and store-scope messages on
  stderr, so the reported failure is not reproduced by that binary. A test
  must still guard the required process contract without claiming to reproduce
  an absent symptom.
- A generic test helper should not become a production parser or line filter;
  exact JSON decoding must reject concatenated documents and arbitrary text.

## 9. Critical evaluation of the critique (pass 2)

The new evidence narrows the likely ownership to the shared root execution
buffering policy, not store routing. Making bd JSON buffered would isolate
framework writes while preserving the raw backend document, but it also
changes the existing error path that deliberately avoids wrapping bd failures.
Therefore the implementation must explicitly preserve raw success payloads and
avoid inventing a generic failure envelope for a failed bd passthrough unless
the command's existing contract already emitted one. The regression should
assert both city and rig scope, and should validate the public `run` boundary;
an executable test is optional only if it would require unavailable native
dependencies.

## 10. Roll-up: apply evaluation to critique (pass 2)

Refine the production seam as follows: capture bd JSON stdout at the root
execution boundary, copy it byte-for-byte only after successful execution, and
keep all command diagnostics on stderr. Preserve the existing raw bd failure
behavior rather than routing it through the generic structured-command
failure envelope. Add a focused test for the buffering policy and a root
invocation test with a fake bd executable for both city and rig routes. The
fixture must emit one JSON document only; the test proves the wrapper does not
prepend its own diagnostics and that the recovery consumer decodes directly.

## 11. Roll-up: apply revised critique to tasks (pass 2)

1. Finish the base preflight and record any environment-only failure.
2. Add RED assertions that bd JSON is captured at the root boundary, that
   `list` and `query` remain raw JSON payloads, and that city/rig diagnostics
   are stderr-only.
3. Add the smallest execution-policy change plus a failure-path regression
   preserving passthrough semantics.
4. Run affected tests and inspect the diff for accidental changes to routing,
   schema manifests, or non-bd JSON behavior.

## 12. Proxy-domain audit (pass 2)

- Target truth remains process stdout as one JSON document for successful bd
  JSON commands.
- Required evidence is `run` with an executable fake backend and explicit city
  and rig routing; direct `doBd` tests are supporting evidence only.
- Insufficient proxies include checking only `shouldBufferJSONExecution`,
  decoding after trimming lines, or checking the installed binary instead of
  the changed source.
- False completion would preserve the payload but lose backend stderr, or
  would convert a bd error into a generic envelope that breaks existing users.
- If the plan succeeds but the bug remains, another root path may bypass the
  switchable writer; the process-level test should therefore assert that all
  captured stdout bytes are the exact fake backend document.

## 13. No-change decisions and next pass

No store/backend code, schema files, recovery scripts, or documentation will
change. The only expected production files are the shared JSON execution seam
and its tests. Run one final planning pass after the baseline result to check
for lost requirements before editing production code.

## 14. Critique of major sections (pass 3)

- The plan now has a concrete source seam and test boundary, but it must not
  overstate a reproduction that the current installed binary does not show.
- The error-path requirement is the key compatibility edge: successful raw bd
  output can be buffered, while failed passthrough behavior must be proven
  separately.
- The “orphan-recovery consumer” requirement needs a concrete existing call
  site or a narrowly named helper test; a new fake consumer would be weak
  evidence.
- Handoff remains constrained to the required polecat branch and refinery;
  no implementation bead closure or broad worktree cleanup belongs here.

## 15. Critical evaluation of the critique (pass 3)

This final critique catches the remaining boundary risk. The source behavior
and historical intent are compatible with a narrow capture-and-copy change,
but only if tests prove byte preservation and stderr separation. Existing
recovery consumers that already decode `gc bd ... --json` directly are the
right compatibility evidence; do not alter them to hide malformed output.
The final implementation should be judged against the target truth and the
acceptance wording, not against whether the stale installed binary reproduces
the original incident.

## 16. Roll-up: apply evaluation to critique (pass 3)

Proceed with TDD at the shared root execution seam. Keep the patch small:
change the BD JSON buffering decision, condition generic failure-envelope
emission on the existing report policy, and add focused root-level tests. Use
existing integration call sites as consumer evidence. If the baseline core
shard fails for the known native ICU dependency, report it separately and use
the affected package tests plus vet as the implementation gate.

## 17. Roll-up: apply revised critique to tasks (pass 3)

- Verify the final baseline result and record it.
- Add a failing test for BD JSON buffering and root-level exact output in city
  and rig scopes; add a failure-path compatibility test if needed.
- Implement and run focused `cmd/gc` tests, then the affected test command.
- Run `go vet ./...`, review status/diff, commit the cohesive patch, push
  `polecat/sdk-8uv1`, and hand off to the refinery without closing the work
  bead.

## 18. Lost-information check and final no-change decisions

The three passes preserve the original requirements: city and rig routing,
single-value JSON, stderr diagnostics, recovery-consumer compatibility,
process-boundary evidence, TDD, and refinery handoff. No requirement was
dropped. No-change decisions remain: no line stripping, no domain/store
changes, no role/config changes, no new production abstraction, and no edits
outside this isolated worktree.

## 19. Execution evidence

The documented fast command shards for `cmd/gc` passed in the base preflight;
the unrelated core-package shard remained active beyond 20 minutes while this
task proceeded. Native-CGO focused compilation is unavailable because the host
does not provide `unicode/regex.h`; the affected tests and vet pass with
`CGO_ENABLED=0`. A source-built no-CGO `gc` process emitted one JSON value for
`gc bd list --json`, while named-session and store-scope diagnostics appeared
only on stderr. The changed tests additionally cover city and explicit rig
routing, exact single-document decoding, and raw failure compatibility.
