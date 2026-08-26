#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 SukramJ.
#
# aiohomematic_snapshot.py — produces the aiohomematic side of the
# cross-stack model-snapshot diff.
#
# Starts a pydevccu.Server with the same 4 devices used by the Go
# snapshot test, connects a CentralUnit via XML-RPC, waits for all
# devices to be created, then dumps the full Device→Channel→DataPoint
# tree to JSON.
#
# Output: tests/integration/testdata/model_snapshot_aiohomematic.json
#
# Usage:
#   python3 script/aiohomematic_snapshot.py
#
# The script must be run from the repository root (openccu-loom/).
# It uses whichever Python3 is on PATH; the aiohomematic venv
# (aiohomematic/.venv) is added to sys.path automatically if it
# exists – otherwise the script relies on aiohomematic and pydevccu
# being installed into the current Python environment.

from __future__ import annotations

import asyncio
import contextlib
import importlib.metadata
import importlib.util
import json
import logging
import os
import subprocess
import sys
from datetime import UTC, datetime
from pathlib import Path


def _ensure_venv() -> None:
    """
    The script needs `aiohomematic`, `pydevccu` and crucially the
    `openccu_data` Python package on sys.path. The latter ships only
    in the aiohomematic venv (it is not pip-installable system-wide
    on PEP-668-managed hosts). When invoked with the system Python
    this guard re-execs the script in an aiohomematic venv if one is
    found. Without `openccu_data` the snapshot would silently emit
    empty translations for every DP and mask real drift.

    Discovery order:
      1. AIOHOMEMATIC_VENV_PYTHON env var (explicit override).
      2. Sibling-repo conventions, resolved relative to this script:
         ../aiohomematic/venv/bin/python3
         ../aiohomematic/.venv/bin/python3
         ../../aiohomematic/venv/bin/python3
         ../../aiohomematic/.venv/bin/python3
    """
    try:
        import openccu_data  # noqa: F401
        return
    except ImportError:
        pass
    candidates: list[str] = []
    if env := os.environ.get("AIOHOMEMATIC_VENV_PYTHON", "").strip():
        candidates.append(env)
    here = Path(__file__).resolve().parent
    # Build candidate paths without resolving symlinks — venv pythons
    # are typically symlinks into a system framework Python; resolving
    # would point cand and sys.executable at the same physical file
    # and trip the loop guard below into a re-exec ping-pong.
    for offset in ("../..", "../../.."):
        for venv_name in ("venv", ".venv"):
            cand = os.path.normpath(str(here / offset / "aiohomematic" / venv_name / "bin" / "python3"))
            candidates.append(cand)
    # Re-exec only when (a) the candidate exists, (b) we are not
    # already running it (guard against infinite loops), and (c) it
    # actually contains the openccu_data package.
    already_in_venv_marker = "_AIOHOMEMATIC_VENV_REEXEC_DONE"
    if os.environ.get(already_in_venv_marker) == "1":
        _require_openccu_data(f"the re-exec target {sys.executable}")
        return
    for cand in candidates:
        if not os.path.exists(cand):
            continue
        # Condition (c). A stale venv that has aiohomematic but no
        # openccu_data re-execs into an interpreter that emits a snapshot
        # with every label missing, and every label lookup downstream is
        # wrapped in `except Exception: pass`, so the run stays silent.
        probe = subprocess.run(  # noqa: S603 - fixed argv, no shell
            [cand, "-c", "import openccu_data"],
            capture_output=True,
            check=False,
        )
        if probe.returncode != 0:
            print(f"[snapshot] skipping {cand}: no openccu_data package", file=sys.stderr)
            continue
        os.environ[already_in_venv_marker] = "1"
        print(f"[snapshot] re-execing in aiohomematic venv: {cand}", file=sys.stderr)
        os.execv(cand, [cand, *sys.argv])
    _require_openccu_data("the active interpreter and no candidate venv")


def _require_openccu_data(where: str) -> None:
    """
    Abort when openccu_data is unavailable.

    Without it aiohomematic's translation loader returns an empty extract and
    logs the reason at DEBUG, below this script's WARNING level: the snapshot
    is written, exits 0, and omits `parameter_label` / `type_label` /
    `model_label` for every entry. The diff then reports thousands of phantom
    label drifts that bury the real ones — the exact failure this guard exists
    to prevent, so it is a hard error rather than a warning.
    """
    try:
        import openccu_data  # noqa: F401
    except ImportError:
        print(
            f"ERROR: openccu_data is not importable from {where}.\n"
            "It ships with the aiohomematic venv; point AIOHOMEMATIC_VENV_PYTHON\n"
            "at that interpreter, or install the reference stack from\n"
            "script/requirements/reference-stack.txt. Refusing to emit a\n"
            "label-less snapshot.",
            file=sys.stderr,
        )
        sys.exit(1)


