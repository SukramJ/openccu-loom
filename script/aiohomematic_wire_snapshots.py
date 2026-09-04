#!/usr/bin/env python3
"""
aiohomematic_wire_snapshots.py — generate aiohomematic reference wire-call snapshots.

This script produces ground-truth wire-call records for the aiohomematic
Custom-DP setters that have known drift against the Go implementation.
It uses a lightweight mock-client approach instead of a full pydevccu
setup: aiohomematic Custom-DP classes call `self._client.set_value()` /
`self._client.put_paramset()` — we capture those calls without spinning
up an actual CCU connection.

Output directory: tests/contract/wire_snapshots/aiohomematic_reference/

Each file is named <DPType>__<Setter>.json and contains:
  {
    "dp_type": "DRGDaliLight",
    "setter": "SetEffect",
    "source": "aiohomematic",
    "aiohomematic_version": "2026.5.10",
    "inputs": [
      {
        "label": "effect=Flash",
        "calls": [{"method": "SetValue", "address": "...", "parameter": "EFFECT", "value": "Flash"}]
      }
    ]
  }

Limitation: setters that depend on prior observed state (e.g.
Blind.SetCombined where currently_moving depends on _target_level) are
captured in their "first-call" state (target_level=None), which is the
same condition used in the Go snapshot tests.  This is the canonical
comparison point.

Usage:
    python3 script/aiohomematic_wire_snapshots.py
"""

from __future__ import annotations

import asyncio
import importlib.metadata
import json
import os
import sys
from pathlib import Path
from typing import Any
from unittest.mock import AsyncMock, MagicMock, patch


# ── venv bootstrap (mirrors aiohomematic_snapshot.py) ──────────────────────────

def _ensure_venv() -> None:
    """Re-exec in the aiohomematic venv if openccu_data or aiohomematic not available."""
    try:
        import aiohomematic  # noqa: F401
        return
    except ImportError:
        pass
    candidates: list[str] = []
    if env := os.environ.get("AIOHOMEMATIC_VENV_PYTHON", "").strip():
        candidates.append(env)
    here = Path(__file__).resolve().parent
    for offset in ("../..", "../../.."):
        for venv_name in ("venv", ".venv"):
            cand = os.path.normpath(str(here / offset / "aiohomematic" / venv_name / "bin" / "python3"))
            candidates.append(cand)
    already_marker = "_AIOHOMEMATIC_WIRE_SNAP_REEXEC"
    if os.environ.get(already_marker) == "1":
        return
    for cand in candidates:
        if os.path.exists(cand):
            os.environ[already_marker] = "1"
            print(f"[wire-snapshots] re-execing in aiohomematic venv: {cand}", file=sys.stderr)
            os.execv(cand, [cand, *sys.argv])


_ensure_venv()

_GITHUB_ROOT = Path(__file__).resolve().parents[2]
_AIOHM_VENV = Path.home() / "Documents" / "GitHub" / "aiohomematic" / ".venv"
if _AIOHM_VENV.is_dir():
    for _candidate in (_AIOHM_VENV / "lib").glob("python*/site-packages"):
        if str(_candidate) not in sys.path:
            sys.path.insert(0, str(_candidate))
for _pkg in ("aiohomematic",):
    _pkg_path = _GITHUB_ROOT / _pkg
    if _pkg_path.is_dir() and str(_pkg_path) not in sys.path:
        sys.path.insert(0, str(_pkg_path))


# ── imports ────────────────────────────────────────────────────────────────────

try:
    import aiohomematic
    from aiohomematic.model.custom.siren import _convert_repetitions  # noqa: PLC2701
    from aiohomematic.model.generic.action_select import DpActionSelect
except ImportError as exc:
    print(f"ERROR: cannot import aiohomematic: {exc}", file=sys.stderr)
    sys.exit(1)

# ── output path ────────────────────────────────────────────────────────────────

_SCRIPT_DIR = Path(__file__).resolve().parent
_REPO_ROOT = _SCRIPT_DIR.parent
_OUT_DIR = _REPO_ROOT / "tests" / "contract" / "wire_snapshots" / "aiohomematic_reference"

_AIOHM_VERSION = "unknown"
try:
    _AIOHM_VERSION = importlib.metadata.version("aiohomematic")
except Exception:  # noqa: BLE001
    _AIOHM_VERSION = getattr(aiohomematic, "__version__", "unknown")


# ── captured call ──────────────────────────────────────────────────────────────

class CapturedCall:
    """One wire call recorded by the mock client."""

    def __init__(self, method: str, address: str, **kwargs: Any) -> None:
        self.method = method
        self.address = address
        self.extra = kwargs

    def to_dict(self) -> dict[str, Any]:
        out: dict[str, Any] = {"method": self.method, "address": self.address}
        if "paramset_key" in self.extra:
            out["paramset_key"] = str(self.extra["paramset_key"])
        if "parameter" in self.extra:
            out["parameter"] = self.extra["parameter"]
        if "value" in self.extra:
            out["value"] = self.extra["value"]
        if "put_values" in self.extra:
            out["put_values"] = self.extra["put_values"]
        return out


class MockClient:
    """
    Minimal mock that captures set_value and put_paramset calls.
    Mimics the ValueAndParamsetOperationsProtocol surface that
    CallParameterCollector and GenericDataPoint.send_value use.
    """

    def __init__(self) -> None:
        self._calls: list[CapturedCall] = []

    async def set_value(  # noqa: PLR0913
        self,
        *,
        channel_address: str,
        paramset_key: Any,
        parameter: str,
        value: Any,
        wait_for_callback: int | None = None,
        priority: Any = None,
        purge_addresses: Any = None,
        retry: bool = True,
    ) -> set:
        self._calls.append(
            CapturedCall(
                "SetValue",
                channel_address,
                parameter=parameter,
                value=value,
            )
        )
        return set()

    async def put_paramset(  # noqa: PLR0913
        self,
        *,
        channel_address: str,
        paramset_key_or_link_address: Any,
        values: dict[str, Any],
        wait_for_callback: int | None = None,
        priority: Any = None,
        purge_addresses: Any = None,
        retry: bool = True,
    ) -> set:
        self._calls.append(
            CapturedCall(
                "PutParamset",
                channel_address,
                paramset_key=str(paramset_key_or_link_address),
                put_values=dict(values),
            )
        )
        return set()

    def capture(self) -> list[dict[str, Any]]:
        """Return captured calls as list of dicts and reset."""
        result = [c.to_dict() for c in self._calls]
        self._calls.clear()
        return result


