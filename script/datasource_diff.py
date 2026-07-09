#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 openccu-loom authors.
#
# datasource_diff.py — compare the embedded CCU dataset between
# pydevccu (Python reference) and godevccu (Go simulator).
#
# Two layers of comparison are performed:
#
#  1. Identity layer — recursively normalise every JSON file and
#     compare structurally. Catches missing files, drifted devices,
#     reordered keys, etc.
#
#  2. Attribution layer — for every (device, channel, paramset,
#     parameter) tuple, extract the per-parameter descriptor fields
#     that downstream consumers (aiohomematic / openccu-loom) rely on:
#         TYPE, OPERATIONS, FLAGS, MIN, MAX, UNIT, DEFAULT, CONTROL,
#         ID, TAB_ORDER, SPECIAL, VALUE_LIST.
#     Any divergence — including a parameter present on one side with
#     a different attribute set — is reported per parameter so that
#     a missing OPERATIONS bit on one side or a different MIN value
#     surfaces explicitly.
#
# Background: parity_audit.md §15.4 calls for bit-for-bit verification
# that aiohomematic+pydevccu and openccu-loom+godevccu run on the same
# data. The first ("identity") layer answers "are the files the same".
# The second ("attribution") layer answers "and is each parameter
# attributed identically". Both must pass before the downstream
# Discovery-payload comparison can be trusted.
#
# Usage: python3 script/datasource_diff.py
# Exit code:
#   0 — both layers identical
#   1 — drift detected; details in stdout

from __future__ import annotations

import json
import os
import sys
from pathlib import Path

# Both source trees are resolved relative to the openccu-loom repo
# root, with environment-variable overrides for non-default layouts.
# The default assumes the sibling-repo convention used in
# CLAUDE.md and CONTRIBUTING.md (each project as a sibling under
# the same parent directory).
_REPO_ROOT = Path(__file__).resolve().parent.parent
_PARENT = _REPO_ROOT.parent

PYDEVCCU_ROOT = Path(os.environ.get("PYDEVCCU_ROOT") or (_PARENT / "pydevccu" / "pydevccu"))
GODEVCCU_ROOT = Path(os.environ.get("GODEVCCU_ROOT") or (_PARENT / "godevccu" / "internal" / "embed" / "data"))

SECTIONS = ("device_descriptions", "paramset_descriptions")

# Vacuous-run floor. When the roots are mis-provisioned (e.g. a CI job
# never checks out the sibling trees), glob() finds zero JSON files, the
# diff compares nothing, and the script would otherwise exit 0 — a green
# parity gate that verified nothing. Below these floors the run is treated
# as a configuration error and fails NON-zero. The real fleet compares 399
# common files per section and ~93k parameters; the floors sit far below
# that so a legitimately scoped fleet still passes, while an empty/near-
# empty run cannot masquerade as parity. Override for unusual layouts via
# the two env vars.
_MIN_COMMON_FILES = int(os.environ.get("DATASOURCE_DIFF_MIN_FILES") or 50)
_MIN_COMPARED_PARAMS = int(os.environ.get("DATASOURCE_DIFF_MIN_PARAMS") or 1000)

# Distinct non-zero exit for a vacuous/mis-provisioned run so logs tell it
# apart from a genuine-drift failure (1).
_EXIT_VACUOUS = 2

# Per-parameter descriptor fields the downstream consumers care about.
# Mirrors what aiohomematic2mqtt / homematicip_local read off the wire
# (`hmproto.ParameterData` in Go) and what discovery payloads ultimately
# build min/max/step/unit/options from.
PARAM_FIELDS = (
    "TYPE",
    "OPERATIONS",
    "FLAGS",
    "MIN",
    "MAX",
    "UNIT",
    "DEFAULT",
    "CONTROL",
    "ID",
    "TAB_ORDER",
    "SPECIAL",
    "VALUE_LIST",
)


def normalise(payload):
    """Recursively sort dicts and ensure stable ordering for diff."""
    if isinstance(payload, dict):
        return {k: normalise(payload[k]) for k in sorted(payload)}
    if isinstance(payload, list):
        # Lists in CCU descriptors are positional (channel order, paramset
        # entries) — keep order but normalise children.
        return [normalise(x) for x in payload]
    return payload


def load_normalised(path: Path):
    with path.open() as fp:
        return normalise(json.load(fp))


def diff_section(section: str) -> dict:
    py_dir = PYDEVCCU_ROOT / section
    go_dir = GODEVCCU_ROOT / section

    py_files = {p.name for p in py_dir.glob("*.json")}
    go_files = {p.name for p in go_dir.glob("*.json")}

    only_py = sorted(py_files - go_files)
    only_go = sorted(go_files - py_files)
    common = sorted(py_files & go_files)

    drift = []
    for name in common:
        py = load_normalised(py_dir / name)
        go = load_normalised(go_dir / name)
        if py != go:
            drift.append(name)

    return {
        "section": section,
        "py_count": len(py_files),
        "go_count": len(go_files),
        "common": len(common),
        "only_pydevccu": only_py,
        "only_godevccu": only_go,
        "drifted": drift,
    }


