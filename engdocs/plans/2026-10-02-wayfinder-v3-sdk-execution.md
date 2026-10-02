# Wayfinder v3 SDK execution-consumption contract

## Actual root CLI / City convergence (2026-10-02)

The built `gc` root now discovers all six routing result schemas through the
normal embedded command-path schema registry: status, targets, eligible,
decisions, outcomes, and ingest. No root gate is bypassed. A built-executable
regression checks each capability manifest and invokes each `--json` path in an
isolated HOME/GC_HOME. A real HTTP fixture additionally proves the v3 outcome
page is emitted verbatim, without an extra CLI `ok` field; the v2 branch retains
its existing success envelope. Both generations have explicit result schemas.

The actual City `TestCityV3CrossRepositoryConvergence` passed against this SDK
worktree and producer `a3eda7e521019e675ba23d7a23dfb0e9c15bb3ab` (producer source
was dirty, so this is not final producer-byte approval). It freshly built the SDK
root and producer CLI, exercised real producer selection and City signing,
SDK ingest/replay/admission, final-argv drift denial, the real recording child,
durable session/launch receipts, and both service and actual CLI production
forwarders. City durable exact record bytes/SHA256 acknowledgement and producer
create/replay, including local-ack loss, passed. Terminal completion remains
`unknown`; a successful Start is not terminal evidence. No City/plugin source or
live keys/config/providers/restarts were changed.

Reproduce the full cross-repository proof (not merely the SDK child witness):

```sh
export TMPDIR="$HOME/.hermes/cache/scratch"
export CGO_CPPFLAGS="-I$(brew --prefix icu4c)/include"
export CGO_LDFLAGS="-L$(brew --prefix icu4c)/lib"
cd /absolute/path/to/City/source
CITY_V3_SDK_ROOT=/absolute/path/to/SDK/source \
CITY_V3_PRODUCER_ROOT=/absolute/path/to/Wayfinder/source \
go test ./cmd/gcfactory -run '^TestCityV3CrossRepositoryConvergence$' -count=1 -v -timeout=15m
```

The successful-Start receipt replay test uses the **original attempt ID** across
ledger reopen and requires exact receipt identity and persisted timestamp.
Claimed-work recovery remains bound to the original authorization and produces
a distinct receipt only for a distinct successful Start. Cleared mutable session
markers/trigger cannot erase the independent durable authorization; absent,
locked, or corrupt authority never licenses a marked legacy launch.

**Trust limit:** the adapter hashes executable bytes before exec but does not
atomically pin the opened image. Require caller-controlled immutable executable
installation and configuration; do not claim resistance to concurrent same-user
replacement between attestation and exec. Remote runtimes, provider credential
validity, live inference, terminal success, and rollout are unproved. The lane
remains opt-in and disabled by default. Final independent review is required.

## Candidate implementation and current proof (2026-10-02)

The sections below describe the original foundation and its obligations; this section records the subsequent candidate, not approval for live activation. The opt-in controller installs a caller-owned adapter only for direct or seam-backed local subprocess. Session launch authorization and successful-start receipt writing run through the real worker/session boundary. Original decision/session/work/generation/instance identity is persisted independently of mutable bead markers. When the caller adapter is absent, the session manager probes an existing ledger read-only; a locked/corrupt ledger is unknown and denies rather than downgrading to legacy. No ledger is created by that fallback probe.

Executable basename must equal the exact account identity. Literal serving and reasoning argv, isolated environment, command, workdir, executable digest, full configuration, and adapter/invocation digest are checked against the signed tuple. `max` and `xhigh` remain unrepresentable, never translated. Real recording-child tests now cover hostile outer PATH/shell init, changed tuple/account/provider/adapter/config/executable bytes, fallback-command substitution, claimed-work recovery with two distinct successful-start receipts, and cleared-marker/trigger plus missing-adapter denial. Terminal facts remain unknown.

The additive producer v3 page is exercised through controller state, Huma routes, generated HTTP client, and CLI, and validated against the actual Wayfinder producer schema. Reproduce without live keys/config/providers/restarts:

