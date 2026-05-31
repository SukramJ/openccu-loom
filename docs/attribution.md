# Third-party design inspiration

OpenCCU-Loom is MIT-licensed (see `LICENSE`). The list below records
external sources we have read and learned from. None of their code is
copied verbatim into the OpenCCU-Loom source tree; we cite them in
file-top comments wherever a Svelte primitive or Go type mirrors an
external counterpart so the lineage stays auditable.

## Home Assistant Frontend — Apache-2.0

- Source: <https://github.com/home-assistant/frontend> (local mirror
  `../frontend/`)
- Used as: visual + structural reference for the `assets/ui/src/lib/control/`
  primitive set (`ControlSlider`, `ControlButton`, `ControlButtonGroup`,
  `ControlNumberButtons`, `ControlTile`, `ControlTileIcon`,
  `ControlTileInfo`) and the feature-stack pattern under
  `card-features/`.
- Specifically referenced:
  - `frontend/src/components/ha-control-slider.ts` — slider thickness,
    track structure, gradient fill, keyboard a11y
  - `frontend/src/components/ha-control-button.ts` — pill button shape
    and the background-opacity active/inactive treatment
  - `frontend/src/components/ha-control-button-group.ts` — segmented
    button container, 12 px spacing, 40 px thickness
  - `frontend/src/components/ha-control-circular-slider.ts` — arc-based
    target-value selector for thermostats
  - `frontend/src/components/tile/ha-tile-icon.ts` and siblings —
    `--tile-color`-driven state colouring
  - `frontend/src/panels/lovelace/cards/hui-tile-card.ts` — tile-card
    layout: hero icon + info + features stack
  - `frontend/src/panels/lovelace/card-features/hui-*-card-feature.ts`
    — per-slot interaction surfaces (toggle, numeric input, target
    temperature, HVAC modes, preset modes, cover open/close, lock
    commands, light color temp, etc.)
  - `frontend/src/resources/theme/color/color.globals.ts` — the
    `--state-*-color` token namespace (state-coloured tiles)
  - `frontend/src/common/entity/state_color.ts` and
    `state_active.ts` — the entity→state→CSS variable mapping logic

Compliance: Apache-2.0 is MIT-compatible. Each new Svelte file under
`assets/ui/src/lib/control/` begins with a comment naming the HA file
it mirrors. No Lit/TypeScript code is reproduced verbatim; each
primitive is implemented from scratch in Svelte 5 + Tailwind 4.

## EQ-3 OCCU — HMSL (non-commercial)

- Source: <https://github.com/eq-3/occu> (local mirror `../occu/`)
- Used as: factual API-inventory source for the CCU `CONTROL` attribute.
  The 84 family names and their slot suffixes are interface metadata
  the CCU XML-RPC API speaks; documenting them in
  `docs/ui/control-inventory.md` is no different from documenting
  HTTP-method names.
- Specifically: `occu/firmware/rftypes/*.xml` (`grep`ed for `control="…"`
  attributes) and the existence of `occu/WebUI/www/rega/esp/controls/*.fn`
  filenames as a hint of which families have a per-family rendering
  template upstream.

Compliance: HMSL is non-commercial. We do not reproduce any OCCU
software (no ReGa code, no `.fn` files, no images) in OpenCCU-Loom.
Extracting identifier names from a public source for the purpose of
talking to the documented API surface is interoperability, not
derivative-work creation. The CCU data archives that DO ship in the
binary are addressed separately by ADR 0003 + `internal/ccudata/embedded/NOTICE`.

## homematicip-local-frontend — MIT

- Source: <https://github.com/SukramJ/homematicip-local-frontend> (local
  mirror `../homematicip-local-frontend/`)
- Used as: structural reference for the HA-panel-context (how an
  in-HA-panel SPA composes its tiles, how it pulls entity state via
  the WS API). Same MIT licence as OpenCCU-Loom.

## matter.js — Apache-2.0

- Source: <https://github.com/project-chip/matter.js> (local mirror
  `../matter.js/`)
- Already covered in CLAUDE.md §"matter.js as the Matter Gold Standard".
  Listed here for completeness.

## Reverse direction

OpenCCU-Loom itself is MIT-licensed. Anyone reading this list to learn
from us is welcome to do the same — the LICENSE file applies.
