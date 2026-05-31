#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 openccu-loom authors.
#
# generate_ha_descriptions.py — emit Go code for the canonical
# HA-Entity-Description tables that `homematicip_local` registers in
# `entity_helpers/registry.py`.
#
# Output:
#   internal/north/mqtt/entity_descriptions_generated.go
#
# The generated file defines:
#   - HmipLocalDescription struct (mirrors HmEntityDescription's
#     wire-relevant fields)
#   - HmipLocalRule struct (mirrors EntityDescriptionRule's matching
#     criteria)
#   - hmipLocalRules variable: every registered rule, priority-sorted
#     per category
#   - HmipLocalLookup(category, parameter, model, unit, postfix,
#     varName) function
#
# This file is **reference data**, not yet wired into the discovery
# builder. The wiring is a follow-up wave so the change can be
# reviewed in isolation. The `parity_audit.md` §17 baseline is the
# audit ground-truth; once this generator's output is wired in, drift
# should drop materially in `name_source`, `suggested_display_precision`,
# `device_class`, `enabled_by_default`, and `translation_key`.

from __future__ import annotations

import dataclasses
import importlib.metadata
import os
import sys
from datetime import UTC, datetime
from pathlib import Path
from typing import Any


def _ensure_venv() -> None:
    try:
        import homeassistant  # noqa: F401
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
                f"[generate-descriptions] re-execing in homematicip_local venv: {cand}",
                file=sys.stderr,
            )
            os.execv(cand, [cand, *sys.argv])


_ensure_venv()

_GITHUB_ROOT = Path(__file__).resolve().parents[2]
_HMIP_LOCAL_ROOT = _GITHUB_ROOT / "homematicip_local"
if _HMIP_LOCAL_ROOT.is_dir() and str(_HMIP_LOCAL_ROOT) not in sys.path:
    sys.path.insert(0, str(_HMIP_LOCAL_ROOT))


# Importing the entity_helpers package runs `_initialize_registry()`
# (entity_helpers/__init__.py:85), which calls `REGISTRY.register_all(
# get_all_rules())` once. Do NOT call it again — that would double
# every rule.
from custom_components.homematicip_local.entity_helpers.registry import REGISTRY


_REPO_ROOT = Path(__file__).resolve().parents[1]
_OUTPUT = _REPO_ROOT / "internal" / "north" / "mqtt" / "entity_descriptions_generated.go"


# ──────────────────────────────────────────────────────────────────────────────
# Value coercion — strip HA enum identity, return raw strings/numbers.
# ──────────────────────────────────────────────────────────────────────────────


def _val(v: Any) -> Any:
    """Strip HA enum types to their string values; passthrough primitives."""
    if v is None:
        return None
    # Filter HA UndefinedType sentinel.
    if type(v).__name__ == "UndefinedType":
        return None
    # StrEnum / IntEnum
    if hasattr(v, "value") and not isinstance(v, (str, int, float, bool)):
        return v.value
    return v


def _go_string(s: str | None) -> str:
    if s is None or s == "":
        return '""'
    # Escape for Go.
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def _go_str_slice(items: tuple[str, ...] | list[str] | None) -> str:
    if not items:
        return "nil"
    return "[]string{" + ", ".join(_go_string(str(x)) for x in items) + "}"


def _go_int_ptr(v: int | None) -> str:
    if v is None:
        return "nil"
    return f"hmipIntPtr({int(v)})"


def _go_float_ptr(v: float | None) -> str:
    if v is None:
        return "nil"
    return f"hmipFloat64Ptr({float(v)})"


def _go_bool(v: bool) -> str:
    return "true" if v else "false"


# ──────────────────────────────────────────────────────────────────────────────
# Description / Rule serialisation
# ──────────────────────────────────────────────────────────────────────────────


