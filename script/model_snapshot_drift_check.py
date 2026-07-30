#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 openccu-loom authors.
#
# model_snapshot_drift_check.py — read the JSON output of
# `script/model_snapshot_diff.py` from stdin and assert per-bucket
# drift counts stay at or below documented baselines. The release
# pipeline (`make snapshot-diff`) uses this to detect regressions
# without failing on the architecturally-accepted residue catalogued
# in `docs/parity/by_design.md`.
#
# The per-bucket defaults below sum to the cross-stack drift baseline
# referenced in CLAUDE.md ("Cross-stack model-snapshot verification").
# Ratchet a baseline DOWN when a fix closes drift; only raise one when
# a genuinely new architectural divergence has a matching entry in
# `docs/parity/by_design.md`. Never raise a baseline merely to silence
# a regression.
#
# Override a baseline via its env var (handy for ratcheting in one run
# before committing the lowered default):
#
#   OPENCCU_LOOM_DRIFT_GENERIC=70 OPENCCU_LOOM_DRIFT_CHANNEL=40 \
#   OPENCCU_LOOM_DRIFT_CUSTOM_ONLY_PY=160 OPENCCU_LOOM_DRIFT_CALC=10 \
#   make snapshot-diff
#
# Usage:
#   python3 script/model_snapshot_diff.py | \
#       python3 script/model_snapshot_drift_check.py
#
# Exit codes:
#   0 — every bucket within baseline
#   1 — at least one bucket exceeds baseline (regression)
#   2 — bad input (no JSON on stdin)

from __future__ import annotations

import json
import os
import sys

# bucket -> (env override var, default baseline).
#
# Defaults are the architecturally-accepted residue catalogued in
# `docs/parity/by_design.md`. The env var names are spelled out here
# verbatim — no string munging — so the documented overrides above
# actually resolve to these keys.
_BASELINES: dict[str, tuple[str, int]] = {
    "generic_data_points.drifted": ("OPENCCU_LOOM_DRIFT_GENERIC", 10),
    "channel_fields": ("OPENCCU_LOOM_DRIFT_CHANNEL", 0),
    "custom_data_points.only_py": ("OPENCCU_LOOM_DRIFT_CUSTOM_ONLY_PY", 0),
    "calculated_data_points.drifted": ("OPENCCU_LOOM_DRIFT_CALC", 0),
}


def _baseline(env_key: str, default: int) -> int:
    raw = os.environ.get(env_key)
    if raw is None:
        return default
    try:
        return int(raw)
    except ValueError:
        print(
            f"[drift-check] invalid env override {env_key}={raw!r}; "
            f"using default {default}",
            file=sys.stderr,
        )
        return default


def main() -> int:
    try:
        report = json.load(sys.stdin)
    except json.JSONDecodeError as exc:
        print(f"[drift-check] failed to parse stdin as JSON: {exc}", file=sys.stderr)
        return 2

    # A missing summary block is not "no drift" — it means the diff did not
    # produce a usable report at all. That distinction matters because the
    # caller (`make snapshot-diff`) deliberately discards the diff's exit
    # status so this check owns the verdict; without the guard, a diff that
    # crashed or wrote nothing would read as a clean run.
    summary = report.get("summary")
    if not isinstance(summary, dict):
        print(
            "[drift-check] report carries no summary block — the diff produced "
            "no usable output; refusing to report a pass",
            file=sys.stderr,
        )
        return 2

    counts = summary.get("drift_counts", {})
    if not counts:
        print("[drift-check] summary reports no drift", file=sys.stderr)
        return 0

    failures: list[str] = []
    total_actual = 0
    total_baseline = 0
    for bucket, (env_key, default_baseline) in _BASELINES.items():
        actual = counts.get(bucket, 0)
        baseline = _baseline(env_key, default_baseline)
        total_actual += actual
        total_baseline += baseline
        marker = "OK" if actual <= baseline else "FAIL"
        print(f"  {marker:4}  {bucket:42}  actual={actual:5}  baseline={baseline}")
        if actual > baseline:
            failures.append(f"{bucket}: actual={actual} > baseline={baseline}")

    # A drift bucket the diff started reporting that we have no baseline
    # for is an unguarded regression channel — fail loudly rather than
    # let new drift slip through a bucket nobody is watching.
    for bucket, actual in counts.items():
        if bucket not in _BASELINES and actual:
            failures.append(f"{bucket}: actual={actual} has no baseline (unguarded bucket)")
            print(f"  FAIL  {bucket:42}  actual={actual:5}  baseline=(none)")

    print(f"  ----  {'TOTAL':42}  actual={total_actual:5}  baseline={total_baseline}")

    if failures:
        print(file=sys.stderr)
        print("[drift-check] regression detected:", file=sys.stderr)
        for f in failures:
            print(f"  - {f}", file=sys.stderr)
        print(
            "If the new residue is a legitimate architecture change, add an",
            file=sys.stderr,
        )
        print(
            "entry to docs/parity/by_design.md and bump the matching baseline",
            file=sys.stderr,
        )
        print("in this script. Otherwise, fix the drift.", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
