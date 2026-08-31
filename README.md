# Gas City SDK — Benjamin Brumbaugh's Fork and Build Source

This repository is **not a second Gas City runtime** and it is not the live Gas City factory. It is Benjamin Brumbaugh's maintained source/build fork of the Gas City SDK.

For the generic upstream project description, concepts, installation guide, and public documentation, read the upstream README first:

**[Upstream Gas City README](https://github.com/gastownhall/gascity/blob/main/README.md)**

Then return here for the fork-specific ownership and deployment rules.

## What this repository is

This fork contains the Go source for the `gc` executable and the reusable Gas City orchestration SDK:

- CLI and supervisor implementation in `cmd/gc/`;
- runtime/session providers and lifecycle reconciliation;
- city configuration, packs, formulas, orders, and routing infrastructure;
- Beads/Dolt provider boundaries;
- HTTP control-plane APIs;
- fork-specific integration work and tests.

The Go module remains:

```text
github.com/gastownhall/gascity
```

The fork preserves the upstream architecture: roles and factory behavior are supplied by configuration and packs. This repository is the implementation/build source; it is not itself a city instance.

## Repository and remote topology

```text
https://github.com/gastownhall/gascity
        │
        │ upstream: public Gas City SDK
        ▼
https://github.com/benjaminbrumbaugh/Gas-City-SDK
        │
        │ origin: Benjamin's private/custom fork
        ▼
clean reviewed gc build
        │
        ▼
~/.local/bin/gc
```

The intended maintenance model is one fork `main` with two remotes:

```text
origin   = benjaminbrumbaugh/Gas-City-SDK
upstream = gastownhall/gascity
```

Fork-specific changes stay on the fork's history. Upstream changes are fetched and merged deliberately; they are not silently copied over local work.

## What this repository is not

| Path or artifact | Role |
|---|---|
| `Gas-City-SDK/` | This SDK source/build fork |
| `Gas-City/` | Live factory workspace: city configuration, packs, agents, formulas, Beads, and runtime state. It does not contain the `gc` command source. |
| `Gas-City-Dashboard/` | Separate Python operational Dashboard Server, owned by the macOS `launchd` user service `com.gascity.dashboard`, with front door `http://localhost:8400/`. |
| `~/.local/bin/gc` | Preferred installed runtime binary on this machine. |
| `/opt/homebrew/bin/gc` | Optional Homebrew-packaged alternative; not a second supervisor and not additive to the custom binary. |
| `~/go/bin/gc` | Compatibility path; keep it as a symlink to `~/.local/bin/gc`, not as an independent runtime build. |
| `Gas-City-SDK/gc` | A checkout-local build artifact, not the canonical installed runtime. |

A city is managed by a `gc` binary; it is not a binary built inside the city repository. Do not launch a supervisor from every checkout that happens to contain a `gc` artifact.

## Runtime and deployment boundary

The production relationship is:

```text
launchd user services:
  ├─ com.gascity.supervisor
  │   └─ ~/.local/bin/gc supervisor run
  │       ├─ machine-wide supervisor API: 127.0.0.1:8372
  │       ├─ registered Gas-City factory
  │       └─ city controllers, sessions, and runtime providers
  └─ com.gascity.dashboard
      └─ Gas-City-Dashboard/assets/gas-town-dashboard/server.py :8400
```

The Dashboard Server is intentionally independent at the process-lifecycle
boundary while remaining coupled to Gas City as its data source and operator
surface. It survives supervisor stop/start and reboot windows so it can show
host state and Gas City unavailability. `gcx start` and `gcx stop` do not own or
stop it; the dashboard repository installs/uninstalls `com.gascity.dashboard`.
A future Wayfinder-specific dashboard must use a separate service label and
source repository.

### Build a candidate

Use a clean, reviewed SDK checkout. Do not build production from a dirty worktree or from a migration/polecat worktree.

```bash
# From the SDK fork root
git status --short --branch
make build

# The Makefile writes bin/gc and injects version, commit, date, and dirty state.
./bin/gc version
go version -m ./bin/gc
```

On macOS, the Makefile detects Homebrew's keg-only ICU dependency when available. A source build may require:

```bash
brew install icu4c
```

Do not use a bare ad-hoc `go build` as the production provenance step when `make build` is available. The Makefile owns version metadata and the self-contained build checks.

### Canonical install path

This fork intentionally defaults `make install` to `~/.local/bin`, the stable
runtime path used by the live supervisor. `INSTALL_DIR` can be overridden for a
separate build artifact, but do not use the default GOPATH install as the
production path.

Keep the compatibility path aligned after promotion:

```bash
ln -sfn "$HOME/.local/bin/gc" "$HOME/go/bin/gc"
test "$(realpath "$HOME/go/bin/gc")" = "$(realpath "$HOME/.local/bin/gc")"
```

This symlink does not interact with Homebrew: Homebrew owns `/opt/homebrew/bin`
and its Cellar, while `~/go/bin` belongs to the Go user workspace.

### Promote the reviewed build

Promotion is an operational action, not a side effect of editing source:

```bash
make install
gc version
gc supervisor install
gc supervisor status --json
```

`make install` now defaults to `~/.local/bin/gc` and does not create a second GOPATH runtime binary. `gc supervisor install` writes/loads the macOS launchd service and starts the supervisor from that installed path. Treat it as a controlled supervisor lifecycle operation: verify the current supervisor, active sessions, and rollback binary before using it.

After promotion, verify the actual running executable, not only the source checkout:

```bash
ps -p "$(gc supervisor status --json | jq -r .pid)" -o pid=,ppid=,command=
gc supervisor status --json
curl --fail --silent http://127.0.0.1:8372/health
```

A successful build, install, or `gc supervisor status` response alone does not prove that launchd owns the running process. Confirm `launchctl print gui/$(id -u)/com.gascity.supervisor` when automatic login startup matters.

### Start the live city

From the live city workspace, not from this SDK repository:

```bash
cd "/Users/benjaminbrumbaugh/Documents/Gas City/Gas-City"
gc start
```

`gc start` registers/starts the city and triggers reconciliation. It does not compile the SDK and it does not replace the installed `gc` binary.

Inspect the separate layers independently:

```bash
gc supervisor status --json
gc cities --json
gc status --json
```

A partial runtime probe is degradation evidence, not proof that the supervisor or every worker is stopped.

## Upstream synchronization

Keep the fork's `main` branch easy to compare with both remotes. Use a focused synchronization branch when bringing in upstream work:

```bash
git fetch origin --prune
git fetch upstream --prune
git switch main
git pull --ff-only origin main
git switch -c sync/upstream-YYYYMMDD
git merge --no-ff upstream/main

# Run the prescribed checks before opening a PR.
make check

git push -u origin HEAD
gh pr create --base main --head HEAD
```

Review upstream changes for compatibility with this fork's integration boundaries before merging. Preserve fork-owned behavior deliberately; do not resolve divergence by copying one checkout wholesale or force-pushing `main`.

For fork-specific work:

```bash
git fetch origin --prune
git switch main
git pull --ff-only origin main
git switch -c feat/<short-description>
# make the smallest scoped change
git add <intended-files>
git commit -m "<type>: <description>"
git push -u origin HEAD
gh pr create --base main --head HEAD
```

The durable delivery boundary is the merged commit on the private fork's `main`, followed by a clean reviewed build and explicit promotion of that artifact.

## Homebrew and alternate binaries

Homebrew provides a packaged Gas City release:

```bash
brew install gascity
gc version
```

That package is an **alternative distribution channel**, not a supplemental supervisor. Do not run a Homebrew supervisor and a custom-fork supervisor together. Choose one binary path, verify its version/provenance, and keep the other installation as an explicit fallback only.

Likewise, `~/go/bin/gc`, `Gas-City-SDK/gc`, and binaries inside migration worktrees are development artifacts unless they have been deliberately promoted. An absolute path in a long-lived worker/session can bypass the shell's PATH ordering and create version skew even when the supervisor uses the preferred binary.

## Dashboards

The SDK retains the upstream embedded dashboard SPA and its support API for compatibility, but this fork disables the embedded SPA in durable supervisor configuration while preserving the API/run-projection plane. Benjamin's operational dashboard is separate and authoritative:

```text
Retained upstream SPA: optional compatibility UI on the supervisor listener
Owned Python dashboard: http://localhost:8400/
```

Generic dashboard layout, copy, interaction, and operations work belongs in `/Users/benjaminbrumbaugh/Documents/Gas City/Gas-City-Dashboard`. Editing `internal/api/dashboardspa` requires an explicit task naming the retained embedded UI; SDK API/run-projection changes remain here when independently justified.

The Python dashboard's source and runtime are documented in:

- `/Users/benjaminbrumbaugh/Documents/Gas City/Gas-City/assets/gas-town-dashboard/READ_ME_FIRST.md`;
- `/Users/benjaminbrumbaugh/Documents/Gas City/Gas-City-Dashboard/READ_ME_FIRST.md`;
- [`Gas-City-Dashboard`](https://github.com/benjaminbrumbaugh/Gas-City-Dashboard).

## Verification checklist

Before treating an SDK build as deployable:

- exact source commit and tree recorded;
- worktree clean except explicitly preserved unrelated work;
- `make build` or the repository-prescribed build passed;
- `gc version` and `go version -m` identify the intended artifact;
- focused tests, `make check`, and relevant integration gates classified honestly;
- installed path and running process command verified;
- launchd service ownership verified when automatic startup is required;
- supervisor `/health`, `/v0/cities`, and the registered city are healthy;
- the separate Python dashboard at `http://localhost:8400/` remains healthy;
- no provider credentials, tokens, or other secrets entered Git or documentation.

## Upstream reference and full documentation

This README explains only the fork/runtime boundary. For general Gas City concepts and the full SDK manual, use:

- **[Upstream README](https://github.com/gastownhall/gascity/blob/main/README.md)**;
- [Local documentation](docs/README.md);
- [Installation](docs/getting-started/installation.md);
- [How Gas City works](docs/getting-started/how-gas-city-works.md);
- [Contributor documentation](engdocs/contributors/index.md);
- [Contributing](CONTRIBUTING.md).

## License

MIT