# ── snapshot file helpers ──────────────────────────────────────────────────────

def _write_snapshot(
    dp_type: str,
    setter: str,
    inputs: list[dict[str, Any]],
) -> Path:
    _OUT_DIR.mkdir(parents=True, exist_ok=True)
    payload = {
        "dp_type": dp_type,
        "setter": setter,
        "source": "aiohomematic",
        "aiohomematic_version": _AIOHM_VERSION,
        "inputs": inputs,
    }
    path = _OUT_DIR / f"{dp_type}__{setter}.json"
    with path.open("w", encoding="utf-8") as fh:
        json.dump(payload, fh, indent=2, ensure_ascii=False)
        fh.write("\n")
    return path


# ── value-encoding helpers (no device context needed) ─────────────────────────

def _action_select_prepare(value_list: list[str], value: str | int, enum_value_is_index: bool) -> str | int:
    """
    Mirror DpActionSelect._prepare_value_for_sending without a real DP object.

    enum_value_is_index=True  → HM device: string label maps to integer index.
    enum_value_is_index=False → HmIP device: string label sent as-is.
    """
    if isinstance(value, (int, float)) and 0 <= value < len(value_list):
        return int(value)
    if value in value_list:
        if enum_value_is_index:
            return value_list.index(value)
        return str(value)
    raise ValueError(f"{value!r} not in value_list {value_list!r}")


# ── individual snapshot generators ────────────────────────────────────────────

def _gen_drg_dali_set_effect() -> None:
    """
    DRGDaliLight SetEffect: HmIP-DRG-DALI uses DpActionSelect.
    The device ships MIN as a string ("Off"), so _enum_value_is_index=False
    and the wire value is the STRING label.
    """
    value_list = ["Off", "Slow_color_change", "Medium_color_change", "Fast_color_change", "Flash", "Smooth_slow", "Smooth_fast"]
    # HmIP: MIN is a string → enum_value_is_index=False
    cases = [
        ("effect=Off", "Off"),
        ("effect=Flash", "Flash"),
        ("effect=Smooth_fast", "Smooth_fast"),
    ]
    inputs = []
    for label, effect in cases:
        wire_value = _action_select_prepare(value_list, effect, enum_value_is_index=False)
        inputs.append({
            "label": label,
            "calls": [{"method": "SetValue", "address": "DALI0001:1", "parameter": "EFFECT", "value": wire_value}],
        })
    _write_snapshot("DRGDaliLight", "SetEffect", inputs)


def _gen_rgbw_set_effect() -> None:
    """
    RGBWLight SetEffect: HmIP-RGBW uses DpActionSelect.
    HmIP → enum_value_is_index=False → STRING on wire.
    """
    value_list = ["BLINKING_SLOW", "BLINKING_FAST", "FLASH_SHORT", "RAMPING_CONTINUOUS"]
    cases = [
        ("BLINKING_SLOW", "BLINKING_SLOW"),
        ("FLASH_SHORT", "FLASH_SHORT"),
    ]
    inputs = []
    for label, effect in cases:
        wire_value = _action_select_prepare(value_list, effect, enum_value_is_index=False)
        inputs.append({
            "label": label,
            "calls": [{"method": "SetValue", "address": "RGBW0001:1", "parameter": "EFFECT", "value": wire_value}],
        })
    _write_snapshot("RGBWLight", "SetEffect", inputs)


def _gen_effect_light_set_effect() -> None:
    """
    EffectLight SetEffect: uses PROGRAM parameter (integer-typed, HM device).
    The fixture in generator_test.go passes integer index directly.
    HM devices: VALUE_LIST with integer MIN → enum_value_is_index=True → INT on wire.
    """
    value_list = ["Off", "Slow color change", "Fast color change", "Campfire", "Waterfall"]
    # HM: MIN is int → enum_value_is_index=True → wire value is INT index
    cases = [
        ("effect=0", 0),
        ("effect=1", 1),
        ("effect=2", 2),
    ]
    inputs = []
    for label, idx in cases:
        wire_value = _action_select_prepare(value_list, idx, enum_value_is_index=True)
        inputs.append({
            "label": label,
            "calls": [{"method": "SetValue", "address": "EFF0001:1", "parameter": "PROGRAM", "value": wire_value}],
        })
    _write_snapshot("EffectLight", "SetEffect", inputs)


def _gen_sound_player_play_sound() -> None:
    """
    SoundPlayer PlaySound: aiohomematic sends LEVEL, SOUNDFILE and REPETITIONS.
    It does NOT send DURATION_UNIT / DURATION_VALUE in play_sound
    (those are only sent by stop_sound).  Loom currently sends extra
    DURATION_UNIT=0 and DURATION_VALUE=10 alongside the audio parameters.
    """
    soundfile_list = ["SOUNDFILE_001", "SOUNDFILE_002", "SOUNDFILE_003", "SOUNDFILE_004", "SOUNDFILE_005"]
    # Maps to generator_test.go PlayConfig cases:
    # file=1,rep=0  → RepetitionsIndex=0 → NO_REPETITION
    # file=3,rep=2  → RepetitionsIndex=2 → REPETITIONS_002 (aiohomematic 3-digit)
    # file=5,loop   → Loop=true          → INFINITE_REPETITIONS
    cases = [
        ("file=1,rep=0", "SOUNDFILE_001", 0.8, 0),
        ("file=3,rep=2", "SOUNDFILE_003", 0.5, 2),
        ("file=5,loop", "SOUNDFILE_005", 1.0, -1),  # Loop → infinite → -1
    ]
    inputs = []
    for label, soundfile, volume, rep_count in cases:
        rep_str = _convert_repetitions(repetitions=rep_count)
        inputs.append({
            "label": label,
            "calls": [{
                "method": "PutParamset",
                "address": "MP3P0001:2",
                "paramset_key": "VALUES",
                "put_values": {
                    "LEVEL": volume,
                    "REPETITIONS": rep_str,
                    "SOUNDFILE": soundfile,
                },
            }],
        })
    _write_snapshot("SoundPlayer", "PlaySound", inputs)


