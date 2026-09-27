# Beads Telemetry Recovery Handoff

## Purpose

This handoff records the investigation and recovery state after a Beads
telemetry queue consumed approximately 88 GB across approximately 23 million
files and broke Time Machine. It is written so another operator can continue
without rediscovering which data is disposable, which data is live, and which
conclusions remain unverified.

The priorities are:

1. Preserve the live Gas City Beads/Dolt ledger.
2. Prevent anonymous Beads telemetry from recreating the unbounded queue.
3. Keep Time Machine viable by keeping gc's high-file-count runtime state out
   of the backup set.
4. Prove local backup recovery without restoring over live data.
5. Later establish a new encrypted Time Machine destination.

The user explicitly does not want an offsite backup. Do not configure
`GC_BACKUP_OFFSITE_PATH`.

## Incident Summary

`~/.beads/eventsData/` accumulated a very large number of `*.evtq` files.
Those files are Beads anonymous telemetry queue data, not the Beads ledger,
Dolt database, or a backup. The directory has been removed and must remain
absent. Do not attempt to restore it.

The prior Time Machine destination was reformatted/deleted. A replacement disk
has been formatted but `tmutil destinationinfo` reports **no destinations
configured**. Nothing is protective yet.

## Verified Runtime Facts (2026-09-20)

These were established with live commands and supersede the earlier
inferences in this document.

### What is running

| Process | Binary | Provenance |
| --- | --- | --- |
| `gc supervisor run` (pid 59072, up 7d) | `~/.local/bin/gc` | fork build `1.4.0-1011-g812ee49c5` = SDK `main` |
| `gc __gc-managed-dolt-scope-watchdog` + `dolt sql-server` (port 29223) | `~/.local/bin/gc`, `/opt/homebrew/bin/dolt` | fork gc; Homebrew dolt |
| `com.gascity.dashboard` | `~/Applications/Gas City Dashboard.app` | product dashboard (separate repo) |

`launchctl list` shows `com.gascity.supervisor` running (pid 59072),
`com.gascity.supervisor.gc-home-ee87423e` **not running** (last exit 78, its
program `/tmp/gca-3866110762/gc` no longer exists), and
`com.gascity.hermes-bridge` not running (exit 1).

### Which `bd` actually runs

The earlier claim that the supervisor resolves Homebrew's `bd` was wrong.
The loaded supervisor's `PATH` begins with `~/.local/bin`, and
`~/.local/bin/bd -> ~/go/bin/bd`. Every managed scope therefore runs:

```text
~/go/bin/bd   v1.1.1-0.20260808152808-869020c2213d
              (gascity-compat: fix/mol-dog-backup-stamps-dolt-backup-state@869020c2213d)
```

That is the fork-pinned Beads module in `go.mod` (`steveyegge/beads
v1.1.1-0.20260808152808-869020c2213d`), not Homebrew's. The Homebrew formula
`beads` is installed (`/opt/homebrew/bin/bd -> Cellar/beads/HEAD-80af94c`,
reports `1.1.0 (dev)`) but is shadowed for the supervisor, hooks, and any
agent shell that inherits the supervisor `PATH`. Homebrew `gascity` 1.4.1 at
`/opt/homebrew/bin/gc` is likewise shadowed by `~/.local/bin/gc`.

`gc`'s own hook PATH prefix (`internal/hooks/hooks.go`
`canonicalGCPathPrefix`) puts `$HOME/go/bin:$HOME/.local/bin` first, so hooks
select the same fork `bd`. No binary divergence exists between supervisor,
hooks, and agents today; explicit binary pinning is not needed.

### Exact Beads telemetry semantics (from the pinned module source)

`cmd/bd/main.go resolveMetricsEnabled()`:

1. `BD_DISABLE_METRICS` — if set at all, `!truthy(value)` decides
   (bidirectional; `1`/`true` disables, `0`/`false`/`""` re-enables).
2. `DO_NOT_TRACK` — disable-only; truthy disables, otherwise falls through.
3. `~/.config/bd/config.yaml` `metrics.disabled: true` (user-global only;
   project config is ignored by design).

When enabled, events are written by a file emitter into
`$HOME/.beads/eventsData` with no retention cap. The pinned module has no
`BD_NO_TELEMETRY` or `BEADS_METRICS_*` disable key. `~/.config/bd/config.yaml`
currently has `metrics.disabled: true`; that is fallback defense only.

### The Time Machine file-count culprit is not telemetry alone

Post-cleanup census of the city root (`~/Documents/Gas City/Gas-City`):

