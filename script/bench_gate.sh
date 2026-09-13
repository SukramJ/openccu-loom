#!/usr/bin/env bash
# bench_gate.sh — ADR 0007's payload-build performance gate.
#
# ADR 0007 §Mitigations puts a ceiling on one operation: "the per-type cached
# reflection path must stay below 500 ns/op for a 20-field struct". The
# operation is `payload.ForWith`; `tests/bench/payload_build_test.go` measures
# it; this script is what turns that measurement into something that can fail.
#
# READ THIS FIRST: that bound has since been WITHDRAWN, because `ForWith` turned
# out to be 1.2 % of the build it sits in. What this script now gates is
# `BenchmarkDiscoveryBuildPerEntity` — the whole per-entity HA-Discovery build —
# and it gates it on ALLOCATIONS, not nanoseconds, because the ns/op ceilings
# below went red on an unchanged tree. The allocation axis turned out to vary
# too, by exactly one, for a reason that is now measured and carried as
# headroom rather than assumed away. Both stories are told in order further
# down; the sections are dated by the order they were learned, not rewritten.
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
# nobody should spend effort on. (Their ns/op values were subsequently RAISED
# rather than held — see the next section, which is why.)
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
# WHY THERE ARE NOW TWO CEILINGS PER BENCHMARK, AND WHICH ONE IS THE GATE
#
# The ns/op ceilings armed by #814 were calibrated on one CI leg and went RED
# on an unchanged tree the third time this gate ran. Three consecutive runs of
# identical code drew three different CPUs:
#
#   AMD EPYC 9V45      1343 / 829  ns/op   (the leg #814 calibrated on)
#   Intel Xeon 8573C   1870 / 1167 ns/op   (+40 %)
#   AMD EPYC 7763      2717 / 1578 ns/op   (+102 %, and 2717 > the 2700 ceiling)
#
# So GitHub's hosted pool spans a factor of two by itself, the 2x headroom over
# its FASTEST member does not cover its slowest, and a gate calibrated that way
# fails on code nobody touched. That is the precise failure mode this script's
# header warns about — "a gate that flakes gets switched off", and this
# repository has already had one switched off.
#
# The fix is not more padding. It is to gate on the number that does not vary.
#
# ALLOCATIONS ARE THE GATE. allocs/op is far less sensitive to the machine
# than ns/op: across all three CPUs above it was 26, 19 and 811, identical to
# the unit, while the timings spanned a factor of two. That is what makes it
# the right axis. It is not, however, the *invariant* the first version of this
# section claimed.
#
# THE ALLOCATION AXIS VARIES TOO — BY ONE, AND FOR A KNOWN REASON
#
# The claim "allocs/op cannot flake, so arm the ceiling exactly at the measured
# value" was tested and is false. BenchmarkDiscoveryBuildPerEntity reports 811
# or 812 allocs/op on one machine and one unchanged tree, depending only on how
# the garbage collector is configured:
#
#   GOGC=off                        20/20 runs -> 811
#   default GOGC, -benchtime=100x    1/20 runs -> 812
#   GOGC=1,      -benchtime=3000x    9/10 runs -> 812
#   GOMEMLIMIT=16MiB + GOGC=1        6/6  runs -> 812
#
# The mechanism is isolated: the build ends in a JSON marshal, and
# encoding/json keeps its encodeState in a sync.Pool. A sync.Pool is DRAINED BY
# THE GC. A pool hit costs 811 allocations; a pool miss costs 812, because the
# state has to be allocated afresh. So the count is a function of GC pressure —
# of GOGC, of GOMEMLIMIT, of how much other memory the process happens to be
# using, and of b.N (a short run is likelier to span a GC than to amortise one
# away). A memory-capped runner, or a leg that simply schedules a GC at a
# different moment, moves this digit on code nobody touched. Arming at exactly
# 811 reproduced the identical failure mode #816 was written to fix — a ratchet
# red on an unchanged tree — with the headroom removed.
#
# WHY THE HEADROOM IS EXACTLY ONE ALLOCATION, AND NOT A PERCENTAGE
#
# Because the dynamic range of the mechanism is exactly one allocation, and
# that is measurable rather than guessed. testing.AllocsPerOp() TRUNCATES
# totalAllocs/N, so a partial miss rate cannot show up as a fraction: 811.9
# reads as 811. The count therefore only reaches 812 when essentially every
# iteration misses the pool — the saturated worst case — and no GC setting can
# push it past the one extra object the pool holds. Every adversarial
# configuration tried above lands in {811, 812} and nothing reached 813. So
# 812 is not padding chosen for comfort; it is the top of the measured range of
# the one identified non-code source of variance. A percentage headroom would
# be worse in both directions at once: 1 % of 811 is eight allocations of slack
# for a real regression to hide in, while 1 % of 19 rounds to nothing and
# leaves the ForWith ceilings exactly as brittle as they are now.
#
# The two ForWith ceilings are NOT loosened. 26 and 19 were stable across every
# configuration in the table above — those paths do not touch the pooled
# encoder — so they stay armed exactly at the measured value, which is where a
# ratchet belongs when the number really does hold.
#
# WHAT THE HEADROOM COSTS, STATED PLAINLY. On the discovery row the gate can
# no longer tell a +1 regression from a pool miss — because a pool miss IS +1.
# No ceiling on this benchmark can; that is a property of the measurement, not
# a choice, and the alternative is not a sharper gate but a gate red on an
# unchanged tree, which is a gate that gets switched off. The sensitivity is
# not wholly lost: a +1 anywhere inside `payload.ForWith` is still caught
# exactly by the TwentyField and DeviceInfo ceilings, which keep zero headroom.
# Only a +1 landing outside ForWith and inside the rest of the discovery build
# now needs +2 to show here.
#
# The mutation proofs this gate has been put through are otherwise unaffected:
# #814's 40-extra-allocs mutation showed 66 and 59, the triple-render mutation
# that armed the discovery ceiling showed 2434, and the one-extra-allocation
# mutation still reds at 27 vs 26 and 20 vs 19 (it no longer reds the discovery
# row, which is exactly the cost described above — ADR 0007 records the
# correction).
#
# FAIL CLOSED: AN ABSENT MEASUREMENT IS NOT A PASS
#
# This is the general principle, and it is worth stating because the gate got
# it wrong. The allocs/op column used to exist only because each benchmark
# happened to call b.ReportAllocs(); this script did not pass -benchmem. In the
# awk below an unset value compares as 0, and `0 > 811` is false — so deleting
# one b.ReportAllocs(), or adding a benchmark without one, produced
#
#   OK BenchmarkDiscoveryBuildPerEntity: 0 allocs/op (ceiling 811)   rc=0
#
# a green gate that measured nothing at all. It hid well, because `make bench`
# in the CI step immediately above this one DOES pass -benchmem, so the log
# still printed the real 811 while the gate read an empty string. The existing
# missing-benchmark guard keys on the ns/op line and never saw it. Both halves
# are fixed: -benchmem is passed explicitly, and a missing allocs/op column is
# now a hard failure rather than a zero. No gate in this script may ever treat
# the absence of a measurement as a satisfied bound.
#
# RE-ARMING: WHAT CAN LEGITIMATELY MOVE THESE NUMBERS
#
# Moving a ceiling is a deliberate act: change the number here, and say what
# moved it and why in the commit message. Two things can move an allocation
# count without anyone in this repository writing a line of code, and the
# second one is the one that can land unattended:
#
#   1. A Go toolchain bump. Cannot arrive by itself: go.mod carries no
#      `toolchain` directive and CI pins the version by hand, so a bump is
#      always somebody's reviewed commit.
#   2. **A go-hamqtt bump — including a patch release.** This is the live
#      unattended path. The reflection walk these benchmarks measure and the
#      hadiscovery.RenderComponent call inside the discovery build are both
#      upstream code, so an allocation change there lands here directly; and
#      Dependabot auto-merge excludes only go-ha-catalog, so a go-hamqtt patch
#      bump can merge without a human reading it. If this gate goes red on a
#      commit that touches nothing but go.mod/go.sum, check the go-hamqtt diff
#      FIRST — a moved count there is a real upstream change worth
#      understanding before it is re-armed, not noise.
#
# Neither is a licence to bump a ceiling to whatever the tree now measures
# without looking at why it moved. Lowering one after a real improvement is
# still the point.
#
# NS/OP IS A BACKSTOP, NOT THE GATE. It stays because allocation count alone
# cannot see a regression that burns CPU without allocating — a quadratic loop,
# a lock convoy, a suddenly-uncached reflection walk. But it is now calibrated
# on the SLOWEST leg observed rather than the fastest, with 1.5x on top, which
# makes it a catastrophic-regression detector rather than a tripwire. Do not
# read a comfortable ns/op margin as headroom for adding work: the allocation
# ceiling above it has none.
#
# name  ceiling_ns_per_op  ceiling_allocs_per_op
CEILINGS=(
    # ADR 0007's literal workload: the per-type cached reflection path over a
    # 20-field struct. ns: 2717 (EPYC 7763, the slowest leg seen) x1.5.
    # allocs: exactly as measured on all three legs.
    "BenchmarkPayloadBuildTwentyField 4100 26"
    # The production call site: internal/north/mqtt/discovery.go harvesting a
    # *device.Device for KindInfo. ns: 1578 (same leg) x1.5.
    "BenchmarkPayloadBuildDeviceInfo 2400 19"
    # The whole per-entity HA-Discovery build, of which the two above are
    # 1.2 % of the time and 2.3 % of the allocations. THIS is the figure worth
    # watching: at ~12 entities per device it is what turns into ~1.1 s of
    # discovery build on a 1000-device boot, and proportionally more on the
    # 32-bit ARMv7 CCU3 this daemon ships to. See ADR 0007's second
    # 2026-09-13 amendment. ns: 95 082 on the Xeon leg and 123 899 on the
    # slower EPYC 7763 leg; the latter x1.5, rounded. The ForWith share holds
    # at 1.2 % on BOTH legs (1174/95082 and 1552/123899), which is the
    # cross-CPU check that the share is a property of the code.
    # allocs: 812, which is 811 + one allocation of deliberate headroom for the
    # GC-drained sync.Pool inside encoding/json — the full measured range of
    # that mechanism, not a percentage. See WHY THE HEADROOM IS EXACTLY ONE
    # ALLOCATION above. Armed at 811 this ceiling went red on an unchanged tree
    # under GOGC pressure.
    "BenchmarkDiscoveryBuildPerEntity 200000 812"
)