def _gen_siren_turn_off() -> None:
    """
    Siren TurnOff: sends DP-defaults for ACOUSTIC_ALARM_SELECTION and
    OPTICAL_ALARM_SELECTION (typically "DISABLE_ACOUSTIC_SIGNAL" /
    "DISABLE_OPTICAL_SIGNAL"), plus DURATION_VALUE default send.
    Loom incorrectly sends empty string "".
    """
    # From the siren fixture in generator_test.go, value_list[0] is the default:
    # ACOUSTIC_ALARM_SELECTION: ["DISABLE_ACOUSTIC_SIGNAL", "FREQUENCY_RISING", "FREQUENCY_FALLING"]
    # OPTICAL_ALARM_SELECTION:  same (in the test fixture the first entry is the default via _min)
    # DURATION_UNIT / DURATION_VALUE: defaults are 0
    inputs = [{
        "label": "priority=normal",
        "calls": [{
            "method": "PutParamset",
            "address": "ASIR0001:3",
            "paramset_key": "VALUES",
            "put_values": {
                "ACOUSTIC_ALARM_SELECTION": "DISABLE_ACOUSTIC_SIGNAL",
                "DURATION_UNIT": 0,
                "DURATION_VALUE": 0,
                "OPTICAL_ALARM_SELECTION": "DISABLE_OPTICAL_SIGNAL",
            },
        }],
    }]
    _write_snapshot("Siren", "TurnOff", inputs)


def _gen_siren_turn_on() -> None:
    """
    Siren TurnOn: sends ACOUSTIC_ALARM_SELECTION, OPTICAL_ALARM_SELECTION,
    and DURATION (as a combined timer). Loom misses DURATION.
    The duration default is 0 (combined timer sends DURATION_VALUE=0, DURATION_UNIT=0).
    """
    inputs = [{
        "label": "acoustic=FREQUENCY_RISING,optical=BLINKING_RED",
        "calls": [{
            "method": "PutParamset",
            "address": "ASIR0001:3",
            "paramset_key": "VALUES",
            "put_values": {
                "ACOUSTIC_ALARM_SELECTION": "FREQUENCY_RISING",
                "DURATION_UNIT": 0,
                "DURATION_VALUE": 0,
                "OPTICAL_ALARM_SELECTION": "BLINKING_RED",
            },
        }],
    }]
    _write_snapshot("Siren", "TurnOn", inputs)


def _gen_climate_rf_set_profile() -> None:
    """
    ClimateRF SetProfile: two put_paramset calls. AUTO_MODE/BOOST_MODE are VALUES
    parameters on the climate channel; WEEK_PROGRAM_POINTER is a MASTER parameter
    on the device root (real device: HM-TC-IT-WM-W-EU VCU0000341/MASTER holds
    WEEK_PROGRAM_POINTER and TEMPERATURE_OFFSET, never a VALUES paramset). Because
    @bind_collector groups by (channel, paramset), the VALUES pair and the MASTER
    pointer flush as separate PutParamset calls.
    """
    profiles = [
        ("WeekProgram1", True, False, "WEEK PROGRAM 1"),
        ("WeekProgram2", True, False, "WEEK PROGRAM 2"),
        ("WeekProgram3", True, False, "WEEK PROGRAM 3"),
    ]
    inputs = []
    for label, auto_mode, boost_mode, week_prog in profiles:
        inputs.append({
            "label": label,
            "calls": [
                {
                    "method": "PutParamset",
                    "address": "RFTHR0001:1",
                    "paramset_key": "VALUES",
                    "put_values": {
                        "AUTO_MODE": auto_mode,
                        "BOOST_MODE": boost_mode,
                    },
                },
                {
                    "method": "PutParamset",
                    "address": "RFTHR0001",
                    "paramset_key": "MASTER",
                    "put_values": {
                        "WEEK_PROGRAM_POINTER": week_prog,
                    },
                },
            ],
        })
    _write_snapshot("ClimateRF", "SetProfile", inputs)


def _gen_color_light_set_color() -> None:
    """
    ColorLight SetColor: should be 1 put_paramset (HUE + SATURATION atomically
    via @bind_collector / CombinedHsColorField). Loom sends 2 separate SetValues.
    """
    cases = [
        ("hue=0,sat=1.0", 0, 1.0),
        ("hue=120,sat=0.8", 120, 0.8),
        ("hue=240,sat=0.5", 240, 0.5),
    ]
    inputs = []
    for label, hue, sat in cases:
        inputs.append({
            "label": label,
            "calls": [{
                "method": "PutParamset",
                "address": "COL0001:1",
                "paramset_key": "VALUES",
                "put_values": {
                    "HUE": hue,
                    "SATURATION": sat,
                },
            }],
        })
    _write_snapshot("ColorLight", "SetColor", inputs)


def _gen_rgbw_set_color() -> None:
    """
    RGBWLight SetColor: should be 1 put_paramset (HUE + SATURATION atomically).
    Loom sends 2 separate SetValues.
    """
    cases = [
        ("hue=0,sat=1.0", 0, 1.0),
        ("hue=180,sat=0.7", 180, 0.7),
    ]
    inputs = []
    for label, hue, sat in cases:
        inputs.append({
            "label": label,
            "calls": [{
                "method": "PutParamset",
                "address": "RGBW0001:1",
                "paramset_key": "VALUES",
                "put_values": {
                    "HUE": hue,
                    "SATURATION": sat,
                },
            }],
        })
    _write_snapshot("RGBWLight", "SetColor", inputs)


def _gen_blind_set_combined() -> None:
    """
    Blind SetCombined: on first call (no prior target level observed),
    aiohomematic does NOT send STOP — only COMBINED_PARAMETER.
    Loom always sends STOP.
    """
    # level=0.5, tilt=0.25 → COMBINED_PARAMETER = "L2=25,L=50"
    inputs = [{
        "label": "level=0.5,tilt=0.25",
        "calls": [{
            "method": "SetValue",
            "address": "BBL0001:1",
            "parameter": "COMBINED_PARAMETER",
            "value": "L2=25,L=50",
        }],
    }]
    _write_snapshot("Blind", "SetCombined", inputs)


def _gen_blind_set_tilt() -> None:
    """
    Blind SetTilt: on first call (no prior target level observed),
    aiohomematic does NOT send STOP — only COMBINED_PARAMETER.
    Loom always sends STOP.
    """
    cases = [
        ("tilt=0.0", "L2=0,L=100"),   # tilt=0 → L2=0, level kept at 1.0 (default open)
        ("tilt=0.5", "L2=50,L=100"),
        ("tilt=1.0", "L2=100,L=100"),
    ]
    inputs = []
    for label, combined_val in cases:
        inputs.append({
            "label": label,
            "calls": [{
                "method": "SetValue",
                "address": "BBL0001:1",
                "parameter": "COMBINED_PARAMETER",
                "value": combined_val,
            }],
        })
    _write_snapshot("Blind", "SetTilt", inputs)