_ensure_venv()

# ──────────────────────────────────────────────────────────────────────────────
# Path bootstrap: make aiohomematic and pydevccu importable when running
# directly from the openccu-loom repo without activating a venv.
# ──────────────────────────────────────────────────────────────────────────────

_GITHUB_ROOT = Path(__file__).resolve().parents[2]


def _bootstrap_sibling_checkouts() -> None:
    """
    Make aiohomematic and pydevccu importable when the script runs straight
    from the openccu-loom repo with no environment prepared for it.

    This is a *fallback*, and the ordering matters. Whatever the active
    interpreter can already import wins: appending (never prepending) keeps
    a deliberately provisioned environment — CI's pinned refstack venv, or
    one named via AIOHOMEMATIC_VENV_PYTHON — in charge of which
    aiohomematic version the snapshot captures. Prepending sibling
    checkouts made every local run silently capture the working copy next
    to the repo instead, so a CI failure pinned to an older release could
    not be reproduced locally at all.
    """
    for pkg in ("aiohomematic", "pydevccu"):
        if importlib.util.find_spec(pkg) is not None:
            continue
        pkg_path = _GITHUB_ROOT / pkg
        if pkg_path.is_dir() and str(pkg_path) not in sys.path:
            sys.path.append(str(pkg_path))


_bootstrap_sibling_checkouts()

# ──────────────────────────────────────────────────────────────────────────────
# Imports
# ──────────────────────────────────────────────────────────────────────────────

try:
    import aiohomematic
    from aiohomematic.central import CentralConfig
    from aiohomematic.central.events import DeviceLifecycleEvent, DeviceLifecycleEventType
    from aiohomematic.client import InterfaceConfig
    from aiohomematic.const import Interface, ParamsetKey
except ImportError as exc:
    print(f"ERROR: cannot import aiohomematic: {exc}", file=sys.stderr)
    print(
        "Install it via: pip install aiohomematic",
        file=sys.stderr,
    )
    sys.exit(1)

try:
    import pydevccu
    from pydevccu import Server as PyDevCCUServer
except ImportError as exc:
    print(f"ERROR: cannot import pydevccu: {exc}", file=sys.stderr)
    print(
        "Install it via: pip install pydevccu",
        file=sys.stderr,
    )
    sys.exit(1)

# ──────────────────────────────────────────────────────────────────────────────
# Configuration
# ──────────────────────────────────────────────────────────────────────────────

logging.basicConfig(
    level=logging.WARNING,
    format="%(asctime)s %(levelname)s %(name)s: %(message)s",
)
_LOGGER = logging.getLogger("aiohomematic_snapshot")

_SCRIPT_DIR = Path(__file__).resolve().parent
_REPO_ROOT = _SCRIPT_DIR.parent
_OUTPUT_PATH = _REPO_ROOT / "tests" / "integration" / "testdata" / "model_snapshot_aiohomematic.json"

def _resolve_devices() -> list[str] | None:
    """
    Pick the device fleet pydevccu will load.

    Default is `None` which makes pydevccu instantiate every embedded
    model (~399 devices). Set the OPENCCU_LOOM_SNAPSHOT_DEVICES env var
    to a comma-separated list (e.g. "HmIP-BWTH,HmIP-BSM") to scope the
    snapshot to a smoke-sized subset.
    """
    override = os.environ.get("OPENCCU_LOOM_SNAPSHOT_DEVICES", "").strip()
    if not override:
        return None
    return [p.strip() for p in override.split(",") if p.strip()]


_DEVICES = _resolve_devices()

# Locale used to resolve every label (model_label / type_label /
# parameter_label). Both stacks must use the same locale so the diff
# is meaningful. Override via OPENCCU_LOOM_SNAPSHOT_LOCALE.
_LOCALE = os.environ.get("OPENCCU_LOOM_SNAPSHOT_LOCALE", "en").strip() or "en"

