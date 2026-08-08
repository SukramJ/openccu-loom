# ADR 0023 — HmIP-Wired Is a ProductGroup, Not an Interface

- **Status**: Accepted
- **Date**: 2026-05-26
- **Related**: ADR 0002 (multi-CCU), [SPECIFICATION.md](https://github.com/SukramJ/openccu-loom/blob/main/SPECIFICATION.md) §2.1 / §5

## Context

OpenCCU-Loom carried `InterfaceHmIPWired` as a separate value in
`pkg/hmenum.Interface` alongside `InterfaceHmIPRF`, mirroring the
visible split between Wired and RF in the HmIP product line. On the
CCU itself there is **no** matching split — both flavours share a
single XML-RPC service (port 2010 / 42010) under the `HmIP-RF`
interface ID. The duplication leaked into the codebase in three
shapes:

- `DetectionPorts[InterfaceHmIPWired]` carried the **same** plain
  and TLS ports as `InterfaceHmIPRF`.
- `PrimaryClientCandidateInterfaces` had to **explicitly exclude**
  Wired to keep facade selection deterministic.
- A configuration with both `HmIP-RF` and `HmIP-Wired` listed would
  build two parallel XML-RPC clients against the same CCU port,
  registering the same `interface_id` twice and racing against the
  daemon's own callback server.

aiohomematic — our CCU-side reference (CLAUDE.md §aiohomematic as
a Reference) — handles this cleanly:

- `Interface` enum (`aiohomematic/const.py:1472`) contains only
  `HMIP_RF`; there is no `HMIP_WIRED` member.
- `ProductGroup` enum (`aiohomematic/const.py:1429`) carries
  `HMIPW = "HmIP-Wired"`; classification happens via the device
  model-name prefix (`get_product_group` in
  `aiohomematic/client/interface_client.py:633`).

## Decision

Treat HmIP-Wired the same way aiohomematic does:

1. Remove `InterfaceHmIPWired` from `pkg/hmenum.Interface`.
2. Classify HmIP-Wired devices through the `ProductGroup` mechanism
   (`ProductGroupHmIPW`), derived from the device model-name prefix
   `hmipw-*` via `hmenum.ProductGroupForModel`, with an interface
   fallback for devices whose model name does not match a canonical
   prefix.
3. Reject `HmIP-Wired` as an interface name in
   `InterfaceSpec.Validate` with a message that points the operator
   at the ProductGroup-based classification path. No silent alias.

This is a deliberate alignment with aiohomematic, not a divergence,
and it removes the duplicate XML-RPC client risk for installations
that previously listed both interfaces.

## Consequences

- **Greenfield config break**: configurations that name `HmIP-Wired`
  as an interface now fail validation at startup. Operators upgrading
  from a pre-release that exposed the dual-interface model must
  remove the redundant entry. Aligns with the project policy of "no
  backwards-compatibility shims for aiohomematic data" (CLAUDE.md).
- **One XML-RPC client per HmIP CCU service**, never two. The
  `interface_id` collision is impossible.
- **Per-device classification** via
  `hmenum.ProductGroupForModel(model, iface)` becomes the
  authoritative source for "is this a Wired device?" — the function
  is a 1:1 port of aiohomematic's `get_product_group`.
- **External wire identity unchanged**: `ProductGroupHmIPW` still
  uses the wire token `"HmIP-Wired"` in REST, WebSocket, MQTT, and
  the snapshot diff schema. No external API breakage.
- The `PushesConfigPendingFor(iface, group)` helper in
  `pkg/hmenum/interface.go` now carries the load for Wired-vs-RF
  decisions: the interface alone cannot distinguish them, so the
  ProductGroup wins.
