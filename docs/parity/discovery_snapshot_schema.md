# Discovery Snapshot Schema

> **Purpose:** Shared JSON schema for three stacks:
>
> 1. **`OpenCCU-Loom`** — emits MQTT discovery payloads via the
>    `internal/north/mqtt` bridge against godevccu.
> 2. **`homematicip_local`** *(primary reference source)* — the HA-native
>    custom integration. Provides HA entity attributes via
>    `entity_helpers/registry.py` resolution; OpenCCU-Loom must emit
>    entities that appear **identical** in HA to those from
>    `homematicip_local`.
> 3. **`aiohomematic2mqtt`** *(secondary cross-check)* — the Python
>    MQTT bridge. Provides MQTT discovery payloads via
>    `create_mqtt_entity` against pydevccu. Useful for cross-checking
>    the MQTT plumbing layer (state topic / value template).
>
> **Hierarchy:** `homematicip_local` is the source of truth;
> the `aiohomematic2mqtt` comparison is secondary — `a2mq` tables are not
> the reference source. A structural per-field diff shows whether OpenCCU-Loom
> produces the same HA entity view for the same wire definition.
>
> Requirements context: the cross-stack snapshot pipeline under `script/` and `docs/parity/by_design.md`. The model snapshot
> (`docs/parity/model_snapshot_schema.md`) checks the wire→domain
> layer; this layer above it checks that the bridge layer derives
> identical HA entities from the model.

---

## Top-Level

```json
{
  "stack": "openccu-loom" | "homematicip_local" | "aiohomematic2mqtt",
  "stack_version": "...",
  "devccu": "godevccu" | "pydevccu",
  "devccu_version": "...",
  "locale": "en",
  "captured_at": "ISO-8601 UTC",
  "entities": [ <Entity>, ... ]
}
```

`entities` is sorted by `join_key` to make the diff deterministic.
`locale` is the language code with which the i18n fields (`name`,
`parameter_label`) were resolved — both stacks **must** use the same
locale.

## Entity

```json
{
  "join_key":         "VCU0001234:1:agg:climate",
  "kind":             "param" | "agg" | "event" | "hub",
  "discovery_topic":  "homeassistant/climate/gh_ccu-01_vcu0001234/1_climate/config",
  "component":        "climate",
  "node_id":          "gh_ccu-01_vcu0001234",
  "object_id":        "1_climate",
  "unique_id":        "openccu-loom_vcu0001234_1_climate",

  "device_address":   "VCU0001234",
  "channel_no":       1,
  "channel_type":     "CLIMATECONTROL_RT_TRANSCEIVER",
  "model":            "HmIP-BWTH",
  "paramset_key":     "VALUES",
  "parameter":        "ACTUAL_TEMPERATURE",

  "payload": { ... full HA discovery payload, key-sorted ... }
}
```

### Fields in Detail

- **`join_key`** — canonical key used by the diff to map entities from
  both stacks onto each other. Construction:
  - `<addr>:<ch>:param:<paramset>.<parameter>` for generic DPs
    (`VALUES.STATE`, `MASTER.CHANNEL_OPERATION_MODE`).
  - `<addr>:<ch>:agg:<component>` for aggregated custom DPs
    (Climate / Cover / Light / Lock / Valve / Siren).
  - `<addr>:<ch>:event:channel` for press-event aggregation.
  - `hub:<scope>:<id>` for hub entities (Programs, Sysvars,
    System Health, Inbox, …).
  - Address always in uppercase, parameter in uppercase,
    component/scope strings in lowercase — both stacks normalize
    at dump time.
- **`kind`** — `param` (generic DP), `agg` (custom DP), `event`
  (press-event aggregate), `hub` (hub entity).
- **`discovery_topic`** — full HA discovery topic string, as it would
  be sent to the broker. Form:
  `homeassistant/<component>/<node_id>/<object_id>/config`.
- **`component`** — HA component (sensor / binary_sensor / number /
  select / switch / button / light / lock / cover / valve / climate /
  siren / event / update / text).
- **`node_id`** / **`object_id`** — the two topic segments. Format
  differs between stacks (Go uses
  `gh_<central>_<addr>`, Python uses `dp.unique_id`-derived forms) —
  the diff treats these fields as **tolerated** because they are not
  semantic.
- **`unique_id`** — HA `unique_id`. Identical string format is not
  required; the diff tolerates this field at the top level and does
  *not* compare its value between stacks. Within `payload` it is still
  included so operators can correlate manually if needed.
- **`device_address`**, **`channel_no`**, **`channel_type`**,
  **`model`** — locate the entity on the wire device.
- **`paramset_key`** / **`parameter`** — populated only for `kind=param`.
  `paramset_key` is `VALUES` or `MASTER`.
- **`payload`** — the **complete** HA discovery payload, key-sorted
  (recursive sort over all map keys, including within list elements
  that are maps, so that textual diffs do not report pure ordering
  differences).

## Notes on the `homematicip_local` Snapshot