| Path | Files | Size |
| --- | --- | --- |
| `.gc/worktrees/` | 782,118 | 57 GB |
| `.gc/migration-worktrees/` | 15,980 | 533 MB |
| `.gc/runtime/` | 5,453 | 4.2 GB |
| `.beads/dolt/` | 214 | 3.5 GB |
| `.dolt-backup/` | 248 | 3.4 GB |
| `~/.gc/cache/repos/` | 41,211 | 832 MB |

`.gc/worktrees/Wayfinder` alone is 47 GB (54 `work/wf-*` linked worktrees,
each a full Rust checkout with a `target/` build dir). All of these paths are
currently **included** in Time Machine (`tmutil isexcluded` → Included). The
Dolt ledger and managed backups are small in file count and are the only
paths worth protecting.

## Data Classification

### Disposable

- `~/.beads/eventsData/` — telemetry queue. Deleted; must stay absent.
- `.gc/worktrees/**` and `.gc/migration-worktrees/**` — agent linked
  worktrees (`git rev-parse --git-common-dir` points at the source repo).
  Reproducible; do not back up. Reaping is gc's job, not Time Machine's.
- `~/.gc/cache/repos/` — pack/repo cache. Reproducible.
- `~/.gc/supervisor.log` (720 MB, rotated archives exist) and
  `.gc/runtime/**` traces — diagnostics only.

### Live Beads/Dolt data: do not delete or restore over it

```text
~/Documents/Gas City/Gas-City/.beads/dolt/
```

### Managed local Dolt backup artifacts: preserve and test

```text
~/Documents/Gas City/Gas-City/.dolt-backup/{gc,gcd,he,sdk,wf}/
```

`dolt-backup-state.json` last recorded `2026-09-19T13:06:02Z` (6s). That
proves a sync ran, not that a restore works.

### Registered local backup: preserve and test

```text
file:///Users/benjaminbrumbaugh/Library/Application Support/Gas City/Backups/Gas-City
```

Registered in `.beads/dolt-backup.json`. It contains one 14 MB `.darc` dated
2026-08-16 — a month stale relative to the managed `.dolt-backup` artifacts.
It is local storage on the same volume, not independent protection.

### Deliberately disabled legacy Beads backup

Both `.beads/config.yaml` files declare `backup.enabled: false`. Keep it.

## Code Containment (this branch)

Branch `fix/bd-telemetry-optout` (from `origin/main`) states the policy in
gc itself so no city depends on the operator's user-global bd preference:

- `internal/execenv`: `BdMetricsDisableEnv`, `BdMetricsDisabledEntry`,
  `WithBdMetricsDisabled(environ)` — the single canonical helper.
- `internal/beads/bdstore.go execEnvFor`: every runner-spawned `bd` gets
  exactly one `BD_DISABLE_METRICS=1` (inherited values replaced; explicit
  per-call override still wins).
- `cmd/gc/bd_env.go`: `applyBdMetricsOptOut` at the runtime-env, city
  process-env, and recovery projection sites; `mergeRuntimeEnv` strips the key
  and re-adds the canonical entry, covering every `sh -c` work query, hook,
  sling, pool, and order runner.
- `cmd/gc/template_resolve.go` (session backend env) and
  `cmd/gc/store_target_exec.go` (exec-store projection).
- `internal/doctor/bd_command.go`: the direct doctor `bd` subprocesses now
  share one constructor that applies the opt-out.
- `internal/orders/env.go`: `BD_DISABLE_METRICS` is a reserved key an
  `[order.env]` cannot override.

Each site has a focused test (`*DisablesMetrics*`,
`TestExecEnvForBd_InjectsMetricsOptOut`, `TestDoctorBdCommandsDisableMetrics`,
`TestMergeRuntimeEnvAlwaysCarriesMetricsOptOut`,
`TestReservedExecEnvKeysIncludeBdMetricsOptOut`).

Not covered by gc env policy: an agent that types `bd` interactively in a
tmux pane whose environment was not projected by gc. The session backend env
projection covers gc-created panes; the user-global YAML remains the fallback
for anything else.

## Remaining Plan

### Deploy containment

1. Merge this branch, `make build && make install` (installs to
   `~/.local/bin/gc`, which is what the loaded LaunchAgent runs).
2. `launchctl kickstart -k gui/$UID/com.gascity.supervisor` (or `gc
   supervisor` restart path) so the running supervisor picks up the binary.
3. `launchctl bootout gui/$UID/com.gascity.supervisor.gc-home-ee87423e` and
   remove its plist — its program path is gone and it only retries at boot.
