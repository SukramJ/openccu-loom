#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 openccu-loom authors.
#
# discovery_snapshot_diff.py — diff the openccu-loom discovery snapshot
# against a reference HA-Entity snapshot.
#
# **Primary mode** (default): compare openccu-loom's MQTT-Discovery
# payloads against `homematicip_local`'s HA-Entity attributes. The
# reference is the HA-native integration; openccu-loom must emit
# entities that look identical in HA.
#
# **Secondary mode** (`--secondary`): compare openccu-loom against
# aiohomematic2mqtt's MQTT-Discovery payloads. Useful as a cross-check
# of the MQTT plumbing layer (state-topic / value-template plumbing
# differs between the two MQTT bridges; the homematicip_local
# comparison ignores that plumbing entirely).
#
# Both inputs follow `docs/parity/discovery_snapshot_schema.md`. The
# diff joins by `join_key`. Stack-specific MQTT plumbing (topic /
# template strings) is dropped in primary mode and presence-checked
# in secondary mode.
#
# Usage:
#   python3 script/discovery_snapshot_diff.py
#   python3 script/discovery_snapshot_diff.py --secondary
#   python3 script/discovery_snapshot_diff.py path/A.json path/B.json
#
# Exit codes:
#   0 — snapshots agree across the entire join intersection
#   1 — drift detected; per-entity report on stdout
#   2 — input files missing

from __future__ import annotations

import argparse
import json
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any

REPO_ROOT = Path(__file__).resolve().parents[1]
DEFAULT_GO = REPO_ROOT / "tests/integration/testdata/discovery_snapshot_openccu-loom.json"
DEFAULT_PRIMARY = REPO_ROOT / "tests/integration/testdata/discovery_snapshot_homematicip_local.json"
DEFAULT_SECONDARY = REPO_ROOT / "tests/integration/testdata/discovery_snapshot_aiohomematic2mqtt.json"

# Top-level snapshot keys we never compare.
META_KEYS = frozenset({"stack", "stack_version", "devccu", "devccu_version", "captured_at"})

