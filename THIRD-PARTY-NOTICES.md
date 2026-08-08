# Third-Party Notices

OpenCCU-Loom is MIT-licensed (see [`LICENSE`](./LICENSE)). It is built on the
prior art of several other projects — it is a Go port of `aiohomematic` for the
CCU side and a semantic port of `matter.js` for the Matter side, and it learns
from a handful of further reference implementations. None of their source is
copied verbatim into the OpenCCU-Loom tree; the Go and Svelte code is written
from scratch and cites the upstream file + function it mirrors in a file-top or
inline comment so the lineage stays auditable.

This file records the upstream projects, their licenses, and the verbatim
copyright notices they carry, so that the people behind them are credited and
the license obligations travel with any redistribution. Two further notices
cover content that actually ships inside the binary:

- Embedded CCU metadata (eQ-3 HomeMatic Software License) →
  [the go-openccu-data module's `NOTICE.md`](https://github.com/SukramJ/go-openccu-data/blob/main/NOTICE.md)
  and [ADR 0003](./docs/adr/0003-embed-occu-extracts.md).
- The narrative credits / acknowledgements →
  [`docs/attribution.md`](./docs/attribution.md).

Full license texts referenced below live under [`licenses/`](./licenses/):
[`MIT.txt`](./licenses/MIT.txt) and [`Apache-2.0.txt`](./licenses/Apache-2.0.txt).

---

## Reference projects (prior art the port is built on)

### aiohomematic — MIT

The reference implementation this project is a Go port of: transports, devices,
paramsets, custom-DP composition, enumerations, the device-profile catalogue.

- Source: <https://github.com/SukramJ/aiohomematic>
- Copyright notice (verbatim from upstream `LICENSE`):

  ```
  Copyright (c) 2021-2026 SukramJ, Daniel Perna
  ```

- License: MIT — see [`licenses/MIT.txt`](./licenses/MIT.txt).

### aiohomematic-config — MIT

Reference for the configuration-panel logic ported into `internal/configui`:
form schemas, parameter grouping, label resolution, preset selection.

- Source: <https://github.com/SukramJ/aiohomematic-config>
- Copyright notice (verbatim from upstream `LICENSE`):

  ```
  Copyright (c) 2021-2026 SukramJ, Daniel Perna
  ```

- License: MIT — see [`licenses/MIT.txt`](./licenses/MIT.txt).

### pydevccu — MIT

The Python HomeMatic CCU simulator that `godevccu` (the in-process test
simulator used by `tests/integration/`) is a pure-Go port of.

- Source: <https://github.com/danielperna84/pydevccu>
- Authors (verbatim from upstream `pyproject.toml`): Daniel Perna, SukramJ
- License: MIT — see [`licenses/MIT.txt`](./licenses/MIT.txt).

### godevccu — MIT

Pure-Go HomeMatic CCU simulator, consumed as a regular Go module dependency
for the integration tests.

- Source: <https://github.com/SukramJ/godevccu>
- Copyright notice (verbatim from upstream `LICENSE`):

  ```
  Copyright (c) 2026 SukramJ
  ```

- License: MIT — see [`licenses/MIT.txt`](./licenses/MIT.txt).

### openccu-data — MIT

Single source of truth for the HomeMatic CCU metadata extracts consumed by the
ecosystem. The *curated* MIT-licensed files (alias table, translation
overrides) ship in the binary; the eQ-3-derived extracts are governed
separately — see [the go-openccu-data module's `NOTICE.md`](https://github.com/SukramJ/go-openccu-data/blob/main/NOTICE.md).

- Source: <https://github.com/SukramJ/openccu-data>
- Copyright notice (verbatim from upstream `LICENSE`):

  ```
  Copyright (c) 2026 SukramJ
  ```

- License: MIT — see [`licenses/MIT.txt`](./licenses/MIT.txt).

### homematicip-local-frontend — MIT

Reference for the Config-UI interaction patterns (session-based MASTER editing,
undo/redo, dirty tracking, preset selection) re-implemented in the Svelte SPA.

- Source: <https://github.com/SukramJ/homematicip-local-frontend>
- Copyright notice (verbatim from upstream `LICENSE`):

  ```
  Copyright (c) 2026 SukramJ
  ```

- License: MIT — see [`licenses/MIT.txt`](./licenses/MIT.txt).

### matter.js — Apache-2.0

The Matter-side gold standard. Everything under `internal/north/matter/` is a
semantic port of matter.js HEAD: cluster IDs, revisions, attribute IDs,
constraints, defaults, and wire shape are mirrored from it. 170+ Go files cite
the matter.js `path:function` they mirror.

- Source: <https://github.com/project-chip/matter.js>
- Copyright: © Project CHIP / the matter.js Authors.
- License: Apache License 2.0 — see [`licenses/Apache-2.0.txt`](./licenses/Apache-2.0.txt).
- No matter.js source is reproduced verbatim. The canonical upstream `NOTICE`
  (if present at the pinned HEAD) travels with the matter.js repository; the
  matter.js schema pin used for parity lives at
  `notes/parity/matter/matter-schema-snapshot.json`.

### home-assistant-matter-bridge — Apache-2.0

Supplementary reference for end-to-end bridge composition (Aggregator + bridged
devices). Not a gold standard — carries Home-Assistant-specific shims — but read
when wiring the bridge.

- Source: <https://github.com/Nabu-Casa/home-assistant-matter-bridge>
- Copyright: © Nabu Casa, Inc. and the home-assistant-matter-bridge authors.
- License: Apache License 2.0 — see [`licenses/Apache-2.0.txt`](./licenses/Apache-2.0.txt).

### Home Assistant Frontend — Apache-2.0

Visual + structural reference for the `assets/ui/src/lib/control/` primitive set
and the card-feature pattern. Each mirroring Svelte file names the HA file it
mirrors at the top; no Lit/TypeScript is reproduced verbatim. The specific files
read are catalogued in [`docs/attribution.md`](./docs/attribution.md).

- Source: <https://github.com/home-assistant/frontend>
- Copyright: © Home Assistant / Open Home Foundation contributors.
- License: Apache License 2.0 — see [`licenses/Apache-2.0.txt`](./licenses/Apache-2.0.txt).

### eQ-3 OCCU — HomeMatic Software License (non-commercial)

Used as a factual API-inventory source for CCU `CONTROL` attribute names. No
OCCU software (ReGa code, `.fn` files, images) is reproduced. The CCU data
archives that *do* ship in the binary are addressed by
[the go-openccu-data module's `NOTICE.md`](https://github.com/SukramJ/go-openccu-data/blob/main/NOTICE.md) and
[ADR 0003](./docs/adr/0003-embed-occu-extracts.md).

- Source: <https://github.com/eq-3/occu>
- License: eQ-3 HomeMatic Software License — free for private, non-commercial
  use; commercial redistribution requires written permission from eQ-3 AG.

"Homematic" and "HomematicIP" are trademarks of eQ-3 AG. OpenCCU-Loom is not
affiliated with or endorsed by eQ-3.

---

## Go module dependencies

The Go dependency graph is entirely permissive (MIT / BSD-2/3-Clause /
Apache-2.0); there is no copyleft (GPL / LGPL / MPL / AGPL) dependency. See
[ADR 0001](./docs/adr/0001-license-mit.md) for the licensing decision. The
direct dependencies declared in [`go.mod`](./go.mod) include:

| Module | License (family) |
| --- | --- |
| `github.com/SukramJ/godevccu` | MIT |
| `github.com/getkin/kin-openapi` | MIT |
| `github.com/go-chi/chi/v5` | MIT |
| `github.com/google/uuid` | BSD-3-Clause |
| `github.com/grandcat/zeroconf` | MIT |
| `github.com/lmittmann/tint` | MIT |
| `github.com/mattn/go-isatty` | MIT |
| `github.com/miekg/dns` | BSD-3-Clause |
| `github.com/mochi-mqtt/server/v2` | MIT |
| `github.com/modelcontextprotocol/go-sdk` | MIT |
| `github.com/pressly/goose/v3` | MIT |
| `github.com/sasha-s/go-deadlock` | Apache-2.0 |
| `go.uber.org/goleak` | MIT |
| `golang.org/x/*` | BSD-3-Clause |
| `gopkg.in/yaml.v3` | MIT / Apache-2.0 |
| `modernc.org/sqlite` | BSD-3-Clause |

This table covers the direct requirements; the full transitive set (including
the indirect dependencies in `go.mod`) is permissive on the same basis. To
regenerate an exhaustive, machine-verified list, run a tool such as
`go-licenses report ./...` against the module graph.
