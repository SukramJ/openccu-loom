#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 SukramJ.
#
# check_api_version_bump — CI guard for the north-bound contract
# version (ADR 0028). When any contract asset (openapi.yaml,
# wsapi.json, schemas/{enums,types}.json) differs from the base ref,
# the APIVersion constant in internal/north/rest/handlers/info.go
# must have been bumped:
#
#   - any asset change      -> APIVersion must increase (>= minor)
#   - breaking OpenAPI diff -> APIVersion major must increase
#
# Breaking-change classification uses oasdiff (Apache-2.0), pinned
# below and executed via `go run`. wsapi.json / enums.json /
# types.json changes are not classified — any diff there requires at
# least a minor bump; reviewers judge whether it is breaking.
#
# Usage: script/check_api_version_bump.sh [<base-ref>]
#   base-ref defaults to origin/main; CI passes the PR base branch.

set -euo pipefail

OASDIFF=github.com/oasdiff/oasdiff@v1.18.6
INFO_GO=internal/north/rest/handlers/info.go
ASSETS=(assets/openapi.yaml assets/wsapi.json assets/schemas/enums.json assets/schemas/types.json)

base_ref="${1:-origin/main}"
base="$(git merge-base HEAD "$base_ref")"

if git diff --quiet "$base" -- "${ASSETS[@]}"; then
  echo "contract assets unchanged vs $base_ref — no APIVersion bump required"
  exit 0
fi

api_version_at() {
  git show "$1:$INFO_GO" | sed -nE 's/^const APIVersion = "([^"]+)"$/\1/p'
}

base_ver="$(api_version_at "$base")"
head_ver="$(api_version_at HEAD)"
if [ -z "$base_ver" ] || [ -z "$head_ver" ]; then
  echo "FAIL: could not extract APIVersion from $INFO_GO" >&2
  exit 1
fi

semver_part() { echo "$1" | cut -d. -f"$2"; }

semver_gt() { # returns success when $1 > $2
  local a b
  for i in 1 2 3; do
    a="$(semver_part "$1" "$i")"; b="$(semver_part "$2" "$i")"
    if [ "$a" -gt "$b" ]; then return 0; fi
    if [ "$a" -lt "$b" ]; then return 1; fi
  done
  return 1
}

echo "contract assets changed vs $base_ref (base APIVersion=$base_ver, head APIVersion=$head_ver)"

if ! semver_gt "$head_ver" "$base_ver"; then
  echo "FAIL: contract assets changed but APIVersion did not increase." >&2
  echo "Bump APIVersion in $INFO_GO and info.version in assets/openapi.yaml" >&2
  echo "(minor for additive changes, major for breaking ones)." >&2
  exit 1
fi

# Classify the OpenAPI diff. oasdiff exits 1 when breaking changes
# exist (--fail-on ERR) and 0 otherwise; other exit codes are tool
# errors and fail the guard.
if ! git diff --quiet "$base" -- assets/openapi.yaml; then
  tmp_base="$(mktemp -t openapi-base-XXXXXX.yaml)"
  trap 'rm -f "$tmp_base"' EXIT
  git show "$base:assets/openapi.yaml" > "$tmp_base"
  set +e
  go run "$OASDIFF" breaking "$tmp_base" assets/openapi.yaml --fail-on ERR
  oasdiff_rc=$?
  set -e
  case "$oasdiff_rc" in
    0)
      echo "OpenAPI diff is non-breaking — minor bump $base_ver -> $head_ver is sufficient"
      ;;
    1)
      base_major="$(semver_part "$base_ver" 1)"
      head_major="$(semver_part "$head_ver" 1)"
      if [ "$head_major" -le "$base_major" ]; then
        echo "FAIL: oasdiff classified the OpenAPI diff as BREAKING," >&2
        echo "but APIVersion only moved $base_ver -> $head_ver (major unchanged)." >&2
        echo "Either bump the major version or rework the change to be additive." >&2
        exit 1
      fi
      echo "breaking OpenAPI diff covered by major bump $base_ver -> $head_ver"
      ;;
    *)
      echo "FAIL: oasdiff exited with $oasdiff_rc (tool error)" >&2
      exit "$oasdiff_rc"
      ;;
  esac
fi

echo "APIVersion bump check passed"