_CENTRAL_NAME = "snapshot-ccu"
_CCU_HOST = "127.0.0.1"
_CCU_PORT = 12034  # picked to avoid conflicts with other tests
_CALLBACK_PORT = 12120
_INIT_TIMEOUT_S = 600

# ──────────────────────────────────────────────────────────────────────────────
# JSON serialisation helpers
# ──────────────────────────────────────────────────────────────────────────────


def _omit_falsy_optional(value: object) -> bool:
    """Return True if this optional value should be omitted (mirrors Go omitempty)."""
    if value is None:
        return True
    if value == "" or value == [] or value == ():
        return True
    return False


def _coerce_special_value(value):
    """
    Drop trivial float→int conversions so SPECIAL entries match the
    Go side (which emits ints when the underlying CCU value is an
    integer multiple). 16383000.0 → 16383000.
    """
    if isinstance(value, float) and value.is_integer():
        return int(value)
    return value


def _coerce_typed(value, parameter_type: str):
    """
    Coerce raw descriptor min/max/default into the typed Go-side
    representation. The wire delivers strings like "5.0" / "0" /
    "true" regardless of TYPE; we cast to the same Python form the
    Go-side decodeTyped() helper produces so the snapshot diff is
    apples-to-apples.
    """
    if value is None:
        return None
    pt = (parameter_type or "").upper()
    if pt in ("BOOL", "ACTION"):
        if isinstance(value, bool):
            return value
        if isinstance(value, (int, float)):
            return value != 0
        if isinstance(value, str):
            return value.strip().lower() in ("true", "1")
        return False
    if pt in ("INTEGER", "ENUM"):
        if isinstance(value, bool):
            return 1 if value else 0
        if isinstance(value, int):
            return value
        if isinstance(value, float):
            return int(value)
        if isinstance(value, str):
            try:
                return int(float(value))
            except (TypeError, ValueError):
                return 0
        return 0
    if pt == "FLOAT":
        if isinstance(value, bool):
            return 1.0 if value else 0.0
        if isinstance(value, (int, float)):
            return float(value)
        if isinstance(value, str):
            try:
                return float(value)
            except (TypeError, ValueError):
                return 0.0
        return 0.0
    return value


