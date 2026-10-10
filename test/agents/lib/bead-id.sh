#!/usr/bin/env bash
# Shared bead-ID matcher for the bash agent scripts in test/agents.
# IDs are matched by shape rather than a store-specific prefix because stores
# may derive different prefixes from their city name.

BEAD_ID_ERE='[A-Za-z][A-Za-z0-9]*(-[A-Za-z0-9]+)+([.][A-Za-z0-9]+)*'

# bead_id_rows prints lines whose first field is a whole bead ID. It always
# returns success so callers using set -euo pipefail can safely consume an
# empty result.
bead_id_rows() {
    grep -E "^${BEAD_ID_ERE}([[:space:]]|\$)" || true
}
