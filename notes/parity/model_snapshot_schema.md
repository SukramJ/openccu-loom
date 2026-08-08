# Model snapshot schema

> **Purpose:** the shared JSON schema both stacks (`OpenCCU-Loom` +
> godevccu, `aiohomematic` + pydevccu) emit so that a structural,
> per-field diff shows whether both stacks derive identical domain
> models from the identical wire data.
>
> Requirement context: second stage of the cross-stack verification
> — the wire data itself is verified (`script/datasource_diff.py`,
> 94,344 parameter keys with no drift); this stage checks whether
> the adapter / pipeline layer produces the same models. See also `notes/parity/by_design.md` for the catalogue of accepted divergences.

---

## Top level

```json
{
  "stack": "openccu-loom" | "aiohomematic",
  "stack_version": "...",
  "devccu": "godevccu" | "pydevccu",
  "devccu_version": "...",
  "locale": "en",
  "captured_at": "ISO-8601 UTC",
  "devices": [ <Device>, ... ]
}
```

`devices` is sorted alphabetically by `address` so the diff is
deterministic. `locale` is the language code under which every
`*_label` field was resolved — both stacks **must** use the same
locale, otherwise a label diff is meaningless.

## Device

```json
{
  "address":      "VCU1769958",
  "model":        "HmIP-BWTH",
  "model_label":  "Wand-Thermostat",
  "name":         "",
  "interface_id": "HmIP-RF",
  "firmware":     "3.0.4",
  "version":      5,
  "product_group":"HMIP",
  "rooms":        [ ... ],
  "functions":    [ ... ],
  "channels":     [ <Channel>, ... ]
}
```

`channels` is sorted by `number`. `model_label` is resolved via
`Translations.DeviceModelLabel(locale, model, sub_model)` and is
empty when no translation exists. `name` is the operator-supplied
device name (often empty in default godevccu / pydevccu setups).

## Channel

```json
{
  "address":     "VCU1769958:1",
  "number":      1,
  "type":        "CLIMATECONTROL_RT_TRANSCEIVER",
  "type_label":  "Klimasteuerung",
  "name":        "",
  "rooms":       [ ... ],
  "functions":   [ ... ],
  "group_no":    0,
  "paramsets":   [ "MASTER", "VALUES", "LINK" ],

  "operation_mode":  "AUTO" | null,

  "generic_data_points":    [ <GenericDP>, ... ],
  "custom_data_points":     [ <CustomDP>, ... ],
  "calculated_data_points": [ <CalculatedDP>, ... ]
}
```

`type_label` via `Translations.ChannelType(locale, type)`.

Every DP list is sorted by `(paramset_key, parameter)`.

## GenericDataPoint

Mirror of `aiohomematic.model.generic.GenericDataPoint` /
`openccu-loom/internal/model/generic.DataPoint[T]`:

```json
{
  "paramset_key":     "VALUES" | "MASTER",
  "parameter":        "ACTUAL_TEMPERATURE",
  "parameter_label":  "Aktuelle Temperatur",
  "type":             "FLOAT" | "BOOL" | "INTEGER" | "ENUM" | "STRING" | "ACTION",
  "operations":     7,
  "flags":          1,
  "min":            <typed>,
  "max":            <typed>,
  "default":        <typed>,
  "unit":           "°C",
  "multiplier":     1.0,
  "special":        [ {"id":"OFF", "value":4.5}, ... ],
  "value_list":     [ "AUTO", "MANU", ... ] | null,
  "control":        "TEMPERATURECONTROL.ACTUAL",
  "id":             "ACTUAL_TEMPERATURE",
  "tab_order":      0,

  "category":       "SENSOR" | "NUMBER" | "BUTTON" | "BINARY_SENSOR" | ...,
  "usage":          "DATA_POINT" | "CDP_PRIMARY" | "CDP_VISIBLE" | "CDP_SECONDARY" | "EVENT" | "NO_CREATE",
  "is_writable":    true,
  "is_readable":    true,
  "is_visible":     true,
  "enabled_default":true,
  "is_internal":    false,
  "is_forced_sensor":false,
  "is_un_ignored":  false,
  "forced_usage":   null
}
```

**Important:** fields that *only* concern the adapter side (MQTT
discovery) — `device_class`, `state_class`, `entity_category`,
`enabled_by_default` as an HA flag, `icon` — do **not** belong in
the model snapshot. They are resolved separately via the
`entity_descriptions_*.go` /
`entity_helpers/descriptions/*.py` files and form their own audit
layer.

## CustomDataPoint

```json
{
  "profile":        "CustomDpIpThermostat",
  "category":       "CLIMATE",
  "primary_dps":    [ "VALUES.SET_POINT_TEMPERATURE" ],
  "wrapped_dps":    [ "VALUES.ACTUAL_TEMPERATURE", "VALUES.SET_POINT_MODE", "VALUES.BOOST_MODE", "MASTER.TEMPERATURE_MAXIMUM", "MASTER.TEMPERATURE_MINIMUM", ... ],

  "min_temp":       5.0,
  "max_temp":       30.5,
  "target_temperature_step": 0.5,
  "modes":          [ "AUTO", "HEAT" ],
  "preset_modes":   [ "BOOST" ],
  "supports_humidity": true,
  ...
}
```

Profile-specific fields (Climate: `min_temp`/`max_temp`, Cover:
`tilt_*`, Light: `effect_list`/`color_modes`) are emitted *only*
when the profile defines them. The diff tolerates missing fields
*only when both sides leave them out* — one side carrying a field
that the other side does not is drift.

## CalculatedDataPoint

```json
{
  "parameter":         "DEW_POINT",
  "type":              "FLOAT",
  "unit":              "°C",
  "source_parameters": [ "ACTUAL_TEMPERATURE", "HUMIDITY" ],
  "category":          "SENSOR",
  "enabled_default":   false
}
```

## Tolerated differences (not counted as drift)

- `captured_at` — capture timestamp.
- `stack`, `stack_version`, `devccu`, `devccu_version` —
  identification fields.
- `Device.firmware`, `Device.version` — read from wire status; must
  match, but are not the audit target.

Every other field must match **bit-exactly**.

## Ordering & stabilisation

- Devices by `address`.
- Channels by `number`.
- DPs by `(paramset_key, parameter)`.
- `wrapped_dps` / `primary_dps` / `value_list` / `special` /
  `source_parameters` each sorted alphabetically.