def _serialize_generic_dp(dp, channel_type: str = "") -> dict:  # type: ignore[type-arg]
    """Serialise a GenericDataPoint to the schema dict."""
    out: dict = {}

    # Mandatory fields
    out["paramset_key"] = str(dp.paramset_key)
    out["parameter"] = dp.parameter
    # parameter_label resolved per (channel_type, parameter, locale)
    try:
        from aiohomematic.ccu_translations import get_parameter_translation
        plabel = get_parameter_translation(parameter=dp.parameter, channel_type=channel_type or None, locale=_LOCALE)
        if plabel:
            out["parameter_label"] = plabel
    except Exception:  # noqa: BLE001
        pass
    out["type"] = str(dp.hmtype) if hasattr(dp, "hmtype") else str(dp._type)  # noqa: SLF001
    out["operations"] = dp._operations  # noqa: SLF001
    # Prefer the raw FLAGS from paramset description; fall back to reconstructing from booleans
    raw_flags = _get_parameter_data_field(dp, "FLAGS")
    out["flags"] = int(raw_flags) if raw_flags is not None else _resolve_flags(dp)
    out["multiplier"] = dp.multiplier

    # Optional numeric/typed fields — omit when None.
    # _min / _max / _default are sometimes strings in aiohomematic
    # (the wire encodes "5.0" / "20" rather than the typed value); the
    # Go side coerces them via the parameter type so the snapshot
    # diff sees identical typed values. Mirror that here.
    #
    # `_default` is read from the raw parameter_data dict, not from
    # `dp._default`: aiohomematic computes `dp._default` as
    # `_convert_value(DEFAULT) or self._min` which collapses a typed
    # 0 onto _min (a Python truthiness artefact, not a wire fact). The
    # snapshot needs the wire-level DEFAULT for parity with openccu-loom
    # which does not have that fallback.
    out_type = out["type"]
    min_val = _coerce_typed(dp._min, out_type)  # noqa: SLF001
    if min_val is not None:
        out["min"] = min_val
    max_val = _coerce_typed(dp._max, out_type)  # noqa: SLF001
    if max_val is not None:
        out["max"] = max_val
    default_raw = _get_parameter_data_field(dp, "DEFAULT")
    default_val = _coerce_typed(default_raw, out_type)
    if default_val is not None:
        out["default"] = default_val

    # unit
    unit = dp._unit  # noqa: SLF001
    if not _omit_falsy_optional(unit):
        out["unit"] = unit

    # special
    special = dp._special  # noqa: SLF001
    if special is not None:
        # Normalise to list[{id, value}]. The wire ships either a flat
        # dict {ID: value, ...} or a list of {ID, VALUE}-keyed dicts;
        # both stacks emit the canonical lowercase {id, value} form so
        # the diff is structural-only.
        normalised: list[dict] = []
        if isinstance(special, dict):
            normalised = [{"id": k, "value": _coerce_special_value(v)} for k, v in special.items()]
        else:
            for entry in special:
                if not isinstance(entry, dict):
                    continue
                eid = entry.get("ID") or entry.get("id")
                if eid is None:
                    continue
                eval_ = entry.get("VALUE") if "VALUE" in entry else entry.get("value")
                normalised.append({"id": eid, "value": _coerce_special_value(eval_)})
        out["special"] = sorted(normalised, key=lambda x: x["id"])

    # value_list
    values = dp._values  # noqa: SLF001
    if values:
        out["value_list"] = list(values)

    # control — from parameter_data if available, else via _raw_unit workaround
    control = _get_parameter_data_field(dp, "CONTROL")
    if not _omit_falsy_optional(control):
        out["control"] = control

    # id
    dp_id = _get_parameter_data_field(dp, "ID")
    if not _omit_falsy_optional(dp_id):
        out["id"] = dp_id

    # tab_order
    tab_order = _get_parameter_data_field(dp, "TAB_ORDER")
    if tab_order is not None:
        out["tab_order"] = tab_order

    # Category, usage, booleans
    out["category"] = str(dp.category)
    out["usage"] = str(dp.usage)
    out["is_writable"] = dp.is_writable
    out["is_readable"] = dp.is_readable
    out["is_visible"] = dp._visible  # noqa: SLF001
    out["enabled_default"] = dp.enabled_default
    out["is_forced_sensor"] = dp.is_forced_sensor
    out["is_un_ignored"] = dp.is_un_ignored

    # forced_usage — omit when empty OR when it equals `no_create`
    # (mirrors the Go snapshot dumper at
    # `tests/integration/model_snapshot_test.go:481-489`). Both stacks
    # set `_forced_usage = no_create` as the suppression-pipeline
    # implementation lever (HIDDEN_PARAMETERS / DataPointUsageNoCreate);
    # the canonical wire attribute is the resolved `usage`, so emitting
    # the duplicate `forced_usage = no_create` only adds false-positive
    # diff entries (19 MASTER.GLOBAL_BUTTON_LOCK on BWTH per W6-A).
    # Other forced-usage values (e.g. `ce_visible`, `data_point`) ARE
    # emitted on both sides — they carry semantic information beyond
    # the resolved `usage`.
    forced_usage = dp._forced_usage  # noqa: SLF001
    if forced_usage is not None and str(forced_usage) != "no_create":
        out["forced_usage"] = str(forced_usage)

    return out


def _resolve_flags(dp) -> int:  # type: ignore[type-arg]
    """Derive flags integer from the visible bit (fallback when _flags not exposed)."""
    # Reconstruct from _visible: Flag.VISIBLE = 1
    # We also check _service: Flag.SERVICE = 8
    flags = 0
    if getattr(dp, "_visible", True):
        flags |= 1
    if getattr(dp, "_service", False):
        flags |= 8
    return flags


def _get_parameter_data_field(dp, key: str):  # type: ignore[type-arg]
    """
    Try to extract a raw parameter-data field (CONTROL, ID, TAB_ORDER) from
    the data point.  aiohomematic stores these on the ParameterDataModel which
    may be accessible via the paramset-description provider.
    """
    # Try via the paramset_description_provider (stored on the device)
    try:
        provider = dp._device.paramset_description_provider  # noqa: SLF001
        raw = provider.get_parameter_data(
            interface_id=dp._device.interface_id,  # noqa: SLF001
            channel_address=dp._channel.address,  # noqa: SLF001
            paramset_key=dp.paramset_key,
            parameter=dp.parameter,
        )
        return raw.get(key)
    except Exception:  # noqa: BLE001
        pass
    return None


