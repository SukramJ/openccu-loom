#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 openccu-loom authors.
#
# homematicip_local_snapshot.py — produces the canonical
# HA-Entity-attribute snapshot from `homematicip_local` (the Home
# Assistant custom integration). This is the **primary** Soll-Quelle
# for the openccu-loom-MQTT discovery diff: openccu-loom must produce
# entities that look identical in HA to what `homematicip_local` would
# create directly.
#
# Strategy: import `homematicip_local`'s
# `custom_components.homematicip_local.entity_helpers.descriptions`,
# register every rule into REGISTRY, boot aiohomematic against
# pydevccu, walk every CallbackDataPoint, look up its
# `HmEntityDescription` via `REGISTRY.find(...)`, combine the
# description's static fields (device_class, state_class,
# entity_category, icon, enabled_by_default,
# native_unit_of_measurement, suggested_display_precision, options,
# …) with the DP's dynamic fields (min, max, step, modes,
# preset_modes, current_humidity, …) and emit one JSON row per HA
# entity.
#
# Output:
#   tests/integration/testdata/discovery_snapshot_homematicip_local.json
#
# Usage:
#   python3 script/homematicip_local_snapshot.py
#
# Auto-re-execs in homematicip_local's venv (the only venv that ships
# `homeassistant` + `aiohomematic` + `pydevccu` + `openccu_data` +
# `paho.mqtt` together).

from __future__ import annotations

import asyncio
import contextlib
import importlib.metadata
import json
import logging
import os
import sys
from datetime import UTC, datetime
from enum import Enum
from pathlib import Path
from typing import Any


# ──────────────────────────────────────────────────────────────────────────────
# Venv bootstrap — homematicip_local-flavoured
# ──────────────────────────────────────────────────────────────────────────────


def _ensure_venv() -> None:
    try:
        import homeassistant  # noqa: F401
        import aiohomematic  # noqa: F401
        import openccu_data  # noqa: F401
        from custom_components.homematicip_local.entity_helpers.registry import (  # noqa: F401
            REGISTRY,
        )
        return
    except ImportError:
        pass
    candidates: list[str] = []
    if env := os.environ.get("HOMEMATICIP_LOCAL_VENV_PYTHON", "").strip():
        candidates.append(env)
    here = Path(__file__).resolve().parent
    for offset in ("../..", "../../.."):
        for venv_name in ("venv", ".venv"):
            cand = os.path.normpath(
                str(here / offset / "homematicip_local" / venv_name / "bin" / "python3")
            )
            candidates.append(cand)
    already = "_HOMEMATICIP_LOCAL_VENV_REEXEC_DONE"
    if os.environ.get(already) == "1":
        return
    for cand in candidates:
        if os.path.exists(cand):
            os.environ[already] = "1"
            print(
                f"[hmip_local-snapshot] re-execing in homematicip_local venv: {cand}",
                file=sys.stderr,
            )
            os.execv(cand, [cand, *sys.argv])


_ensure_venv()

_GITHUB_ROOT = Path(__file__).resolve().parents[2]
_HMIP_LOCAL_ROOT = _GITHUB_ROOT / "homematicip_local"
if _HMIP_LOCAL_ROOT.is_dir() and str(_HMIP_LOCAL_ROOT) not in sys.path:
    sys.path.insert(0, str(_HMIP_LOCAL_ROOT))


# ──────────────────────────────────────────────────────────────────────────────
# Imports
# ──────────────────────────────────────────────────────────────────────────────


try:
    import aiohomematic
    from aiohomematic.central import CentralConfig
    from aiohomematic.central.events import (
        DeviceLifecycleEvent,
        DeviceLifecycleEventType,
    )
    from aiohomematic.client import InterfaceConfig
    from aiohomematic.const import (
        CallSource,
        DataPointCategory,
        DeviceTriggerEventType,
        Interface,
        ParamsetKey,
    )
    from aiohomematic.model.custom import CustomDataPoint