BENCH_RE='^(BenchmarkPayloadBuildTwentyField|BenchmarkPayloadBuildDeviceInfo|BenchmarkDiscoveryBuildPerEntity)$'

echo "bench_gate: ${RUNS} runs x ${BENCHTIME} per benchmark; the gate reads the minimum"

# -benchmem is passed EXPLICITLY even though every gated benchmark calls
# b.ReportAllocs(). Relying on the benchmark to opt itself in makes the gate's
# own measurement a property of the code under test: delete one
# b.ReportAllocs(), or add a benchmark without one, and the allocs/op column
# simply stops being printed. See FAIL CLOSED below for why that used to be
# silent.
if ! RAW="$(go test -tags=bench -run '^$' -bench "$BENCH_RE" -benchmem \
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
        ns = ""; al = ""
        for (i = 1; i <= NF; i++) {
            if ($i == "ns/op")     ns = $(i-1) + 0
            if ($i == "allocs/op") al = $(i-1) + 0
        }
        if (ns == "") next
        if (!(name in minns) || ns < minns[name]) minns[name] = ns
        # MINIMUM on the allocation axis too, for the same reason as ns/op.
        # Allocation noise is one-sided in the same direction that timing noise
        # is: the environment can only ADD allocations to a run (a GC-drained
        # sync.Pool that has to allocate a fresh object instead of reusing one
        # — see THE ALLOCATION AXIS VARIES below), never remove one the code
        # actually performs. So the minimum is again the sample least polluted
        # by the runner, and taking the maximum here would have inverted the
        # carefully-argued statistic one line above it on the axis that is now
        # the gate. An unset al is NOT folded in as a zero; it is recorded as a
        # missing measurement and failed on in END.
        if (al == "") noal[name] = 1
        else if (!(name in minal) || al < minal[name]) minal[name] = al
        seen[name] = 1
    }
    END {
        n = split(ceilings, c, " ")
        bad = 0
        for (i = 1; i <= n; i += 3) {
            name = c[i]; nsceil = c[i+1] + 0; alceil = c[i+2] + 0
            if (!(name in seen)) {
                printf "::error::bench_gate: %s produced no measurement\n", name
                bad = 1
                continue
            }
            if ((name in noal) || !(name in minal)) {
                printf "::error::bench_gate: %s reported no allocs/op column — the allocation gate MEASURED NOTHING and fails closed. Check that the benchmark still calls b.ReportAllocs() and that this script still passes -benchmem.\n", name
                bad = 1
            } else if (minal[name] > alceil) {
                printf "::error::%s: %d allocs/op > ceiling %d allocs/op — the allocation gate does not flake on runner speed, so this is a real regression\n", name, minal[name], alceil
                bad = 1
            } else {
                printf "OK %s: %d allocs/op (ceiling %d allocs/op)\n", name, minal[name], alceil
            }
            if (minns[name] > nsceil) {
                printf "::error::%s: %.1f ns/op > ceiling %d ns/op\n", name, minns[name], nsceil
                bad = 1
            } else {
                printf "OK %s: %.1f ns/op (ceiling %d ns/op, backstop)\n", name, minns[name], nsceil
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