def _serialize_custom_dp(cdp) -> dict:  # type: ignore[type-arg]
    """Serialise a CustomDataPoint."""
    # Fully-qualified class name
    profile = f"{type(cdp).__module__}.{type(cdp).__qualname__}"
    category = str(cdp.category)

    # wrapped_dps: all generic DPs referenced by this custom DP
    # They live in cdp._data_points (dict[Field, GenericDataPointProtocol])
    wrapped = set()
    for dp in cdp._data_points.values():  # noqa: SLF001
        pk = str(dp.paramset_key)
        param = dp.parameter
        wrapped.add(f"{pk}.{param}")

    return {
        "profile": profile,
        "category": category,
        "wrapped_dps": sorted(wrapped),
    }


def _serialize_calculated_dp(cdp) -> dict:  # type: ignore[type-arg]
    """Serialise a CalculatedDataPoint."""
    return {
        "parameter": str(cdp.parameter),
        "category": str(cdp.category),
    }


def _resolve_operation_mode(channel) -> str:  # type: ignore[type-arg]
    """Mirror openccu-loom's `Channel.OperationMode` read-chain.

    aiohomematic's `Channel.operation_mode` only checks the VALUES
    paramset; openccu-loom also falls back to the MASTER paramset.
    Match the Go behaviour for snapshot parity (G-44).
    """
    # Stage 1 — VALUES (aiohomematic native path)
    if (raw := channel.operation_mode):
        return str(raw)
    # Stage 2 — MASTER paramset fallback
    try:
        from aiohomematic.const import Parameter, ParamsetKey
    except Exception:  # noqa: BLE001
        return ""
    try:
        master_params = channel.paramset_descriptions.get(ParamsetKey.MASTER, {})
    except Exception:  # noqa: BLE001
        master_params = {}
    if Parameter.CHANNEL_OPERATION_MODE.value not in master_params:
        return ""
    # Read the loaded value (W8 step 3b seeded MASTER values).
    for dp in channel.get_readable_data_points(paramset_key=ParamsetKey.MASTER):
        if str(dp.parameter) == Parameter.CHANNEL_OPERATION_MODE.value:
            value = dp.value
            if value is not None:
                return str(value)
            break
    # Stage 3 — Descriptor default (openccu-loom emits the wire-DEFAULT
    # value when nothing has been observed). Mirror the same fallback
    # to avoid spurious drift on unseen channels.
    desc = master_params.get(Parameter.CHANNEL_OPERATION_MODE.value, {})
    default = desc.get("DEFAULT")
    return str(default) if default is not None else ""


def _serialize_channel(channel) -> dict:  # type: ignore[type-arg]
    """Serialise a Channel."""
    # Generic data points, sorted by (paramset_key, parameter)
    generic_dps = sorted(
        channel.generic_data_points,
        key=lambda dp: (str(dp.paramset_key), dp.parameter),
    )

    # Custom data point (at most one per channel in aiohomematic)
    custom_dps = []
    if channel.custom_data_point is not None:
        custom_dps.append(_serialize_custom_dp(channel.custom_data_point))

    # Calculated data points, sorted by parameter name
    calc_dps = sorted(
        channel.calculated_data_points,
        key=lambda dp: str(dp.parameter),
    )

    # G-44 helper closure (defined at first invocation):
    # openccu-loom's Channel.OperationMode falls through MASTER when no
    # VALUES-side CHANNEL_OPERATION_MODE DP exists. aiohomematic's
    # `channel.operation_mode` only inspects the VALUES paramset
    # (`device.py:1156-1162` `get_generic_data_point(...)`), so for the
    # 26 affected channels (HmIP-SMO230 etc.) where the parameter only
    # lives in MASTER, the Python snapshot was emitting an empty
    # string. Match Go's read-chain by consulting MASTER as a
    # fallback. Closes parity_audit gap **G-44**.
    # ----------------------------------------------------------------

    # Rooms and functions: pydevccu returns empty sets normally
    rooms = sorted(channel.rooms) if channel.rooms else []
    functions = []
    if channel.function:
        functions = [channel.function]

    # paramset_keys: list of uppercase strings
    paramsets = [str(pk) for pk in channel.paramset_keys]

    type_label = ""
    try:
        from aiohomematic.ccu_translations import get_channel_type_translation
        type_label = get_channel_type_translation(channel_type=channel.type_name, locale=_LOCALE) or ""
    except Exception:  # noqa: BLE001
        pass

    out = {
        "address": channel.address,
        "number": channel.no if channel.no is not None else 0,
        "type": channel.type_name,
        "name": channel.name or "",
        "rooms": rooms,
        "functions": functions,
        "group_no": channel.group_no if channel.group_no is not None else 0,
        "paramsets": paramsets,
        "operation_mode": _resolve_operation_mode(channel),
        "generic_data_points": [_serialize_generic_dp(dp, channel_type=channel.type_name) for dp in generic_dps],
        "custom_data_points": custom_dps,
        "calculated_data_points": [_serialize_calculated_dp(dp) for dp in calc_dps],
    }
    if type_label:
        out["type_label"] = type_label
    return out