```sh
scripts/test-wayfinder-v3-convergence.sh /absolute/path/to/Wayfinder
```

Artifacts are `temp/wayfinder-v3-proof/convergence.log` and `outcomes-v3.json`. This is a disposable real local execution witness, not proof of live provider authentication or an installed City-to-plugin submission.

The failed full consumer suite had exactly two API failures: `TestOpenAPISpecInSync` (three stale OpenAPI artifacts) and `TestPaginationDialectGuard` (new bounded v3 page missing from the approved 100-row keyset map). Regenerated the spec and generated client; declared the new page alongside the existing bounded outcome page. Both tests pass on the candidate and on the initial HEAD `5f6a914829bd364fe31d235a42b6dbdef22c2058` and campaign baseline `5b1013338` under the same scratch/ICU environment. Incidental corrupt-bead/request logs were not the failure cause.

**Unproved:** check-to-launch replacement of a writable executable by a concurrent same-user process remains a TOCTOU limit, as does remote runtime attestation. No final independent approval or live rollout is claimed. The lane remains disabled unless explicitly configured; the original foundation notes below must not be read as current implementation evidence.

## Candidate production convergence interface

The consumer lane is opt-in via root `[routing_execution] enabled = true`; absent/false denies new v3 execution. The local registry binds each exact static target to an absolute caller-owned executable, SHA-256 of its bytes, isolated environment, literal model/effort argv positions, account/provider, work directory, and `subprocess` transport. No aliases or effort translation are supplied.

The proposed producer interface is additive `GET /v0/city/{cityName}/routing/outcomes-v3`, returning `schema_version=routing/outcome/v3`, `items`, `next_cursor`, `total`, and `partial`. Existing typed v2 HTTP/client signatures remain unchanged. `gc routing outcomes --json` uses this v3 projection when routing status advertises `execution_enabled`; otherwise it remains v2. Thus City's existing `forward-v3` command need not relabel or change flags. Only durable launch receipts provide actual target/config/session/execution identity; terminal disposition remains unknown. Decisions with neither a launch nor authoritative non-admission are omitted with `partial=true`, because the producer v3 schema cannot honestly encode null actual tuple with unknown disposition.

This section records the convergence contract; completion/proofs are reported below after execution. No live activation is authorized.

## Status: foundation only; no live activation

This branch adds a versioned signed execution tuple, a default-deny local adapter
seam at existing readiness/CAS admission, and a controller-prepared launch check.
It does **not** constitute a complete, reviewed, production v3 consumer. No
provider, configuration, key, pin, deployment, or controller restart is changed.
The SDK production checkout and the city/plugin/producer sources are untouched.

The plugin's original consumer commit is **not approved**. Parent integration
must select a fixed independently reviewed plugin before producer transport is
connected. No v4 or producer ranking/protocol implementation belongs here.

## Signed interface

`internal/routingdecision.DecisionPayload` keeps schema 1 byte-compatible (both
legacy unlinked records and `routing/v2:<64 lower hex>` recommendation records).
The database and public authority-file schemas stay at 1. Legacy signing
preimage domains and framing remain unchanged. Schema 2 uses v2 domains at all
four preimages: `gascity.routing-decision.v2`,
`gascity.routing-decision-approval.v2`, `gascity.routing-decision-signature.v2`,
and `gascity.routing-decision-binding.v2`, each terminated by one NUL byte.

For a v3 selection, set decision **and approval** `schema=2`, set
`recommendation_id=routing/v3:<64 lower hex>`, and supply `execution`:

```json
{
  "schema": 1,
  "canonical_model": "producer-verified canonical identity",
  "serve_as": "literal producer-verified serving argument",
  "reasoning_effort": "high",
  "account": "local exact authorized account identity",
  "provider": "local exact provider identity",
  "target": "SDK exact configured static routing target",
  "config_digest": "64 lowercase hex",
  "adapter_id": "local versioned adapter identity",
  "adapter_digest": "64 lowercase hex",
  "invocation_digest": "64 lowercase hex"
}
```