except ImportError as exc:
    print(f"ERROR: cannot import aiohomematic: {exc}", file=sys.stderr)
    sys.exit(1)

try:
    # Importing the entity_helpers package runs `_initialize_registry()`
    # (entity_helpers/__init__.py:85) which populates REGISTRY exactly
    # once. Importing the package as a side-effect — REGISTRY then
    # exposes the populated rules via the symbol below.
    import custom_components.homematicip_local.entity_helpers  # noqa: F401
    from custom_components.homematicip_local.entity_helpers.registry import REGISTRY
except ImportError as exc:
    print(f"ERROR: cannot import homematicip_local: {exc}", file=sys.stderr)
    print(
        "hint: ensure /Users/markus/Documents/GitHub/homematicip_local exists and "
        "the venv has homeassistant + custom_components installed.",
        file=sys.stderr,
    )
    sys.exit(1)

try:
    import pydevccu
    from pydevccu import Server as PyDevCCUServer
except ImportError as exc:
    print(f"ERROR: cannot import pydevccu: {exc}", file=sys.stderr)
    sys.exit(1)


# ──────────────────────────────────────────────────────────────────────────────
# Configuration
# ──────────────────────────────────────────────────────────────────────────────


logging.basicConfig(
    level=logging.WARNING,
    format="%(asctime)s %(levelname)s %(name)s: %(message)s",
)
_LOGGER = logging.getLogger("hmip_local_snapshot")

_SCRIPT_DIR = Path(__file__).resolve().parent
_REPO_ROOT = _SCRIPT_DIR.parent
_OUTPUT_PATH = (
    _REPO_ROOT
    / "tests"
    / "integration"
    / "testdata"
    / "discovery_snapshot_homematicip_local.json"
)


def _resolve_devices() -> list[str] | None:
    override = os.environ.get("OPENCCU_LOOM_SNAPSHOT_DEVICES", "").strip()
    if not override:
        return None
    return [p.strip() for p in override.split(",") if p.strip()]


_DEVICES = _resolve_devices()
_LOCALE = os.environ.get("OPENCCU_LOOM_SNAPSHOT_LOCALE", "en").strip() or "en"

_CENTRAL_NAME = "snapshot-ccu"
_CCU_HOST = "127.0.0.1"
_CCU_PORT = 12036
_CALLBACK_PORT = 12122
_INIT_TIMEOUT_S = 600


# REGISTRY is already populated by the import side-effect of
# `custom_components.homematicip_local.entity_helpers` — do NOT call
# register_all again or every rule will be duplicated.


# Map aiohomematic's fine-grained DataPointCategory onto the HA
# Discovery component string. Mirrors openccu-loom's
# `componentFromCategory` helper (`internal/north/mqtt/category_component.go`)
# 1:1 — ensures both stacks settle on the same HA platform when the
# wire DP has a stable model-derived category.
_CATEGORY_TO_HA_COMPONENT: dict[str, str] = {
    "sensor": "sensor",
    "hub_sensor": "sensor",
    "binary_sensor": "binary_sensor",
    "hub_binary_sensor": "binary_sensor",
    "number": "number",
    "action_number": "number",
    "hub_number": "number",
    "switch": "switch",
    "schedule_switch": "switch",
    "hub_switch": "switch",
    "button": "button",
    "action": "button",
    "hub_button": "button",
    "select": "select",
    "action_select": "select",
    "hub_select": "select",
    "climate": "climate",
    "cover": "cover",
    "lock": "lock",
    "light": "light",
    "valve": "valve",
    "siren": "siren",
    "event": "event",
    "event_group": "event",
    "text": "text",
    "text_display": "text",
    "hub_text": "text",
    "update": "update",
    "hub_update": "update",
    "week_profile": "select",
}


def _ha_component(category: str) -> str:
    """Return the HA component for an aiohomematic DataPointCategory."""
    return _CATEGORY_TO_HA_COMPONENT.get(category, category)


