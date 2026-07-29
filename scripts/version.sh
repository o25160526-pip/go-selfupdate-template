#!/usr/bin/env bash
# Thin wrapper around tools/genversion so shell callers get the same logic.
# Everything is UTC on purpose: see docs/VERSIONING.md.
set -euo pipefail
export TZ=UTC
exec go run ./tools/genversion "$@"
