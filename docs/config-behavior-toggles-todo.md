# TODO — per-central behavior toggles (single PR)

Adopts two aiohomematic operator toggles into openccu-loom:

- `light_last_brightness` — when **on**, a plain light turn-on restores the
  last non-zero brightness; when **off**, the light turns on at full (100%).
  **Default: `true`** (preserves today's openccu-loom behavior).
- `use_group_channel_for_cover_state` — when **on**, a cover that has a
  group-channel LEVEL reports its position from the group channel; when
  **off**, it reports from its own channel. **Default: `true`** (aiohomematic
  parity; matches today's hardwired behavior).

## Design

Both mechanisms already exist in the domain model (`light.LastLevel` tracking,
`cover.useGroupChannelForState` + `groupLevel`); only the operator toggle is
missing. Rather than change the shared `custom.Constructor` signature (which
every CDP factory shares), the two booleans ride on `device.Device` — mirroring
aiohomematic's `device.config_provider.config.*`. Defaults are `true` at every
layer, so an unset/zero device keeps current behavior and existing tests are
untouched.

Flow: `config.CentralConfig.Behavior` → `WireCentrals` → `DevicePipeline` →
stamp each `device.Device` before `materialiseCustomDataPoints` →
`Light.New` / `cover.applyGroupLevel` read `ch.Device()`.

## Checklist

### Part 1 — the two named CDP toggles

- [x] **Config** — `CentralConfig.Behavior { LightLastBrightness, UseGroupChannelForCoverState *bool }` (`cfg:"expert"`); accessors return `true` when nil.
- [x] **Device** — `device.Device` carries the two flags (init `true` in `device.New`, `SetCustomDPBehavior`, accessors).
- [x] **Wiring** — `DevicePipeline.WithCustomDPBehavior(...)` (default true), set from `cc.Behavior` in `WireCentrals`; `materialiseCustomDataPoints` stamps each device.
- [x] **Light** — `Light.enableLastBrightness` from `cfg.Channel.Device()`; `turnOnLevel()` branches all turn-on sites (full 1.0 when disabled).
- [x] **Cover** — `applyGroupLevel` reads `ch.Device().UseGroupChannelForCoverState()`.
- [x] **Tests** (test-first): config defaults/round-trip; device accessors; light enabled→LastLevel / disabled→full; cover enabled→group / disabled→own; pipeline stamping.

### Part 2 — hub scan + filtering toggles

- [x] **`enable_sysvar_scan` / `enable_program_scan`** (default true) — per-central gate that suppresses the sysvar / program hub scan entirely.
- [x] **`include_internal_sysvars` (true) / `include_internal_programs` (false)** — daemon-side filter so MQTT/REST agree (today internal filtering is client-side only).
- [x] **`sysvar_markers` / `program_markers`** — marker (ReGa-description-prefix) filtering. Needs the `description` field surfaced on Sysvar/Program summaries (the known hub-parity gap) before the marker match can run daemon-side.
- [x] **`sysvar_scan_interval`** — per-central cadence override for the periodic sysvar refresh.

### Part 3 — device-lifecycle toggles

- [x] **`enable_device_firmware_check`** (default **true** — deliberate divergence from the reference's false, to preserve 0.2.0's firmware-update entities; see `docs/parity/by_design.md`) — gate the firmware-update entity surface.
- [x] **`delay_new_device_creation`** (default false) — defer creation of newly-paired devices until their description is complete.

### Wrap-up

- [x] **Docs** — document every new key in `example.config.full.yaml`.
- [x] **CHANGELOG** — entry under `[0.3.0] → ### Added` (0.2.0 is tagged; post-tag changes belong to 0.3.0).
- [x] `make lint` + `make test` green.

Defaults mirror aiohomematic (`const.py`) except `enable_device_firmware_check`
(true here vs false there — see by_design.md): `enable_program_scan` /
`enable_sysvar_scan` / `include_internal_sysvars` / `light_last_brightness` /
`use_group_channel_for_cover_state` = true, `include_internal_programs` /
`delay_new_device_creation` = false, markers = empty.