def _gen_text_display_write_rows() -> None:
    """
    TextDisplay WriteRows: aiohomematic calls send_text() once per row, each
    call wrapped in its own Collector, so every row lands as ONE put_paramset
    on the device's single display channel (text_display.py: send_text
    writes DISPLAY_DATA_BACKGROUND_COLOR, _TEXT_COLOR, _ICON, _ALIGNMENT,
    _STRING, _ID, then DISPLAY_DATA_COMMIT — "must be last" per the source
    comment — all via the same collector, so they flush as one call).
    DISPLAY_DATA_ID selects which row a write applies to; there is no
    per-row *channel* to address — HmIP-WRCD, the only text-display model,
    carries every DISPLAY_DATA_* parameter on one channel alone
    (../godevccu/internal/embed/data/paramset_descriptions/HmIP-WRCD.json).
    Loom's WriteRows emits the identical sequence: it calls its own Write per
    row, which applies the row defaults and carries DISPLAY_DATA_COMMIT inside
    the row's paramset. The address here is the fixture's channel, the same one
    every other TextDisplay snapshot uses, so the comparison is about wire
    shape rather than about which channel the fixture happened to pick.
    """
    rows = [
        {"ID": 1, "Text": "Line one"},
        {"ID": 2, "Text": "Line two"},
        {"ID": 3, "Text": "Line three"},
    ]
    calls = []
    for row in rows:
        calls.append({
            "method": "PutParamset",
            "address": "SDV0001:1",
            "paramset_key": "VALUES",
            "put_values": {
                "DISPLAY_DATA_BACKGROUND_COLOR": "WHITE",
                "DISPLAY_DATA_TEXT_COLOR": "BLACK",
                "DISPLAY_DATA_ICON": "NO_ICON",
                "DISPLAY_DATA_ALIGNMENT": "CENTER",
                "DISPLAY_DATA_STRING": row["Text"],
                "DISPLAY_DATA_ID": row["ID"],
                "DISPLAY_DATA_COMMIT": True,
            },
        })
    inputs = [{"label": "3-rows", "calls": calls}]
    _write_snapshot("TextDisplay", "WriteRows", inputs)


# ── additional known-equivalent snapshots ─────────────────────────────────────

def _gen_switch_turn_off() -> None:
    """Switch TurnOff: equivalent to Loom."""
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "VCU0001:3", "parameter": "STATE", "value": False}],
    }]
    _write_snapshot("Switch", "TurnOff", inputs)


def _gen_blind_open() -> None:
    """
    Blind Open: aiohomematic sends COMBINED_PARAMETER "L2=0,L=100" (fully open).
    Loom matches this output.
    """
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "BBL0001:1", "parameter": "COMBINED_PARAMETER", "value": "L2=0,L=100"}],
    }]
    _write_snapshot("Blind", "Open", inputs)


def _gen_blind_close() -> None:
    """
    Blind Close: aiohomematic sends COMBINED_PARAMETER "L2=0,L=0" (fully closed).
    Loom matches this output.
    """
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "BBL0001:1", "parameter": "COMBINED_PARAMETER", "value": "L2=0,L=0"}],
    }]
    _write_snapshot("Blind", "Close", inputs)


def _gen_blind_open_tilt() -> None:
    """
    Blind OpenTilt: aiohomematic sends COMBINED_PARAMETER "L2=100,L=0".
    Tilt fully open (L2=100), level at minimum.
    """
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "BBL0001:1", "parameter": "COMBINED_PARAMETER", "value": "L2=100,L=0"}],
    }]
    _write_snapshot("Blind", "OpenTilt", inputs)


def _gen_blind_close_tilt() -> None:
    """
    Blind CloseTilt: aiohomematic sends COMBINED_PARAMETER "L2=0,L=0".
    Tilt fully closed (L2=0), level at minimum.
    """
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "BBL0001:1", "parameter": "COMBINED_PARAMETER", "value": "L2=0,L=0"}],
    }]
    _write_snapshot("Blind", "CloseTilt", inputs)


def _gen_cover_open() -> None:
    """Cover Open: aiohomematic sets LEVEL=1.0 (fully open). Equivalent to Loom."""
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "ROLL0001:1", "parameter": "LEVEL", "value": 1.0}],
    }]
    _write_snapshot("Cover", "Open", inputs)


def _gen_cover_close() -> None:
    """Cover Close: aiohomematic sets LEVEL=0.0 (fully closed). Equivalent to Loom."""
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "ROLL0001:1", "parameter": "LEVEL", "value": 0.0}],
    }]
    _write_snapshot("Cover", "Close", inputs)


def _gen_cover_set_position() -> None:
    """Cover SetPosition: aiohomematic sets LEVEL directly. Equivalent to Loom."""
    cases = [
        ("level=0.0", 0.0),
        ("level=0.5", 0.5),
        ("level=1.0", 1.0),
    ]
    inputs = []
    for label, level in cases:
        inputs.append({
            "label": label,
            "calls": [{"method": "SetValue", "address": "ROLL0001:1", "parameter": "LEVEL", "value": level}],
        })
    _write_snapshot("Cover", "SetPosition", inputs)


def _gen_garage_open() -> None:
    """Garage Open: aiohomematic sends DOOR_COMMAND="OPEN" via DpActionSelect. Equivalent to Loom."""
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "MOHO0001:1", "parameter": "DOOR_COMMAND", "value": "OPEN"}],
    }]
    _write_snapshot("Garage", "Open", inputs)


def _gen_garage_close() -> None:
    """Garage Close: aiohomematic sends DOOR_COMMAND="CLOSE". Equivalent to Loom."""
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "MOHO0001:1", "parameter": "DOOR_COMMAND", "value": "CLOSE"}],
    }]
    _write_snapshot("Garage", "Close", inputs)


def _gen_garage_vent() -> None:
    """Garage Vent: aiohomematic sends DOOR_COMMAND="PARTIAL_OPEN". Equivalent to Loom."""
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "MOHO0001:1", "parameter": "DOOR_COMMAND", "value": "PARTIAL_OPEN"}],
    }]
    _write_snapshot("Garage", "Vent", inputs)


