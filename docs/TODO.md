# OpenCCU-Loom — Open Work (grouped checklist)

A working checklist of open items, grouped by area, so they can be worked off
one by one. Synthesised from the authoritative sources: `docs/roadmap.md`,
`SPECIFICATION.md`, `docs/parity/by_design.md`, `docs/testplan.md`, plus code
markers.

Legend — type: **[gap]** real, fixable gap · **[gap-test]** missing test for
already-correct code · **[planned]** intentional future / trigger-driven work.
Priority **P1/P2/P3**. Effort **S** (hours) / **M** (days) / **L** (weeks).

> Intentional, documented divergences (~97 items) live in
> `docs/parity/by_design.md`. They are **by design, not open work**, and are not
> tracked here.

When an item lands: tick it and add a `CHANGELOG.md` entry.

## Suggested order

1. Tag `v0.14.6` (if the CLI fix should ship).
2. Quick wins: device-removal broadcast → MQTT `last_event_age` publish.
3. Test debt (reliability + coordinators) — it dominates the risk toward 1.0.

---

## 1. Release / Ops

- [ ] **Tag & release `v0.14.6`** — merged to `main`, currently untagged. `[planned]` P2 S — operator decision (CLI `OPENCCU_LOOM_DATA_DIR` fix).
- [ ] **openccu-data resync after upstream releases** — `make update-ccu-data`; no automation (`docs/roadmap.md`). `[planned]` P3 S (standing task).

## 2. North — REST / WebSocket / MQTT / SPA

- [ ] **Broadcast device removal to the SPA** — `DeviceRemovedEvent` is produced but not pushed over WS; the SPA only catches it on a periodic refresh (`by_design.md` BD-A1-V13). `[gap]` P2 S.
- [ ] **MQTT `last_event_age` discovery + publish path** — metric is observed by the scheduler, but `hub_mqtt_publisher.go` publishes only `SystemHealth` (A3-G5). `[gap]` P2 S–M.
- [ ] **Wire the 5 WebSocket command stubs** — `ws_set_schedule_enabled`, `ws_get_link_form_schema`, `ws_get_link_profiles`, `ws_test_link_profile`, `ws_determine_parameter` return `ErrUnimplemented`; domain layer defined (A7-BD-WS-STUBS). `[planned]` P2 M.
- [ ] **SPA schedule editor — add/edit/delete slot UI** — REST/WS paths exist; the Svelte "add slot" button is unwired (BD-HubDataPoint-EmptySimpleEntry). `[gap]` P2 M.
- [ ] **Reconcile the `TODO(openapi-typescript)` blocks (8 remaining)** — field/shape drift between live server responses and the HA client types (`docs/audit/architecture-*`). `[gap]` P2 M.
- [ ] **Per-interface install-mode** — REST currently exposes install-mode hub-wide; per-interface needs a Connectivity-style map + REST revision (A3-G2). `[planned]` P3 M.
- [ ] **Manual device add** — reference `AddNewDevicesManually` has no REST/WS endpoint yet (A5-P02). `[planned]` P3 M.
- [ ] **Firmware refresh operator service** — `FirmwareRefresher` interface present, not wired (A5-P03). `[planned]` P3 M.
- [ ] **Link-peer boot-time fetch** — auto-wire `RefreshDeviceLinkPeers` after profiling per-channel RPC cost (BD-A1-V07). `[planned]` P3 M.

## 3. Matter bridge (trigger-driven — add when a controller / use-case needs it)

- [ ] **AccessRestrictionList (ARL, 0x002B)** — skeleton present, not mounted; needs fabric-scoped TLV store + AddNOC rollback + command dispatch (BD-Matter-ARL-NotMounted; `access_restriction.go:22`). `[planned]` P3 L.
- [ ] **TimeSynchronization (0x0038) optional mount** — implementation exists, not mounted (BD-Matter-TimeSync-NotMounted). `[planned]` P3 M.
- [ ] **Actions (0x0025) on the Aggregator** — no scene/action surface yet (BD-Matter-Actions-NotMounted). `[planned]` P3 M.
- [ ] **AccessControl Extension store** — fabric-scoped store + conflict resolution + ExtensionChanged fan-out; no known consumer writes it today. `[planned]` P3 M.
- [ ] **Event-driven re-announce + matter.js backoff** — fixed 30-min mDNS cadence today (L7-D07). `[planned]` P3 S–M.
- [ ] **Cluster-revision parity test cases (8 remaining)** — Thermostat, DoorLock, WindowCovering, … production paths are Apple-pair-verified; only the parity tests are missing (BD-Matter-RemainingAuditTestGaps). `[gap-test]` P2 M.

## 4. Southbound — Homegear depth

- [ ] **Full Homegear programs / rooms / functions parity** — sysvars done (0.12+); the rest is post-1.0 (Homegear has no ReGa; some operations have no equivalent). `SPECIFICATION.md` §2.2. `[planned]` P3 L.

## 5. Persistence / Config

- [ ] **Per-central feature-flag layer** — single daemon config today; add a per-`CentralUnit` flag layer if operators want per-central scan profiles (A5-P04). `[planned]` P3 M.

## 6. Testing (largest single debt — ~60–81 person-days toward 1.0)

- [ ] **Reliability timing tests** — backoff/jitter envelopes via the `internal/clock` abstraction (`docs/testplan.md` P1-2). `[gap]` P1 M.
- [ ] **Pre-release load / soak test** — ≥1000 devices, 10k req/s, 60-min soak + a `tests/loadtest` package behind `-tags=loadtest` (`docs/testplan.md`:199-234). `[planned]` P1 L (before 1.0).
- [ ] **Coordinator integration scenarios** — multi-step recovery, failover chains (testplan P2). `[gap]` P2 M–L.
- [ ] **Store/cache coherency** — paramset patches + invalidation over the SQLite txn paths (testplan P3). `[gap]` P2 M.
- [ ] **Consolidated event-bus race battery** — ~38 async scenarios collapsed into one `-race` suite (testplan P2-3). `[gap]` P2 M.
- [ ] **Close the test-LOC gap** — Go ~24k vs the Python family ~107k (testplan). `[planned]` P2 L (ongoing).

## 7. Docs / ADRs

- [ ] **SPA user-guide screenshots (3)** — main nav / device list, device detail, channel-config form (`docs/user` guide). `[gap]` P3 S.
- [ ] **Matter commissioning screenshot** — QR + manual pairing code (`docs/user` Matter guide). `[gap]` P3 S.
- [ ] **Backfill missing ADRs** — some recent (post-0.12) architectural decisions live in commit messages but are not yet formal ADRs under `docs/adr/`. `[gap]` P3 M.