def _serialize_device(device) -> dict:  # type: ignore[type-arg]
    """Serialise a Device."""
    # channels sorted by channel number
    channels = sorted(
        device.channels.values(),
        key=lambda ch: ch.no if ch.no is not None else -1,
    )

    # version from device_description
    version = device._device_description.get("VERSION") or 0  # noqa: SLF001

    # rooms: device-level rooms (set)
    rooms = sorted(device.rooms) if device.rooms else []

    model_label = ""
    name = ""
    try:
        from aiohomematic.ccu_translations import get_device_model_translation
        sub_model = device._device_description.get("SUBTYPE")  # noqa: SLF001
        model_label = get_device_model_translation(model=device.model, sub_model=sub_model, locale=_LOCALE) or ""
    except Exception:  # noqa: BLE001
        pass
    try:
        name = device.name or ""
    except Exception:  # noqa: BLE001
        pass

    out = {
        "address": device.address,
        "model": device.model,
        "interface_id": str(device.product_group),
        "firmware": device.firmware or "",
        "version": version,
        "product_group": str(device.product_group),
        "rooms": rooms,
        "channels": [_serialize_channel(ch) for ch in channels],
    }
    if model_label:
        out["model_label"] = model_label
    if name:
        out["name"] = name
    return out


# ──────────────────────────────────────────────────────────────────────────────
# Main async logic
# ──────────────────────────────────────────────────────────────────────────────


