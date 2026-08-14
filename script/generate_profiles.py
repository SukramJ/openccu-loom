#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 openccu-loom authors.
"""
Generate openccu-loom's device-profile registry, profile-config catalog,
and default-data-points table from aiohomematic.

The script imports aiohomematic (which must be importable in the active
Python env), reads its in-memory profile registry plus the static
PROFILE_CONFIGS / DEFAULT_DATA_POINTS dicts, and emits three Go source
files plus one contract test:

  - internal/model/custom/generated_profiles.go
      Per-(category, model, profile) registrations from
      DeviceProfileRegistry._configs, including ScheduleChannelNo and
      ExtendedDeviceConfig data, and a pointer into the catalog below.

  - internal/model/custom/generated_profile_configs.go
      Per-DeviceProfile *ProfileConfig literal exposing the channel
      group structure (fields, channel_fields, fixed_channel_fields,
      primary_channel, secondary_channels, state_channel_offset,
      allow_undefined_generic_data_points), plus additional_data_points
      and include_default_data_points. Exported as
      `var ProfileConfigs = map[hmenum.DeviceProfile]*ProfileConfig`.

  - internal/model/custom/generated_default_data_points.go
      DEFAULT_DATA_POINTS as map[int][]hmenum.Parameter (offset → params).

  - tests/contract/profile_parity_generated_test.go
      Pinned-count contract test.

To regenerate by hand:
    python3 script/generate_profiles.py

The script discovers the openccu-loom repo root from its own location, so
no command-line arguments are required.
"""

from __future__ import annotations

import datetime
import re
import shutil
import subprocess
import sys
from pathlib import Path


# ---------------------------------------------------------------------------
# Path discovery
# ---------------------------------------------------------------------------

SCRIPT_DIR = Path(__file__).resolve().parent
REPO_ROOT = SCRIPT_DIR.parent

OUT_PROFILES = REPO_ROOT / "internal" / "model" / "custom" / "generated_profiles.go"
OUT_CONFIGS = REPO_ROOT / "internal" / "model" / "custom" / "generated_profile_configs.go"
OUT_DEFAULTS = REPO_ROOT / "internal" / "model" / "custom" / "generated_default_data_points.go"
OUT_TEST = REPO_ROOT / "tests" / "contract" / "profile_parity_generated_test.go"

PARAMETER_GO = REPO_ROOT / "pkg" / "hmenum" / "parameter.go"
FIELD_GO = REPO_ROOT / "pkg" / "hmenum" / "field.go"
DATAPOINT_GO = REPO_ROOT / "pkg" / "hmenum" / "datapoint.go"


# ---------------------------------------------------------------------------
# Parsers for the existing hmenum Go sources
# ---------------------------------------------------------------------------

_CONST_RE = re.compile(
    r"""^\s+
        (?P<name>[A-Z][A-Za-z0-9]+)\s+
        [A-Za-z][A-Za-z0-9]*\s*=\s*"
        (?P<value>[^"]+)
        "
    """,
    re.VERBOSE | re.MULTILINE,
)


def _load_const_map(path: Path, prefix: str) -> dict[str, str]:
    """Return wire-value → Go-const-name mapping for `<prefix>...` constants."""
    src = path.read_text()
    mapping: dict[str, str] = {}
    for m in _CONST_RE.finditer(src):
        name = m.group("name")
        if not name.startswith(prefix):
            continue
        mapping[m.group("value")] = name
    return mapping


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _product_group_for(model: str) -> str:
    norm = model.lower()
    if norm.startswith("hmip-") or norm.startswith("hmipw-"):
        return "HmIP"
    if norm.startswith("hmw-"):
        return "HmW"
    if norm.startswith("hm-") or norm.startswith("zel") or norm.startswith("bc-"):
        return "HM"
    if norm.startswith("cuxd"):
        return "Virtual"
    return "Unknown"


def _go_string(s: str) -> str:
    escaped = s.replace("\\", "\\\\").replace('"', '\\"')
    return f'"{escaped}"'


def _go_param_ref(wire: str, params: dict[str, str]) -> str:
    """Return Go expression for a Parameter wire value."""
    if wire in params:
        return f"hmenum.{params[wire]}"
    # Fallback: typed string literal (no panic on unknowns).
    return f"hmenum.Parameter({_go_string(wire)})"


def _go_field_ref(wire: str, fields: dict[str, str]) -> str:
    """Return Go expression for a Field wire value."""
    if wire in fields:
        return f"hmenum.{fields[wire]}"
    return f"hmenum.Field({_go_string(wire)})"