4. Verify with a representative child: `ps eww <bd pid>` or a `gc bd`
   invocation under a shell with `BD_DISABLE_METRICS=false` exported, and
   confirm `~/.beads/eventsData` stays absent.

### Keep Time Machine viable — DONE 2026-09-20

Reproducible runtime state is excluded; only the ledger, backups, and city
config remain in the backup set (`tmutil isexcluded` verified):

```text
[Excluded]  Gas-City/.gc/worktrees            (782k files, 57 GB)
[Excluded]  Gas-City/.gc/migration-worktrees  (16k files)
[Excluded]  Gas-City/.gc/runtime              (5k files, 4.2 GB)
[Excluded]  ~/.gc/cache                       (41k files)
[Included]  Gas-City/.beads/dolt, .dolt-backup, city.toml
```

These are per-item (xattr) exclusions — `tmutil addexclusion -p` (sticky
path) requires root. They persist as long as the directory inode does; gc
recreates children, not these parents. Re-check after any `gc` command that
recreates `.gc/` wholesale.

Separately, reap the 54 stale `Wayfinder/work/wf-*` worktrees through gc's
own worktree lifecycle once the city is confirmed idle on them — that is a
Gas City operational task, not a backup task.

### Prove local backup recovery — drill PASSED 2026-09-20

Each of the five on-disk databases (`gc gcd he sdk wf`) was restored with
`dolt backup restore file://<city>/.dolt-backup/<db> <db>` into an isolated
`/var/tmp` directory (3.4 GB total, removed afterwards; live store untouched):

| db | restored HEAD vs live | issues | open | convoys open |
| --- | --- | --- | --- | --- |
| gc | 850 commits behind (backup 06:05, live 10:54 PDT Sep 19) | 4747 | 160 | 41 |
| gcd | identical | 1151 | 81 | — |
| he | identical | 77 | 28 | — |
| sdk | identical | 198 | 38 | — |
| wf | identical | 245 | 78 | — |

All restores reported `On branch main`, 30–31 tables, and answered SQL
against `issues`. The `gc` gap is the ~5 hours of live writes after the last
managed backup cycle (`dolt-backup-state.json` 2026-09-19T13:06:02Z) and
before the city was disturbed — no cycle has run since. The remaining step
after the supervisor restart: require a fresh `dolt-backup-state.json` stamp
and no stale-backup doctor warning, then the gap closes.

The registered Library backup URL (`~/Library/Application Support/Gas
City/Backups/Gas-City`, one `.darc` from Aug 16) was not drilled: it is a
month stale and superseded by `.dolt-backup`.

### Age out closed beads (redundant state) — code landed, policy opt-in

The operator does not want the city holding redundant state: projects live
in Git, and the whole filesystem goes to Time Machine. The largest
accumulation is closed `session` beads (4,241 of 4,747 rows in the `gc`
store) which nothing aged out — wisp GC only covers ephemeral roots and the
order-tracking watchdog only covers tracking rows.

`cmd/gc/closed_bead_retention.go` adds an opt-in `closed_durable` policy:

```toml
[beads.policies.closed_durable]
delete_after_close = "14d"
```

It deletes closed non-ephemeral beads older than the TTL that have no
parent or dependency edge (either direction) to non-closed work, oldest
first, 500 per hourly run. It is **dry-run until
`GC_CLOSED_BEAD_RETENTION_ENFORCE=1`** is set in the supervisor environment,
and enforcement defers to `doctor.BulkDeleteSafe` (stale managed backup ⇒
skip). Dry-run against the live `gc` store at 14d: 2,462 eligible, 11
protected by open links.

Not applied to `city.toml` yet. Rollout: add the policy, restart, read the
dry-run advisory line in `supervisor.log` for a cycle, then set the env in
the LaunchAgent plist and reload. Follow with `bd compact` once the backlog
has drained so Dolt actually releases chunks — row deletes alone do not
shrink `.beads/dolt`.

### mol-dog-backup — retired 2026-09-22

There is no backup nag mail in the store today (zero `message` beads). The
nagging comes from `mol-dog-doctor.sh` `[WARN: backup stale]` and the
`bd-backup-freshness` / `bd-backup-size` / `dolt-backup` doctor checks, which
only fire when a *registered* backup goes stale — so disabling the order
without also silencing those checks would create the flood.

Once hourly Time Machine was running (22 snapshots at a steady ~60 min
cadence, encrypted `MAC_STUDIO_TM`), the order was retired in the live city
by configuration only (`Gas-City` commit `7f813ba`):