# MQTT-only payload fields. Two behaviours:
#   - secondary mode (openccu-loom vs aiohomematic2mqtt): replace value
#     with a presence-bool so the diff still flags "one stack publishes
#     this topic, the other doesn't".
#   - primary mode (openccu-loom vs homematicip_local): drop entirely.
#     homematicip_local is not an MQTT bridge; expecting it to emit
#     state_topic / value_template / availability is meaningless.
MQTT_ONLY_FIELDS: frozenset[str] = frozenset({
    "state_topic",
    "command_topic",
    "json_attributes_topic",
    "json_attributes_template",
    "value_template",
    "availability",
    "availability_mode",
    "availability_topic",
    "mode_command_topic",
    "mode_state_topic",
    "mode_state_template",
    "temperature_command_topic",
    "temperature_state_topic",
    "temperature_state_template",
    "current_temperature_topic",
    "current_temperature_template",
    "current_humidity_topic",
    "current_humidity_template",
    "action_topic",
    "action_template",
    "preset_mode_command_topic",
    "preset_mode_state_topic",
    "preset_mode_value_template",
    "set_position_topic",
    "position_topic",
    "position_template",
    "tilt_command_topic",
    "tilt_status_topic",
    "tilt_status_template",
    # MQTT-bridge wire-format conventions; not relevant for the
    # native HA-integration comparison.
    "payload_on",
    "payload_off",
    "payload_open",
    "payload_close",
    "payload_stop",
    "payload_press",
    "payload_lock",
    "payload_unlock",
    "payload_available",
    "payload_not_available",
    "force_update",
    "expire_after",
    "optimistic",
    "retain",
    "qos",
    # state_value_template / set_position_template — bridge-specific
    # value derivation that the native integration does not project.
    "state_value_template",
    "set_position_template",
    # State-token mapping fields — they configure how HA parses the
    # state_topic, but the resulting HA Entity is identical regardless
    # of the token (HA's native cover/lock/switch entities already
    # know "open/closed", "locked/unlocked", "true/false"). Native
    # integrations don't emit them; openccu-loom-MQTT does to bind
    # the wire payload to HA's expected tokens. No semantic drift.
    "state_on",
    "state_off",
    "state_open",
    "state_closed",
    "state_opening",
    "state_closing",
    "state_stopped",
    "state_locked",
    "state_unlocked",
    "state_locking",
    "state_unlocking",
    "state_jammed",
    "position_open",
    "position_closed",
    # Light-Schema fields (`schema`, `brightness*`, `flash`,
    # `supported_color_modes`) — these are MQTT-light-platform-specific
    # discovery fields. The native HA integration uses LightEntity
    # capabilities directly. Tracked separately when we add the Light-
    # builder fields, but for the primary diff they are MQTT-only.
    "schema",
    "brightness",
    "brightness_scale",
    "flash",
    "supported_color_modes",
    # off_delay / force_update / state_value_template — already
    # covered above; listed here for cluster-completeness.
    "off_delay",
    # MQTT-discovery name field — openccu-loom computes a synthetic
    # name; the HA integration relies on HA's name resolution pipeline
    # (translation_key + name_source + device_class_translation).
    # The two cannot be made bit-equal without booting a HA instance.
    "name",
    # Cover-tilt MQTT-discovery fields — HA-native CoverEntity exposes
    # tilt via `_attr_current_cover_tilt_position` etc., not these
    # value-template-binding fields.
    "tilt_command_template",
    "tilt_min",
    "tilt_max",
    "tilt_opened_value",
    "tilt_closed_value",
    "tilt_optimistic",
    # Light Kelvin/Mireds — MQTT-light fields binding the wire payload
    # to HA's color-temp range. Native LightEntity uses
    # `_attr_min_color_temp_kelvin` / `_attr_max_color_temp_kelvin`.
    "min_kelvin",
    "max_kelvin",
    "min_mireds",
    "max_mireds",
    "color_temp_kelvin",
    "color_temp_command_topic",
    "color_temp_state_topic",
    "color_temp_value_template",
    # Event aggregation — HA-native exposes press events via device
    # triggers, not as `event` entities. The `event_types` list is
    # MQTT-event-platform specific.
    "event_types",
    # Climate / Cover / Light-MQTT-platform-specific fields that have
    # no HA-native equivalent attribute (their HA-native counterpart
    # is exposed differently — via _attr_supported_features bits, not
    # discrete fields).
    "supported_features",
    "transition",
    "effect",
    "effect_list",
    "current_humidity_template",
    "swing_mode_command_topic",
    "swing_mode_state_topic",
    "swing_modes",
    # Number-MQTT-platform-specific fields without HA-native equivalent.
    # `mode` (slider/box/auto) defaults to "auto" in HA-MQTT and "box"
    # in HA-native; both are display-hints, not state. `command_template`
    # is MQTT-write-side multiplier-inverter; HA-native uses
    # `async_set_native_value` directly.
    "mode",
    "command_template",
    # Siren-MQTT-platform-specific fields. HA-native SirenEntity uses
    # `_attr_supported_features` bits; these MQTT-only fields configure
    # which capability the MQTT-Siren platform exposes — no semantic
    # divergence between the two stacks.
    "support_volume_set",
    "support_duration",
    "available_tones",
    "support_tones",
    # Cover-Vent-Service: openccu-loom exposes a custom `ventilate`
    # method as its own command topic; HA-native CoverEntity surfaces
    # ventilate as a custom service call rather than a discovery field.
    "vent_command_topic",
    "vent_command_template",
})

# Entity-level keys we always drop (identity-only, not semantic).
DROPPED_FIELDS: frozenset[str] = frozenset({
    "default_entity_id",
    "object_id",
    "origin",
    "device",
    "unique_id",
})


def load(path: Path) -> dict:
    with path.open() as fp:
        return json.load(fp)


def normalise_payload(payload: dict[str, Any], *, mqtt_only_mode: str) -> dict[str, Any]:
    """Project payload onto comparable fields.

    `mqtt_only_mode`:
      - `"drop"` — strip every MQTT-only field entirely (primary mode,
        openccu-loom vs homematicip_local).
      - `"presence"` — replace MQTT-only fields with a presence-bool
        (secondary mode, openccu-loom vs aiohomematic2mqtt).
    """
    out: dict[str, Any] = {}
    for k, v in payload.items():
        if k in DROPPED_FIELDS:
            continue
        if k in MQTT_ONLY_FIELDS:
            if mqtt_only_mode == "presence":
                out[f"_present:{k}"] = v not in (None, "", [], {}, False)
            # else: drop entirely
            continue
        if v in (None, "", [], {}):
            continue
        out[k] = v
    return out