def _go_category_ref(wire: str, cats: dict[str, str]) -> str:
    if wire in cats:
        return f"hmenum.{cats[wire]}"
    # Defensive fallback (should never trigger for the categories we use).
    return f"hmenum.DataPointCategory({_go_string(wire)})"


def _go_profile_ref(wire: str) -> str:
    """Return Go expression for a DeviceProfile wire value."""
    return f"hmenum.DeviceProfile({_go_string(wire)})"


# ---------------------------------------------------------------------------
# Field-value emitters (Bare / Visible / Hidden)
# ---------------------------------------------------------------------------


def _go_field_value(field_value, params: dict[str, str]) -> str:
    """
    Render an aiohomematic FieldValue (Parameter | FieldMapping) as a Go
    `FieldValue` literal using the Bare/Visible/Hidden helpers.
    """
    from aiohomematic.const import Parameter
    from aiohomematic.model.custom.profile import FieldMapping

    if isinstance(field_value, FieldMapping):
        param_ref = _go_param_ref(field_value.parameter.value, params)
        if field_value.is_visible is True:
            return f"Visible({param_ref})"
        if field_value.is_visible is False:
            return f"Hidden({param_ref})"
        return f"Bare({param_ref})"
    if isinstance(field_value, Parameter):
        return f"Bare({_go_param_ref(field_value.value, params)})"
    raise TypeError(f"Unexpected FieldValue: {field_value!r}")


def _go_fields_map(fields_map, fields: dict[str, str], params: dict[str, str]) -> str:
    """Render a Mapping[Field, FieldValue] as a Go map literal."""
    if not fields_map:
        return "nil"
    items = sorted(fields_map.items(), key=lambda kv: kv[0].value)
    lines = ["map[hmenum.Field]FieldValue{"]
    for f, v in items:
        lines.append(f"\t\t\t{_go_field_ref(f.value, fields)}: {_go_field_value(v, params)},")
    lines.append("\t\t}")
    return "\n".join(lines)


def _go_channel_fields(
    channel_fields,
    fields: dict[str, str],
    params: dict[str, str],
    *,
    indent_level: int = 2,
) -> str:
    """Render Mapping[int|None, Mapping[Field, FieldValue]] as a Go map."""
    if not channel_fields:
        return "nil"
    pad = "\t" * indent_level
    inner_pad = "\t" * (indent_level + 1)

    def key_repr(k):
        if k is None:
            return "AnyChannelOffset"
        return str(int(k))

    items = sorted(channel_fields.items(), key=lambda kv: (kv[0] is None, kv[0] if kv[0] is not None else 0))
    lines = ["map[int]map[hmenum.Field]FieldValue{"]
    for ch, fmap in items:
        sub_items = sorted(fmap.items(), key=lambda kv: kv[0].value)
        sub_lines = [f"{pad}{key_repr(ch)}: {{"]
        for f, v in sub_items:
            sub_lines.append(
                f"{inner_pad}{_go_field_ref(f.value, fields)}: {_go_field_value(v, params)},"
            )
        sub_lines.append(f"{pad}}},")
        lines.append("\n".join(sub_lines))
    lines.append("\t" * (indent_level - 1) + "}")
    return "\n".join(lines)


def _go_fixed_channel_fields_paramonly(
    fixed_fields,
    fields: dict[str, str],
    params: dict[str, str],
    *,
    indent_level: int = 2,
) -> str:
    """
    Render a Mapping[int, Mapping[Field, Parameter]] as a Go map literal
    suitable for ExtendedDeviceConfig.FixedChannelFields, which is typed
    map[int]map[hmenum.Field]hmenum.Parameter (no FieldValue wrapping).
    """
    if not fixed_fields:
        return "nil"
    pad = "\t" * indent_level
    inner_pad = "\t" * (indent_level + 1)
    items = sorted(fixed_fields.items(), key=lambda kv: kv[0])
    lines = ["map[int]map[hmenum.Field]hmenum.Parameter{"]
    for ch, fmap in items:
        sub_items = sorted(fmap.items(), key=lambda kv: kv[0].value)
        sub_lines = [f"{pad}{int(ch)}: {{"]
        for f, p in sub_items:
            sub_lines.append(
                f"{inner_pad}{_go_field_ref(f.value, fields)}: {_go_param_ref(p.value, params)},"
            )
        sub_lines.append(f"{pad}}},")
        lines.append("\n".join(sub_lines))
    lines.append("\t" * (indent_level - 1) + "}")
    return "\n".join(lines)


