---
title: Web dashboard
description: The supervisor hosts a built-in web dashboard for all your cities.
---

Gas City supports two dashboard modes. Upstream defaults to a single-page app
compiled into `gc` and served from the supervisor root. This fork keeps that
compatibility code but can disable only the embedded UI while retaining the
supervisor API and run projections. Set a canonical external front door with:

```toml
[supervisor]
embedded_dashboard = false
dashboard_url = "http://localhost:8400/"
```

`gc dashboard` uses the configured external URL unless the invocation supplies
an explicit `--api` embedded origin. Without either, it advertises an embedded
URL only after the actual root responds with HTML; `/api` health by itself is
not evidence that the SPA is mounted.

## Open it

Start the supervisor, then open the URL it prints:

```bash
gc supervisor start
# Supervisor API listening on http://127.0.0.1:8372
# Dashboard:  http://127.0.0.1:8372/
```

If the supervisor is already running, `gc dashboard` opens it in your browser
and prints the URL too:

```bash
gc dashboard
# Opened the dashboard in your browser: http://127.0.0.1:8372
```

Pass `--no-open` to print the URL without launching a browser (useful over SSH
or in scripts):

```bash
gc dashboard --no-open
# The embedded dashboard is served by the gc supervisor at http://127.0.0.1:8372
```

`gc dashboard` does not start a server. It opens the configured external front
door, or points the browser at a supervisor/standalone origin only after that
origin proves it serves the embedded SPA. In embedded mode, one supervisor
serves every registered city; pick a city from the dashboard switcher.

## What it shows

The dashboard reads the supervisor's typed API directly (same origin), so it
reflects live state: agents and their sessions, beads, mail, formula runs, and a
health view (system, local tools, per-rig store health, and the dolt store
trend).

## Security posture

The dashboard is served on the supervisor's bind address, which defaults to
loopback (`127.0.0.1`). It is intended for local, single-operator use:

- It is same-origin with the API; browser mutations carry the supervisor's
  `X-GC-Request` CSRF header.
- When the supervisor binds a non-localhost address without
  `allow_mutations`, it runs read-only and the dashboard disables its mutating
  controls.

## Turn it off

Set `embedded_dashboard = false` in `[supervisor]` to disable only the embedded
SPA while retaining `/api` and run projections. Configure `dashboard_url` when
an external dashboard should be opened and advertised. The legacy
`GC_SUPERVISOR_DASHBOARD=0` escape hatch remains a stronger embedded-plane
disable that also removes the dashboard BFF `/api` plane. A separately
configured external `dashboard_url` remains operator-facing under that setting.
