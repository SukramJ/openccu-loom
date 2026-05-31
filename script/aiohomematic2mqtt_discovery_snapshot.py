#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 openccu-loom authors.
#
# aiohomematic2mqtt_discovery_snapshot.py — produces the
# aiohomematic2mqtt side of the cross-stack HA-Discovery snapshot diff.
#
# Boots a pydevccu.Server, connects an aiohomematic CentralUnit to it
# (same path as ControlUnit), iterates every CallbackDataPoint via the
# central's query_facade, drives each through aiohomematic2mqtt's
# `create_mqtt_entity` factory with a recording MQTTClient, and dumps
# the captured `homeassistant/.../config` payloads to JSON.
#
# Output:
#   tests/integration/testdata/discovery_snapshot_aiohomematic2mqtt.json
#
# Usage:
#   python3 script/aiohomematic2mqtt_discovery_snapshot.py
#
# The script must be run from the openccu-loom repository root. It
# auto-re-execs into an aiohomematic venv when one is found and the
# system Python lacks `openccu_data` (mirrors aiohomematic_snapshot.py).

from __future__ import annotations

import asyncio
import contextlib
import importlib.metadata
import json
import logging
import os
import sys
from datetime import UTC, datetime
from pathlib import Path
from typing import Any
from unittest.mock import MagicMock


# ──────────────────────────────────────────────────────────────────────────────
# Venv bootstrap — aiohomematic2mqtt-flavoured
#
# We need three Python packages on sys.path simultaneously:
#   - `paho.mqtt` (only in the aiohomematic2mqtt venv)
#   - `aiohomematic2mqtt` (only in the aiohomematic2mqtt venv / repo)
#   - `aiohomematic`, `pydevccu`, `openccu_data` (shipped via the
#      aiohomematic venv on PEP-668-managed hosts)
#
# Strategy: re-exec in aiohomematic2mqtt's venv so paho is importable,
# then prepend the aiohomematic venv's site-packages so openccu_data
# (and a fresh aiohomematic) become importable too.
# ──────────────────────────────────────────────────────────────────────────────


def _ensure_venv() -> None:
    try:
        import paho  # noqa: F401
        import openccu_data  # noqa: F401
        return
    except ImportError:
        pass
    candidates: list[str] = []
    if env := os.environ.get("AIOHOMEMATIC2MQTT_VENV_PYTHON", "").strip():
        candidates.append(env)
    here = Path(__file__).resolve().parent
    for offset in ("../..", "../../.."):
        for venv_name in ("venv", ".venv"):
            cand = os.path.normpath(
                str(here / offset / "aiohomematic2mqtt" / venv_name / "bin" / "python3")
            )
            candidates.append(cand)
    already = "_AIOHOMEMATIC2MQTT_VENV_REEXEC_DONE"
    if os.environ.get(already) == "1":
        return
    for cand in candidates:
        if os.path.exists(cand):
            os.environ[already] = "1"
            print(
                f"[discovery-snapshot] re-execing in aiohomematic2mqtt venv: {cand}",
                file=sys.stderr,
            )
            os.execv(cand, [cand, *sys.argv])


_ensure_venv()

_GITHUB_ROOT = Path(__file__).resolve().parents[2]

# Augment sys.path so `openccu_data` (only present in the aiohomematic
# venv) and the source-tree variants of the four sibling repos become
# importable inside whichever venv we landed in.
for _root in (
    Path.home() / "Documents" / "GitHub" / "aiohomematic" / ".venv",
    Path.home() / "Documents" / "GitHub" / "aiohomematic" / "venv",
):
    if _root.is_dir():
        for _candidate in (_root / "lib").glob("python*/site-packages"):
            if str(_candidate) not in sys.path:
                sys.path.append(str(_candidate))

for _pkg in ("aiohomematic", "aiohomematic2mqtt", "pydevccu"):
    _pkg_path = _GITHUB_ROOT / _pkg
    if _pkg_path.is_dir() and str(_pkg_path) not in sys.path:
        sys.path.insert(0, str(_pkg_path))


