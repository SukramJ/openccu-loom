#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 openccu-loom authors.
#
# model_snapshot_diff.py — diff the openccu-loom and aiohomematic
# model-snapshot JSON files (produced by
# `tests/integration/model_snapshot_test.go` and
# `script/aiohomematic_snapshot.py` respectively) and report any
# attribute-level drift per (device, channel, data point).
#
# Both inputs follow `notes/parity/model_snapshot_schema.md`. This
# script focuses on per-field comparison and ignores the metadata
# block (stack / stack_version / devccu / devccu_version /
# captured_at). Anything else must match bit-exact.
#
# Usage:
#   python3 script/model_snapshot_diff.py [GO_JSON] [PY_JSON]
#
# Defaults:
#   GO_JSON = tests/integration/testdata/model_snapshot_openccu-loom.json
#   PY_JSON = tests/integration/testdata/model_snapshot_aiohomematic.json
#
# Exit codes:
#   0 — snapshots agree on all model fields
#   1 — drift detected; per-tuple report on stdout

from __future__ import annotations

import json
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any

REPO_ROOT = Path(__file__).resolve().parents[1]
DEFAULT_GO = REPO_ROOT / "tests/integration/testdata/model_snapshot_openccu-loom.json"
DEFAULT_PY = REPO_ROOT / "tests/integration/testdata/model_snapshot_aiohomematic.json"

# Top-level keys we never compare — they are stack identification and
# capture metadata.
META_KEYS = frozenset({"stack", "stack_version", "devccu", "devccu_version", "captured_at"})

# Fields we tolerate diverging on a single DP because they may differ
# in representation but not in semantics.
TOLERATED_DP_FIELDS: frozenset[str] = frozenset({
    # Custom-DP `profile` is a stack-internal type-name string. Both
    # stacks emit the same Custom-DP shape but with different format:
    # Go uses Go-type strings (`*climate.Climate`), Python uses fully-
    # qualified class paths (`aiohomematic.model.custom.climate.
    # CustomDpRfThermostat`). The canonical wire-equivalent attribute
    # is `category`, which is compared.
    "profile",
    # Custom-DP `wrapped_dps` lists the Generic-DPs the custom profile
    # composes. The two stacks compose Custom-DPs from non-identical
    # sets — openccu-loom resolves only the wire-VALUES it touches,
    # aiohomematic enumerates every `_dp_*` field on the class. The
    # difference is structural (the custom-domain *behaviour* matches;
    # the *book-keeping* of which DPs are wrapped does not). Compared
    # at the snapshot level it is too noisy to be useful — the
    # paramset_descriptions diff already confirms the wire DPs match.
    "wrapped_dps",
})


def load(path: Path) -> dict:
    with path.open() as fp:
        return json.load(fp)


def normalise_dp(dp: dict) -> dict:
    """Drop empty / None fields so absence and `null` compare equal."""
    out: dict[str, Any] = {}
    for k, v in dp.items():
        if v in (None, "", [], {}):
            continue
        out[k] = v
    return out


def index_devices(snap: dict) -> dict[str, dict]:
    return {d["address"]: d for d in snap.get("devices", [])}


def index_channels(dev: dict) -> dict[int, dict]:
    return {c["number"]: c for c in dev.get("channels", [])}


def index_dps(ch: dict, key: str) -> dict[tuple, dict]:
    """Index DPs by (paramset_key, parameter) — generic — or by
    (parameter,) for calculated/custom.

    For custom DPs the match key is `category` rather than the raw
    `profile` string. The two stacks emit the same Custom-DP under
    different profile-string formats (Go: `*climate.Climate`; Python:
    `aiohomematic.model.custom.climate.CustomDpRfThermostat`); the
    canonical attribute is the HA category each profile maps onto.
    """
    out: dict[tuple, dict] = {}
    for dp in ch.get(key, []):
        if "paramset_key" in dp:
            out[(dp.get("paramset_key"), dp.get("parameter"))] = dp
        elif "profile" in dp:
            out[(dp.get("category", "custom"),)] = dp
        else:
            out[(dp.get("parameter"),)] = dp
    return out


# Both "ignored" and "no_create" mean "not surfaced as a north-bound
# entity". OpenCCU-Loom marks visibility-gate-suppressed parameters as
# `ignored` (usage + forced_usage) so the user can un-ignore them via the
# un-ignore feature (ADR 0015) — a capability aiohomematic does not have;
# aiohomematic emits `no_create`. The Ignored marker is OpenCCU-Loom-internal
# metadata for that extra feature, behaviourally equivalent to no_create at
# the model level. See notes/parity/by_design.md.
_HIDDEN_USAGES = frozenset({"ignored", "no_create"})