def _description_literal(desc: Any) -> str:
    if desc is None:
        return "HmipLocalDescription{}"
    fields = {f.name for f in dataclasses.fields(desc)}
    out: list[str] = []
    out.append(f"Key: {_go_string(getattr(desc, 'key', '') or '')}")
    if "device_class" in fields:
        out.append(f"DeviceClass: {_go_string(_val(desc.device_class))}")
    if "state_class" in fields:
        out.append(f"StateClass: {_go_string(_val(desc.state_class))}")
    if "entity_category" in fields:
        out.append(f"EntityCategory: {_go_string(_val(desc.entity_category))}")
    if "icon" in fields:
        out.append(f"Icon: {_go_string(_val(desc.icon))}")
    if "translation_key" in fields:
        out.append(f"TranslationKey: {_go_string(_val(desc.translation_key))}")
    if "native_unit_of_measurement" in fields:
        out.append(
            f"UnitOfMeasurement: {_go_string(_val(desc.native_unit_of_measurement))}"
        )
    elif "unit_of_measurement" in fields:
        out.append(
            f"UnitOfMeasurement: {_go_string(_val(desc.unit_of_measurement))}"
        )
    if "suggested_display_precision" in fields:
        out.append(
            f"SuggestedDisplayPrecision: {_go_int_ptr(getattr(desc, 'suggested_display_precision', None))}"
        )
    if "entity_registry_enabled_default" in fields:
        v = getattr(desc, "entity_registry_enabled_default", True)
        # Default in HA is True; emit nil when default to keep file small,
        # explicit pointer when False.
        if v is False:
            out.append("EnabledByDefault: hmipBoolPtr(false)")
        # else: omit (= nil = default)
    if "options" in fields:
        opts = getattr(desc, "options", None)
        if opts:
            out.append(f"Options: {_go_str_slice([str(x) for x in opts])}")
    if "name_source" in fields:
        ns = _val(getattr(desc, "name_source", None))
        if ns:
            out.append(f"NameSource: {_go_string(str(ns))}")
    if "multiplier" in fields:
        mult = getattr(desc, "multiplier", None)
        if mult is not None:
            out.append(f"Multiplier: {_go_float_ptr(float(mult))}")

    body = ", ".join(out)
    return "HmipLocalDescription{" + body + "}"


def _rule_literal(rule: Any) -> str:
    parts: list[str] = []
    parts.append(f"Description: {_description_literal(rule.description)}")
    parts.append(f"Category: {_go_string(_val(rule.category))}")
    parts.append(f"Parameters: {_go_str_slice(rule.parameters)}")
    parts.append(f"Devices: {_go_str_slice(rule.devices)}")
    parts.append(f"Unit: {_go_string(rule.unit)}")
    parts.append(f"Postfix: {_go_string(rule.postfix)}")
    parts.append(f"VarNameContains: {_go_string(rule.var_name_contains)}")
    parts.append(f"Priority: {int(rule.priority)}")
    return "{" + ", ".join(parts) + "}"


# ──────────────────────────────────────────────────────────────────────────────
# File template
# ──────────────────────────────────────────────────────────────────────────────


_HEADER = """// SPDX-License-Identifier: MIT
// Copyright (C) 2026 openccu-loom authors.
//
// Code generated by `script/generate_ha_descriptions.py`. DO NOT EDIT.
//
// Source: homematicip_local @ {ha_version} (REGISTRY +
//         entity_helpers/descriptions/*.py).
// Generated: {generated_at}
//
// This file mirrors the HA-Entity-Description tables that
// `homematicip_local` registers in its `entity_helpers/registry.py`.
// `HmipLocalLookup` returns the canonical EntityDescription for a
// (category, parameter, model, unit, postfix) tuple — the same data
// the HA-native integration uses to populate `_attr_device_class`,
// `_attr_state_class`, `_attr_entity_category`, `_attr_icon`,
// `_attr_native_unit_of_measurement`,
// `_attr_entity_registry_enabled_default`,
// `_attr_suggested_display_precision`, `_attr_options`,
// `_attr_translation_key`, `_attr_name`, and the per-Number
// `multiplier`.
//
// Reference data only — wiring this into the discovery builder is a
// follow-up; the parity_audit §17 baseline is what production behavior
// is measured against.

package mqtt

import "strings"

// HmipLocalDescription is the wire-relevant subset of
// `homematicip_local`'s `HmEntityDescription`. Fields are ordered to
// match the Python dataclass; pointer types denote optional values
// (nil = "not set", which is HA's default).
type HmipLocalDescription struct {{
\tKey                       string
\tDeviceClass               string
\tStateClass                string
\tEntityCategory            string
\tIcon                      string
\tTranslationKey            string
\tUnitOfMeasurement         string
\tSuggestedDisplayPrecision *int
\tEnabledByDefault          *bool   // nil = HA default (true)
\tOptions                   []string
\tNameSource                string
\tMultiplier                *float64 // nil = no override
}}

// HmipLocalRule is one entry in the priority-sorted rule list.
// Matching is "all specified criteria must match" (mirrors
// `EntityDescriptionRule.matches`).
type HmipLocalRule struct {{
\tDescription     HmipLocalDescription
\tCategory        string
\tParameters      []string // nil = match any
\tDevices         []string // nil = match any (case-insensitive prefix)
\tUnit            string   // "" = match any
\tPostfix         string   // "" = match any
\tVarNameContains string   // "" = match any
\tPriority        int
}}

func hmipIntPtr(v int) *int          {{ return &v }}
func hmipFloat64Ptr(v float64) *float64 {{ return &v }}
func hmipBoolPtr(v bool) *bool       {{ return &v }}

func paramListContains(list []string, parameter string) bool {{
\tif len(list) == 0 {{
\t\treturn true
\t}}
\tup := strings.ToUpper(parameter)
\tfor _, p := range list {{
\t\tif strings.ToUpper(p) == up {{
\t\t\treturn true
\t\t}}
\t}}
\treturn false
}}

func deviceListMatches(list []string, model string) bool {{
\tif len(list) == 0 {{
\t\treturn true
\t}}
\tlow := strings.ToLower(model)
\tfor _, d := range list {{
\t\tif strings.HasPrefix(low, strings.ToLower(d)) {{
\t\t\treturn true
\t\t}}
\t}}
\treturn false
}}

func varNameMatches(needle, haystack string) bool {{
\tif needle == "" {{
\t\treturn true
\t}}
\treturn strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}}

// HmipLocalLookup returns the canonical EntityDescription for the
// given matching criteria. Returns the first matching rule (rules
// are pre-sorted by descending priority); falls back to the
// per-category default (`hmipLocalDefaults`); finally returns nil
// when neither produces a match.
//
// Empty strings disable that criterion. Pass the wire `parameter`
// in upper case (matches the rule literals; the helper case-folds).
func HmipLocalLookup(category, parameter, model, unit, postfix, varName string) *HmipLocalDescription {{
\tif category == "" {{
\t\treturn nil
\t}}
\tfor i := range hmipLocalRules {{
\t\tr := &hmipLocalRules[i]
\t\tif r.Category != category {{
\t\t\tcontinue
\t\t}}
\t\tif !paramListContains(r.Parameters, parameter) {{
\t\t\tcontinue
\t\t}}
\t\tif !deviceListMatches(r.Devices, model) {{
\t\t\tcontinue
\t\t}}
\t\tif r.Unit != "" && r.Unit != unit {{
\t\t\tcontinue
\t\t}}
\t\tif r.Postfix != "" && !strings.EqualFold(r.Postfix, postfix) {{
\t\t\tcontinue
\t\t}}
\t\tif !varNameMatches(r.VarNameContains, varName) {{
\t\t\tcontinue
\t\t}}
\t\td := r.Description
\t\treturn &d
\t}}
\tif def, ok := hmipLocalDefaults[category]; ok {{
\t\treturn &def
\t}}
\treturn nil
}}

"""


