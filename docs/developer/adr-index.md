# Architecture Decision Records

Architecture Decision Records (ADRs) capture the consequential design choices behind OpenCCU-Loom — the decision, its context, and its consequences — so future readers can understand *why* the code looks the way it does.

!!! info "Who this page is for"
    Contributors and developers who need the rationale behind a design choice. ADRs are immutable once landed: a superseded decision gets a new ADR rather than an edit to the old one.

The table below catalogues every ADR. Each links to the record on GitHub.

| # | Title | Summary |
| --- | --- | --- |
| [0001](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0001-license-mit.md) | License: MIT | The source is licensed MIT. |
| [0002](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0002-multi-ccu-first-class.md) | Multi-CCU as a first-class feature | One daemon serves many CCUs, scoped by `central_name`, from 0.1.0. |
| [0003](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0003-embed-occu-extracts.md) | Embed openccu-data metadata artifacts | Translations, easymodes, and profiles are embedded from openccu-data. |
| [0004](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0004-decorators-vs-cross-cutting.md) | Python decorators ↔ Go cross-cutting | How decorator-based concerns from the Python reference map to Go. |
| [0005](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0005-visibility-as-outbound-filter.md) | Visibility as outbound filter | Visibility is applied as an outbound filter on REST and MQTT. |
| [0006](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0006-naming-conventions.md) | Naming conventions for REST + MQTT | Shared naming rules across the REST and MQTT surfaces. |
| [0007](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0007-strong-model-source-interface.md) | Strong model: Source interface | A single Source interface backs both reads and writes. |
| [0008](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0008-aggregated-state-default-flip.md) | AggregatedState default-flip | Flips the AggregatedState default and removes the legacy path. |
| [0009](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0009-service-method-command-topics.md) | Service-method command topics | Service-method command topics in HA Discovery. |
| [0010](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0010-discovery-payload-from-model.md) | Discovery payload from the model | HA-Discovery payload construction moves into the model. |
| [0011](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0011-mqtt-topic-and-payload-architecture.md) | MQTT topic & payload architecture | The topic and payload structure for the MQTT plane. |
| [0012](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0012-matter-pure-go-implementation.md) | Matter bridge: pure-Go | The Matter bridge is implemented in pure Go, no CGo SDK. |
| [0013](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0013-matter-commissioning-bring-up.md) | Matter wire-protocol design rules | Wire-protocol rules learned from chip-tool commissioning bring-up. |
| [0014](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0014-parameter-ignore-unignore-mechanics.md) | Parameter ignore / un-ignore mechanics | How parameters are ignored and un-ignored. |
| [0015](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0015-datapoint-usage-ignored.md) | Split Ignored from NoCreate | Separates `Ignored` from `NoCreate` in `DataPointUsage`. |
| [0016](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0016-custom-dp-aware-ui-rendering.md) | Custom-DP-aware UI rendering | UI rendering is aware of custom data points. |
| [0017](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0017-logging-and-diagnostics.md) | Logging and diagnostics | The logging and diagnostics model. |
| [0018](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0018-health-parity-with-aiohomematic.md) | Health tracker parity | The health tracker mirrors aiohomematic. |
| [0019](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0019-persistent-values-cache.md) | Persistent VALUES cache | A persistent VALUES cache with a wire-DP lifecycle. |
| [0020](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0020-external-client-wire-contract.md) | External-client wire contract | The wire contract external clients depend on. |
| [0021](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0021-mdns-self-advertisement.md) | mDNS self-advertisement | mDNS self-advertisement for LAN auto-discovery. |
| [0022](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0022-ws-resume-and-kind.md) | WebSocket resume cursor + kind | The WebSocket resume cursor and envelope `kind` discriminator. |
| [0023](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0023-hmipwired-is-product-group.md) | HmIP-Wired is a ProductGroup | HmIP-Wired is modeled as a ProductGroup, not an interface. |
| [0024](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0024-instance-and-ccu-identity.md) | Instance vs CCU identity | Daemon-instance vs CCU identity, and the two interface ids. |
| [0025](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0025-mcp-northbound-adapter.md) | MCP server as a north-bound adapter | The MCP server is a north-bound adapter. |
| [0026](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0026-mcp-dev-mode.md) | MCP dev-mode | A build-tag-gated MCP introspection surface. |
| [0027](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0027-encrypt-config-secrets-at-rest.md) | Encrypt config secrets at rest | Config secrets are encrypted at rest. |

## Related reading

- [Architecture](architecture.md) — how these decisions show up in the package layout.
- [`SPECIFICATION.md`](https://github.com/SukramJ/openccu-loom/blob/main/SPECIFICATION.md) — the design intent the ADRs implement.
- [Matter parity contract](../matter-parity-contract.md) — the binding rules for the Matter-side ADRs (0012, 0013).
