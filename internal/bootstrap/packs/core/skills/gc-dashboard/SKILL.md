---
name: gc-dashboard
description: API server and web dashboard — config, start, monitor
---

# Dashboard

Benjamin's configured dashboard product is the separate `Gas-City-Dashboard`
front door at `http://localhost:8400/`. The SDK's embedded SPA is optional,
upstream-compatibility code; a healthy `/api` plane does not prove that UI is
mounted.

## Prerequisites

The dashboard is a separate web server. It needs a GC API server to talk to,
but it no longer has to be launched from inside a city directory.

### Standalone city mode

If you are using `gc start` without the machine-wide supervisor, the dashboard
talks to that city's own API server. This `[api]` config is only consulted in
standalone mode — if the machine-wide supervisor is running, it takes over
(see Supervisor mode below) and this per-city port is ignored. Ensure the
city API is enabled in `city.toml`:

```toml
[api]
port = 9443
```

Then start the city normally with `gc start`. The API server starts with the
controller on that port.

### Supervisor mode

If you are using the machine-wide supervisor, the dashboard talks to the
supervisor API instead. The default supervisor API address is:

```text
http://127.0.0.1:8372
```

In this mode, per-city `[api]` ports are ignored. The dashboard detects
supervisor mode automatically via `/v0/cities`, enables a city selector, and
routes requests through `/v0/city/{name}/...`.

## Starting the dashboard

```
gc dashboard                               # Auto-discover API server, open dashboard in browser
gc dashboard --no-open                    # Print the dashboard URL instead of opening a browser
gc dashboard serve                        # Print where the web dashboard is served
gc dashboard --city /path/to/city         # Optional city context for standalone discovery
gc dashboard --api http://127.0.0.1:8372 # Optional override
```

`gc dashboard` first reads `[supervisor] dashboard_url` and opens that validated
external front door when configured. Otherwise it probes the actual root HTML
surface before offering an embedded supervisor or standalone URL. API health
alone is never treated as UI availability. The `--api` flag remains available
as an override only when that origin serves the embedded root.

## Features

The dashboard provides:

- **Convoys** — progress tracking, tracked issues, create new convoys
- **Crew** — named worker status with activity detection
- **Polecats** — ephemeral worker activity and work status
- **Activity timeline** — categorized event feed with filters
- **Mail** — inbox with threading, compose, and all-traffic view
- **Merge queue** — open PRs with CI and mergeable status
- **Escalations** — priority-colored escalation list
- **Ready work** — items available for assignment
- **Health** — system heartbeat and agent counts
- **Issues** — backlog with priority, age, labels, assignment
- **Command palette** (Cmd+K) — execute gc commands from the browser

Real-time updates via SSE (Server-Sent Events) from the API server.