def canon_hidden_usage(dp: dict) -> dict:
    """Canonicalise the two semantically-equivalent hidden usages so the
    Ignored↔NoCreate divergence does not register as drift. Applied to BOTH
    sides, so a *real* usage drift (e.g. ignored↔data_point, where only one
    side is a hidden usage) still surfaces."""
    if dp.get("usage") not in _HIDDEN_USAGES:
        return dp
    out = dict(dp)
    out["usage"] = "no_create"
    if out.get("forced_usage") in _HIDDEN_USAGES:
        out.pop("forced_usage", None)
    return out


# OpenCCU-Loom splits the aiohomematic `ce_visible` usage into two: genuine
# extra sensors stay `ce_visible`, while a custom entity's group-STATE status
# transmitter (the redundant channel that restates the primary's projection)
# is tagged `ce_state` so the Matter projection can drop it by default without
# hiding real extra sensors. Both are visible constituents behaviourally
# identical to `ce_visible` for HA / MQTT / REST; the reference model has only
# `ce_visible`, so canonicalise `ce_state` back to it (both sides) before the
# diff. See notes/parity/by_design.md.
def canon_state_usage(dp: dict) -> dict:
    """Canonicalise the OpenCCU-Loom-only `ce_state` usage (and matching
    forced_usage) back to `ce_visible` so the split does not register as drift.
    Applied to BOTH sides — the reference side never carries `ce_state`, so a
    real drift where only one side is `ce_visible` still surfaces."""
    if dp.get("usage") != "ce_state" and dp.get("forced_usage") != "ce_state":
        return dp
    out = dict(dp)
    if out.get("usage") == "ce_state":
        out["usage"] = "ce_visible"
    if out.get("forced_usage") == "ce_state":
        out["forced_usage"] = "ce_visible"
    return out


# OpenCCU-Loom replaces the raw schedule channel-lock bitfield DPs with a
# structured per-channel ScheduleChannelSwitch surface and suppresses the raw
# DPs (usage=no_create, enabled_default=false) so Home Assistant does not show
# redundant bitfield/select/number entities alongside the switches. aiohomematic
# builds the SAME switch surface but ALSO leaves the raw DPs visible
# (usage=data_point, enabled_default=true). See notes/parity/by_design.md
# (BD-Visibility-ScheduleChannelLocks).
_SCHEDULE_LOCK_PARAMS = frozenset({
    "WEEK_PROGRAM_CHANNEL_LOCKS",
    "WEEK_PROGRAM_TARGET_CHANNEL_LOCK",
    "WEEK_PROGRAM_TARGET_CHANNEL_LOCKS",
})


def is_schedule_lock_suppression(go: dict, py: dict) -> bool:
    """True for exactly the OpenCCU-Loom schedule-lock suppression signature
    (go=no_create vs py=data_point on a lock param). Any other drift on these
    params still surfaces."""
    return (
        go.get("parameter") in _SCHEDULE_LOCK_PARAMS
        and go.get("usage") == "no_create"
        and py.get("usage") == "data_point"
    )


# Click-event press parameters surfaced as keypress events. The Go
# `IsClickEvent()` set is the authority; every member name begins with PRESS
# (PRESS_SHORT, PRESS_LONG, PRESS_CONT, PRESS, …).
def _is_click_press(param: str | None) -> bool:
    return bool(param) and param.startswith("PRESS")


# Parameters OpenCCU-Loom keeps hidden on variant device models (e.g.
# HM-Sec-Key-S/-O/-Generic, HM-Sec-Win-Generic, HmIP-SWSD-2) where the
# reference stack surfaces them. The built-in per-device un-ignore /
# custom-profile visibility is scoped to the base/exact model, so a variant
# inherits the default-hidden state. Accepted by-design suppression.
_REFERENCE_ONLY_VISIBLE_PARAMS: frozenset[str] = frozenset({
    "DIRECTION",
    "SMOKE_DETECTOR_ALARM_STATUS",
})


def is_reference_only_visibility(go: dict, py: dict) -> bool:
    """True when OpenCCU-Loom hides one of the variant-model parameters
    (`usage=no_create`) while aiohomematic surfaces it (`data_point` /
    `ce_visible`). The usage/forced_usage/enabled_default flip is the entire
    (deliberate) signature — see notes/parity/by_design.md
    (BD-Visibility-VariantModelHiddenParams). Any other usage combination on
    these parameters still surfaces."""
    return (
        go.get("parameter") in _REFERENCE_ONLY_VISIBLE_PARAMS
        and go.get("usage") == "no_create"
        and py.get("usage") in ("data_point", "ce_visible")
    )