# ──────────────────────────────────────────────────────────────────────────────
# Helpers — coerce HA enum values into JSON-serialisable primitives
# ──────────────────────────────────────────────────────────────────────────────


def _enum_value(v: Any) -> Any:
    """HA's StrEnum-derived classes serialise as `<EnumName.MEMBER: 'value'>`
    if you forget the `.value`. Recurse into containers so nothing leaks
    enum identity into the JSON."""
    if isinstance(v, Enum):
        return v.value
    if isinstance(v, (list, tuple)):
        return [_enum_value(x) for x in v]
    if isinstance(v, dict):
        return {k: _enum_value(x) for k, x in v.items()}
    return v


def _is_undefined(v: Any) -> bool:
    """HA marks unset description fields with a sentinel UndefinedType."""
    return type(v).__name__ == "UndefinedType"


def _opt(v: Any) -> Any:
    """Drop UndefinedType / empty / None for omitempty-style serialisation."""
    if v is None or _is_undefined(v):
        return None
    if v == "" or v == [] or v == ():
        return None
    return _enum_value(v)


# ──────────────────────────────────────────────────────────────────────────────
# Description → flat-dict projection (HA-friendly attribute view)
# ──────────────────────────────────────────────────────────────────────────────


def _description_payload(desc: Any) -> dict[str, Any]:
    """Project a HmEntityDescription onto the canonical attribute set the
    diff compares (keep names aligned with the openccu-loom-Discovery-
    payload field names)."""
    if desc is None:
        return {}
    out: dict[str, Any] = {}

    if (v := _opt(getattr(desc, "device_class", None))) is not None:
        out["device_class"] = v
    if (v := _opt(getattr(desc, "state_class", None))) is not None:
        out["state_class"] = v
    if (v := _opt(getattr(desc, "entity_category", None))) is not None:
        out["entity_category"] = v
    if (v := _opt(getattr(desc, "icon", None))) is not None:
        out["icon"] = v
    if (v := _opt(getattr(desc, "native_unit_of_measurement", None))) is not None:
        out["unit_of_measurement"] = v
    elif (v := _opt(getattr(desc, "unit_of_measurement", None))) is not None:
        out["unit_of_measurement"] = v
    if (v := _opt(getattr(desc, "suggested_display_precision", None))) is not None:
        out["suggested_display_precision"] = v

    # `entity_registry_enabled_default` is HA's canonical name; the MQTT
    # discovery field is `enabled_by_default` — normalise to the latter.
    if (v := getattr(desc, "entity_registry_enabled_default", None)) is False:
        out["enabled_by_default"] = False
    elif v is True:
        # True is the HA default — Mosquitto / aiohomematic2mqtt / openccu-loom
        # all omit it from the payload when true. Mirror the same omission so
        # the snapshot diff doesn't report a noisy default-vs-omitted drift.
        pass

    if (v := _opt(getattr(desc, "options", None))) is not None:
        out["options"] = v
    if (v := _opt(getattr(desc, "translation_key", None))) is not None:
        out["translation_key"] = v
    # name_source is HA-internal — not a valid MQTT-Discovery field.
    # Drift on it can never be closed via openccu-loom-MQTT, so we omit
    # it from the snapshot to keep the diff focused on actionable
    # fields.

    # Number-specific descriptor fields.
    for field in (
        "native_min_value",
        "native_max_value",
        "native_step",
        "mode",
    ):
        if (v := _opt(getattr(desc, field, None))) is not None:
            # Map HA's native_* names onto the canonical MQTT field names.
            mapped = {
                "native_min_value": "min",
                "native_max_value": "max",
                "native_step": "step",
                "mode": "mode",
            }[field]
            out[mapped] = v

    return out