def _gen_light_turn_on() -> None:
    """Light TurnOn: aiohomematic sets LEVEL=1.0. Equivalent to Loom."""
    inputs = [{
        "label": "default-brightness",
        "calls": [{"method": "SetValue", "address": "DIM0001:1", "parameter": "LEVEL", "value": 1.0}],
    }]
    _write_snapshot("Light", "TurnOn", inputs)


def _gen_light_turn_off() -> None:
    """Light TurnOff: aiohomematic sets LEVEL=0.0. Equivalent to Loom."""
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "DIM0001:1", "parameter": "LEVEL", "value": 0.0}],
    }]
    _write_snapshot("Light", "TurnOff", inputs)


def _gen_light_set_level() -> None:
    """Light SetLevel: aiohomematic sets LEVEL directly. Equivalent to Loom."""
    cases = [
        ("level=0.0", 0.0),
        ("level=0.5", 0.5),
        ("level=1.0", 1.0),
    ]
    inputs = []
    for label, level in cases:
        inputs.append({
            "label": label,
            "calls": [{"method": "SetValue", "address": "DIM0001:1", "parameter": "LEVEL", "value": level}],
        })
    _write_snapshot("Light", "SetLevel", inputs)


def _gen_color_temp_light_set_kelvin() -> None:
    """ColorTempLight SetKelvin: aiohomematic sets COLOR_TEMPERATURE. Equivalent to Loom."""
    cases = [
        ("kelvin=2700", 2700),
        ("kelvin=4000", 4000),
        ("kelvin=6500", 6500),
    ]
    inputs = []
    for label, kelvin in cases:
        inputs.append({
            "label": label,
            "calls": [{"method": "SetValue", "address": "CT0001:1", "parameter": "COLOR_TEMPERATURE", "value": kelvin}],
        })
    _write_snapshot("ColorTempLight", "SetKelvin", inputs)


def _gen_drg_dali_set_kelvin() -> None:
    """DRGDaliLight SetKelvin: aiohomematic sets COLOR_TEMPERATURE. Equivalent to Loom."""
    inputs = [{
        "label": "kelvin=4000",
        "calls": [{"method": "SetValue", "address": "DALI0001:1", "parameter": "COLOR_TEMPERATURE", "value": 4000}],
    }]
    _write_snapshot("DRGDaliLight", "SetKelvin", inputs)


def _gen_fixed_color_light_set_color() -> None:
    """
    FixedColorLight SetColor: aiohomematic sends COLOR parameter as STRING.
    Note: Loom maps CYAN→TURQUOISE and MAGENTA→PURPLE, matching aiohomematic's mapping.
    Equivalent to Loom.
    """
    cases = [
        ("WHITE", "WHITE"),
        ("RED", "RED"),
        ("GREEN", "GREEN"),
        ("BLUE", "BLUE"),
        ("CYAN", "TURQUOISE"),
        ("YELLOW", "YELLOW"),
        ("MAGENTA", "PURPLE"),
    ]
    inputs = []
    for label, wire_color in cases:
        inputs.append({
            "label": label,
            "calls": [{"method": "SetValue", "address": "FC0001:1", "parameter": "COLOR", "value": wire_color}],
        })
    _write_snapshot("FixedColorLight", "SetColor", inputs)


def _gen_fixed_color_light_set_color_behaviour() -> None:
    """FixedColorLight SetColorBehaviour: both stacks send the label, not an index.

    COLOR_BEHAVIOUR is an ENUM whose MIN is a string on every device that carries it
    (HmIP-BSL: MIN 'OFF', VALUE_LIST OFF, ON, BLINKING_*, FLASH_*, BILLOW_*, OLD_VALUE,
    DO_NOT_CARE), and both stacks key their wire form on that: aiohomematic via
    DpSelect's string branch for HmIP enums, openccu-loom via Select.EnumWireValue.

    This docstring used to claim aiohomematic sends an integer and that the two were
    equivalent. Neither half held: the asserted indices 0/2/3 belong to no device's list,
    and the Go fixture beside the comparison declared a six-entry list invented so that
    those indices would fall out.
    """
    cases = [
        ("DO_NOT_CARE", "DO_NOT_CARE"),
        ("OLD_VALUE", "OLD_VALUE"),
        ("ON", "ON"),
    ]
    inputs = []
    for label, value in cases:
        inputs.append({
            "label": label,
            "calls": [{"method": "SetValue", "address": "FC0001:1", "parameter": "COLOR_BEHAVIOUR", "value": value}],
        })
    _write_snapshot("FixedColorLight", "SetColorBehaviour", inputs)


def _gen_smoke_siren_turn_on() -> None:
    """SmokeSiren TurnOn: aiohomematic sends SMOKE_DETECTOR_COMMAND="INTRUSION_ALARM". Equivalent to Loom."""
    inputs = [{
        "label": "INTRUSION_ALARM",
        "calls": [{"method": "SetValue", "address": "SWSD0001:1", "parameter": "SMOKE_DETECTOR_COMMAND", "value": "INTRUSION_ALARM"}],
    }]
    _write_snapshot("SmokeSiren", "TurnOn", inputs)


def _gen_smoke_siren_turn_off() -> None:
    """SmokeSiren TurnOff: aiohomematic sends SMOKE_DETECTOR_COMMAND="INTRUSION_ALARM_OFF". Equivalent to Loom."""
    inputs = [{
        "label": "INTRUSION_ALARM_OFF",
        "calls": [{"method": "SetValue", "address": "SWSD0001:1", "parameter": "SMOKE_DETECTOR_COMMAND", "value": "INTRUSION_ALARM_OFF"}],
    }]
    _write_snapshot("SmokeSiren", "TurnOff", inputs)


def _gen_irrigation_valve_open() -> None:
    """
    IrrigationValve Open: aiohomematic sends PutParamset {ON_TIME, STATE=True}.
    duration=120s matches the Go snapshot fixture default.
    Equivalent to Loom.
    """
    inputs = [{
        "label": "duration=120s",
        "calls": [{
            "method": "PutParamset",
            "address": "WHS0001:1",
            "paramset_key": "VALUES",
            "put_values": {"ON_TIME": 120, "STATE": True},
        }],
    }]
    _write_snapshot("IrrigationValve", "Open", inputs)


def _gen_irrigation_valve_close() -> None:
    """IrrigationValve Close: aiohomematic sends SetValue STATE=False. Equivalent to Loom."""
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "WHS0001:1", "parameter": "STATE", "value": False}],
    }]
    _write_snapshot("IrrigationValve", "Close", inputs)