def is_local_button_event_suppression(go: dict, py: dict) -> bool:
    """True for the actuator-local-button signature: OpenCCU-Loom surfaces a
    keypress *event* (usage=event) on the local push-button input channels of
    actuators (HMW-LC-Bl1/Dim1L wired blind+dimmer actuators, HB-LC-Bl1PBU-FM)
    where aiohomematic creates nothing (usage=no_create).

    OpenCCU-Loom deliberately exposes these local presses as event sources so
    automations can react to a wall-button press; aiohomematic suppresses them.
    A deliberate, more-capable surface — see notes/parity/by_design.md
    (BD-Visibility-ActuatorLocalButtonEvents). Any OTHER usage combination on a
    press parameter still surfaces."""
    return (
        _is_click_press(go.get("parameter"))
        and go.get("usage") == "event"
        and py.get("usage") == "no_create"
    )


def is_redundant_forced_usage(go: dict, py: dict, drift: dict) -> bool:
    """True when the only meaningful divergence is a Go `forced_usage` that
    merely restates the `usage` both stacks already agree on, with the
    aiohomematic side leaving `forced_usage` unset.

    OpenCCU-Loom marks a verdict with SetForcedUsage even where aiohomematic
    arrives at the same `usage` without an explicit force (the click-event
    PRESS_* button promotion is the bulk case: both stacks surface
    `usage=data_point`; only the Go book-keeping field differs). Because the
    realised `usage` is compared independently, any force that actually CHANGES
    the outcome shows up as a `usage` drift and is not swallowed here."""
    if "forced_usage" not in drift or "usage" in drift:
        return False
    go_fu, py_fu = drift["forced_usage"]
    return py_fu is None and go_fu is not None and go_fu == go.get("usage")


def diff_dp(go_dp: dict, py_dp: dict) -> dict:
    """Per-field comparison; returns a dict {field: (go, py)} for any
    diverging field. Empty dict means no drift."""
    go = canon_state_usage(canon_hidden_usage(normalise_dp(go_dp)))
    py = canon_state_usage(canon_hidden_usage(normalise_dp(py_dp)))
    drift: dict[str, tuple[Any, Any]] = {}
    for field in set(go) | set(py):
        if field in TOLERATED_DP_FIELDS:
            continue
        if go.get(field) != py.get(field):
            drift[field] = (go.get(field), py.get(field))
    if is_schedule_lock_suppression(go, py):
        # The suppression deliberately flips usage + enabled_default; both are
        # expected. A drift on any OTHER field of these params still reports.
        drift.pop("usage", None)
        drift.pop("enabled_default", None)
    if is_local_button_event_suppression(go, py):
        # OpenCCU-Loom surfaces a keypress event on an actuator's local
        # push-button channel where aiohomematic creates nothing. The
        # usage/forced_usage/enabled_default flip is the entire (deliberate)
        # signature; a drift on any OTHER field of these params still reports.
        drift.pop("usage", None)
        drift.pop("forced_usage", None)
        drift.pop("enabled_default", None)
    if is_reference_only_visibility(go, py):
        # OpenCCU-Loom keeps a variant-model parameter hidden where the
        # reference surfaces it. Same deliberate usage/forced_usage/
        # enabled_default signature; any other field drift still reports.
        drift.pop("usage", None)
        drift.pop("forced_usage", None)
        drift.pop("enabled_default", None)
    if is_redundant_forced_usage(go, py, drift):
        # OpenCCU-Loom records a visibility verdict via SetForcedUsage even
        # when the resulting `usage` matches what aiohomematic reaches without
        # an explicit force (e.g. click-event PRESS_* promoted to a button on
        # both stacks). When BOTH sides agree on `usage` and the Go
        # `forced_usage` merely restates that same usage while aiohomematic
        # leaves it unset, the field carries no behavioural information — the
        # observable surface is identical. A force that CHANGES the outcome
        # still surfaces as a `usage` drift, which is NOT tolerated. See
        # notes/parity/by_design.md (BD-Visibility-RedundantForcedUsage).
        drift.pop("forced_usage", None)
    return drift


def _is_unnamed_channel(name: Any, number: Any) -> bool:
    """Report whether a channel carries no custom name.

    aiohomematic represents an unnamed channel's name as the channel number
    stringified (channel N -> "N", model/support.py get_channel_name fallback);
    openccu-loom leaves it null. Against the name-less pydevccu/godevccu
    simulators every channel is unnamed, so the two stacks emit the same
    "no custom name" state in different shapes. A real assigned name (e.g.
    "Living Room") is neither null nor the channel number and still differs.
    """
    if name in (None, ""):
        return True
    return number is not None and str(name) == str(number)