def _go_fixed_channel_fields_fieldvalue(
    fixed_fields,
    fields: dict[str, str],
    params: dict[str, str],
    *,
    indent_level: int = 2,
) -> str:
    """
    Render Mapping[int, Mapping[Field, FieldValue]] (i.e. profile-config
    fixed_channel_fields, which permits visible() wrappers) as a Go map
    of map[int]map[hmenum.Field]FieldValue.
    """
    if not fixed_fields:
        return "nil"
    pad = "\t" * indent_level
    inner_pad = "\t" * (indent_level + 1)
    items = sorted(fixed_fields.items(), key=lambda kv: kv[0])
    lines = ["map[int]map[hmenum.Field]FieldValue{"]
    for ch, fmap in items:
        sub_items = sorted(fmap.items(), key=lambda kv: kv[0].value)
        sub_lines = [f"{pad}{int(ch)}: {{"]
        for f, v in sub_items:
            sub_lines.append(
                f"{inner_pad}{_go_field_ref(f.value, fields)}: {_go_field_value(v, params)},"
            )
        sub_lines.append(f"{pad}}},")
        lines.append("\n".join(sub_lines))
    lines.append("\t" * (indent_level - 1) + "}")
    return "\n".join(lines)


def _go_additional_data_points(
    additional,
    params: dict[str, str],
    *,
    indent_level: int = 2,
) -> str:
    """
    Render Mapping[int|tuple[int,...], tuple[Parameter,...]] as a Go map
    of map[int][]hmenum.Parameter. Tuple keys are expanded — every channel
    in the tuple gets the same parameter list.
    """
    if not additional:
        return "nil"
    pad = "\t" * indent_level
    inner_pad = "\t" * (indent_level + 1)

    # Expand tuple keys.
    expanded: dict[int, tuple] = {}
    for k, v in additional.items():
        keys = list(k) if isinstance(k, tuple) else [k]
        for sk in keys:
            expanded[int(sk)] = v

    items = sorted(expanded.items(), key=lambda kv: kv[0])
    lines = ["map[int][]hmenum.Parameter{"]
    for ch, ptuple in items:
        sub_lines = [f"{pad}{ch}: {{"]
        for p in ptuple:
            sub_lines.append(f"{inner_pad}{_go_param_ref(p.value, params)},")
        sub_lines.append(f"{pad}}},")
        lines.append("\n".join(sub_lines))
    lines.append("\t" * (indent_level - 1) + "}")
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# generated_profile_configs.go
# ---------------------------------------------------------------------------


PROFILE_CONFIGS_HEADER = """// SPDX-License-Identifier: MIT
// Copyright (C) 2026 openccu-loom authors.
//
// Code generated by script/generate_profiles.py. DO NOT EDIT.
// Generated at: {generated_at}

package custom

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// GeneratedProfileConfigCount is the number of *ProfileConfig entries
// that this file populates ProfileConfigs with.
const GeneratedProfileConfigCount = {count}

// ProfileConfigs is the auto-generated catalog of every
// PROFILE_CONFIGS entry, keyed by [hmenum.DeviceProfile]. Profile
// literals in [generated_profiles.go] reference entries here by pointer
// via [Profile.Config].
var ProfileConfigs = map[hmenum.DeviceProfile]*ProfileConfig{{
{entries}}}
"""