# ──────────────────────────────────────────────────────────────────────────────
# Dynamic-field readers per platform
#
# These mirror what homematicip_local's per-platform Entity classes
# expose to HA via `*_attr_*` properties: numeric ranges, climate
# capabilities, light effects, cover support flags, etc.
# ──────────────────────────────────────────────────────────────────────────────


def _read_generic_dynamic(dp: Any, desc: Any, category: str) -> dict[str, Any]:
    """Mirror homematicip_local's per-Entity HA-attribute computation.

    Per-platform behaviour:
      - NUMBER: emits `_attr_native_min_value`, `_attr_native_max_value`,
        `_attr_native_step` (number.py:233-235). Bounds are wire * multiplier.
      - SELECT: emits `_attr_options` from `dp.values` (select.py).
      - Other (sensor, binary_sensor, button, switch, …): no
        descriptor-derived bounds — HA does not attach min/max/step
        to those entity types.

    Multiplier source priority is description.multiplier ↦ dp.multiplier
    (number.py:226-232).
    """
    out: dict[str, Any] = {}
    cat = (category or "").lower()

    if (v := _opt(getattr(dp, "unit", None))) is not None:
        out.setdefault("unit_of_measurement", v)

    if cat == "number":
        mult = None
        if desc is not None and getattr(desc, "multiplier", None) is not None:
            mult = float(getattr(desc, "multiplier"))
        elif (m := getattr(dp, "multiplier", None)) is not None:
            try:
                mult = float(m)
            except (TypeError, ValueError):
                mult = None
        if mult is None:
            mult = 1.0

        raw_min = _opt(getattr(dp, "min", None))
        raw_max = _opt(getattr(dp, "max", None))
        if isinstance(raw_min, (int, float)):
            out["min"] = float(raw_min) * mult
        if isinstance(raw_max, (int, float)):
            out["max"] = float(raw_max) * mult

        hmtype = str(getattr(dp, "hmtype", "") or "").upper()
        out["step"] = 1.0 if hmtype == "INTEGER" else 0.01 * mult

    if cat == "select":
        if (v := _opt(getattr(dp, "values", None))) is not None:
            out["options"] = list(v)

    # Sensor with device_class=enum: HA requires `options` for the
    # enum-typed sensor — mirrors `aiohomematic2mqtt/platforms/sensor.py:117`
    # and `homematicip_local`'s sensor platform which forwards
    # `data_point.values` as `_attr_options`. Without this enum-sensors
    # in HA fail validation.
    if cat == "sensor":
        dc = (getattr(desc, "device_class", None) if desc is not None else None)
        if dc is not None and str(_val(dc)) == "enum":
            if (v := _opt(getattr(dp, "values", None))) is not None:
                out["options"] = list(v)

    return out


def _read_custom_dynamic(dp: Any, category: str) -> dict[str, Any]:
    out: dict[str, Any] = {}
    cat = (category or "").lower()

    if cat == "climate":
        if (v := _opt(getattr(dp, "min_temp", None))) is not None:
            out["min_temp"] = v
        if (v := _opt(getattr(dp, "max_temp", None))) is not None:
            out["max_temp"] = v
        if (v := _opt(getattr(dp, "target_temperature_step", None))) is not None:
            out["temp_step"] = v
        modes = _opt(getattr(dp, "modes", None))
        if modes is not None:
            out["modes"] = list(modes)
        profiles = _opt(getattr(dp, "profiles", None))
        if profiles is not None:
            out["preset_modes"] = [str(p) for p in profiles if str(p) != "none"]
        if getattr(dp, "current_humidity", None) is not None:
            out["supports_humidity"] = True
        out["temperature_unit"] = "C"
    elif cat == "cover":
        # supports_position / supports_tilt are common openccu-loom
        # discovery-payload fields. CustomDpCover exposes them via
        # capability getters; keep them only if set to True.
        if getattr(dp, "supports_position", None):
            out["supports_position"] = True
        if getattr(dp, "supports_tilt", None):
            out["supports_tilt"] = True
        # Cover device_class falls out from the EntityDescription rule
        # (matched by device prefix — BLIND, SHUTTER, GARAGE, …).
    elif cat == "light":
        for f in ("min_kelvin", "max_kelvin", "transition", "effect", "effect_list"):
            if (v := _opt(getattr(dp, f, None))) is not None:
                out[f] = v
        cm = _opt(getattr(dp, "supported_color_modes", None))
        if cm is not None:
            out["supported_color_modes"] = sorted(str(c) for c in cm)
    elif cat == "lock":
        # payload_lock / payload_unlock are MQTT-only.
        pass
    elif cat == "siren":
        if getattr(dp, "support_volume_set", None):
            out["support_volume_set"] = True
        tones = _opt(getattr(dp, "available_tones", None))
        if tones is not None:
            out["available_tones"] = list(tones)
    elif cat == "valve":
        # No specific dynamic fields beyond device_class.
        pass

    return out


