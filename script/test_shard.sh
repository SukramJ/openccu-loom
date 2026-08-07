#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 OpenCCU-Loom authors.
#
# test_shard.sh — print the Go packages that belong to one CI test shard.
#
#   script/test_shard.sh <index> <total> [package-list-file]
#   script/test_shard.sh --exempt
#
# The race-detector test run is by far the longest job in CI, so ci.yml splits
# it across several runners. This script owns the split so the partitioning
# rule lives in one reviewable place instead of being inlined in YAML.
#
# The rule: `go list ./...` emits the module's packages in a stable sorted
# order, and package N goes to shard `N mod <total>`. That is deterministic
# (same input, same assignment on every runner) and exhaustive (every package
# lands in exactly one shard, none is dropped) — the two properties that make
# the union of the shards equivalent to a single `go test ./...`.
#
# Round-robin rather than contiguous blocks: the package names are sorted, so
# contiguous blocks would pile the alphabetically adjacent heavyweights —
# internal/north/matter/* alone is a third of the tree — onto one runner while
# another idles.
#
# Race-exempt packages are subtracted from the shards and printed by
# `--exempt` instead. They still run in CI, just without -race, because the
# detector finds nothing there and costs a great deal: tests/contract is
# static analysis over the module (type checker, repo walks) and spawns no
# goroutine at all, yet it was the single most expensive package in the race
# run at ~105 s — the floor of the whole Linux leg, and unsplittable because
# it is one package.
#
# A package belongs here only with that evidence. TestRaceExemptPackagesHaveNoGoroutines
# re-checks it on every run: the moment an exempt package spawns a goroutine,
# the build fails and the exemption has to be argued again rather than
# quietly covering concurrent code.
#
# The optional third argument names a file to read the package list from
# ("-" for stdin) instead of shelling out to `go list`. CI does not pass it;
# it exists so a caller that already has the list (the contract test that
# checks this partitioning, for one) does not pay for a second module load.

set -euo pipefail

# Package suffixes exempt from the race run. Matched against the end of the
# import path so the module prefix does not have to be repeated.
RACE_EXEMPT=(
    /tests/contract
)

exempt_pattern() {
    local IFS='|'
    printf '(%s)$' "${RACE_EXEMPT[*]}"
}

if [[ "${1:-}" == "--exempt" ]]; then
    if [[ -n "${2:-}" ]]; then
        cat -- "$2"
    else
        go list ./...
    fi | grep -E "$(exempt_pattern)" || true
    exit 0
fi

INDEX="${1:?usage: test_shard.sh <index> <total> [package-list-file]}"
TOTAL="${2:?usage: test_shard.sh <index> <total> [package-list-file]}"
SOURCE="${3:-}"

if [[ ! "$INDEX" =~ ^[0-9]+$ || ! "$TOTAL" =~ ^[0-9]+$ ]]; then
    echo "test_shard: index and total must be positive integers" >&2
    exit 2
fi
if [[ "$TOTAL" -lt 1 ]]; then
    echo "test_shard: total must be >= 1, got $TOTAL" >&2
    exit 2
fi
if [[ "$INDEX" -lt 1 || "$INDEX" -gt "$TOTAL" ]]; then
    echo "test_shard: index $INDEX out of range 1..$TOTAL" >&2
    exit 2
fi

if [[ -n "$SOURCE" ]]; then
    cat -- "$SOURCE"
else
    go list ./...
fi | grep -Ev "$(exempt_pattern)" | awk -v idx="$INDEX" -v total="$TOTAL" 'NF > 0 && NR % total == idx % total'
