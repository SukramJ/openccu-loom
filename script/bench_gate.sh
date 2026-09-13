#!/usr/bin/env bash
# bench_gate.sh — ADR 0007's payload-build performance gate.
#
# ADR 0007 §Mitigations puts a ceiling on one operation: "the per-type cached
# reflection path must stay below 500 ns/op for a 20-field struct". The
# operation is `payload.ForWith`; `tests/bench/payload_build_test.go` measures
# it; this script is what turns that measurement into something that can fail.
#
# WHY MINIMUM-OF-N, NOT MEAN OR MEDIAN
#
# A shared CI runner adds time; it never removes it. Contention, a co-tenant
# VM, a throttled core and a stop-the-world GC pause all push a sample up, and
# nothing pushes one below the cost the code actually has. So the minimum over
# N independent runs is the sample least polluted by the runner, and it is the
# only summary statistic whose noise is one-sided in the safe direction: noise
# can make the gate pass a run it should have failed (retry catches that), but
# it cannot make the gate fail a run it should have passed. A mean or a median
# drifts with the runner's load and would have to be padded so far that the
# gate stops meaning anything. This matters more than the usual amount here:
# this repository has already had a gate that flaked and was switched off, and
# an unenforced red gate is worse than no gate at all.
#
# This is not a theoretical preference. On the 4-core developer machine the
# benchmark was written on, ten runs of the unchanged twenty-field benchmark
# spanned 4 773 - 5 990 ns/op with the box near-idle and 32 907 - 49 634 ns/op
# with three other build jobs on it: a factor of seven between two runs of
# identical code, and a spread wide enough that any average-based gate would
# either flake or be padded into meaninglessness. The minimum tracked the quiet
# figure in both cases, which is the property this gate needs. On the CI runner
# the ceilings below are calibrated against (ubuntu-latest, AMD EPYC 9V45) the
# seven runs spanned 1 343 - 1 735 ns/op, so even there one sample in seven sat
# 29 % high while the minimum held steady.
#
# On top of the minimum the ceilings below carry an explicit headroom factor
# over the measured figure, so a CI runner that is simply slower silicon than
# the machine the numbers were taken on does not turn the gate red on an
# unchanged tree. The headroom is stated per ceiling, not hidden in a global
# multiplier, so a reader can see what is actually being enforced.
#
# THESE CEILINGS ARE A RATCHET, NOT THE ADR'S BOUND
#
# The ADR's 500 ns/op is NOT met. Measured on the CI runner these ceilings are
# calibrated against: 1 343 ns/op for the ADR's own twenty-field workload (2.7x
# the bound) and 829 ns/op for the production device harvest (1.7x). See the
# ADR's 2026-09-13 amendment. The ceilings below are armed at what the code
# measures today so that it cannot get worse while the gap is addressed
# separately. Lowering a ceiling after an improvement is the point; raising one
# needs a reason in the commit message.
#
# AND THE ADR'S BOUND HAS SINCE BEEN WITHDRAWN AS NOT WORTH MEETING
#
# The ADR's second 2026-09-13 amendment measured the denominator the first one
# lacked: `payload.ForWith` is 1.2 % of the per-entity HA-Discovery build it is
# part of (1 174 ns/op inside 95 082 ns/op), and 2.3 % of its allocations (19
# of 811). Closing the 500 ns/op gap entirely would buy 0.7 % of a discovery
# build. So the two ForWith ceilings below are no longer a placeholder for an
# optimisation that is coming; they are plain regression protection for a path
# nobody should spend effort on, and they stay at their current values.
#
# BenchmarkDiscoveryBuildPerEntity is the ceiling that replaced the ADR's
# bound, and it is the one worth watching: at ~12 entities per device it is
# what turns into ~1.1 s of discovery build on a 1 000-device boot, and into
# proportionally more on the 32-bit ARMv7 CCU3 this daemon ships to.
#
# VERIFIED TO FAIL
#
# Giving `payload.ForWith` 40 extra allocations per call turned this gate red
# on CI at the ceilings below — 3 139 ns/op vs 2 700 and 2 444 ns/op vs 1 700,
# at 66 and 59 allocs/op instead of 26 and 19. A 2.3x regression, caught. The
# mutation was reverted and the same gate returned green.
#
# NOTE FOR LOCAL RUNS: the ceilings are calibrated for the CI runner, which is
# server silicon. A modest or loaded developer machine will exceed them on
# unchanged code — that is expected, and CI is the authority. Locally, compare
# a before/after pair on the same box rather than reading the verdict.
#
# Usage:  script/bench_gate.sh          (or: make bench-gate)
# Env:    BENCH_GATE_RUNS      number of independent runs (default 7)
#         BENCH_GATE_BENCHTIME go test -benchtime value  (default 300ms)

set -euo pipefail