def diff_channel(go_ch: dict, py_ch: dict) -> dict:
    drifts: dict[str, Any] = {}
    ch_number = py_ch.get("number", go_ch.get("number"))

    # Channel-level scalar fields.
    #
    # `paramsets` is intentionally excluded: openccu-loom does not
    # retain the verbatim wire PARAMSETS list on the Channel struct
    # (only ParamsetIn for routing). The wire-layer datasource_diff
    # confirms identity at the paramset_descriptions level, so a
    # snapshot-side drift here is a representation gap, not a model
    # gap.
    for field in ("address", "number", "type", "name", "rooms", "functions",
                  "group_no", "operation_mode"):
        go_v = go_ch.get(field)
        py_v = py_ch.get(field)
        # Treat empty list and missing as equal.
        if go_v in (None, "", [], 0) and py_v in (None, "", [], 0):
            continue
        # An unnamed channel is null (openccu-loom) vs the channel number
        # stringified (aiohomematic); both encode "no custom name".
        if (
            field == "name"
            and _is_unnamed_channel(go_v, ch_number)
            and _is_unnamed_channel(py_v, ch_number)
        ):
            continue
        if go_v != py_v:
            drifts.setdefault("channel_fields", {})[field] = (go_v, py_v)

    # DP collections.
    for collection in ("generic_data_points", "custom_data_points", "calculated_data_points"):
        go_dps = index_dps(go_ch, collection)
        py_dps = index_dps(py_ch, collection)
        only_go = sorted(go_dps.keys() - py_dps.keys())
        only_py = sorted(py_dps.keys() - go_dps.keys())
        per_dp_drift: dict[str, dict] = {}
        for key in sorted(go_dps.keys() & py_dps.keys()):
            d = diff_dp(go_dps[key], py_dps[key])
            if d:
                per_dp_drift[".".join(map(str, key))] = d
        if only_go or only_py or per_dp_drift:
            drifts[collection] = {
                "only_in_openccu-loom": [".".join(map(str, k)) for k in only_go],
                "only_in_aiohomematic": [".".join(map(str, k)) for k in only_py],
                "drifted": per_dp_drift,
            }
    return drifts


# aiohomematic reports a device that carries no wire FIRMWARE as the
# canonical default "0.0" (aiohomematic model/device.py:1790 —
# `self._device_description.get("FIRMWARE") or "0.0"`); the openccu-loom
# snapshot leaves the same "no reported firmware" state as the empty string.
# Both encode the identical wire condition, so the two sentinels are
# canonicalised to compare equal. A genuine firmware mismatch (e.g. "1.2.0"
# vs "1.4.0") still differs after canonicalisation and surfaces as
# device_fields drift.
_NO_FIRMWARE = frozenset({None, "", "0.0"})


def _canon_firmware(value: Any) -> Any:
    return "" if value in _NO_FIRMWARE else value


# interface_id / product_group record which XML-RPC interface served a device,
# which is a property of the *simulator's* fixture topology, not the
# openccu-loom model port: godevccu and pydevccu organise the same classic
# BidCos-RF devices (e.g. `263 x`, `ZEL STG RM DWT 10`, `ASH550`) under
# different interface endpoints, so the two stacks report HmIP-RF vs BidCos-RF
# for the same address. On a real CCU a device is received on one interface and
# both stacks read the same value, so these fields agree in production; against
# the two simulators they diverge with no model-fidelity meaning. Tolerating
# them keeps device_fields sensitive to a genuine model / firmware / version
# regression. See notes/parity/by_design.md.
_TOLERATED_DEVICE_FIELDS = frozenset({"interface_id", "product_group"})


def diff_device(go_dev: dict, py_dev: dict) -> dict:
    drifts: dict[str, Any] = {}
    for field in ("address", "model", "interface_id", "firmware",
                  "version", "product_group"):
        if field in _TOLERATED_DEVICE_FIELDS:
            continue
        go_v = go_dev.get(field)
        py_v = py_dev.get(field)
        if field == "firmware":
            if _canon_firmware(go_v) == _canon_firmware(py_v):
                continue
        elif go_v == py_v:
            continue
        # Report the raw values; the comparison above already canonicalised
        # the no-firmware sentinel so only a genuine divergence lands here.
        drifts.setdefault("device_fields", {})[field] = (go_v, py_v)

    go_chs = index_channels(go_dev)
    py_chs = index_channels(py_dev)
    only_go = sorted(go_chs.keys() - py_chs.keys())
    only_py = sorted(py_chs.keys() - go_chs.keys())
    if only_go:
        drifts["only_in_openccu-loom_channels"] = only_go
    if only_py:
        drifts["only_in_aiohomematic_channels"] = only_py

    per_channel_drift: dict[int, dict] = {}
    for n in sorted(go_chs.keys() & py_chs.keys()):
        d = diff_channel(go_chs[n], py_chs[n])
        if d:
            per_channel_drift[n] = d
    if per_channel_drift:
        drifts["channels"] = per_channel_drift
    return drifts


