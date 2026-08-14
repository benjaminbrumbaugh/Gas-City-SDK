# Gas City projected-client guard for Beads

This patch is applied to the exact Beads module used by Gas City when a city/HQ
store has a colocated legacy Dolt data root and workers use an endpoint-only
`BEADS_DIR` view.

## Immutable base

- Module: `github.com/steveyegge/beads`
- Version: `v1.1.1-0.20260808152808-869020c2213d`
- Origin commit: `869020c2213db2bfd9e1ba0aa54c7e60d52e5f2c`
- Module sum: `h1:A+v6IXiGqyGAwuL99RbyplQjAGblyfkbiTgU5GKB1gA=`
- Patch source series: `0d4bfe34566649ca3074e924b9df4fa8b7c7143d` → `67f41ae7c48ee510a9877646919f6680e27dd322`
- Final patch tree: `2d47220a10836d93d66868047e2c98f51b515727`
- Series diff SHA-256: `21fccebea8372e46abf2ed485a9384d533a4d9710c04414897dc16b2a4719b3a`
- Format-patch 0001 SHA-256: `587a64a9114f6fcfb260b3537716ed425dfdacfc0406210081d1fb9098b535ac`
- Format-patch 0002 SHA-256: `fb27d8d326787fdba696ba5b033269735e6c8a3b0ffaa81e497229ea9be6705a`

## Contract

When `GC_BD_CLIENT_GUARD=1` or `<BEADS_DIR>/.gc-projected-client-policy`
contains exactly `v1\n`, the real `bd` binary enforces a strict top-level
issue-command allowlist in root `PersistentPreRunE`, before telemetry, store
open, or local file writes. Unknown, future, config, backup, migration, doctor,
Dolt, and other administrative commands fail closed. Normal issue commands
such as `show`, singular/plural `comment(s)`, `gate check`, `update`, `close`,
`create`, `dep`, and `list`
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
`9bc1d973dec11af31d3da6aa5b96c426a791ff4ab22515efe03c7291d72be558`.
