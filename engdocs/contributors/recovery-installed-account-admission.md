# Recovery Wayfinder installed-account admission (gc-ibou)

## Enforced contract

Gas City owns the host-side admission snapshot. A recovery advisor admits exact,
case-sensitive `account_ref` strings only from explicitly configured city provider
keys whose resolved command is either the identical name or an absolute path with
the identical basename. The final resolved command must pass `exec.LookPath`.
Paths stay local. No aliases are inferred, no account names are normalized, and
no credential files are read or provider commands executed to construct this
snapshot.

`config.ResolveProvider` alone is insufficient: provider keys are arbitrary;
`path_check` may name another executable; inheritance may supply the command;
and `workspace.start_command` may replace the checked command afterwards. The
recovery adapter therefore checks identity and executable presence again on the
final resolved command. A provider named `opencode-go` whose command is merely
`opencode`, or `claude-personal` whose command is `true`, is not evidence that the
named account is installed. Such aliases are excluded, not silently translated.
A same-named executable wrapper is supported as an operator-declared identity.

The worker copies and validates the nonempty admission snapshot, rejects unknown
candidate and entitlement accounts at construction, and rechecks the narrowed
outbound packet immediately before HTTP. Incoming candidate accounts must be in
the snapshot, and the existing routing/v3 result validator additionally binds
all candidate execution targets and the echoed request to the submitted packet.
Substituting another admitted account is still rejected. Rejection disables the
advisory route; deterministic configured recovery-target order remains the
fail-open recovery path.

## Deliberate limitations

- Executable presence is NOT authentication, entitlement, subscription, quota,
  credential validity, or successful inference proof. Same-name wrappers,
  symlinks, PATH contents, and frontend/backend bindings remain operator-owned
  trust boundaries. No check here can attest that `codex` or a frontend is logged
  into a particular remote account. The credential-free SDK currently has no
  independent backend-account registry/attestation contract.
- Provider-qualified frontend identities are not invented from the frontend
  binary. Operators need an actual same-named wrapper or a separately reviewed
  future account-resolution contract; this change does not add one.
- Admission is a controller reconciliation snapshot, not a launch-time guarantee
  against executable removal or PATH replacement. It describes the controller's
  host, not necessarily a remote runtime host.
- Recovery advice selects an already configured target; it does not change that
  target's launch provider, model, credentials, or session. Catalog membership is
  not a target-to-launch-account binding. That stronger consumer/runtime contract
  remains outside this recovery-only patch.
- The request fixture now uses `claude-personal` rather than `personal-max`.
  Account-sensitive inventory/account-scope fingerprints and decision ID change
  accordingly; this is a local canonical fixture, not a new published upstream
  routing/v3 golden vector.

## Proof owners

- `cmd/gc/recovery_responder_controller_test.go`: arbitrary aliases, unchecked
  workspace overrides, path-check-only installation, same-named executable
  identities, and deterministic fallback when no identities are admitted.
- `internal/worker/recovery_responder_accounts_test.go`: required/copied snapshot,
  unknown unreferenced entitlement, pre-HTTP outgoing rejection, admitted-account
  substitution in incoming results, and request/response acceptance for legitimate
  `claude-personal`, `claude-gladstone`, and `codex` identifiers.
- `internal/worker/recovery_responder_wayfinder_test.go`: unknown incoming account,
  constructor admission, routing/v3 canonicalization/fingerprints and the existing
  complete result-binding and malformed-wire regression matrix.

These are hermetic executable-discovery and transport tests, not live-provider
inference or credential probes. Temporary test executables are never run.