def _render_profile_config(
    profile_value: str,
    cfg,
    fields: dict[str, str],
    params: dict[str, str],
) -> str:
    """Render one *ProfileConfig literal for the ProfileConfigs map."""
    cg = cfg.channel_group

    primary_set = cg.primary_channel is not None
    primary_value = cg.primary_channel if primary_set else 0

    # Render the optional state-channel-offset pointer literal inline.
    if cg.state_channel_offset is not None:
        sco_lit = f"intPtr({int(cg.state_channel_offset)})"
    else:
        sco_lit = "nil"

    # Secondary channels.
    if cg.secondary_channels:
        sec_lit = "[]int{" + ", ".join(str(int(c)) for c in cg.secondary_channels) + "}"
    else:
        sec_lit = "nil"

    fields_lit = _go_fields_map(cg.fields, fields, params)
    chf_lit = _go_channel_fields(cg.channel_fields, fields, params, indent_level=4)
    fcf_lit = _go_fixed_channel_fields_fieldvalue(cg.fixed_channel_fields, fields, params, indent_level=4)
    add_lit = _go_additional_data_points(cfg.additional_data_points, params, indent_level=3)

    # Build with explicit indentation. The map's outer indent is one tab.
    lines: list[str] = []
    lines.append(f"\t{_go_profile_ref(profile_value)}: {{")
    lines.append(f"\t\tProfileType: {_go_profile_ref(profile_value)},")
    lines.append("\t\tChannelGroup: ChannelGroupConfig{")
    lines.append(f"\t\t\tPrimaryChannel:                  {int(primary_value)},")
    lines.append(f"\t\t\tPrimaryChannelSet:               {str(primary_set).lower()},")
    lines.append(f"\t\t\tSecondaryChannels:               {sec_lit},")
    lines.append(f"\t\t\tStateChannelOffset:              {sco_lit},")
    lines.append(
        f"\t\t\tAllowUndefinedGenericDataPoints: {str(cg.allow_undefined_generic_data_points).lower()},"
    )
    lines.append(f"\t\t\tFields: {fields_lit},")
    lines.append(f"\t\t\tChannelFields: {chf_lit},")
    lines.append(f"\t\t\tFixedChannelFields: {fcf_lit},")
    lines.append("\t\t},")
    lines.append(f"\t\tAdditionalDataPoints: {add_lit},")
    lines.append(f"\t\tIncludeDefaultDataPoints: {str(cfg.include_default_data_points).lower()},")
    lines.append("\t},")
    return "\n".join(lines)


def _render_profile_configs_go(
    profile_configs,
    fields: dict[str, str],
    params: dict[str, str],
) -> str:
    entries: list[str] = []
    items = sorted(profile_configs.items(), key=lambda kv: kv[0].value)
    for prof, cfg in items:
        entries.append(_render_profile_config(prof.value, cfg, fields, params))
    body = "\n".join(entries) + "\n"
    return PROFILE_CONFIGS_HEADER.format(
        generated_at=datetime.datetime.now(datetime.UTC).replace(microsecond=0).isoformat(),
        count=len(items),
        entries=body,
    )


# ---------------------------------------------------------------------------
# generated_default_data_points.go
# ---------------------------------------------------------------------------


DEFAULT_DP_TEMPLATE = """// SPDX-License-Identifier: MIT
// Copyright (C) 2026 openccu-loom authors.
//
// Code generated by script/generate_profiles.py. DO NOT EDIT.
// Generated at: {generated_at}

package custom

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// GeneratedDefaultDataPointCount is the number of channel-offset
// entries that DefaultDataPoints exposes.
const GeneratedDefaultDataPointCount = {count}

// DefaultDataPoints is the per-channel-offset table of generic data
// points every profile inherits unless
// [ProfileConfig.IncludeDefaultDataPoints] is false. Tuple keys in the
// source are expanded so each channel offset is its own map entry.
var DefaultDataPoints = map[int][]hmenum.Parameter{{
{entries}}}
"""


def _render_default_data_points_go(default_dps, params: dict[str, str]) -> str:
    # Expand tuple keys.
    expanded: dict[int, tuple] = {}
    for k, v in default_dps.items():
        keys = list(k) if isinstance(k, tuple) else [k]
        for sk in keys:
            expanded[int(sk)] = v

    items = sorted(expanded.items(), key=lambda kv: kv[0])
    lines: list[str] = []
    for ch, ptuple in items:
        lines.append(f"\t{ch}: {{")
        for p in ptuple:
            lines.append(f"\t\t{_go_param_ref(p.value, params)},")
        lines.append("\t},")
    body = "\n".join(lines) + "\n"

    return DEFAULT_DP_TEMPLATE.format(
        generated_at=datetime.datetime.now(datetime.UTC).replace(microsecond=0).isoformat(),
        count=len(items),
        entries=body,
    )


# ---------------------------------------------------------------------------
# generated_profiles.go
# ---------------------------------------------------------------------------


PROFILES_HEADER = """// SPDX-License-Identifier: MIT
// Copyright (C) 2026 openccu-loom authors.
//
// Code generated by script/generate_profiles.py. DO NOT EDIT.
// Regenerate via `make generate` (requires Python 3.14+ and the
// Package importable in the active environment).
// Generated at: {generated_at}

package custom

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// GeneratedProfileCount is the number of profiles the generator
// produced. The parity contract test asserts this value.
const GeneratedProfileCount = {count}

// intPtr returns a pointer to v. The generated literals use it to
// avoid the awkward `var x = 1; ... &x` pattern at every call site.
func intPtr(v int) *int {{ return &v }}

// RegisterGeneratedProfiles installs every profile the generator
// produced onto r. Called from [DefaultRegistry] at init() time.
func RegisterGeneratedProfiles(r *Registry) {{
"""

