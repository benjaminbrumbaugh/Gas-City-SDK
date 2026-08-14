# Gas City projected-client guard for Beads

This patch is applied to the exact Beads module used by Gas City when a city/HQ
store has a colocated legacy Dolt data root and workers use an endpoint-only
`BEADS_DIR` view.

## Immutable base

- Module: `github.com/steveyegge/beads`
- Version: `v1.1.1-0.20260808152808-869020c2213d`
- Origin commit: `869020c2213db2bfd9e1ba0aa54c7e60d52e5f2c`
- Module sum: `h1:A+v6IXiGqyGAwuL99RbyplQjAGblyfkbiTgU5GKB1gA=`
- Patch source series: `0d4bfe34566649ca3074e924b9df4fa8b7c7143d` → `67f41ae7c48ee510a9877646919f6680e27dd322` → `b79df684c908002b5c51fa26746b730319986c46` → `206f33f4c9630a0fa162a47cd4024cef99bcb204`
- Final patch tree: `ee93e0d831d2bd893ead42803f7848f434fe7e90`
- Series diff SHA-256: `90db91a218117cf17055140320a9c0ededded7030620a0adf8d808f7abfdad5b`
- Format-patch 0001 SHA-256: `587a64a9114f6fcfb260b3537716ed425dfdacfc0406210081d1fb9098b535ac`
- Format-patch 0002 SHA-256: `fb27d8d326787fdba696ba5b033269735e6c8a3b0ffaa81e497229ea9be6705a`
- Format-patch 0003 SHA-256: `487a5db67fe722fdb19dc3885cd2de3cce501cf58bf0c0897cbda8d784e4d80f`
- Format-patch 0004 SHA-256: `269dc5cfc672984c60629d029330f5abe5ca68cf86f43cdd0475dbf2dc79e218`

## Contract

When `GC_BD_CLIENT_GUARD=1` or `<BEADS_DIR>/.gc-projected-client-policy`
contains exactly `v1\n`, the real `bd` binary enforces a strict top-level
issue-command allowlist in root `PersistentPreRunE`, before telemetry, store
open, or local file writes. Unknown, future, config, backup, migration, doctor,
Dolt, and other administrative commands fail closed. Normal issue commands
such as `show`, singular/plural `comment(s)`, `gate check`, read-only `query`
with a required limit in `1..1000`, `update`, `close`, `create`, `dep`, and `list`
continue to use the canonical server-backed database.

This is the hard gate below PATH/process-name resolution. Gas City's `bd` PATH
shim remains only defense in depth.

## Verification

```sh
TMPDIR=/private/tmp go test -v ./cmd/bd -run '^TestProjectedClientGuard' -count=1
```

Expected: seven guard tests pass.

Build from exact commit `206f33f4c9630a0fa162a47cd4024cef99bcb204`
with VCS stamping retained:

```sh
go build -tags gms_pure_go \
  -ldflags '-X main.Version=v1.1.1-0.20260808152808-869020c2213d -X main.Build=gascity-compat' \
  -o ./bd ./cmd/bd
codesign --force --sign - ./bd
```

The bounded-query VCS-stamped candidate built on 2026-08-14 had SHA-256:
`399c17d4638bc8ccf26f7e647cb1db6950504d39c2fcfe239ffa24f2b603ab69`.
