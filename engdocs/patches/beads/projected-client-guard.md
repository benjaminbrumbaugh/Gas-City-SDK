# Gas City projected-client guard for Beads

This patch is applied to the exact Beads module used by Gas City when a city/HQ
store has a colocated legacy Dolt data root and workers use an endpoint-only
`BEADS_DIR` view.

## Immutable base

- Module: `github.com/steveyegge/beads`
- Version: `v1.1.1-0.20260808152808-869020c2213d`
- Origin commit: `869020c2213db2bfd9e1ba0aa54c7e60d52e5f2c`
- Module sum: `h1:A+v6IXiGqyGAwuL99RbyplQjAGblyfkbiTgU5GKB1gA=`
- Patch source commit: `0d4bfe34566649ca3074e924b9df4fa8b7c7143d`
- Binary diff SHA-256: `4bdfb99a33bfd6e4deacb97631fdd4a436a37bc09724dce1d86ccbfb18f5d13a`
- Format-patch file SHA-256: `587a64a9114f6fcfb260b3537716ed425dfdacfc0406210081d1fb9098b535ac`

## Contract

When `GC_BD_CLIENT_GUARD=1` or `<BEADS_DIR>/.gc-projected-client-policy`
contains exactly `v1\n`, the real `bd` binary enforces a strict top-level
issue-command allowlist in root `PersistentPreRunE`, before telemetry, store
open, or local file writes. Unknown, future, config, backup, migration, doctor,
Dolt, and other administrative commands fail closed. Normal issue commands
such as `show`, `comments`, `update`, `close`, `create`, `dep`, and `list`
continue to use the canonical server-backed database.

This is the hard gate below PATH/process-name resolution. Gas City's `bd` PATH
shim remains only defense in depth.

## Verification

```sh
TMPDIR=/private/tmp go test -v ./cmd/bd -run '^TestProjectedClientGuard' -count=1
```

Expected: five guard tests pass.

Build with the installed compatibility identity preserved:

```sh
go build -tags gms_pure_go \
  -ldflags '-X main.Version=v1.1.1 -X main.Build=gas-city-routing -X main.Commit=869020c2213db2bfd9e1ba0aa54c7e60d52e5f2c -X main.Branch=fix/gascity-projected-client-guard' \
  -o ./bd ./cmd/bd
codesign --force --sign - ./bd
```

The guarded candidate built on 2026-08-13 had SHA-256:
`88eb4e108571ebe2e294872d7cf9d8a6e2466c8e3f3476d2410e57db9982e77c`.