Every field is required and signed. Execution schema is 1. Reasoning is exactly
`none|low|medium|high`; `max`, `xhigh`, missing/default, and unknown values are
refused, never translated. Canonical model, literal serve-as, account, provider,
target, and config digest must also equal the corresponding top-level decision
fields. Schema 1 cannot carry `execution`. Schema 2 requires a v3 recommendation
identity and a complete selection. BindingID now commits to the complete tuple
and recommendation identity only for schema 2; legacy binding goldens remain
unchanged. Digests use the existing SDK unprefixed SHA-256 encoding; the external
signer explicitly bridges producer `sha256:` values, not SDK string repair.

The authority must obtain selection via the fixed reviewed plugin's verified
`SelectionAt`, preserve serve-as literally, and refuse stale/no-selection or
incomplete typed responses. SDK signature verification attests that authority;
it is not an independent Rust verification or a ranking implementation.

## City signing conformance

Read-only inspection found the City issuer had selected v2 domains for schema 2.
The SDK initially retained v1 domains; this mismatch was fixed in a separate
RED/GREEN cycle (`schema-2 decision uses legacy domain` before the fix). No City
source was edited. A test vector was generated by executing the actual City
`Issuer.Sign` from copied, unmodified `issuer.go` and `execution.go` in the
SDK worktree temp module, using a public zero-seed test authority.
`testdata/city_schema2_signed.json` preserves that real output.
`TestCitySchemaTwoSignedVectorMatchesSDK` requires byte-identical full signing
preimages, binding digests, and successful detached signature verification.
Legacy schema-1 vectors still pass. This proves the signing wire, not runtime
execution or plugin approval. Source SHA-256 hashes at generation:

- City issuer.go: `502b0ce61d161a1258d11a8733b4690a61f580ebfa83df3e2ab65344b4ff6f99`
- City execution.go: `bc00faa7de5b0dd0d869d33619a9f51f2484bd6c7ac6f6b752b06fdd0571a2b0`

## Local execution-adapter interface (not wired in production)

`CityRuntime.routingExecutionAdapter` currently has this exact shape:

```go
func(config.Agent, *config.City, *runtime.Config) (routingdecision.ExecutionBinding, error)
```

- Admission passes the exact resolved static target Agent and City, with a nil
  runtime pointer. The local adapter must fully resolve its executable tuple
  itself; returning a requested/signed tuple is not a local attestation.
- Final launch passes the final prepared `runtime.Config` after command,
  template overrides, resume selection, working-directory environment, and
  trigger identity are resolved. Attest from **these final bytes**, not a
  separate PATH-check or a model label.
- Return exact locally authorized canonical model, literal serve-as, reasoning,
  wrapper/account, provider, target, full config, adapter, and invocation digests.
  `DecisionPayload.MatchesExecution` rejects any difference or incomplete tuple.
- Nil adapter is intentional default-deny at schema-2 admission and marked
  schema-2 launch. The branch installs no real adapter; positive tests use an
  explicitly injected trusted local collaborator.
- The executable/account registry, adapter digest preimage, invocation digest
  preimage, wrapper isolation capability, and supported local runtime still
  require cross-repository agreement. Do not invent account aliases or infer
  canonical-to-literal serving aliases from a successful command.

Existing signed approval verification, record revision, ready-work CAS,
compensation, work fence, target digest, and fresh-work eligibility remain in
place. Admission adds the tuple check before route metadata CAS. It does not
change any capacity, overlap, claim, or session reservation mechanism.

## Launch interface and proof limit

`TemplateParams.RoutingLaunchCheck` is an in-memory callback:

```go
func(session.Info, runtime.Config) error
```

The ordinary `CityRuntime.buildDesiredState` attaches a callback and
`startPreparedStartCandidate` invokes it before runtime liveness observations,
zombie recycling, warm-bind nudge, or worker `StartResolved`. It re-reads the
exact trigger work and signed admitted decision, rechecks signature/expiry,
carrier/work fence, target/config identity, and local final tuple. Errors are
sanitized and no launch occurs.

This is a prepared-controller launch fence, **not** a proved universal runtime
boundary. Remaining convergence work:

1. Ordinary and refreshed desired-state overlays now reinstall the callback
   onto both concrete and base rows. Direct API worker creation, other
   reconstruction, wake/recovery, and fallback paths still require proof.
2. Bind a v3 authorization marker durably into the concrete session so missing or
   cleared trigger/no-selection cases cannot become an unmarked legacy bypass.
3. Define claimed active-work continuation separately from fresh admission; the
   current marked check requires admitted/open/unassigned work and cannot prove
   active-work continuation. Never migrate active work to a new recommendation.
4. Run a recording executable through a real supported local provider and assert
   final literal argv, account selection and hostile env/config overrides. The
   present runtime-boundary test records through `runtime.Fake`, not a child.
5. Close the check-to-launch mutation window and prove adapter/executable bytes
   cannot change after attestation (remote runtime attestation is not provided).

No production adapter is installed while these obligations remain open.

## Small causal durable outcome seam

The SDK now has:

```go
Store.ListExecutionOutcomeDecisions(OutcomeListOptions) (DecisionPage, error)
ProjectExecutionOutcome(DecisionWithAudits, OutcomeAuthoritySnapshot) (ExecutionOutcomeObservation, error)
```

The bounded keyset reader uses actual persisted decision records, transition
audits, and the exact admission receipt index. It is separate from the existing
strict v2 outcome page, which must never mislabel v3 records. The observation
copies the signed requested tuple, reports durable non-admission, and otherwise
reports `unknown`. Actual tuple remains null: neither admission nor a work
closure proves a launched executable. Session/execution IDs can come only from
validated SDK session/execution authority records with matching identities.
Read time is never used as evidence: timestamps come from the latest durable
transition, falling back only to the immutable signed creation timestamp.

This observation is **not** the producer `routing/outcome/v3` protocol, and no
producer outcome schema/ranking is duplicated. It is a usable domain seam over
durable authority records, not yet exposed via a v3 HTTP/CLI projection. Existing
production outcome-authority readers still lack a terminal disposition record
bound to decision/work/session/execution/target/config/adapter/invocation. No
terminal success or actual tuple is invented to fill that gap.

Required next interfaces: an SDK-owned durable launch/completion receipt writer
at successful actual runtime operations, exact read auth and typed projection
for the observation, and city mapping of those facts into the fixed plugin's
`SubmitOutcome`. The receipt must bind the full execution tuple and original
claim/session generation; work metadata alone is not authority.

## Verification

Strict RED/GREEN cycles were exercised for the signed v3 tuple, exact-match
consumer, default-deny admission, final prepared launch callback, ordinary
controller callback wiring, stable read evidence time, v2/v3 outcome isolation,
and durable v3 observation seam. Existing schema-1 canonical byte/binding goldens
and routing-decision suites remain the compatibility owner. Full hook/gate
results belong in the PR; focused green tests alone do not authorize activation.

## Gate failure diagnosis (October 2, 2026)

The first broad `GC_FAST_UNIT=1 go test ./cmd/gc -count=1` failed with 25
top-level failures. Its TMPDIR was the assigned worktree `temp/go-tmp`, below
a Git checkout and a path containing a space. This result was **not** green.
Both initial diagnostic Python wrappers printed Go exit 1 but incorrectly
returned wrapper exit 0; subsequent wrappers propagate child exit status.

The exact failure-name set was extracted from `temp/cmd-gc-v3.log`. The branch
and an unmodified `git archive 5b1013338` extracted under this worktree were
run with the identical regex and original TMPDIR, recording complete Go JSON
events as `temp/repro-{branch,baseline}.jsonl`. Both Go runs exited 1. Parsed
top-level/subtest failure sets matched exactly; branch-only and baseline-only
failure sets were empty. Neither had collection/build errors. Relevant
assertions matched: missing fixture outputs; shell syntax failure on an
unquoted path with a space; URL-escaped file source mismatch; Git discovering
the enclosing checkout for deliberately non-repository fixtures; long fake
Dolt process arguments failing inspection; warmup count and shell-quoted
scaffold expectations. This is comparison evidence, not a blanket waiver.