The HA-native integration does **not** operate over MQTT. The
`homematicip_local` dumper maps, per CallbackDataPoint, the HA entity
view that appears in the HA entity registry:

- **Static fields** (from `entity_helpers/registry.py` lookup):
  `device_class`, `state_class`, `entity_category`, `icon`,
  `unit_of_measurement` (from `native_unit_of_measurement`),
  `enabled_by_default` (from `entity_registry_enabled_default`),
  `suggested_display_precision`, `options`, `translation_key`,
  `name_source`, `min`/`max`/`step`/`mode` (Number).
- **Dynamic fields** (from the DP itself):
  - Climate: `min_temp`, `max_temp`, `temp_step`, `modes`,
    `preset_modes`, `supports_humidity`, `temperature_unit`.
  - Cover: `supports_position`, `supports_tilt`.
  - Light: `min_kelvin`, `max_kelvin`, `transition`, `effect`,
    `effect_list`, `supported_color_modes`.
  - Siren: `support_volume_set`, `available_tones`.
  - Generic: `min`, `max`, `step`, `options`, `unit_of_measurement`.
- **Name** is best-effort (`dp.translated_name`); HA's full
  title-casing pipeline with `name_source`/`device_class_translation`/
  `translation_key` is not trivially reproducible outside HA.
  Drift on `name` is expected and is not P0.

**MQTT-only fields** (state_topic, value_template, availability,
payload_on/off, expire_after, force_update, etc.) are completely
**removed** from the comparison in primary mode — they are semantically
meaningless for the HA-native integration. The diff has a `--secondary`
mode that presence-checks instead of dropping; usable for the
cross-check against `aiohomematic2mqtt`.

## Tolerated Differences (not counted as drift)

At snapshot level:

- `captured_at`, `stack`, `stack_version`, `devccu`, `devccu_version`
  — identification/timestamp fields.

At entity level:

- `discovery_topic`, `node_id`, `object_id`, `unique_id` — the two
  stacks produce different topic/ID formats (Go:
  `gh_<central>_<addr>`, Python: `<dp.unique_id>`); semantic
  identity is already established via `join_key`.

At payload level (fields are removed from `payload` before comparison):

- `availability` (complete list) — topic paths differ, but both stacks
  use structurally the same `payload_available`/`payload_not_available`
  values.
- `availability_mode` — both set `all`; deterministic but redundant
  with the `availability` list.
- `availability_topic` — single-source form (some Python platforms use
  this instead of the list).
- `state_topic`, `command_topic`, `json_attributes_topic`,
  `mode_command_topic`, `mode_state_topic`,
  `temperature_command_topic`, `temperature_state_topic`,
  `current_temperature_topic`, `current_humidity_topic`,
  `action_topic`, `preset_mode_command_topic`,
  `preset_mode_state_topic`, `set_position_topic`, `position_topic`,
  `position_template`, `tilt_command_topic`, `tilt_status_topic`,
  `value_template`, `mode_state_template`, `action_template`,
  `current_temperature_template`, `current_humidity_template`,
  `temperature_state_template`, `preset_mode_value_template`,
  `tilt_status_template`, `json_attributes_template` — topic paths
  and template snippets follow different conventions per stack;
  semantic parity is checked via the presence of the fields (see
  below), not their content.
- `device.identifiers`, `device.via_device`, `device.configuration_url`
  — stack-specific identifiers.
- `origin.name`, `origin.sw_version`, `origin.support_url` — bridge
  branding.
- `default_entity_id`, `object_id` (inside the payload) — when
  present, depends on the unique_id format.

All other fields must match **bit-exactly**. In particular, the
following must match:

- `name`
- `enabled_by_default`
- `entity_category` (None / `config` / `diagnostic`)
- `device_class`
- `state_class`
- `unit_of_measurement`
- `icon`
- `min`, `max`, `step`, `precision`
- `min_temp`, `max_temp`, `temp_step`, `temperature_unit`
- `modes`, `preset_modes` (Climate)
- `effect_list`, `color_modes`, `supported_color_modes` (Light)
- `options` (Select / Sensor)
- `device_class` / `state_class` (Sensor / BinarySensor)
- `payload_on`, `payload_off` (BinarySensor / Switch)
- `payload_press` (Button)
- `force_update`, `expire_after`, `optimistic`, `retain`
- `suggested_display_precision`
- `supports_position`, `supports_tilt` (Cover)
- `current_humidity_*` presence (Climate, must only be set when a
  humidity sensor exists)
- Presence of all topic fields (any missing `command_topic` or
  `state_topic` is drift; the string value is not).

The diff operates in two modes: **value comparison** for the
"must-match" list above; **presence comparison** (bool: present yes/no)
for all topic/template fields from the tolerance list.

## Sorting & Stabilization

- `entities` sorted by `join_key` (lexicographic).
- Within each `payload`, map keys are sorted recursively (including
  within list elements that are maps).
- Lists with primitive elements (e.g. `modes`, `preset_modes`,
  `options`) are **not** sorted — the order is HA-semantic (e.g.
  "auto" before "heat" affects the UI order). Diff comparison
  respects the original order.