def _gen_modulating_valve_set_level() -> None:
    """ModulatingValve SetLevel: aiohomematic sets LEVEL. Equivalent to Loom."""
    cases = [
        ("level=0.0", 0.0),
        ("level=0.5", 0.5),
        ("level=1.0", 1.0),
    ]
    inputs = []
    for label, level in cases:
        inputs.append({
            "label": label,
            "calls": [{"method": "SetValue", "address": "MOD0001:1", "parameter": "LEVEL", "value": level}],
        })
    _write_snapshot("ModulatingValve", "SetLevel", inputs)


def _gen_hood_set_fan_speed() -> None:
    """
    Hood SetFanSpeed: aiohomematic sends LEVEL as integer (0=off, 1=low, 2=medium, 3=high).
    Equivalent to Loom.
    """
    cases = [
        ("OFF", 0),
        ("LOW", 1),
        ("MEDIUM", 2),
        ("HIGH", 3),
    ]
    inputs = []
    for label, level in cases:
        inputs.append({
            "label": label,
            "calls": [{"method": "SetValue", "address": "COOK0001:1", "parameter": "LEVEL", "value": level}],
        })
    _write_snapshot("Hood", "SetFanSpeed", inputs)


def _gen_climate_ip_set_mode() -> None:
    """
    ClimateIP SetMode: aiohomematic uses @bind_collector and sends
    CONTROL_MODE (DpActionInteger) plus SET_POINT_TEMPERATURE for HEAT/OFF modes.
    Matches the Go snapshot output (the Go fixture seeds mode state to avoid
    the is_state_change guard).
    Equivalent to Loom.
    """
    cases = [
        ("mode=auto",
         [{"method": "PutParamset", "address": "BWTH0001:1", "paramset_key": "VALUES",
           "put_values": {"CONTROL_MODE": 0}}]),
        ("mode=heat",
         [{"method": "PutParamset", "address": "BWTH0001:1", "paramset_key": "VALUES",
           "put_values": {"CONTROL_MODE": 1, "SET_POINT_TEMPERATURE": 5}}]),
        ("mode=off",
         [{"method": "PutParamset", "address": "BWTH0001:1", "paramset_key": "VALUES",
           "put_values": {"CONTROL_MODE": 1, "SET_POINT_TEMPERATURE": 4.5}}]),
    ]
    inputs = [{"label": label, "calls": calls} for label, calls in cases]
    _write_snapshot("ClimateIP", "SetMode", inputs)


def _gen_climate_ip_set_temperature() -> None:
    """ClimateIP SetTemperature: aiohomematic sends SET_POINT_TEMPERATURE. Equivalent to Loom."""
    cases = [
        ("temp=5", 5),
        ("temp=20", 20),
        ("temp=30", 30),
    ]
    inputs = []
    for label, temp in cases:
        inputs.append({
            "label": label,
            "calls": [{"method": "SetValue", "address": "BWTH0001:1", "parameter": "SET_POINT_TEMPERATURE", "value": temp}],
        })
    _write_snapshot("ClimateIP", "SetTemperature", inputs)


def _gen_climate_ip_enable_boost() -> None:
    """ClimateIP EnableBoost: aiohomematic sends BOOST_MODE=True. Equivalent to Loom."""
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "BWTH0001:1", "parameter": "BOOST_MODE", "value": True}],
    }]
    _write_snapshot("ClimateIP", "EnableBoost", inputs)


def _gen_climate_ip_disable_boost() -> None:
    """ClimateIP DisableBoost: aiohomematic sends BOOST_MODE=False. Equivalent to Loom."""
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "BWTH0001:1", "parameter": "BOOST_MODE", "value": False}],
    }]
    _write_snapshot("ClimateIP", "DisableBoost", inputs)


def _gen_climate_ip_set_profile() -> None:
    """
    ClimateIP SetProfile (week program), already-in-AUTO scenario: writes a
    single ACTIVE_PROFILE SetValue. Mirrors CustomDpIpThermostat.set_profile's
    `if self.mode != ClimateMode.AUTO` guard (climate.py:859-861) being false,
    matching tests/test_model_climate.py:1259-1266, which drives SET_POINT_MODE
    to AUTO via a data-point event before calling
    set_profile(profile=WEEK_PROGRAM_1) and asserts exactly this call.
    When the thermostat is NOT already in AUTO, set_profile also writes
    CONTROL_MODE and BOOST_MODE first — and because those two and
    ACTIVE_PROFILE are all VALUES parameters on the same channel,
    @bind_collector batches all three into one put_paramset instead of one
    round-trip each (CallParameterCollector.add_data_point / _send_paramset,
    model/data_point.py:1648-1667,1724-1776). That scenario is not captured
    here — the Go side pins it with a table test instead of a wire snapshot,
    since it depends on locally-observed device state rather than a fixed
    literal input.
    """
    cases = [
        ("WeekProgram1", 1),
        ("WeekProgram2", 2),
        ("WeekProgram3", 3),
    ]
    inputs = []
    for label, idx in cases:
        inputs.append({
            "label": f"profile={label}",
            "calls": [{"method": "SetValue", "address": "BWTH0001:1", "parameter": "ACTIVE_PROFILE", "value": idx}],
        })
    _write_snapshot("ClimateIP", "SetProfile", inputs)


def _gen_climate_rf_set_mode() -> None:
    """
    ClimateRF SetMode: aiohomematic uses RF thermostat (HM-CC-RT-DN).
    HEAT → SetValue MANU_MODE=5 (min temp = 5.0 for the fixture).
    AUTO → SetValue AUTO_MODE=True.
    OFF  → PutParamset {MANU_MODE: 5, SET_TEMPERATURE: 5} (MANU + off-temp 5.0).
    Note: Loom's OFF path sends SET_TEMPERATURE=5.0 (not 4.5 = _OFF_TEMPERATURE),
    because the ClimateRF fixture uses a different min_temp=5.0.
    Equivalent to Loom for the fixture-configured min_temp.
    """
    cases = [
        ("HEAT",
         [{"method": "SetValue", "address": "RFTHR0001:1", "parameter": "MANU_MODE", "value": 5}]),
        ("AUTO",
         [{"method": "SetValue", "address": "RFTHR0001:1", "parameter": "AUTO_MODE", "value": True}]),
        ("OFF",
         [{"method": "PutParamset", "address": "RFTHR0001:1", "paramset_key": "VALUES",
           "put_values": {"MANU_MODE": 5, "SET_TEMPERATURE": 5}}]),
    ]
    inputs = [{"label": label, "calls": calls} for label, calls in cases]
    _write_snapshot("ClimateRF", "SetMode", inputs)


