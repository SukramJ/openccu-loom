#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 openccu-loom authors.
#
# Compares the embedded openccu-data extracts against a local
# openccu-data checkout using the SHA-256 hashes recorded in
# internal/ccudata/embedded/MANIFEST.json.
#
# Exit codes:
#   0 — all manifest hashes match (OK) or repo path does not exist (optional tool)
#   1 — one or more hashes differ (DRIFT)
#   2 — manifest entry present but source file is missing in repo (MISSING)
#
# Usage:
#   OPENCCU_DATA_PATH=/path/to/openccu-data ./script/ccudata_drift.sh
#
# The env-var defaults to ../openccu-data.
# If the path does not exist the script prints a warning and exits 0 —
# the tool is optional and must not break CI environments without a
# local openccu-data checkout.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
EMBED_DIR="${REPO_ROOT}/internal/ccudata/embedded"
MANIFEST="${EMBED_DIR}/MANIFEST.json"

OPENCCU_DATA_PATH="${OPENCCU_DATA_PATH:-../openccu-data}"
SRC_DIR="${OPENCCU_DATA_PATH}/openccu_data/data"

# ── Guard: repo not checked out ──────────────────────────────────────────────
if [[ ! -d "${SRC_DIR}" ]]; then
  echo "ccudata-drift: openccu-data not found at ${OPENCCU_DATA_PATH} — skipping (set OPENCCU_DATA_PATH to override)" >&2
  exit 0
fi

# ── Parse MANIFEST.json with jq ──────────────────────────────────────────────
if ! command -v jq &>/dev/null; then
  echo "ccudata-drift: jq is required but not found in PATH" >&2
  exit 1
fi

overall=0   # 0=OK, 1=DRIFT, 2=MISSING (highest wins)

set_status() {
  local code="$1"
  if (( code > overall )); then
    overall=$code
  fi
}

n=$(jq '.files | length' "${MANIFEST}")
for i in $(seq 0 $((n - 1))); do
  rel=$(jq -r ".files[$i].path" "${MANIFEST}")
  want_hash=$(jq -r ".files[$i].sha256" "${MANIFEST}")
  src="${SRC_DIR}/${rel}"

  if [[ ! -f "${src}" ]]; then
    echo "MISSING  ${rel} (not in ${SRC_DIR})" >&2
    set_status 2
    continue
  fi

  got_hash=$(sha256sum "${src}" | awk '{print $1}')
  if [[ "${got_hash}" == "${want_hash}" ]]; then
    echo "OK       ${rel}"
  else
    echo "DRIFT    ${rel}" >&2
    echo "         want: ${want_hash}" >&2
    echo "         got:  ${got_hash}" >&2
    set_status 1
  fi
done

if (( overall == 0 )); then
  echo "ccudata-drift: all ${n} manifest entries in sync with ${OPENCCU_DATA_PATH}"
  exit 0
fi

if (( overall == 2 )); then
  echo "" >&2
  echo "ccudata-drift: source file(s) missing in upstream repo — check OPENCCU_DATA_PATH" >&2
  exit 2
fi

cat >&2 <<'EOF'

ccudata-drift: drift detected. To resolve:
  1. Inspect the diff above.
  2. Run 'make update-ccu-data' to pull the new archives into embedded/.
  3. Recompute MANIFEST.json hashes (sha256sum internal/ccudata/embedded/*.json.gz).
  4. Run 'make test' to validate; commit archives + MANIFEST.json together.
EOF
exit 1