# ──────────────────────────────────────────────────────────────────────────────
# Snapshot row construction
# ──────────────────────────────────────────────────────────────────────────────


def _join_key_for_dp(dp: Any) -> tuple[str, dict[str, Any]]:
    """Return (join_key, metadata) — same convention as
    aiohomematic2mqtt_discovery_snapshot.py so the diff aligns."""
    category_str = str(dp.category) if hasattr(dp, "category") else ""
    meta: dict[str, Any] = {
        "kind": "",
        "device_address": "",
        "channel_no": 0,
        "channel_type": "",
        "model": "",
        "paramset_key": "",
        "parameter": "",
    }

    if category_str.startswith("hub_"):
        meta["kind"] = "hub"
        scope = category_str.removeprefix("hub_")
        ident = getattr(dp, "name", None) or getattr(dp, "unique_id", "?")
        return f"hub:{scope}:{ident}", meta

    channel = getattr(dp, "channel", None)
    device = getattr(channel, "device", None) if channel is not None else None
    if device is None:
        device = getattr(dp, "device", None)
    addr = (getattr(device, "address", "") or "").upper()
    ch_no = (getattr(channel, "no", 0) or 0) if channel is not None else 0
    ch_type = (getattr(channel, "type_name", "") or "") if channel is not None else ""
    model = getattr(device, "model", "") or ""

    meta["device_address"] = addr
    meta["channel_no"] = ch_no
    meta["channel_type"] = ch_type
    meta["model"] = model

    if isinstance(dp, CustomDataPoint):
        meta["kind"] = "agg"
        return f"{addr}:{ch_no}:agg:{category_str}", meta

    paramset_raw = str(getattr(dp, "paramset_key", "VALUES") or "VALUES")
    parameter = getattr(dp, "parameter", "") or ""
    paramset_join = "VALUES" if paramset_raw == "CALCULATED" else paramset_raw

    if not parameter and category_str == "update":
        meta["kind"] = "param"
        meta["paramset_key"] = "VALUES"
        meta["parameter"] = "UPDATE"
        return f"{addr}:0:param:VALUES.UPDATE", meta

    if not parameter:
        synth = (category_str or "special").upper()
        meta["kind"] = "param"
        meta["paramset_key"] = "SPECIAL"
        meta["parameter"] = synth
        return f"{addr}:{ch_no}:param:SPECIAL.{synth}", meta

    meta["kind"] = "param"
    meta["paramset_key"] = paramset_raw
    meta["parameter"] = parameter
    return f"{addr}:{ch_no}:param:{paramset_join}.{parameter}", meta


