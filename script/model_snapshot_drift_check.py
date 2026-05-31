#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 openccu-loom authors.
#
# model_snapshot_drift_check.py — read the JSON output of
# `script/model_snapshot_diff.py` from stdin and assert per-bucket
# drift counts stay below documented baselines. CI uses this to
# detect regressions without failing on the architecturally-accepted
# residue documented in `parity_audit.md` §0.2.
#
# Override baselines via env vars (one digit per bucket):
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

# Baselines reflect the architecturally-accepted residue documented
# in parity_audit.md §0.2. Adjust together with the audit when a fix
# closes more drift, never just to silence a regression.
_DEFAULT_BASELINES = {
    "generic_data_points.drifted": 70,
    "channel_fields": 40,
    "custom_data_points.only_py": 160,
    "calculated_data_points.drifted": 10,
}


def _baseline(name: str, default: int) -> int:
    env_key = "OPENCCU_LOOM_DRIFT_" + name.upper().replace(".", "_").replace("DATA_POINTS", "").replace("__", "_")
    raw = os.environ.get(env_key)
    if raw is None:
        return default
    try:
        return int(raw)
    except ValueError:
        print(f"[drift-check] invalid env override {env_key}={raw!r}; using default {default}", file=sys.stderr)
        return default


def main() -> int:
    try:
        report = json.load(sys.stdin)
    except json.JSONDecodeError as exc:
        print(f"[drift-check] failed to parse stdin as JSON: {exc}", file=sys.stderr)
        return 2

    counts = report.get("summary", {}).get("drift_counts", {})
    if not counts:
        print("[drift-check] no drift_counts in report; treating as pass", file=sys.stderr)
        return 0

    failures: list[str] = []
    for bucket, default_baseline in _DEFAULT_BASELINES.items():
        actual = counts.get(bucket, 0)
        baseline = _baseline(bucket, default_baseline)
        marker = "OK" if actual <= baseline else "FAIL"
        print(f"  {marker:4}  {bucket:42}  actual={actual:5}  baseline={baseline}")
        if actual > baseline:
            failures.append(f"{bucket}: actual={actual} > baseline={baseline}")

    if failures:
        print(file=sys.stderr)
        print("[drift-check] regression detected:", file=sys.stderr)
        for f in failures:
            print(f"  - {f}", file=sys.stderr)
        print("Update parity_audit.md §0.2 if the new residue is a", file=sys.stderr)
        print("legitimate architecture change, then bump the baseline.", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
