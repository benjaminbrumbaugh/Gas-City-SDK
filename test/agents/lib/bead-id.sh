#!/usr/bin/env bash
# Shared bead-ID matcher for the Bash agent scripts in test/agents.
# This file is sourced by those scripts; it is not an agent itself.
#
# Match IDs by shape, never by a hardcoded prefix. Bead stores may mint
# prefixes configured by the city, so a literal gc-, bd-, or mc- filter can
# silently drop real rows from an inbox.
#
# Source this file in other scripts:
#   source "$(dirname "${BASH_SOURCE[0]}")/lib/bead-id.sh"

# A whole-token bead ID: a letter-led alphanumeric prefix, one or more dash
# segments, and optional .N child segments (for example, ga-t832q4.2).
BEAD_ID_ERE='[A-Za-z][A-Za-z0-9]*(-[A-Za-z0-9]+)+([.][A-Za-z0-9]+)*'

# bead_id_rows reads stdin and prints only lines whose first field is a bead
# ID. It always succeeds so callers using set -euo pipefail can safely compose
# the filter. Keep the expression portable across GNU grep and BSD grep.
bead_id_rows() {
    grep -E "^${BEAD_ID_ERE}([[:space:]]|\$)" || true
}