def main() -> int:
    # Side-effect of `import custom_components.homematicip_local.entity_helpers`
    # has already populated REGISTRY (see header comment). Just use it.
    import custom_components.homematicip_local.entity_helpers  # noqa: F401
    from custom_components.homematicip_local.entity_helpers.defaults import (
        DEFAULT_DESCRIPTIONS,
    )

    # Flatten + priority-sort (descending).
    flat: list[Any] = []
    for cat_rules in REGISTRY._rules_by_category.values():  # noqa: SLF001
        flat.extend(cat_rules)
    flat.sort(
        key=lambda r: (
            -int(r.priority),
            _val(r.category) or "",
            r.description.key or "",
        )
    )

    try:
        ha_version = importlib.metadata.version("homematicip_local")
    except Exception:  # noqa: BLE001
        ha_version = "source"

    out_lines: list[str] = []
    out_lines.append(_HEADER.format(
        ha_version=ha_version,
        generated_at=datetime.now(UTC).strftime("%Y-%m-%dT%H:%M:%SZ"),
    ))

    out_lines.append(f"// hmipLocalRules contains {len(flat)} rules from")
    out_lines.append("// homematicip_local/custom_components/homematicip_local/entity_helpers/descriptions/.")
    out_lines.append("var hmipLocalRules = []HmipLocalRule{")
    for rule in flat:
        out_lines.append("\t" + _rule_literal(rule) + ",")
    out_lines.append("}")
    out_lines.append("")
    out_lines.append("// hmipLocalDefaults mirrors homematicip_local's `DEFAULT_DESCRIPTIONS`")
    out_lines.append("// (entity_helpers/defaults.py). Consulted by `HmipLocalLookup` when no")
    out_lines.append("// rule matches the criteria — last-resort per-category description.")
    out_lines.append("var hmipLocalDefaults = map[string]HmipLocalDescription{")
    for cat, desc in sorted(DEFAULT_DESCRIPTIONS.items(), key=lambda kv: str(_val(kv[0]) or "")):
        cat_str = str(_val(cat) or "")
        out_lines.append(f"\t{_go_string(cat_str)}: {_description_literal(desc)},")
    out_lines.append("}")
    out_lines.append("")

    body = "\n".join(out_lines)
    _OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    _OUTPUT.write_text(body, encoding="utf-8")

    print(
        f"wrote {_OUTPUT.relative_to(_REPO_ROOT)}\n"
        f"  rules    : {len(flat)}\n"
        f"  size     : {_OUTPUT.stat().st_size:,} bytes",
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