PROFILES_FOOTER = "}\n"


def _render_extended(
    extended,
    fields: dict[str, str],
    params: dict[str, str],
) -> str:
    """Render an *ExtendedDeviceConfig pointer literal (or `nil`)."""
    if extended is None:
        return "nil"

    fcf = _go_fixed_channel_fields_paramonly(
        extended.fixed_channel_fields, fields, params, indent_level=4
    )
    add = _go_additional_data_points(extended.additional_data_points, params, indent_level=4)

    return (
        "&ExtendedDeviceConfig{\n"
        f"\t\t\tFixedChannelFields: {fcf},\n"
        f"\t\t\tAdditionalDataPoints: {add},\n"
        "\t\t}"
    )


def _render_profile_entry(
    *,
    model: str,
    category_value: str,
    profile_value: str,
    channels: tuple,
    schedule_channel_no,
    extended,
    fields: dict[str, str],
    params: dict[str, str],
    cats: dict[str, str],
) -> str:
    pg = _product_group_for(model)
    chan_parts = [
        f"{{Channel: {int(c)}, Role: ChannelRolePrimary}}"
        for c in channels
        if c is not None
    ]
    channels_lit = (
        "nil"
        if not chan_parts
        else "[]ChannelRoleAssignment{" + ", ".join(chan_parts) + "}"
    )

    if schedule_channel_no is None:
        schedule_lit = "nil"
    else:
        schedule_lit = f"intPtr({int(schedule_channel_no)})"

    extended_lit = _render_extended(extended, fields, params)
    config_lit = f"ProfileConfigs[{_go_profile_ref(profile_value)}]"

    return (
        "\tr.MustRegister(Profile{\n"
        f"\t\tName:              {_go_profile_ref(profile_value)},\n"
        f"\t\tDeviceType:        {_go_string(model)},\n"
        f"\t\tProductGroup:      hmenum.ProductGroup{pg},\n"
        f"\t\tCategory:          {_go_category_ref(category_value, cats)},\n"
        f"\t\tChannels:          {channels_lit},\n"
        f"\t\tScheduleChannelNo: {schedule_lit},\n"
        f"\t\tExtended:          {extended_lit},\n"
        f"\t\tConfig:            {config_lit},\n"
        "\t})\n"
    )


def _render_profiles_go(
    entries,
    fields: dict[str, str],
    params: dict[str, str],
    cats: dict[str, str],
) -> str:
    out: list[str] = [
        PROFILES_HEADER.format(
            generated_at=datetime.datetime.now(datetime.UTC).replace(microsecond=0).isoformat(),
            count=len(entries),
        )
    ]
    for e in entries:
        out.append(_render_profile_entry(fields=fields, params=params, cats=cats, **e))
    out.append(PROFILES_FOOTER)
    return "".join(out)


# ---------------------------------------------------------------------------
# Profile collection
# ---------------------------------------------------------------------------


def _collect_profile_entries():
    """
    Walk DeviceProfileRegistry._configs and return a sorted list of dicts,
    one per (category, model, DeviceConfig). Tuple registrations are
    expanded so every DeviceConfig becomes its own entry.
    """
    import aiohomematic.model.custom  # noqa: F401  (triggers registrations)
    from aiohomematic.model.custom.registry import DeviceProfileRegistry

    entries: list[dict] = []
    for category, model_map in DeviceProfileRegistry._configs.items():  # noqa: SLF001
        for model, val in model_map.items():
            cfgs = val if isinstance(val, tuple) else (val,)
            for cfg in cfgs:
                entries.append(
                    {
                        "model": model,
                        "category_value": category.value,
                        "profile_value": cfg.profile_type.value,
                        "channels": tuple(cfg.channels),
                        "schedule_channel_no": cfg.schedule_channel_no,
                        "extended": cfg.extended,
                    }
                )

    # Stable sort: model, category, profile, channel-tuple.
    entries.sort(
        key=lambda e: (
            e["model"],
            e["category_value"],
            e["profile_value"],
            e["channels"],
        )
    )
    return entries


# ---------------------------------------------------------------------------
# Test template
# ---------------------------------------------------------------------------