# ──────────────────────────────────────────────────────────────────────────────
# Imports (after venv bootstrap so the right packages are picked up)
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
        DeviceTriggerEventType,
        Interface,
        ParamsetKey,
    )
except ImportError as exc:
    print(f"ERROR: cannot import aiohomematic: {exc}", file=sys.stderr)
    sys.exit(1)

try:
    import aiohomematic2mqtt
    from aiohomematic2mqtt import platforms
    from aiohomematic2mqtt.platforms.event import MqttEvent
except ImportError as exc:
    print(f"ERROR: cannot import aiohomematic2mqtt: {exc}", file=sys.stderr)
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
_LOGGER = logging.getLogger("discovery_snapshot")

_SCRIPT_DIR = Path(__file__).resolve().parent
_REPO_ROOT = _SCRIPT_DIR.parent
_OUTPUT_PATH = (
    _REPO_ROOT
    / "tests"
    / "integration"
    / "testdata"
    / "discovery_snapshot_aiohomematic2mqtt.json"
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
_CCU_PORT = 12035
_CALLBACK_PORT = 12121
_INIT_TIMEOUT_S = 600

# HA Discovery components emitted by aiohomematic2mqtt that collapse a
# whole channel into one entity (one CustomDataPoint -> one HA entity).
_AGGREGATE_COMPONENTS = {"climate", "cover", "light", "lock", "valve", "siren"}


# ──────────────────────────────────────────────────────────────────────────────
# Recording MQTT client — captures every publish without touching a broker
# ──────────────────────────────────────────────────────────────────────────────


class RecordingMqttClient:
    """Drop-in stand-in for `aiohomematic2mqtt.MQTTClient` that records
    every publish into an in-memory buffer. The aiohomematic2mqtt entity
    classes call `client.publish(topic=..., payload=..., qos=, retain=)`
    and `client.subscribe(topic=...)`; we only need those two methods.

    `mqtt_base_topic` is HA's discovery-prefix root — in production it
    is the operator-configured base (default `homeassistant`). For a
    parity-faithful snapshot we mirror that default.
    """

    def __init__(self) -> None:
        self.published: list[tuple[str, str | bytes, int, bool]] = []
        self.mqtt_base_topic = "homeassistant"
        self.hass_birth_gracetime = 0
        self.hass_status_topic = "homeassistant/status"

    def publish(
        self,
        topic: str = "",
        payload: str | bytes = b"",
        qos: int = 0,
        retain: bool = False,
    ) -> None:
        self.published.append((topic, payload, qos, retain))

    def subscribe(self, topic: str = "") -> tuple[int, int | None]:
        return (0, 1)


# ──────────────────────────────────────────────────────────────────────────────
# Snapshot serialisation helpers
# ──────────────────────────────────────────────────────────────────────────────


def _sort_keys(value: Any) -> Any:
    """Recursively key-sort all dicts; preserve list order."""
    if isinstance(value, dict):
        return {k: _sort_keys(value[k]) for k in sorted(value)}
    if isinstance(value, list):
        return [_sort_keys(v) for v in value]
    return value


def _join_key_for_dp(dp: Any) -> tuple[str, dict[str, Any]]:
    """Return (join_key, metadata) for a CallbackDataPoint.

    Metadata fields populated:
      - kind: param | agg | hub
      - device_address (uppercase)
      - channel_no
      - channel_type
      - model
      - paramset_key (only for kind=param)
      - parameter (only for kind=param)
    """
    category = str(dp.category) if hasattr(dp, "category") else ""
    meta: dict[str, Any] = {
        "kind": "",
        "device_address": "",
        "channel_no": 0,
        "channel_type": "",
        "model": "",
        "paramset_key": "",
        "parameter": "",
    }

    # Hub-class entities have category strings starting with `hub_`.
    if category.startswith("hub_"):
        meta["kind"] = "hub"
        scope = category.removeprefix("hub_")
        ident = getattr(dp, "name", None) or getattr(dp, "unique_id", "?")
        join = f"hub:{scope}:{ident}"
        return join, meta

    # Channel-bound DPs (Generic + Custom + Calculated). Some
    # CallbackDataPoints (Update / DpFirmwareUpdate) attach to the
    # device directly and do not have a `.channel` attribute — fall
    # back to `dp.device` for those.
    channel = getattr(dp, "channel", None)
    device = getattr(channel, "device", None) if channel is not None else None
    if device is None:
        device = getattr(dp, "device", None)
    addr = (getattr(device, "address", "") or "").upper()
    ch_no = getattr(channel, "no", 0) or 0 if channel is not None else 0
    ch_type = (getattr(channel, "type_name", "") or "") if channel is not None else ""
    model = getattr(device, "model", "") or ""

    meta["device_address"] = addr
    meta["channel_no"] = ch_no
    meta["channel_type"] = ch_type
    meta["model"] = model

    # CustomDataPoint -> aggregated entity
    from aiohomematic.model.custom import CustomDataPoint

    if isinstance(dp, CustomDataPoint):
        meta["kind"] = "agg"
        component = category  # category equals the HA component name
        join = f"{addr}:{ch_no}:agg:{component}"
        return join, meta

    # CalculatedDataPoint surfaces with paramset_key == CALCULATED in
    # aiohomematic but openccu-loom exposes calculated DPs through the
    # VALUES paramset alias (`ch.DataPoints()`). Normalise to VALUES
    # for the join — the Python `paramset_key` field stays
    # CALCULATED in the snapshot row for diagnostics, but the join key
    # uses the canonical VALUES so both stacks line up on the same
    # logical entity.
    paramset_raw = str(getattr(dp, "paramset_key", "VALUES") or "VALUES")
    parameter = getattr(dp, "parameter", "") or ""
    paramset_join = "VALUES" if paramset_raw == "CALCULATED" else paramset_raw
    # Update / Firmware DPs have category=="update" but live device-
    # scoped (no channel). They emit a single per-device entity. Map
    # them into an `update` kind so the openccu-loom side can match.
    if not parameter and category == "update":
        meta["kind"] = "param"
        meta["paramset_key"] = "VALUES"
        meta["parameter"] = "UPDATE"
        join = f"{addr}:0:param:VALUES.UPDATE"
        return join, meta

    # WeekProfile / TextDisplay / other specialised CallbackDataPoints
    # carry no `parameter` attribute. Fall back to a category-derived
    # synthetic name so the join key is still unique and meaningful.
    if not parameter:
        synth = (category or "special").upper()
        meta["kind"] = "param"
        meta["paramset_key"] = "SPECIAL"
        meta["parameter"] = synth
        join = f"{addr}:{ch_no}:param:SPECIAL.{synth}"
        return join, meta

    meta["kind"] = "param"
    meta["paramset_key"] = paramset_raw
    meta["parameter"] = parameter
    join = f"{addr}:{ch_no}:param:{paramset_join}.{parameter}"
    return join, meta


def _join_key_for_event_group(event_group: Any) -> tuple[str, dict[str, Any]]:
    """Channel event aggregation: one HA `event` entity per channel."""
    channel = event_group.channel
    device = channel.device
    addr = (getattr(device, "address", "") or "").upper()
    ch_no = getattr(channel, "no", 0) or 0
    return (
        f"{addr}:{ch_no}:event:channel",
        {
            "kind": "event",
            "device_address": addr,
            "channel_no": ch_no,
            "channel_type": getattr(channel, "type_name", "") or "",
            "model": getattr(device, "model", "") or "",
            "paramset_key": "",
            "parameter": "",
        },
    )


def _parse_ha_topic(topic: str) -> tuple[str, str, str] | None:
    parts = topic.split("/")
    if len(parts) < 5 or parts[0] != "homeassistant" or parts[-1] != "config":
        return None
    component = parts[1]
    # mqtt_base_topic = "homeassistant", so the path is fixed-shape:
    # homeassistant/<component>/<...>/config. The middle bit varies per
    # plain entity vs event entity.
    if len(parts) == 5:
        # homeassistant/<component>/<node_id>/<object_id>/config
        return component, parts[2], parts[3]
    # homeassistant/<component>/<unique_id>/config (event entities use
    # this shape — see MqttEvent._ha_path).
    return component, "", parts[2]


# ──────────────────────────────────────────────────────────────────────────────
# Main async logic
# ──────────────────────────────────────────────────────────────────────────────


async def run() -> None:
    # ── 1. Start pydevccu ────────────────────────────────────────────────────
    _LOGGER.info("Starting pydevccu on %s:%d devices=%s", _CCU_HOST, _CCU_PORT, _DEVICES)
    ccu = PyDevCCUServer(addr=(_CCU_HOST, _CCU_PORT), devices=_DEVICES)
    try:
        ccu.start()
    except Exception as exc:
        print(f"ERROR: pydevccu failed to start: {exc}", file=sys.stderr)
        sys.exit(1)

    central = None
    try:
        # ── 2. Connect CentralUnit ───────────────────────────────────────────
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
            central_id="snapshot-discovery",
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
        # Give the pipeline time to wire CustomDataPoints + CalculatedDataPoints
        # — they are spawned after the wire-level device-creation event.
        await asyncio.sleep(2)

        # ── 3. Seed MASTER paramset values ───────────────────────────────────
        # openccu-loom loads MASTER eagerly during hydration; mirror that here
        # so MASTER-scoped DPs (CHANNEL_OPERATION_MODE, …) are visible.
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
            _LOGGER.info("MASTER seeded (%d DPs)", len(load_tasks))

        # ── 4. Build recording MQTT client + ControlUnit-shaped fake ─────────
        rec = RecordingMqttClient()

        cu_fake = MagicMock()
        cu_fake.central = central
        cu_fake.add_subscription = MagicMock()
        cu_fake.on_mqtt_message = MagicMock()
        cu_fake.on_mqtt_connect = MagicMock()
        cu_fake.on_mqtt_subscribe = MagicMock()

        # ── 5. Iterate every CallbackDataPoint and create MQTT entities ──────
        all_data_points = central.query_facade.get_data_points()
        _LOGGER.info("Discovered %d data points", len(all_data_points))

        seen_topics: dict[str, dict[str, Any]] = {}

        async def _record_entity_topics(snapshot_meta: dict[str, Any]) -> None:
            """Drain rec.published into seen_topics, retaining the first
            metadata block for each unique discovery topic."""
            while rec.published:
                topic, payload, _qos, _retain = rec.published.pop(0)
                if not topic.startswith("homeassistant/"):
                    continue
                if not topic.endswith("/config"):
                    continue
                if topic in seen_topics:
                    continue
                if isinstance(payload, bytes):
                    try:
                        body = json.loads(payload.decode("utf-8"))
                    except Exception:  # noqa: BLE001
                        continue
                elif isinstance(payload, str):
                    try:
                        body = json.loads(payload)
                    except Exception:  # noqa: BLE001
                        continue
                else:
                    continue
                seen_topics[topic] = {"payload": body, "meta": snapshot_meta}

        # Generic + Custom + Calculated DPs
        for dp in all_data_points:
            join, meta = _join_key_for_dp(dp)
            try:
                ent = await platforms.create_mqtt_entity(
                    hm_entity=dp, control_unit=cu_fake, mqtt_client=rec
                )
            except Exception as exc:  # noqa: BLE001
                _LOGGER.debug(
                    "skip entity for %s: %s",
                    getattr(dp, "full_name", repr(dp)),
                    exc,
                )
                continue
            if ent is None:
                continue
            # Send only the HA-Discovery message — no HM/state on the wire
            # for the snapshot (we want the config payload, not retained
            # state).
            try:
                ent.send_discovery_information(
                    message_type=__import__("aiohomematic2mqtt.const", fromlist=["MessageType"]).MessageType.HA
                )
            except Exception as exc:  # noqa: BLE001
                _LOGGER.debug(
                    "send_discovery failed for %s: %s",
                    getattr(dp, "full_name", repr(dp)),
                    exc,
                )
            await _record_entity_topics({"join_key": join, **meta})

        # Channel event groups
        for event_type in (DeviceTriggerEventType.KEYPRESS, DeviceTriggerEventType.IMPULSE):
            for event_group in central.query_facade.get_event_groups(
                event_type=event_type, registered=False
            ):
                join, meta = _join_key_for_event_group(event_group)
                # Ensure the event group is materialised (register internal
                # state without subscribing to broker callbacks).
                try:
                    event_group.register()
                except Exception:  # noqa: BLE001
                    pass
                mq = MqttEvent(event_group=event_group, control_unit=cu_fake, mqtt_client=rec)
                try:
                    mq.send_discovery_information(
                        message_type=__import__("aiohomematic2mqtt.const", fromlist=["MessageType"]).MessageType.HA
                    )
                except Exception as exc:  # noqa: BLE001
                    _LOGGER.debug("event send_discovery failed: %s", exc)
                await _record_entity_topics({"join_key": join, **meta})

        # ── 6. Build the snapshot rows ───────────────────────────────────────
        entities = []
        for topic, captured in seen_topics.items():
            body = captured["payload"]
            meta = captured["meta"]
            comp_node_obj = _parse_ha_topic(topic)
            component = comp_node_obj[0] if comp_node_obj else ""
            node_id = comp_node_obj[1] if comp_node_obj else ""
            object_id = comp_node_obj[2] if comp_node_obj else ""
            entities.append(
                {
                    "join_key": meta.get("join_key", ""),
                    "kind": meta.get("kind", ""),
                    "discovery_topic": topic,
                    "component": component,
                    "node_id": node_id,
                    "object_id": object_id,
                    "unique_id": str(body.get("unique_id", "") or ""),
                    "device_address": meta.get("device_address", ""),
                    "channel_no": meta.get("channel_no", 0),
                    "channel_type": meta.get("channel_type", ""),
                    "model": meta.get("model", ""),
                    "paramset_key": meta.get("paramset_key", ""),
                    "parameter": meta.get("parameter", ""),
                    "payload": _sort_keys(body),
                }
            )
        entities.sort(key=lambda e: e["join_key"])

        # ── 7. Stack metadata ────────────────────────────────────────────────
        try:
            ahm_version = importlib.metadata.version("aiohomematic")
        except Exception:  # noqa: BLE001
            ahm_version = getattr(aiohomematic, "__version__", "unknown")
        try:
            ahm2mq_version = importlib.metadata.version("aiohomematic2mqtt")
        except Exception:  # noqa: BLE001
            ahm2mq_version = getattr(aiohomematic2mqtt, "__version__", "unknown")
        try:
            pydev_version = importlib.metadata.version("pydevccu")
        except Exception:  # noqa: BLE001
            pydev_version = getattr(pydevccu, "__version__", "unknown")

        snapshot = {
            "stack": "aiohomematic2mqtt",
            "stack_version": f"aiohomematic2mqtt={ahm2mq_version}, aiohomematic={ahm_version}",
            "devccu": "pydevccu",
            "devccu_version": pydev_version,
            "locale": _LOCALE,
            "captured_at": datetime.now(UTC).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "entities": entities,
        }

        # ── 8. Write output ──────────────────────────────────────────────────
        _OUTPUT_PATH.parent.mkdir(parents=True, exist_ok=True)
        with _OUTPUT_PATH.open("w", encoding="utf-8") as fh:
            json.dump(snapshot, fh, indent=2, ensure_ascii=False, default=str)
            fh.write("\n")

        print(
            f"Discovery snapshot written to {_OUTPUT_PATH}\n"
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