# Numbers are parsed and compared here, so the shell's locale must not decide
# what a decimal point looks like. On a German-locale developer machine awk's
# printf emitted "8902,0" and every downstream comparison silently degraded.
export LC_ALL=C

RUNS="${BENCH_GATE_RUNS:-7}"
BENCHTIME="${BENCH_GATE_BENCHTIME:-300ms}"

# name<space>ceiling_ns_per_op
#
# BenchmarkPayloadBuildTwentyField is the ADR's literal workload: the
# per-type cached reflection path over a 20-field struct.
# BenchmarkPayloadBuildDeviceInfo is the production call site in
# internal/north/mqtt/discovery.go, harvesting a *device.Device for KindInfo.
# Each ceiling is the minimum this benchmark measured on the CI runner, doubled.
# The 2x is headroom for runner silicon, not for noise — the minimum-of-N above
# already handles noise. GitHub's hosted pool is not one machine, and a leg
# scheduled on an older part can be genuinely slower at identical code; 2x
# covers that while still failing on any regression that actually matters (a
# doubling of the harvest cost is not a rounding error). Tighten these when the
# pool stops varying, or when the path gets faster.
#
# A NOTE ON WHY THE HEADROOM IS NOT PARANOIA. These ceilings were first
# calibrated on an AMD EPYC 9V45 leg (1 343 / 829 ns/op). The very next run, on
# the same workflow and an unchanged tree, drew an Intel Xeon Platinum 8573C
# and measured 1 870 / 1 167 ns/op — 40 % slower at identical code. Without the
# 2x the gate would have gone red on a tree nobody had touched. GitHub's hosted
# pool really is not one machine, and this is the receipt.
CEILINGS=(
    # measured 1343 ns/op (AMD EPYC 9V45, min of 7); 1870 ns/op on a later
    # Xeon 8573C leg, which is what the 2x headroom is for
    "BenchmarkPayloadBuildTwentyField 2700"
    # measured 829 ns/op (same EPYC run), 1167 ns/op on the Xeon leg
    "BenchmarkPayloadBuildDeviceInfo 1700"
    # measured 95 082 ns/op (Xeon 8573C). This is the one that matters: the
    # whole per-entity discovery build, of which the two ForWith figures above
    # are 1.2 %. See the ADR's second 2026-09-13 amendment.
    "BenchmarkDiscoveryBuildPerEntity 190000"
)

BENCH_RE='^(BenchmarkPayloadBuildTwentyField|BenchmarkPayloadBuildDeviceInfo|BenchmarkDiscoveryBuildPerEntity)$'

echo "bench_gate: ${RUNS} runs x ${BENCHTIME} per benchmark; the gate reads the minimum"

if ! RAW="$(go test -tags=bench -run '^$' -bench "$BENCH_RE" \
    -benchtime="$BENCHTIME" -count="$RUNS" ./tests/bench/ 2>&1)"; then
    echo "$RAW" >&2
    echo "::error::bench_gate: the benchmark run itself failed" >&2
    exit 2
fi

echo "$RAW"
echo

# One awk pass does the whole job: collapse the run lines to a per-benchmark
# minimum and compare each against its ceiling. Keeping the float arithmetic
# inside awk avoids bash's integer-only comparison and the locale trap above.
#
# `go test` prints
#   BenchmarkX-4   	  123456	      789.0 ns/op	 ...
# so the name carries a -GOMAXPROCS suffix and ns/op may be fractional.
REPORT="$(echo "$RAW" | awk -v ceilings="${CEILINGS[*]}" '
    /ns\/op/ {
        name = $1
        sub(/-[0-9]+$/, "", name)
        v = ""
        for (i = 1; i <= NF; i++) if ($i == "ns/op") { v = $(i-1) + 0; break }
        if (v == "") next
        if (!(name in min) || v < min[name]) min[name] = v
        seen[name] = 1
    }
    END {
        n = split(ceilings, c, " ")
        bad = 0
        for (i = 1; i <= n; i += 2) {
            name = c[i]; ceil = c[i+1] + 0
            if (!(name in seen)) {
                printf "::error::bench_gate: %s produced no measurement\n", name
                bad = 1
                continue
            }
            if (min[name] > ceil) {
                printf "::error::%s: %.1f ns/op > ceiling %d ns/op\n", name, min[name], ceil
                bad = 1
            } else {
                printf "OK %s: %.1f ns/op (ceiling %d ns/op)\n", name, min[name], ceil
            }
        }
        exit bad
    }
')" && rc=0 || rc=$?

echo "$REPORT"

if [[ "$rc" -ne 0 ]]; then
    echo
    echo "A ceiling here is a ratchet at what the code measured when the gate"
    echo "was armed. Either the change made the payload harvest slower, or the"
    echo "runner was pathologically loaded across all ${RUNS} runs. Re-run"
    echo "before assuming the latter."
    exit 1
fi

echo
echo "bench_gate: ok — ADR 0007's payload-build ratchet holds"