def extract_param_attributes(paramset_descriptor: dict) -> dict:
    """
    Walk a paramset descriptor JSON and return a dict keyed on
    `(channel_address, paramset_key, parameter_name)` whose values are
    the [PARAM_FIELDS] subset of the parameter descriptor. Missing
    fields are reported as the literal string "<absent>" so the
    downstream diff distinguishes "field missing" from "field = null".
    """
    out = {}
    # paramset_descriptor structure (pydevccu format):
    # {
    #   "VCU_ADDRESS:N": {
    #     "MASTER": {"PARAM_NAME": {"TYPE": ..., ...}, ...},
    #     "VALUES": {...},
    #     "LINK":   {"SENDER:N": {"PARAM_NAME": {...}, ...}, ...},
    #   },
    #   ...
    # }
    for ch_addr, paramsets in paramset_descriptor.items():
        if not isinstance(paramsets, dict):
            continue
        for paramset_key, payload in paramsets.items():
            if not isinstance(payload, dict):
                continue
            # LINK paramsets are sender-keyed; flatten one level deeper.
            if paramset_key == "LINK":
                for sender, params in payload.items():
                    if not isinstance(params, dict):
                        continue
                    for pname, pdesc in params.items():
                        if not isinstance(pdesc, dict):
                            continue
                        out[(ch_addr, paramset_key, sender, pname)] = {
                            f: pdesc.get(f, "<absent>") for f in PARAM_FIELDS
                        }
            else:
                for pname, pdesc in payload.items():
                    if not isinstance(pdesc, dict):
                        continue
                    out[(ch_addr, paramset_key, pname)] = {
                        f: pdesc.get(f, "<absent>") for f in PARAM_FIELDS
                    }
    return out


def diff_attribution(common_files: list[str]) -> dict:
    """
    For every common paramset_descriptions/<file>.json, extract the
    per-parameter attribute snapshot from both sides and report any
    divergence. Returns a dict keyed by device file name.
    """
    py_dir = PYDEVCCU_ROOT / "paramset_descriptions"
    go_dir = GODEVCCU_ROOT / "paramset_descriptions"

    summary = {
        "files_compared": 0,
        "parameter_keys_compared": 0,
        "drifted_files": [],
        "drifted_parameters": 0,
        "only_in_pydevccu": 0,
        "only_in_godevccu": 0,
    }
    drift_detail = {}

    for name in common_files:
        py = load_normalised(py_dir / name)
        go = load_normalised(go_dir / name)

        py_attrs = extract_param_attributes(py)
        go_attrs = extract_param_attributes(go)

        only_py = sorted(py_attrs.keys() - go_attrs.keys())
        only_go = sorted(go_attrs.keys() - py_attrs.keys())
        shared = sorted(py_attrs.keys() & go_attrs.keys())

        drifts = []
        for key in shared:
            if py_attrs[key] != go_attrs[key]:
                drifts.append(
                    {
                        "key": list(key),
                        "py": py_attrs[key],
                        "go": go_attrs[key],
                    }
                )

        summary["files_compared"] += 1
        summary["parameter_keys_compared"] += len(shared)
        summary["drifted_parameters"] += len(drifts)
        summary["only_in_pydevccu"] += len(only_py)
        summary["only_in_godevccu"] += len(only_go)

        if drifts or only_py or only_go:
            summary["drifted_files"].append(name)
            drift_detail[name] = {
                "only_in_pydevccu": [list(k) for k in only_py],
                "only_in_godevccu": [list(k) for k in only_go],
                "drifted_parameters": drifts[:20],
                "drifted_count": len(drifts),
            }

    return {"summary": summary, "detail": drift_detail}


def main():
    identity = {section: diff_section(section) for section in SECTIONS}
    identity_summary = {
        section: {
            "py_count": r["py_count"],
            "go_count": r["go_count"],
            "common": r["common"],
            "only_pydevccu": len(r["only_pydevccu"]),
            "only_godevccu": len(r["only_godevccu"]),
            "drifted": len(r["drifted"]),
        }
        for section, r in identity.items()
    }

    common_paramsets = sorted(
        {p.name for p in (PYDEVCCU_ROOT / "paramset_descriptions").glob("*.json")}
        & {p.name for p in (GODEVCCU_ROOT / "paramset_descriptions").glob("*.json")}
    )
    attribution = diff_attribution(common_paramsets)

    report = {
        "identity_layer": {
            "summary": identity_summary,
            "detail": identity,
        },
        "attribution_layer": attribution,
    }
    print(json.dumps(report, indent=2))

    # Fail loudly on a vacuous run before the drift verdict: a run that
    # compared (almost) nothing cannot certify parity, regardless of the
    # 0-drift tally it would otherwise report.
    min_common = min((r["common"] for r in identity_summary.values()), default=0)
    compared_params = attribution["summary"]["parameter_keys_compared"]
    if min_common < _MIN_COMMON_FILES or compared_params < _MIN_COMPARED_PARAMS:
        print(
            "[datasource-diff] VACUOUS RUN — refusing to certify parity.\n"
            f"  common files (min across sections) = {min_common} "
            f"(floor {_MIN_COMMON_FILES})\n"
            f"  parameters compared                = {compared_params} "
            f"(floor {_MIN_COMPARED_PARAMS})\n"
            f"  PYDEVCCU_ROOT = {PYDEVCCU_ROOT}\n"
            f"  GODEVCCU_ROOT = {GODEVCCU_ROOT}\n"
            "Both roots must point at provisioned pydevccu / godevccu data "
            "trees (device_descriptions/ + paramset_descriptions/).",
            file=sys.stderr,
        )
        return _EXIT_VACUOUS

    identity_drift = sum(
        s["drifted"] + s["only_pydevccu"] + s["only_godevccu"]
        for s in identity_summary.values()
    )
    attribution_drift = (
        attribution["summary"]["drifted_parameters"]
        + attribution["summary"]["only_in_pydevccu"]
        + attribution["summary"]["only_in_godevccu"]
    )
    total_drift = identity_drift + attribution_drift
    return 0 if total_drift == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