def main(argv: list[str]) -> int:
    go_path = Path(argv[1]) if len(argv) > 1 else DEFAULT_GO
    py_path = Path(argv[2]) if len(argv) > 2 else DEFAULT_PY

    if not go_path.exists():
        print(f"missing: {go_path}", file=sys.stderr)
        return 2
    if not py_path.exists():
        print(f"missing: {py_path}\n"
              f"hint: run `python3 script/aiohomematic_snapshot.py` first",
              file=sys.stderr)
        return 2

    go = load(go_path)
    py = load(py_path)

    go_devs = index_devices(go)
    py_devs = index_devices(py)
    only_go = sorted(go_devs.keys() - py_devs.keys())
    only_py = sorted(py_devs.keys() - go_devs.keys())

    # Per project policy (user clarification 2026-04-29):
    # openccu-loom intentionally exposes every parameter as a DP — every
    # MASTER parameter shows up as a Generic DP whether or not
    # aiohomematic surfaces it. So `only_in_openccu-loom` is the
    # designed extra, not drift. The audit-relevant signals are:
    #   1. drifted attributes on the DPs both sides agree exist, and
    #   2. DPs aiohomematic emits that openccu-loom is missing.
    summary = {
        "openccu-loom_devices": len(go_devs),
        "aiohomematic_devices": len(py_devs),
        "only_in_openccu-loom_devices": only_go,
        "only_in_aiohomematic_devices": only_py,
        "drifted_devices": [],
    }
    detail: dict[str, dict] = {}
    drift_counts: dict[str, int] = defaultdict(int)
    extra_counts: dict[str, int] = defaultdict(int)

    for addr in sorted(go_devs.keys() & py_devs.keys()):
        d = diff_device(go_devs[addr], py_devs[addr])
        if not d:
            continue
        # Drift = drifted shared DPs OR only_in_aiohomematic.
        # only_in_openccu-loom is reported under a separate "extras"
        # heading and does NOT contribute to the exit code.
        has_real_drift = False

        # Device-identity drift (model / firmware / version / product_group /
        # interface_id) and channels aiohomematic exposes that openccu-loom
        # lacks are real model regressions. diff_device already computes them;
        # fold their counts into drift_counts so total_drift reflects them —
        # otherwise a whole missing channel or a device model/firmware
        # mismatch scores zero and passes the gate green.
        if "device_fields" in d:
            drift_counts["device_fields"] += len(d["device_fields"])
            has_real_drift = True
        if "only_in_aiohomematic_channels" in d:
            drift_counts["only_in_aiohomematic_channels"] += len(
                d["only_in_aiohomematic_channels"]
            )
            has_real_drift = True

        for ch_num, ch_drift in (d.get("channels") or {}).items():
            if "channel_fields" in ch_drift:
                drift_counts["channel_fields"] += len(ch_drift["channel_fields"])
                has_real_drift = True
            for collection, drifts in ch_drift.items():
                if collection == "channel_fields":
                    continue
                drifted = len(drifts.get("drifted", {}))
                only_py_n = len(drifts.get("only_in_aiohomematic", []))
                only_go_n = len(drifts.get("only_in_openccu-loom", []))
                if drifted:
                    drift_counts[f"{collection}.drifted"] += drifted
                    has_real_drift = True
                if only_py_n:
                    drift_counts[f"{collection}.only_py"] += only_py_n
                    has_real_drift = True
                if only_go_n:
                    extra_counts[f"{collection}.only_go"] += only_go_n
        if has_real_drift:
            summary["drifted_devices"].append(addr)
            detail[addr] = d

    summary["drift_counts"] = dict(drift_counts)
    summary["openccu-loom_extra_dps"] = dict(extra_counts)

    print(json.dumps({"summary": summary, "detail": detail}, indent=2))

    # Drift score excludes the extras — only fields that diverge AND
    # DPs aiohomematic has but openccu-loom lacks count.
    total_drift = (
        len(only_py)  # devices missing on Go side
        + sum(drift_counts.values())
    )
    return 0 if total_drift == 0 else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