def _entity_row(dp: Any) -> dict[str, Any] | None:
    """Build a snapshot row for a single CallbackDataPoint."""
    join_key, meta = _join_key_for_dp(dp)

    category = getattr(dp, "category", None)
    if category is None:
        return None

    # Resolve the EntityDescription from REGISTRY.
    parameter = getattr(dp, "parameter", None) or ""
    device_model = meta.get("model") or None
    unit = getattr(dp, "unit", None) or None
    postfix = getattr(dp, "data_point_name_postfix", None) or None

    desc = REGISTRY.find(
        category=category,
        parameter=parameter or None,
        device_model=device_model,
        unit=unit,
        postfix=postfix,
    )

    payload: dict[str, Any] = _description_payload(desc)

    # Component is the HA Discovery component string. aiohomematic's
    # DataPointCategory uses fine-grained values (action_number,
    # schedule_switch, hub_*, …) that all collapse onto the matching
    # HA platform; normalise via `_ha_component` so the snapshot
    # aligns with openccu-loom's `componentFromCategory` output and
    # the diff doesn't false-positive on representational granularity.
    component = _ha_component(str(category))

    # Name — best-effort. HA's full title-casing pipeline depends on
    # translation tables, has_entity_name, name_source and
    # device_class_translation; we capture the data-point's
    # `translated_name` which is what the integration's name() method
    # primarily uses. Drift on `name` is therefore expected and is
    # tracked but not P0.
    if (n := _opt(getattr(dp, "translated_name", None))) is not None:
        payload["name"] = n
    elif (n := _opt(getattr(dp, "name", None))) is not None:
        payload["name"] = n

    # Dynamic fields per kind.
    if isinstance(dp, CustomDataPoint):
        payload.update(_read_custom_dynamic(dp, component))
    else:
        payload.update(_read_generic_dynamic(dp, desc, component))

    return {
        "join_key": join_key,
        "kind": meta["kind"],
        "discovery_topic": "",  # not applicable — hmip_local is not MQTT
        "component": component,
        "node_id": "",
        "object_id": "",
        "unique_id": str(getattr(dp, "unique_id", "") or ""),
        "device_address": meta["device_address"],
        "channel_no": meta["channel_no"],
        "channel_type": meta["channel_type"],
        "model": meta["model"],
        "paramset_key": meta["paramset_key"],
        "parameter": meta["parameter"],
        "payload": payload,
    }


def _event_row(event_group: Any) -> dict[str, Any]:
    """Press-event aggregation: one HA `event` entity per channel."""
    channel = event_group.channel
    device = channel.device
    addr = (getattr(device, "address", "") or "").upper()
    ch_no = getattr(channel, "no", 0) or 0
    payload: dict[str, Any] = {
        "device_class": "button",
        "event_types": list(event_group.event_types),
    }
    name = getattr(event_group, "full_name", None)
    if name:
        payload["name"] = name
    return {
        "join_key": f"{addr}:{ch_no}:event:channel",
        "kind": "event",
        "discovery_topic": "",
        "component": "event",
        "node_id": "",
        "object_id": "",
        "unique_id": f"event_{getattr(channel, 'unique_id', '?')}_{event_group.device_trigger_event_type.short}",
        "device_address": addr,
        "channel_no": ch_no,
        "channel_type": getattr(channel, "type_name", "") or "",
        "model": getattr(device, "model", "") or "",
        "paramset_key": "",
        "parameter": "",
        "payload": payload,
    }


# ──────────────────────────────────────────────────────────────────────────────
# Main async logic
# ──────────────────────────────────────────────────────────────────────────────