TEST_TEMPLATE = """// SPDX-License-Identifier: MIT
// Copyright (C) 2026 openccu-loom authors.
//
// Code generated by script/generate_profiles.py. DO NOT EDIT.

package contract

import (
\t"testing"

\t"github.com/SukramJ/openccu-loom/internal/model/custom"
)

// TestDeviceProfileRegistryParity pins the registered profile count to
// the number the generator observed at generation time. Drift means
// someone edited generated_profiles.go by hand — the fix is to
// regenerate, not to update this assertion.
func TestDeviceProfileRegistryParity(t *testing.T) {
\tr := custom.DefaultRegistry()
\tif got := r.Len(); got != custom.GeneratedProfileCount {
\t\tt.Fatalf("registry has %d profiles, generator emitted %d", got, custom.GeneratedProfileCount)
\t}
}

// TestProfileConfigCatalogParity pins the size of the auto-generated
// ProfileConfigs catalog to the value the generator observed.
func TestProfileConfigCatalogParity(t *testing.T) {
\tif got := len(custom.ProfileConfigs); got != custom.GeneratedProfileConfigCount {
\t\tt.Fatalf("ProfileConfigs has %d entries, generator emitted %d", got, custom.GeneratedProfileConfigCount)
\t}
}

// TestDefaultDataPointsParity pins the number of channel-offset entries
// that DefaultDataPoints exposes.
func TestDefaultDataPointsParity(t *testing.T) {
\tif got := len(custom.DefaultDataPoints); got != custom.GeneratedDefaultDataPointCount {
\t\tt.Fatalf("DefaultDataPoints has %d entries, generator emitted %d", got, custom.GeneratedDefaultDataPointCount)
\t}
}
"""


# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------


def main() -> int:
    try:
        import aiohomematic.model.custom  # noqa: F401
        from aiohomematic.model.custom.profile import (
            DEFAULT_DATA_POINTS,
            PROFILE_CONFIGS,
        )
    except ImportError as exc:
        print(
            f"generate_profiles: cannot import aiohomematic: {exc}\n"
            "  install it: pip install aiohomematic\n"
            "  (the generator is run rarely; a project venv with the\n"
            "   pinned aiohomematic version is the recommended setup.)",
            file=sys.stderr,
        )
        return 2

    # Build wire-value → Go-const lookups by parsing the existing hmenum
    # sources. Doing this dynamically avoids hard-coding the Python→Go
    # naming conversion (e.g. DISPLAY_DATA_ID → DisplayDataID,
    # MIN_MAX_VALUE_NOT_RELEVANT_FOR_MANU_MODE → MinMaxNotRelevantForManuMode)
    # since the Go side is already the source of truth for those names.
    params = _load_const_map(PARAMETER_GO, "Parameter")
    fields = _load_const_map(FIELD_GO, "Field")
    cats = _load_const_map(DATAPOINT_GO, "DataPointCategory")

    # 1. Profile entries.
    entries = _collect_profile_entries()

    # 2. Render and write.
    OUT_PROFILES.parent.mkdir(parents=True, exist_ok=True)
    OUT_PROFILES.write_text(_render_profiles_go(entries, fields, params, cats))

    OUT_CONFIGS.parent.mkdir(parents=True, exist_ok=True)
    OUT_CONFIGS.write_text(_render_profile_configs_go(PROFILE_CONFIGS, fields, params))

    OUT_DEFAULTS.parent.mkdir(parents=True, exist_ok=True)
    OUT_DEFAULTS.write_text(_render_default_data_points_go(DEFAULT_DATA_POINTS, params))

    OUT_TEST.parent.mkdir(parents=True, exist_ok=True)
    OUT_TEST.write_text(TEST_TEMPLATE)

    # Post-process every emitted file with gofumpt (preferred) or gofmt
    # (fallback) so the generated source matches the project's style
    # exactly. Skip silently if neither tool is on PATH — golangci-lint
    # in CI will catch any drift before the change merges.
    formatter = shutil.which("gofumpt") or shutil.which("gofmt")
    if formatter:
        for path in (OUT_PROFILES, OUT_CONFIGS, OUT_DEFAULTS, OUT_TEST):
            subprocess.run([formatter, "-w", str(path)], check=False)

    print(
        f"generate_profiles: wrote {len(entries)} profiles to {OUT_PROFILES.relative_to(REPO_ROOT)}\n"
        f"  profile configs ({len(PROFILE_CONFIGS)}) → {OUT_CONFIGS.relative_to(REPO_ROOT)}\n"
        f"  default data points → {OUT_DEFAULTS.relative_to(REPO_ROOT)}\n"
        f"  parity test → {OUT_TEST.relative_to(REPO_ROOT)}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
