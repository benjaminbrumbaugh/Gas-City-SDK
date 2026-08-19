# READ ME FIRST — Extraneous and Unused Dashboard

This directory contains the **bundled upstream Gas City/Gas Town dashboard SPA** embedded into the `gc` binary and served by the supervisor by default.

For Benjamin Brumbaugh's owned-dashboard workspace, this dashboard is **extraneous and unused**. It is not the authoritative source and it is not the relocation destination.

## Authoritative dashboard

Source:

`/Users/benjaminbrumbaugh/Documents/Gas City/Gas-City/assets/gas-town-dashboard`

Live front door:

`http://localhost:8400/`

Destination:

`/Users/benjaminbrumbaugh/Documents/Gas City/Gas-City-Dashboard`

Do not copy features from this embedded SPA into the owned dashboard, and do not use this directory as the cutover source. Set `GC_SUPERVISOR_DASHBOARD_SPA=0` in the supervisor environment to keep the host-side `/api` support plane without serving this UI. Set it to `1` and restart only when this bundled SPA is needed temporarily. The broader `GC_SUPERVISOR_DASHBOARD=0` switch also removes the `/api` plane and is therefore not appropriate while the owned dashboard consumes that plane.
