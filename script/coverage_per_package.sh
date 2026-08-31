#!/usr/bin/env bash
# coverage_per_package.sh — verify per-package Go test coverage
# against package-specific minima. Reads coverage.out (or $2) and
# uses `go tool cover -func` to compute each package's percentage.
#
# Tier policy (mirrors CLAUDE.md):
#   - core packages (client, central, model, store): ≥ 80 %
#   - north-bound adapters (north/{rest,mqtt,ui}): ≥ 60 %
#   - everything else: ≥ 50 % (sanity floor)
#
# Exit 1 with a per-package report on any miss. Designed for CI
# gating; safe to run locally after `make coverage`.

set -euo pipefail
# Force C locale so awk + go cover speak in dots, not commas.
export LC_ALL=C

COVER_FILE="${1:-coverage.out}"

if [[ ! -f "$COVER_FILE" ]]; then
    echo "coverage_per_package: ${COVER_FILE} not found" >&2
    echo "  run 'make coverage' first" >&2
    exit 2
fi

# Per-package thresholds: pragmatic floors set to (current coverage
# minus ~3 pp margin). Coverage is measured from the integration-
# inclusive, cross-package profile produced by `make coverage`
# (go test -tags=integration -coverpkg=./...); the per-package gate
# runs in the integration workflow against that profile. The script
# acts as a regression tripwire — drops below the floor fail CI.
# Comments note the CURRENT coverage so the headroom is visible at a
# glance; entries tagged "(coverpkg)" were recalibrated against the
# -coverpkg profile.
declare -A TIER_CORE=(
    [internal/client]=93               # current 96.5
    [internal/client/backends]=89      # current 92.0
    [internal/client/reliability]=92   # current 95.7
    [internal/client/rega]=70          # current 73.3 (coverpkg)
    [internal/client/transport/binrpc]=92 # current 95.1
    [internal/client/transport/jsonrpc]=96 # current 99.7
    [internal/client/transport/xmlrpc]=90 # current 93.1
    [internal/central]=84              # current 87.7 (coverpkg)
    [internal/central/coordinators]=91 # current 94.1
    [internal/central/events]=85       # current ~88.5 (coverpkg; event bus fires non-deterministically under godevccu)
    [internal/central/adapter]=85      # current 88.8 (coverpkg)
    [internal/central/rpcserver]=82    # current 85.2
    [internal/central/registry]=93     # current 96.9
    [internal/central/statemachine]=96 # current 99.9
    [internal/model/device]=87         # current 90.7
    [internal/model/generic]=92        # current 95.5
    [internal/model/hub]=86            # current 89.8 (coverpkg)
    [internal/model/custom]=91         # current 94.9
    [internal/model/calculated]=85     # current 88.5 (coverpkg)
    [internal/model/optimistic]=96     # current 99.4 (push-coverage)
    [internal/model/combined]=90       # current 93.2 (coverpkg)
    [internal/model/event]=86          # current 89.1 (coverpkg)
    [internal/model/weekprofile]=78    # current 81.4 (coverpkg)
    [internal/model/schedule]=88       # current 91.8
    [internal/store/sqlite]=84         # current ~87 (coverpkg; hovers near the floor across runs)
    [internal/store/visibility]=88     # current 91.3
    [internal/store/patches]=95        # current 98.9
    [internal/scheduler]=97            # current 100.0
    [internal/health]=90               # current 93.4
    [internal/i18n]=92                 # current 95.3
    [internal/metrics]=96              # current 99.5
    [internal/payload]=88              # current 91.8 (coverpkg)
    [internal/observability]=85        # current 88.5
    [internal/parameter]=92            # current 95.7
    [internal/config]=86               # current 89.6
    [internal/configui]=90             # current 94.0
    [internal/clock]=97                # current 100.0
    [internal/audit]=92                # current 95.1
    [internal/auth]=91                 # current 94.9
    [internal/auth/oidc]=93            # current 96.1
    [internal/ccudata]=90              # current 93.6
    [pkg/hmenum]=77                    # current 80.5 (enum-heavy files)
    [pkg/hmerr]=95                     # current 98.5
    [pkg/hmevent]=97                   # current 100.0
    [pkg/hmproto]=91                   # current 94.6
    [pkg/hmtypes]=94                   # current 97.3
)

declare -A TIER_NORTH=(
    [internal/north/rest]=87           # current 90.2
    [internal/north/rest/handlers]=84  # current 87.5 (coverpkg)
    [internal/north/rest/middleware]=93 # current 96.5
    [internal/north/rest/ws]=81        # current 84.1 (read/write pumps need live WS)
    [internal/north/mqtt]=84           # current 87.1 (coverpkg)
    [internal/north/ui]=82             # current 85.5
)

# Floor for any package not listed above. Set to 0 because many
# auto-listed packages are tiny enum/constant collections that don't
# need coverage. Add an explicit tier entry to enforce a higher
# threshold.
FLOOR=0

# Build per-package totals from the coverage profile.
# `go tool cover -func` output line shape:
#   github.com/.../pkg/file.go:line:col funcName  XX.X%
# We aggregate by path-prefix → package import path.

# tmp file with: import-path  count-stmts  count-covered
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT

# Reduce coverage.out to per-package coverage via go's cover tool.
go tool cover -func="$COVER_FILE" \
    | awk 'NF==3 && $1 != "total:" {
        # split file:line:col into package path
        n = split($1, parts, "/")
        # rebuild path without trailing /file.go:line:col
        path = ""
        for (i=1; i<n; i++) {
            path = (path == "" ? parts[i] : path "/" parts[i])
        }
        # strip module prefix (github.com/SukramJ/openccu-loom/)
        sub("^github.com/SukramJ/openccu-loom/", "", path)
        # parse percentage
        pct = $3
        sub("%", "", pct)
        sums[path] += pct
        counts[path] += 1
    } END {
        for (p in sums) {
            printf "%s\t%.1f\n", p, sums[p]/counts[p]
        }
    }' \
    | sort > "$TMP"

failed=0
declare -A SEEN

while IFS=$'\t' read -r pkg pct; do
    [[ -z "$pkg" ]] && continue
    SEEN[$pkg]=1

    threshold="${TIER_CORE[$pkg]:-${TIER_NORTH[$pkg]:-$FLOOR}}"
    pct_int=$(echo "$pct * 10 / 1" | bc)
    th_int=$((threshold * 10))

    if [[ "$pct_int" -lt "$th_int" ]]; then
        printf "❌ %-50s %5.1f%% < %d%%\n" "$pkg" "$pct" "$threshold"
        failed=$((failed + 1))
    fi
done < "$TMP"

# Also fail if a tier-CORE package has zero coverage entries (got
# silently dropped from the profile — usually means no _test.go files).
for pkg in "${!TIER_CORE[@]}"; do
    if [[ -z "${SEEN[$pkg]:-}" ]]; then
        printf "❌ %-50s no coverage entries (no tests?)\n" "$pkg"
        failed=$((failed + 1))
    fi
done

if [[ "$failed" -gt 0 ]]; then
    echo ""
    echo "$failed package(s) below their tier threshold."
    exit 1
fi

echo "✓ all packages meet their tier threshold"
