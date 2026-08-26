#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 SukramJ.
#
# model_snapshot_drift_check.py — read the JSON output of
# `script/model_snapshot_diff.py` from stdin and assert per-bucket
# drift counts stay at or below documented baselines. The release
# pipeline (`make snapshot-diff`) uses this to detect regressions
# without failing on the architecturally-accepted residue catalogued
# in `notes/parity/by_design.md`.
#
# The per-bucket defaults below sum to the cross-stack drift baseline
# referenced in CLAUDE.md ("Cross-stack model-snapshot verification").
# Ratchet a baseline DOWN when a fix closes drift; only raise one when
# a genuinely new architectural divergence has a matching entry in
# `notes/parity/by_design.md`. Never raise a baseline merely to silence
# a regression.
#
# Override a baseline via its env var (handy for ratcheting in one run
# before committing the lowered default):
#
#   OPENCCU_LOOM_DRIFT_GENERIC=70 OPENCCU_LOOM_DRIFT_CHANNEL=40 \
#   OPENCCU_LOOM_DRIFT_CUSTOM_ONLY_PY=160 OPENCCU_LOOM_DRIFT_CALC=10 \
#   OPENCCU_LOOM_DRIFT_MISSING_DEVICES=0 \
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
# `notes/parity/by_design.md`. The env var names are spelled out here
# verbatim — no string munging — so the documented overrides above
# actually resolve to these keys.
_BASELINES: dict[str, tuple[str, int]] = {
    "generic_data_points.drifted": ("OPENCCU_LOOM_DRIFT_GENERIC", 10),
    "channel_fields": ("OPENCCU_LOOM_DRIFT_CHANNEL", 0),
    "custom_data_points.only_py": ("OPENCCU_LOOM_DRIFT_CUSTOM_ONLY_PY", 0),
    "calculated_data_points.drifted": ("OPENCCU_LOOM_DRIFT_CALC", 0),
}

# Devices the reference stack built and we did not. The diff reports them
# outside `drift_counts` (which is computed over the *intersection* of device
# addresses only), so nothing here would see them without reading the list
# explicitly — a whole device missing from the model, the loudest regression
# there is, would score zero drift.
_MISSING_DEVICES_KEY = "only_in_aiohomematic_devices"
_MISSING_DEVICES_ENV = "OPENCCU_LOOM_DRIFT_MISSING_DEVICES"


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

    # Every check below runs even when `drift_counts` is empty: the counts
    # cover the intersection of device addresses only, so "no counts" is not
    # "no drift" — an empty block is exactly what a whole missing device
    # produces, and the unguarded-bucket check has nothing to say about it
    # either.
    counts = summary.get("drift_counts", {})

    failures: list[str] = []

    missing_devices = summary.get(_MISSING_DEVICES_KEY) or []
    missing_baseline = _baseline(_MISSING_DEVICES_ENV, 0)
    if len(missing_devices) > missing_baseline:
        marker = "FAIL"
        failures.append(
            f"{_MISSING_DEVICES_KEY}: actual={len(missing_devices)} > "
            f"baseline={missing_baseline} ({', '.join(missing_devices[:10])}"
            f"{', …' if len(missing_devices) > 10 else ''})"
        )
    else:
        marker = "OK"
    print(
        f"  {marker:4}  {_MISSING_DEVICES_KEY:42}  "
        f"actual={len(missing_devices):5}  baseline={missing_baseline}"
    )

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
            "entry to notes/parity/by_design.md and bump the matching baseline",
            file=sys.stderr,
        )
        print("in this script. Otherwise, fix the drift.", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
