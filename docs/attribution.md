# Credits

OpenCCU-Loom is MIT-licensed (see
[`LICENSE`](https://github.com/SukramJ/openccu-loom/blob/main/LICENSE)).
This page records the external projects we have read and learned from,
and credits the people and licences behind them. None of their code is
copied verbatim into the OpenCCU-Loom source tree; we cite them in
file-top comments wherever a Svelte primitive or Go type mirrors an
external counterpart so the lineage stays auditable.

The license obligations and verbatim copyright notices for every upstream
project — plus the Go module dependencies — live in
[`THIRD-PARTY-NOTICES.md`](https://github.com/SukramJ/openccu-loom/blob/main/THIRD-PARTY-NOTICES.md); full license texts are
under [`licenses/`](https://github.com/SukramJ/openccu-loom/blob/main/licenses). This page is the narrative companion to that
machine-oriented list.

## aiohomematic — MIT

- Source: <https://github.com/SukramJ/aiohomematic> (local mirror
  `../aiohomematic/`)
- Authored by **SukramJ** and **Daniel Perna**.
- Used as: the reference implementation this whole project is a Go port of —
  transports, devices, paramsets, custom-DP composition, enumerations and
  their string values, interface classification, paramset normalization and
  patches, and the device-profile registration shape. The device-profile
  catalogue was *derived* from the aiohomematic registry and, since
  [ADR 0063](adr/0063-self-maintained-device-profiles.md), is maintained in
  this repository rather than regenerated.

Compliance: MIT, same as OpenCCU-Loom. No Python source is reproduced verbatim;
the Go code is a from-scratch semantic port that cites the aiohomematic file +
function it mirrors. The CCU side of the project follows aiohomematic as its
gold standard (see CLAUDE.md §"aiohomematic as a Reference").

## aiohomematic-config — MIT

- Source: <https://github.com/SukramJ/aiohomematic-config> (local mirror
  `../aiohomematic-config/`)
- Used as: reference for the configuration-panel logic ported into
  `internal/configui` — form schemas, parameter grouping, label resolution,
  visibility filters, preset selection.

Compliance: MIT. Same provenance rules as aiohomematic above.

## pydevccu / godevccu — MIT

- Sources: <https://github.com/danielperna84/pydevccu> (authors: **Daniel
  Perna**, **SukramJ**) and the Go port
  <https://github.com/SukramJ/godevccu>, consumed as a regular module
  dependency for the integration tests.
- Used as: the in-process HomeMatic CCU simulator that the hermetic
  `tests/integration/` suite runs against — `godevccu` is a pure-Go port of
  `pydevccu`, so no Python toolchain is needed.

Compliance: both MIT.

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
  `notes/reference/control-inventory.md` is no different from documenting
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
binary are addressed separately by ADR 0003 and the `NOTICE.md` of the
`github.com/SukramJ/go-openccu-data` module they come from.

## homematicip-local-frontend — MIT

- Source: <https://github.com/SukramJ/homematicip-local-frontend> (local
  mirror `../homematicip-local-frontend/`)
- Used as: structural reference for the HA-panel-context (how an
  in-HA-panel SPA composes its tiles, how it pulls entity state via
  the WS API). Same MIT licence as OpenCCU-Loom.

## matter.js — Apache-2.0

- Source: <https://github.com/project-chip/matter.js> (local mirror
  `../matter.js/`)
- Copyright: © Project CHIP / the matter.js Authors.
- Used as: the gold standard for the entire Matter side. Everything under
  `internal/north/matter/` is a semantic port of matter.js HEAD — cluster IDs,
  revisions, attribute IDs, constraints, defaults and wire shape are mirrored
  from it, and 170+ Go files cite the matter.js `path:function` they mirror.
  See CLAUDE.md §"matter.js as the Matter Gold Standard".

Compliance: Apache-2.0 is MIT-compatible. No matter.js source is reproduced
verbatim; the Go code is written from scratch. The Apache-2.0 license text is
kept at [`licenses/Apache-2.0.txt`](https://github.com/SukramJ/openccu-loom/blob/main/licenses/Apache-2.0.txt) and the
matter.js schema pin used for parity at
`notes/parity/matter/matter-schema-snapshot.json`.

## home-assistant-matter-bridge — Apache-2.0

- Source: <https://github.com/Nabu-Casa/home-assistant-matter-bridge> (local
  mirror `../home-assistant-matter-bridge/`)
- Copyright: © Nabu Casa, Inc. and the home-assistant-matter-bridge authors.
- Used as: a supplementary reference for end-to-end bridge composition
  (Aggregator + bridged devices). Not a gold standard — it carries
  Home-Assistant-specific shims — but useful when wiring the bridge.

Compliance: Apache-2.0, see [`licenses/Apache-2.0.txt`](https://github.com/SukramJ/openccu-loom/blob/main/licenses/Apache-2.0.txt).
No source is reproduced verbatim.

## Reverse direction

OpenCCU-Loom itself is MIT-licensed. Anyone reading this list to learn
from us is welcome to do the same — the LICENSE file applies.