- `[orders] skip = [..., "mol-dog-backup"]` — no more 6h `dolt backup sync`.
- `[[orders.overrides]] name = "mol-dog-doctor"` with
  `env.GC_DOCTOR_BACKUP_STALE_S = "3153600000"` — the doctor dog's
  backup-freshness advisory never fires for the intentionally frozen
  artifact dir.
- `.dolt-backup/` (4 GB) is left in place: the per-rig `dolt-backup` doctor
  check treats a populated dir as satisfied, and `bd-backup-freshness`
  only reads `backup_state.json`, which this city never writes. `gc doctor`
  reports all backup checks ✓ after the change.

No SDK code or pack files changed; the three doctor checks and the pack's
order remain for upstream compatibility and for cities without Time Machine.
Do not re-enable the legacy `backup.enabled`. The operator chose to skip a
restore drill from Time Machine.

### Establish Time Machine protection — DONE 2026-09-22

`MAC_STUDIO_TM` (1 TB Samsung T7-class SSD, USB 3.x 5 Gb/s, case-sensitive
APFS, FileVault-encrypted) is the registered destination with hourly
`AutoBackup` (`AutoBackupInterval = 3600`). Backups are IOPS-bound, not
bandwidth-bound (~2–2.5k ops/s at ~6 KB); the fix for slow passes is more
exclusions, not a different cable. Beyond the gc runtime dirs, the following
reproducible trees were also excluded with `tmutil addexclusion` (xattr,
no root): `~/Documents/Weft/target` (792k files, 193 GB),
`~/Documents/Hermes-Agent/{node_modules,venv}`, `~/Documents/Wayfinder/target`,
`~/go/pkg`, `~/.cache/{uv,codex-runtimes,opencode}`, `~/.rustup`, `~/.npm`,
and project `.venv*` dirs. Project `temp/` and `tmp/` dirs under
`~/Documents` were cleared after archiving every dirty worktree to its
branch on GitHub.

## Deferred: the "overnight" backup schedule — TimeMachineEditor

Not retired yet by operator decision (2026-09-22: "I'm not ready to remove
the overnight thing yet, but don't forget about it").

What it is: **TimeMachineEditor** (`/Applications/TimeMachineEditor.app`,
tclementdev) with its scheduler LaunchDaemon
`/Library/LaunchDaemons/com.tclementdev.timemachineeditor.scheduler.plist`
(`RunAtLoad = 1`, `KeepAlive`). It is the only non-Apple backup scheduler on
the box — no crontab, no other backup LaunchAgents. It was presumably set up
to confine backups to an overnight window while the file-count problem made
daytime passes disruptive. Its scheduler process was not running when
checked and native `AutoBackup = 1` / `AutoBackupInterval = 3600` are in
effect, so today the hourly cadence is Apple's own; TME is dormant config
that would take over if it is re-armed.

Why it can go: hourly incrementals now finish inside their hour with the
exclusions in place; there is no need for a night-only window.

How to retire it (root required — operator step):

```bash
sudo launchctl bootout system/com.tclementdev.timemachineeditor.scheduler
sudo rm -f /Library/LaunchDaemons/com.tclementdev.timemachineeditor.scheduler.plist
# then drag TimeMachineEditor.app to the Trash, and confirm:
defaults read /Library/Preferences/com.apple.TimeMachine AutoBackup   # expect 1
```

If TME had switched TM to manual mode, re-enable hourly in System Settings →
General → Time Machine → Options → Back up frequency: Automatically every
hour.

## Completion Criteria

1. `~/.beads/eventsData` remains absent after normal managed and agent-path
   Beads activity.
2. Every gc-created `bd` subprocess path has a tested, deployed
   `BD_DISABLE_METRICS=1` policy (this branch), and the deployed
   `~/.local/bin/gc` contains it.
3. The selected `bd` binary is known: `~/go/bin/bd` (fork-pinned module) for
   supervisor, hooks, and agents.
4. No offsite backup environment variable or destination is configured.
5. Each live Beads/Dolt database has passed an isolated restore drill from
   `.dolt-backup`. (Done 2026-09-22, before the order was retired.)
6. `.gc/worktrees`, `.gc/migration-worktrees`, `.gc/runtime`, and
   `~/.gc/cache` are Time Machine-excluded. (Done.)
7. The new encrypted Time Machine destination completes hourly backups.
   (Done; restore drill from Time Machine skipped by operator decision.)
8. `mol-dog-backup` no longer runs and no backup advisory fires. (Done.)
9. Closed durable beads with no open links age out. (Deployed dry-run; the
   operator flips `GC_CLOSED_BEAD_RETENTION_ENFORCE=1` in the supervisor
   LaunchAgent after reviewing the advisory count.)
