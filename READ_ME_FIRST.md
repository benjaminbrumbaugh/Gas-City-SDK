# READ ME FIRST — Custom Gas City SDK Fork

This repository is Benjamin Brumbaugh's **customization and build fork of Gas City**.

- Go module: `github.com/gastownhall/gascity`
- Upstream source: `https://github.com/gastownhall/gascity`
- Benjamin's fork: `https://github.com/benjaminbrumbaugh/Gas-City-SDK`

It carries custom and forward-looking changes to Gas City itself, including the `gc` CLI, supervisor, HTTP APIs, runtime providers, session/controller behavior, typed contracts, routing-admission seams, and reusable tests. Build and SDK-level customization work belongs here.

Do not confuse this repository with the live city workspace at:

`/Users/benjaminbrumbaugh/Documents/Gas City/Gas-City`

The live workspace owns `city.toml`, active agents/rigs, operational scripts, and city-specific state. Before claiming that a locally installed or running `gc` binary uses this checkout, verify its executable/build provenance against the intended commit.

## Dashboard warning

`internal/api/dashboardspa` is the upstream embedded dashboard bundled with `gc`. It is extraneous and unused for Benjamin's owned-dashboard cutover; read the marker in that directory before touching it.
