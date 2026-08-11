# Architecture Decision Records

Architecture Decision Records (ADRs) capture the consequential design choices behind OpenCCU-Loom — the decision, its context, and its consequences — so future readers can understand *why* the code looks the way it does.

!!! info "Who this page is for"
    Contributors and developers who need the rationale behind a design choice. ADRs are immutable once landed: a superseded decision gets a new ADR rather than an edit to the old one.

The table below catalogues every ADR. Each entry links to the record itself.

| # | Title | Summary |
| --- | --- | --- |
| [0001](../adr/0001-license-mit.md) | License: MIT | The source is licensed MIT. |
| [0002](../adr/0002-multi-ccu-first-class.md) | Multi-CCU as a first-class feature | One daemon serves many CCUs, scoped by `central_name`, from 0.1.0. |
| [0003](../adr/0003-embed-occu-extracts.md) | Embed openccu-data metadata artifacts | Translations, easymodes, and profiles are embedded from openccu-data. |
| [0004](../adr/0004-decorators-vs-cross-cutting.md) | Python decorators ↔ Go cross-cutting | How decorator-based concerns from the Python reference map to Go. |
| [0005](../adr/0005-visibility-as-outbound-filter.md) | Visibility as outbound filter | Visibility is applied as an outbound filter on REST and MQTT. |
| [0006](../adr/0006-naming-conventions.md) | Naming conventions for REST + MQTT | Shared naming rules across the REST and MQTT surfaces. |
| [0007](../adr/0007-strong-model-source-interface.md) | Strong model: Source interface | A single Source interface backs both reads and writes. |
| [0008](../adr/0008-aggregated-state-default-flip.md) | AggregatedState default-flip | Flips the AggregatedState default and removes the legacy path. |
| [0009](../adr/0009-service-method-command-topics.md) | Service-method command topics | Service-method command topics in HA Discovery. |
| [0010](../adr/0010-discovery-payload-from-model.md) | Discovery payload from the model | HA-Discovery payload construction moves into the model. |
| [0011](../adr/0011-mqtt-topic-and-payload-architecture.md) | MQTT topic & payload architecture | The topic and payload structure for the MQTT plane. |
| [0012](../adr/0012-matter-pure-go-implementation.md) | Matter bridge: pure-Go | The Matter bridge is implemented in pure Go, no CGo SDK. |
| [0013](../adr/0013-matter-commissioning-bring-up.md) | Matter wire-protocol design rules | Wire-protocol rules learned from chip-tool commissioning bring-up. |
| [0014](../adr/0014-parameter-ignore-unignore-mechanics.md) | Parameter ignore / un-ignore mechanics | How parameters are ignored and un-ignored. |
| [0015](../adr/0015-datapoint-usage-ignored.md) | Split Ignored from NoCreate | Separates `Ignored` from `NoCreate` in `DataPointUsage`. |
| [0016](../adr/0016-custom-dp-aware-ui-rendering.md) | Custom-DP-aware UI rendering | UI rendering is aware of custom data points. |
| [0017](../adr/0017-logging-and-diagnostics.md) | Logging and diagnostics | The logging and diagnostics model. |
| [0018](../adr/0018-health-parity-with-aiohomematic.md) | Health tracker parity | The health tracker mirrors aiohomematic. |
| [0019](../adr/0019-persistent-values-cache.md) | Persistent VALUES cache | A persistent VALUES cache with a wire-DP lifecycle. |
| [0020](../adr/0020-external-client-wire-contract.md) | External-client wire contract | The wire contract external clients depend on. |
| [0021](../adr/0021-mdns-self-advertisement.md) | mDNS self-advertisement | mDNS self-advertisement for LAN auto-discovery. |
| [0022](../adr/0022-ws-resume-and-kind.md) | WebSocket resume cursor + kind | The WebSocket resume cursor and envelope `kind` discriminator. |
| [0023](../adr/0023-hmipwired-is-product-group.md) | HmIP-Wired is a ProductGroup | HmIP-Wired is modeled as a ProductGroup, not an interface. |
| [0024](../adr/0024-instance-and-ccu-identity.md) | Instance vs CCU identity | Daemon-instance vs CCU identity, and the two interface ids. |
| [0025](../adr/0025-mcp-northbound-adapter.md) | MCP server as a north-bound adapter | The MCP server is a north-bound adapter. |
| [0026](../adr/0026-mcp-dev-mode.md) | MCP dev-mode | A build-tag-gated MCP introspection surface. |
| [0027](../adr/0027-encrypt-config-secrets-at-rest.md) | Encrypt config secrets at rest | Config secrets are encrypted at rest. |
| [0028](../adr/0028-contract-digest-and-version-guard.md) | Contract digest & version guard | A contract digest and version guard couple API-schema changes to the types-repo release. |
| [0029](../adr/0029-tier-model-stop-teardown.md) | Tier-model teardown for `Unit.Stop` | `Unit.Stop` tears down subsystems in tiered order. |
| [0030](../adr/0030-eventbus-dispatch-striping-rejected.md) | Event-bus dispatch striping: rejected | Per-central isolation already meets the goal; dispatch striping is rejected. |
| [0031](../adr/0031-im-opcode-dispatch-seam.md) | IM opcode dispatch seam | A testable gate and per-opcode seam are extracted from `handleIMOpcode`. |
| [0032](../adr/0032-sigma-resume-extraction.md) | Sigma resumption extraction | Sigma-resumption extraction is already satisfied; the finding is corrected. |
| [0033](../adr/0033-groups-cluster-stays-stub.md) | Groups cluster stays a stub | The minimal Groups-cluster stub is a deliberate, matter.js-conformant divergence. |
| [0034](../adr/0034-adapter-package-taxonomy.md) | Adapter package taxonomy | `internal/central/adapter` stays one package with a documented taxonomy. |
| [0035](../adr/0035-hub-refresh-set-extraction.md) | Hub refresh-set extraction | The refresh-coordination sub-component is extracted from `HubCoordinator`. |
| [0036](../adr/0036-bridge-decomposition.md) | Matter `Bridge` decomposition | The `CommissioningSession`/`IMEngine` facade split is deferred. |
| [0037](../adr/0037-otlp-span-exporter.md) | OTLP span exporter | A pluggable span exporter ships a lean OTLP/HTTP exporter, not the OTel-gRPC SDK. |
| [0038](../adr/0038-cross-stack-ci-gate.md) | Cross-stack CI gate | The cross-stack model-snapshot parity gate runs in nightly CI. |
| [0039](../adr/0039-subscribe-dispatch-seam.md) | Subscribe dispatch seam | Cohesive sub-helpers are extracted from `handleSubscribeRequest`. |
| [0040](../adr/0040-measurement-history.md) | Measurement history | Measurement history is stored in embedded SQLite with an opt-in push exporter. |
| [0041](../adr/0041-persist-auth-sessions.md) | Persist auth sessions | Auth sessions persist in SQLite as a save-through cache. |
| [0042](../adr/0042-clear-ccu-cache-and-repull.md) | Clear CCU cache and re-pull | Clearing CCU-derivable caches and re-pulling is a first-class operation. |
| [0043](../adr/0043-ccu-authentication-provider.md) | CCU as an authentication provider | Login can be delegated to a CCU's own user database. |
| [0044](../adr/0044-single-port-onboarding-and-ha-ingress-auth.md) | Single-port onboarding + HA Ingress auth | Onboarding runs on a single port with HA Ingress auth passthrough. |
| [0045](../adr/0045-login-and-onboarding-into-spa.md) | Login + onboarding into the SPA | Login and first-run onboarding live in the Svelte SPA. |
| [0046](../adr/0046-ssdp-ccu-discovery.md) | SSDP CCU discovery | CCUs are discovered on the LAN via active SSDP/UPnP. |
| [0047](../adr/0047-northbound-bridge-registry.md) | North-bound bridge registry | North-bound bridges are `Service`s owned by a `Registry`. |
| [0048](../adr/0048-chiptool-godevccu-send-receive-matrix.md) | chip-tool ↔ godevccu send/receive matrix | A hermetic per-DP-type Matter send/receive suite runs chip-tool against godevccu. |
| [0049](../adr/0049-matter-one-endpoint-per-device.md) | Matter one endpoint per device | Matter exposes one endpoint per physical device by default. |
| [0050](../adr/0050-mqtt-transport-shared-module.md) | MQTT transport → shared go-mqtt module | The in-tree MQTT transport is extracted into the external shared `go-mqtt` module. |
| [0051](../adr/0051-northbound-authorization-model.md) | North-bound authorization model | Role-based MinRole gating unified across REST + WS, plus backup-at-rest sealing. |
| [0052](../adr/0052-daemon-level-alarm-mqtt-topics.md) | Daemon-level alarm MQTT topics | The alarm engine publishes panel state/commands on daemon-level topics, not per-central ones. |
| [0053](../adr/0053-go-openccu-data-module.md) | CCU metadata via go-openccu-data | Embedded OCCU extracts come from the versioned go-openccu-data module instead of a hand-synced copy. |
| [0054](../adr/0054-remote-ingress-proxy-addon.md) | Remote ingress proxy add-on | OpenCCU-Loom Remote: an HA Ingress panel for remote instances via token injection, instead of the local-only Ingress passthrough. |
| [0055](../adr/0055-groups-jpages-proxy.md) | Heating groups via the CCU jpages proxy | Heating-group administration goes through the CCU's own jpages endpoints, the only interface that drives the member roster. |
| [0056](../adr/0056-room-areas-and-zone-naming.md) | Room areas, and the zone/area naming split | Operator-defined areas group CCU rooms one level up; the alarm partition is renamed "zone" to free the word. |
| [0057](../adr/0057-addon-self-update.md) | CCU add-on self-update | The CCU/RaspberryMatic add-on updates itself through the firmware's own `install_addon` path, with no WebUI round trip. |
| [0058](../adr/0058-mdns-ccu-serials.md) | mDNS TXT carries the configured CCU serials | Partially supersedes ADR 0021: the TXT record advertises the configured serials so clients can match before authenticating. |
| [0059](../adr/0059-security-safety-mqtt-plane.md) | The Security & Safety MQTT plane | Extends ADR 0052: hazard classes aggregate across centrals, so they get daemon-level topics too. |
| [0060](../adr/0060-loom-prefixed-interface-ids.md) | `loom`-prefixed CCU-facing interface ids | Partially supersedes ADR 0024: the wire-boundary `InitInterfaceID` carries a `loom` prefix and drops the repeated central name. |
| [0061](../adr/0061-migration-down-path-unsupported.md) | The migration Down path is unsupported | `goose` Down blocks exist to satisfy the tool; they are destructive and must never run in production. |
| [0062](../adr/0062-suppression-reasons-are-recomputed.md) | Suppression reasons are recomputed, not recorded | Extends ADR 0015: the `Ignored` mark says *that* a parameter is hidden, never which rule hid it, so the reason is re-derived from the same rule sets. |

## Related reading

- [Architecture](architecture.md) — how these decisions show up in the package layout.
- [Matter parity contract](../matter-parity-contract.md) — the binding rules for the Matter-side ADRs (0012, 0013, 0031, 0033, 0036, 0039, 0048, 0049).