async def run() -> None:
    _LOGGER.info("Starting pydevccu on %s:%d devices=%s", _CCU_HOST, _CCU_PORT, _DEVICES)
    ccu = PyDevCCUServer(addr=(_CCU_HOST, _CCU_PORT), devices=_DEVICES)
    try:
        ccu.start()
    except Exception as exc:
        print(f"ERROR: pydevccu failed to start: {exc}", file=sys.stderr)
        sys.exit(1)

    central = None
    try:
        device_event = asyncio.Event()

        def _on_lifecycle(event: DeviceLifecycleEvent) -> None:
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

        central = await CentralConfig(
            name=_CENTRAL_NAME,
            host=_CCU_HOST,
            username="admin",
            password="admin",
            central_id="snapshot-hmiplocal",
            interface_configs=interface_configs,
            callback_port_xml_rpc=_CALLBACK_PORT,
            program_markers=(),
            sysvar_markers=(),
            start_direct=True,
        ).create_central()

        central.event_bus.subscribe(
            event_type=DeviceLifecycleEvent,
            event_key=None,
            handler=_on_lifecycle,
        )

        await central.start()

        _LOGGER.info("Waiting for devices…")
        with contextlib.suppress(TimeoutError):
            await asyncio.wait_for(device_event.wait(), timeout=_INIT_TIMEOUT_S)
        if not device_event.is_set():
            print("ERROR: no devices created", file=sys.stderr)
            return
        await asyncio.sleep(2)

        # Seed MASTER paramset values — openccu-loom loads MASTER eagerly; mirror
        # to keep MASTER-scoped DPs (CHANNEL_OPERATION_MODE) visible.
        load_tasks: list[Any] = []
        for dev in central.device_registry.devices:
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

        # Build entity rows — one per CallbackDataPoint plus one per
        # ChannelEventGroup (press-event aggregation).
        entities: list[dict[str, Any]] = []
        for dp in central.query_facade.get_data_points():
            try:
                row = _entity_row(dp)
            except Exception as exc:  # noqa: BLE001
                _LOGGER.debug("skip dp %s: %s", repr(dp), exc)
                continue
            if row is not None:
                entities.append(row)

        for event_type in (DeviceTriggerEventType.KEYPRESS, DeviceTriggerEventType.IMPULSE):
            for event_group in central.query_facade.get_event_groups(
                event_type=event_type, registered=False
            ):
                try:
                    event_group.register()
                except Exception:  # noqa: BLE001
                    pass
                try:
                    entities.append(_event_row(event_group))
                except Exception as exc:  # noqa: BLE001
                    _LOGGER.debug("skip event group: %s", exc)

        # Deduplicate on join_key (defensive — should already be unique)
        # then sort.
        seen: dict[str, dict[str, Any]] = {}
        for ent in entities:
            seen.setdefault(ent["join_key"], ent)
        entities = sorted(seen.values(), key=lambda e: e["join_key"])

        # Stack metadata
        try:
            ahm_version = importlib.metadata.version("aiohomematic")
        except Exception:  # noqa: BLE001
            ahm_version = getattr(aiohomematic, "__version__", "unknown")
        try:
            pydev_version = importlib.metadata.version("pydevccu")
        except Exception:  # noqa: BLE001
            pydev_version = getattr(pydevccu, "__version__", "unknown")
        try:
            hmip_local_version = importlib.metadata.version("homematicip_local")
        except Exception:  # noqa: BLE001
            hmip_local_version = "source"

        snapshot = {
            "stack": "homematicip_local",
            "stack_version": f"homematicip_local={hmip_local_version}, aiohomematic={ahm_version}",
            "devccu": "pydevccu",
            "devccu_version": pydev_version,
            "locale": _LOCALE,
            "captured_at": datetime.now(UTC).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "entities": entities,
        }

        _OUTPUT_PATH.parent.mkdir(parents=True, exist_ok=True)
        with _OUTPUT_PATH.open("w", encoding="utf-8") as fh:
            json.dump(snapshot, fh, indent=2, ensure_ascii=False, default=str)
            fh.write("\n")

        print(
            f"hmip_local snapshot written to {_OUTPUT_PATH}\n"
            f"  entities  : {len(entities)}\n"
            f"  file size : {_OUTPUT_PATH.stat().st_size:,} bytes",
        )

    finally:
        if central is not None:
            with contextlib.suppress(Exception):
                await central.stop()
        with contextlib.suppress(Exception):
            await ccu.stop()


if __name__ == "__main__":
    asyncio.run(run())
