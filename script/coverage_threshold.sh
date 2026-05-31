#!/usr/bin/env bash
# coverage_threshold.sh — fail if total Go test coverage is below
# the threshold passed as $1 (default: 80).
#
# Reads coverage.out (or the file named in $2). Uses `go tool cover`
# to compute the total. Exits 1 with a clear error message on
# threshold miss. Designed for CI gating; safe to run locally.

set -euo pipefail

THRESHOLD="${1:-80}"
COVER_FILE="${2:-coverage.out}"

if [[ ! -f "$COVER_FILE" ]]; then
    echo "coverage_threshold: ${COVER_FILE} not found" >&2
    echo "  run 'make coverage' first" >&2
    exit 2
fi

# `go tool cover -func=…` last line is `total: ... XX.X%`.
TOTAL_LINE="$(go tool cover -func="$COVER_FILE" | tail -1)"
PCT="$(echo "$TOTAL_LINE" | grep -oE '[0-9]+\.[0-9]+' | tail -1)"

if [[ -z "$PCT" ]]; then
    echo "coverage_threshold: could not parse coverage percentage from:" >&2
    echo "  $TOTAL_LINE" >&2
    exit 2
fi

# bash arithmetic is integer-only — multiply by 10 and compare ints.
PCT_INT="$(echo "$PCT * 10 / 1" | bc)"
THRESHOLD_INT="$((THRESHOLD * 10))"

if [[ "$PCT_INT" -lt "$THRESHOLD_INT" ]]; then
    echo "❌ coverage ${PCT}% < threshold ${THRESHOLD}%"
    echo "   $TOTAL_LINE"
    exit 1
fi

echo "✓ coverage ${PCT}% (threshold ${THRESHOLD}%)"
echo "  $TOTAL_LINE"