def diff_entity(go: dict, ref: dict, *, mqtt_only_mode: str) -> dict[str, tuple[Any, Any]]:
    g = normalise_payload(go.get("payload") or {}, mqtt_only_mode=mqtt_only_mode)
    r = normalise_payload(ref.get("payload") or {}, mqtt_only_mode=mqtt_only_mode)
    drift: dict[str, tuple[Any, Any]] = {}
    for field in set(g) | set(r):
        gv = g.get(field)
        rv = r.get(field)
        if gv != rv:
            drift[field] = (gv, rv)
    return drift


def index_entities(snap: dict) -> dict[str, dict]:
    return {e["join_key"]: e for e in snap.get("entities", [])}


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(
        description="Diff openccu-loom MQTT discovery against a reference HA snapshot."
    )
    parser.add_argument(
        "go_path",
        nargs="?",
        type=Path,
        default=DEFAULT_GO,
        help="openccu-loom discovery snapshot JSON",
    )
    parser.add_argument(
        "ref_path",
        nargs="?",
        type=Path,
        help="reference snapshot JSON (default: homematicip_local; with --secondary: aiohomematic2mqtt)",
    )
    parser.add_argument(
        "--secondary",
        action="store_true",
        help="compare against aiohomematic2mqtt (MQTT cross-check) instead of homematicip_local",
    )
    args = parser.parse_args(argv[1:])

    if args.ref_path is None:
        args.ref_path = DEFAULT_SECONDARY if args.secondary else DEFAULT_PRIMARY

    mode = "presence" if args.secondary else "drop"
    ref_label = "aiohomematic2mqtt" if args.secondary else "homematicip_local"

    if not args.go_path.exists():
        print(f"missing: {args.go_path}", file=sys.stderr)
        print(
            "hint: run\n"
            "  go test -tags=integration -timeout=600s "
            "-run TestDiscoverySnapshotDumpAgainstGodevccu ./tests/integration/...",
            file=sys.stderr,
        )
        return 2
    if not args.ref_path.exists():
        print(f"missing: {args.ref_path}", file=sys.stderr)
        if args.secondary:
            print(
                "hint: run `python3 script/aiohomematic2mqtt_discovery_snapshot.py`",
                file=sys.stderr,
            )
        else:
            print(
                "hint: run `python3 script/homematicip_local_snapshot.py`",
                file=sys.stderr,
            )
        return 2

    go = load(args.go_path)
    ref = load(args.ref_path)

    go_entities = index_entities(go)
    ref_entities = index_entities(ref)

    only_go = sorted(go_entities.keys() - ref_entities.keys())
    only_ref = sorted(ref_entities.keys() - go_entities.keys())
    shared = sorted(go_entities.keys() & ref_entities.keys())

    field_counts: dict[str, int] = defaultdict(int)
    drifted: dict[str, dict[str, tuple[Any, Any]]] = {}
    for jk in shared:
        d = diff_entity(go_entities[jk], ref_entities[jk], mqtt_only_mode=mode)
        if d:
            drifted[jk] = d
            for field in d:
                field_counts[field] += 1

    summary = {
        "mode": "primary" if not args.secondary else "secondary",
        "reference_stack": ref_label,
        "mqtt_only_field_handling": mode,
        "openccu-loom_entities": len(go_entities),
        f"{ref_label}_entities": len(ref_entities),
        "shared_entities": len(shared),
        "drifted_entities": len(drifted),
        "only_in_openccu-loom": only_go,
        f"only_in_{ref_label}": only_ref,
        "field_drift_counts": dict(sorted(field_counts.items(), key=lambda kv: -kv[1])),
    }

    print(json.dumps({"summary": summary, "drifted": drifted}, indent=2, ensure_ascii=False))

    total = len(only_go) + len(only_ref) + len(drifted)
    return 0 if total == 0 else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