async def run() -> None:
    """Start pydevccu, connect central, dump snapshot."""

    # ── 1. Start pydevccu ────────────────────────────────────────────────────
    _LOGGER.info("Starting pydevccu on %s:%d with devices: %s", _CCU_HOST, _CCU_PORT, _DEVICES)
    ccu = PyDevCCUServer(addr=(_CCU_HOST, _CCU_PORT), devices=_DEVICES)
    try:
        ccu.start()
    except Exception as exc:
        print(f"ERROR: pydevccu failed to start: {exc}", file=sys.stderr)
        sys.exit(1)

    _LOGGER.info("pydevccu started")

    try:
        # ── 2. Connect CentralUnit ───────────────────────────────────────────
        device_event = asyncio.Event()

        def _on_device_lifecycle(event: DeviceLifecycleEvent) -> None:
            if event.event_type == DeviceLifecycleEventType.CREATED:
                device_event.set()

        interface_configs = frozenset(
            {
                InterfaceConfig(
                    central_name=_CENTRAL_NAME,
                    interface=Interface.BIDCOS_RF,
                    port=_CCU_PORT,
                )
            }
        )

        try:
            central = await CentralConfig(
                name=_CENTRAL_NAME,
                host=_CCU_HOST,
                username="admin",
                password="admin",
                central_id="snapshot-1234",
                interface_configs=interface_configs,
                callback_port_xml_rpc=_CALLBACK_PORT,
                program_markers=(),
                sysvar_markers=(),
                start_direct=True,
            ).create_central()
        except Exception as exc:
            print(f"ERROR: could not create CentralUnit: {exc}", file=sys.stderr)
            sys.exit(1)

        central.event_bus.subscribe(
            event_type=DeviceLifecycleEvent,
            event_key=None,
            handler=_on_device_lifecycle,
        )

        _LOGGER.info("Starting central…")
        try:
            await central.start()
        except Exception as exc:
            print(f"ERROR: central.start() failed: {exc}", file=sys.stderr)
            await central.stop()
            sys.exit(1)

        # ── 3. Wait for devices ──────────────────────────────────────────────
        _LOGGER.info("Waiting up to %ds for devices to be created…", _INIT_TIMEOUT_S)
        with contextlib.suppress(TimeoutError):
            await asyncio.wait_for(device_event.wait(), timeout=_INIT_TIMEOUT_S)

        if not device_event.is_set():
            print("ERROR: timed out waiting for devices from pydevccu", file=sys.stderr)
            await central.stop()
            sys.exit(1)

        # Give aiohomematic a moment to finish wiring custom / calculated DPs
        await asyncio.sleep(2)

        # ── 3b. Seed MASTER paramset values (G-44) ───────────────────────────
        # openccu-loom calls getParamset(MASTER) eagerly during hydration, so
        # channel.operation_mode (CHANNEL_OPERATION_MODE) is always observed
        # after the pipeline. aiohomematic only loads MASTER values on
        # on_config_changed events, so without an explicit load the snapshot
        # captures operation_mode=None for all channels, producing 26 spurious
        # drifts. We reproduce openccu-loom's eager seeding here so both
        # snapshots reflect the same initial CCU state.
        from aiohomematic.const import CallSource
        devices_for_seed = central.device_registry.devices
        _LOGGER.info("Seeding MASTER paramset values for %d devices…", len(devices_for_seed))
        load_tasks = []
        for dev in devices_for_seed:
            for ch in dev.channels.values():
                for dp in ch.get_readable_data_points(paramset_key=ParamsetKey.MASTER):
                    load_tasks.append(
                        dp.load_data_point_value(
                            call_source=CallSource.MANUAL_OR_SCHEDULED,
                            direct_call=True,
                        )
                    )
        if load_tasks:
            await asyncio.gather(*load_tasks, return_exceptions=True)
            _LOGGER.info("MASTER seed complete (%d DPs loaded)", len(load_tasks))

        # ── 4. Build snapshot ────────────────────────────────────────────────
        devices = central.device_registry.devices
        _LOGGER.info("Loaded %d devices", len(devices))

        serialised_devices = sorted(
            [_serialize_device(d) for d in devices],
            key=lambda d: d["address"],
        )

        # Stack / version metadata
        aiohm_version = getattr(aiohomematic, "__version__", "unknown")
        try:
            aiohm_version = importlib.metadata.version("aiohomematic")
        except Exception:  # noqa: BLE001
            pass

        pydevccu_version = getattr(pydevccu, "__version__", "unknown")
        try:
            pydevccu_version = importlib.metadata.version("pydevccu")
        except Exception:  # noqa: BLE001
            pass

        snapshot = {
            "stack": "aiohomematic",
            "stack_version": aiohm_version,
            "devccu": "pydevccu",
            "devccu_version": pydevccu_version,
            "locale": _LOCALE,
            "captured_at": datetime.now(UTC).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "devices": serialised_devices,
        }

        # ── 5. Write output ──────────────────────────────────────────────────
        _OUTPUT_PATH.parent.mkdir(parents=True, exist_ok=True)
        with _OUTPUT_PATH.open("w", encoding="utf-8") as fh:
            json.dump(snapshot, fh, indent=2, ensure_ascii=False, default=str)
            fh.write("\n")

        # Stats
        total_channels = sum(len(d["channels"]) for d in serialised_devices)
        total_generic = sum(
            len(ch["generic_data_points"])
            for d in serialised_devices
            for ch in d["channels"]
        )
        total_custom = sum(
            len(ch["custom_data_points"])
            for d in serialised_devices
            for ch in d["channels"]
        )
        total_calc = sum(
            len(ch["calculated_data_points"])
            for d in serialised_devices
            for ch in d["channels"]
        )

        print(
            f"Snapshot written to {_OUTPUT_PATH}\n"
            f"  devices   : {len(serialised_devices)}\n"
            f"  channels  : {total_channels}\n"
            f"  generic DPs: {total_generic}\n"
            f"  custom DPs : {total_custom}\n"
            f"  calc DPs   : {total_calc}\n"
            f"  file size  : {_OUTPUT_PATH.stat().st_size:,} bytes"
        )

        # ── 6. Stop central ──────────────────────────────────────────────────
        await central.stop()

    finally:
        # ── 7. Stop pydevccu ─────────────────────────────────────────────────
        _LOGGER.info("Stopping pydevccu…")
        try:
            loop = asyncio.get_event_loop()
            if loop.is_running():
                asyncio.create_task(ccu.stop())  # noqa: RUF006
            else:
                loop.run_until_complete(ccu.stop())
        except Exception:  # noqa: BLE001
            with contextlib.suppress(Exception):
                ccu.stop()  # type: ignore[func-returns-value]


if __name__ == "__main__":
    asyncio.run(run())