def _gen_climate_rf_set_temperature() -> None:
    """ClimateRF SetTemperature: aiohomematic sends SET_TEMPERATURE. Equivalent to Loom."""
    cases = [
        ("temp=5", 5),
        ("temp=15", 15),
        ("temp=30", 30),
    ]
    inputs = []
    for label, temp in cases:
        inputs.append({
            "label": label,
            "calls": [{"method": "SetValue", "address": "RFTHR0001:1", "parameter": "SET_TEMPERATURE", "value": temp}],
        })
    _write_snapshot("ClimateRF", "SetTemperature", inputs)


def _gen_sound_player_stop_sound() -> None:
    """
    SoundPlayer StopSound: aiohomematic sends PutParamset {DURATION_UNIT=0, DURATION_VALUE=0, LEVEL=0}.
    Equivalent to Loom.
    """
    inputs = [{
        "label": "priority=normal",
        "calls": [{
            "method": "PutParamset",
            "address": "MP3P0001:2",
            "paramset_key": "VALUES",
            "put_values": {"DURATION_UNIT": 0, "DURATION_VALUE": 0, "LEVEL": 0},
        }],
    }]
    _write_snapshot("SoundPlayer", "StopSound", inputs)


def _gen_sound_player_led_turn_on() -> None:
    """
    SoundPlayerLED TurnOn: aiohomematic sends PutParamset with COLOR, LEVEL, ON_TIME,
    ON_TIME_LIST_1 (flash interval), RAMP_TIME, REPETITIONS.
    Equivalent to Loom (brightness=128/255 ≈ 0.5020, flash=500ms, rep=3).
    """
    inputs = [{
        "label": "brightness=128,flash=500ms,rep=3",
        "calls": [{
            "method": "PutParamset",
            "address": "MP3P0001:6",
            "paramset_key": "VALUES",
            "put_values": {
                "COLOR": "WHITE",
                "LEVEL": 0.5019607843137255,
                "ON_TIME": 0,
                "ON_TIME_LIST_1": "500MS",
                "RAMP_TIME": 0,
                "REPETITIONS": "REPETITIONS_003",
            },
        }],
    }]
    _write_snapshot("SoundPlayerLED", "TurnOn", inputs)


def _gen_sound_player_led_turn_off() -> None:
    """
    SoundPlayerLED TurnOff: SoundPlayerLED is a Loom-specific type with no
    aiohomematic equivalent. The reference is derived from the Go implementation
    which sends PutParamset {COLOR="BLACK", ON_TIME=0} to atomically clear
    the timer and colour state.
    """
    inputs = [{
        "label": "priority=normal",
        "calls": [{
            "method": "PutParamset",
            "address": "MP3P0001:6",
            "paramset_key": "VALUES",
            "put_values": {
                "COLOR": "BLACK",
                "ON_TIME": 0,
            },
        }],
    }]
    _write_snapshot("SoundPlayerLED", "TurnOff", inputs)


def _gen_text_display_write() -> None:
    """
    TextDisplay Write (single-row): aiohomematic sends per-row PutParamset with
    all display fields + DISPLAY_DATA_COMMIT=True in the same paramset.
    This is the HmIP-SWDO-I pattern where commit is included in each call.
    Matches the Go Write snapshot output.
    Equivalent to Loom.
    """
    cases = [
        ("row1-text", 1, "Hello"),
        ("row2-text", 2, "World"),
        ("row3-empty", 3, ""),
    ]
    inputs = []
    for label, row_id, text in cases:
        inputs.append({
            "label": label,
            "calls": [{
                "method": "PutParamset",
                "address": "SDV0001:1",
                "paramset_key": "VALUES",
                "put_values": {
                    "DISPLAY_DATA_ALIGNMENT": "CENTER",
                    "DISPLAY_DATA_BACKGROUND_COLOR": "WHITE",
                    "DISPLAY_DATA_COMMIT": True,
                    "DISPLAY_DATA_ICON": "NO_ICON",
                    "DISPLAY_DATA_ID": row_id,
                    "DISPLAY_DATA_STRING": text,
                    "DISPLAY_DATA_TEXT_COLOR": "BLACK",
                },
            }],
        })
    _write_snapshot("TextDisplay", "Write", inputs)


def _gen_text_display_clear() -> None:
    """
    TextDisplay Clear: aiohomematic clears a single row with empty string + commit.
    Matches the Go Clear snapshot.
    Equivalent to Loom.
    """
    inputs = [{
        "label": "row1",
        "calls": [{
            "method": "PutParamset",
            "address": "SDV0001:1",
            "paramset_key": "VALUES",
            "put_values": {
                "DISPLAY_DATA_ALIGNMENT": "CENTER",
                "DISPLAY_DATA_BACKGROUND_COLOR": "WHITE",
                "DISPLAY_DATA_COMMIT": True,
                "DISPLAY_DATA_ICON": "NO_ICON",
                "DISPLAY_DATA_ID": 1,
                "DISPLAY_DATA_STRING": "",
                "DISPLAY_DATA_TEXT_COLOR": "BLACK",
            },
        }],
    }]
    _write_snapshot("TextDisplay", "Clear", inputs)


def _gen_text_display_write_with_sound() -> None:
    """
    TextDisplay WriteWithSound: adds ACOUSTIC_NOTIFICATION_SELECTION to the Write paramset.
    Equivalent to Loom.
    """
    inputs = [{
        "label": "row1-sound=LONG_SHORT",
        "calls": [{
            "method": "PutParamset",
            "address": "SDV0001:1",
            "paramset_key": "VALUES",
            "put_values": {
                "ACOUSTIC_NOTIFICATION_SELECTION": "LONG_SHORT",
                "DISPLAY_DATA_ALIGNMENT": "CENTER",
                "DISPLAY_DATA_BACKGROUND_COLOR": "WHITE",
                "DISPLAY_DATA_COMMIT": True,
                "DISPLAY_DATA_ICON": "NO_ICON",
                "DISPLAY_DATA_ID": 1,
                "DISPLAY_DATA_STRING": "Alert",
                "DISPLAY_DATA_TEXT_COLOR": "BLACK",
            },
        }],
    }]
    _write_snapshot("TextDisplay", "WriteWithSound", inputs)


# ── known-equivalent snapshots (documented for completeness) ──────────────────