The narrow test-environment remedy was to use the runtime-authorized,
space-free, non-checkout scratch TMPDIR
`/Users/benjaminbrumbaugh/.hermes/cache/scratch`. Test logs and baseline artifacts
remain in the assigned worktree temp. No unrelated source/tests were repaired
and no gates were bypassed. **All 25 exact failures passed on both baseline
and branch** under this same remedy: baseline Go exit 0 (21.649s), branch Go
exit 0 (22.397s), zero failed events and 25 top-level pass events each. JSON
logs are `temp/scratch-{baseline,branch}.jsonl`; child exit results are
`temp/scratch-exits.json`. Final hook gates use this TMPDIR as well.

## Historical push gate failure; no bypass

The normal pre-commit passed lint, generated docs/clients/dashboard, vet, docsync,
and dashboard checks/smoke. `make build` and `make dashboard-ci` passed.
The first normal push ran all ten fast jobs: all six cmd/gc shards, Darwin
compile, and the two runner selftests passed. The unit-core sweep failed on
exactly two tests:

- `internal/sessionlog.TestFindCodexSessionFileMatchesEquivalentResolvedWorkDir`: got empty path instead of the equivalent resolved-workdir transcript.
- `internal/testutil/providerledger.TestCatalogMatchesProductionWiringAndDocumentation`: the `runtime.builtin.tmux` waiver owned by `ga-80po0c.3` expired September 17, 2026 and became fleet-fatal after October 1, 2026. ACP/herdr warnings are separately retained; tmux is the fatal assertion.

Both tests reproduced narrowly on this branch and on unmodified baseline
`5b1013338`, under the same corrected scratch TMPDIR, with Go exit 1 and the
same respective failed assertions. Evidence is preserved in
`temp/v3-push.log`, `temp/push-baseline-sessionlog.log`, and
`temp/push-baseline-providerledger.log`. No test deadline, date, waiver clock,
provider ledger, or unrelated source was changed to evade the gate. Push/PR
publication is blocked until the owning gate defects are resolved normally.

## Current parent-owned convergence state (October 2, 2026)

Plugin PR #11 is merged, with independent exact-byte approval of
`c2e1020dd8b6b4c8d67c566d5c95c8270b2b947d`. City PR #41 remains draft at
`c4abade1ac00bd91a66006b7b45fdea1575ce5ab`. SDK consumer source was committed
at `45cc591ec9b0f492837b3726151670b011f58301`, not published.

Independent adversarial review reproduced three P1 defects despite positive
convergence: an installed-but-unavailable authority hook permitted legacy
downgrade, outer-shell creation falsely attested actual executable launch, and
the real controller wake incarnation change prevented original-work recovery.
Commit `67ea05800e0296f1fa9b752c6bb2fda14901cd62` contains tracked regressions
and fixes: independent durable classification, direct bound-executable exec,
and append-only authorized successor incarnations. Normal pre-commit passed.
The implementer was interrupted during a broad test run; that run is not a
passing gate. Parent has independently inspected the committed fixes; renewed
exact-byte adversarial approval remains outstanding.

Remote main `0d823e02cfb868eef9662c3cac858495ea845f16` (PR #59) independently
landed the approved tmux conformance and real alias-fixture changes. Parent
merged it into this branch without changing foreign work. This supersedes the
two historical baseline defects above, but not all test-harness safety gaps:
normal full-gate paths still contain foreign sibling/process cleanup. The
separate `fix/sdk-gate-delivery` lane owns narrow ownership fixes; no full push
gate is authorized until its owned-only cleanup is proved and reviewed.

Final proof uses the clean producer worktree at `a3eda7e521019e675ba23d7a23dfb0e9c15bb3ab`,
not installed runtime state. Required next actions remain agent-owned: close
test cleanup safety, independently approve final consumer bytes, run actual
City/signer/SDK/recording-child/producer convergence and normal final gates,
publish and merge, and remove owned residue. Live activation, provider calls,
key installation and existing-session migration remain outside authorization.
