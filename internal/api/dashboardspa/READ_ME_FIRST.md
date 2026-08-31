# READ ME FIRST — Retained Upstream Dashboard

This directory contains the **bundled upstream Gas City/Gas Town dashboard SPA** embedded into the `gc` binary and served by the supervisor by default.

For Benjamin Brumbaugh's installation, this dashboard is retained for upstream
merge and regeneration compatibility but disabled at runtime. It is not the
authoritative product source and it is not the destination for generic
dashboard work.

## Authoritative dashboard

Source:

`/Users/benjaminbrumbaugh/Documents/Gas City/Gas-City-Dashboard`

Live front door:

`http://localhost:8400/`

Do not implement product UI features here or copy this SPA into the owned
dashboard. Mechanical bundle/client regeneration remains allowed when required
by an upstream merge or intentional API-schema change. Editing this SPA itself
requires a task that explicitly names the embedded upstream UI.