def _gen_switch_turn_on() -> None:
    """Switch TurnOn: equivalent to Loom."""
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "VCU0001:3", "parameter": "STATE", "value": True}],
    }]
    _write_snapshot("Switch", "TurnOn", inputs)


def _gen_switch_turn_on_for() -> None:
    """Switch TurnOnFor 60s: equivalent to Loom (PutParamset {ON_TIME, STATE})."""
    inputs = [{
        "label": "duration=60s",
        "calls": [{
            "method": "PutParamset",
            "address": "VCU0001:3",
            "paramset_key": "VALUES",
            "put_values": {"ON_TIME": 60.0, "STATE": True},
        }],
    }]
    _write_snapshot("Switch", "TurnOnFor", inputs)


def _gen_lock_lock() -> None:
    """Lock.Lock: equivalent to Loom."""
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "DLD0001:1", "parameter": "LOCK_TARGET_LEVEL", "value": "LOCKED"}],
    }]
    _write_snapshot("Lock", "Lock", inputs)


def _gen_lock_unlock() -> None:
    """Lock.Unlock: equivalent to Loom."""
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "DLD0001:1", "parameter": "LOCK_TARGET_LEVEL", "value": "UNLOCKED"}],
    }]
    _write_snapshot("Lock", "Unlock", inputs)


def _gen_lock_open() -> None:
    """Lock.Open: equivalent to Loom."""
    inputs = [{
        "label": "priority=normal",
        "calls": [{"method": "SetValue", "address": "DLD0001:1", "parameter": "LOCK_TARGET_LEVEL", "value": "OPEN"}],
    }]
    _write_snapshot("Lock", "Open", inputs)


# ── main ───────────────────────────────────────────────────────────────────────

def main() -> None:
    """Generate all reference snapshots."""
    generators = [
        # Drift snapshots (these will FAIL the Go compare test until fixed)
        ("DRGDaliLight", "SetEffect", _gen_drg_dali_set_effect),
        ("RGBWLight", "SetEffect", _gen_rgbw_set_effect),
        ("EffectLight", "SetEffect", _gen_effect_light_set_effect),
        ("SoundPlayer", "PlaySound", _gen_sound_player_play_sound),
        ("Siren", "TurnOff", _gen_siren_turn_off),
        ("Siren", "TurnOn", _gen_siren_turn_on),
        ("ClimateRF", "SetProfile", _gen_climate_rf_set_profile),
        ("ColorLight", "SetColor", _gen_color_light_set_color),
        ("RGBWLight", "SetColor", _gen_rgbw_set_color),
        ("Blind", "SetCombined", _gen_blind_set_combined),
        ("Blind", "SetTilt", _gen_blind_set_tilt),
        ("TextDisplay", "WriteRows", _gen_text_display_write_rows),
        # Known-equivalent snapshots (will PASS the compare test)
        ("Switch", "TurnOn", _gen_switch_turn_on),
        ("Switch", "TurnOff", _gen_switch_turn_off),
        ("Switch", "TurnOnFor", _gen_switch_turn_on_for),
        ("Blind", "Open", _gen_blind_open),
        ("Blind", "Close", _gen_blind_close),
        ("Blind", "OpenTilt", _gen_blind_open_tilt),
        ("Blind", "CloseTilt", _gen_blind_close_tilt),
        ("Cover", "Open", _gen_cover_open),
        ("Cover", "Close", _gen_cover_close),
        ("Cover", "SetPosition", _gen_cover_set_position),
        ("Garage", "Open", _gen_garage_open),
        ("Garage", "Close", _gen_garage_close),
        ("Garage", "Vent", _gen_garage_vent),
        ("Light", "TurnOn", _gen_light_turn_on),
        ("Light", "TurnOff", _gen_light_turn_off),
        ("Light", "SetLevel", _gen_light_set_level),
        ("ColorTempLight", "SetKelvin", _gen_color_temp_light_set_kelvin),
        ("DRGDaliLight", "SetKelvin", _gen_drg_dali_set_kelvin),
        ("FixedColorLight", "SetColor", _gen_fixed_color_light_set_color),
        ("FixedColorLight", "SetColorBehaviour", _gen_fixed_color_light_set_color_behaviour),
        ("SmokeSiren", "TurnOn", _gen_smoke_siren_turn_on),
        ("SmokeSiren", "TurnOff", _gen_smoke_siren_turn_off),
        ("IrrigationValve", "Open", _gen_irrigation_valve_open),
        ("IrrigationValve", "Close", _gen_irrigation_valve_close),
        ("ModulatingValve", "SetLevel", _gen_modulating_valve_set_level),
        ("Hood", "SetFanSpeed", _gen_hood_set_fan_speed),
        ("ClimateIP", "SetMode", _gen_climate_ip_set_mode),
        ("ClimateIP", "SetTemperature", _gen_climate_ip_set_temperature),
        ("ClimateIP", "EnableBoost", _gen_climate_ip_enable_boost),
        ("ClimateIP", "DisableBoost", _gen_climate_ip_disable_boost),
        ("ClimateIP", "SetProfile", _gen_climate_ip_set_profile),
        ("ClimateRF", "SetMode", _gen_climate_rf_set_mode),
        ("ClimateRF", "SetTemperature", _gen_climate_rf_set_temperature),
        ("SoundPlayer", "StopSound", _gen_sound_player_stop_sound),
        ("SoundPlayerLED", "TurnOn", _gen_sound_player_led_turn_on),
        ("SoundPlayerLED", "TurnOff", _gen_sound_player_led_turn_off),
        ("TextDisplay", "Write", _gen_text_display_write),
        ("TextDisplay", "Clear", _gen_text_display_clear),
        ("TextDisplay", "WriteWithSound", _gen_text_display_write_with_sound),
        ("Lock", "Lock", _gen_lock_lock),
        ("Lock", "Unlock", _gen_lock_unlock),
        ("Lock", "Open", _gen_lock_open),
    ]

    written = 0
    for dp_type, setter, fn in generators:
        try:
            fn()
            print(f"  OK  {dp_type}__{setter}.json")
            written += 1
        except Exception as exc:  # noqa: BLE001
            print(f"  ERR {dp_type}__{setter}: {exc}", file=sys.stderr)

    print(f"\nWrote {written} reference snapshots to {_OUT_DIR}/")
    print(f"aiohomematic version: {_AIOHM_VERSION}")


if __name__ == "__main__":
    main()
