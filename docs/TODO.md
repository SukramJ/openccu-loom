# OpenCCU-Loom — Open Work (grouped checklist)

A working checklist of open items, grouped by area. Synthesised from
`docs/roadmap.md`, `SPECIFICATION.md`, `docs/parity/by_design.md`,
`docs/testplan.md` and code markers.

> **Important:** a 2026-06 verification pass found that several items the audit
> docs (`by_design.md`) listed as open were **already implemented** in later
> waves — the notes were stale. Such items are ticked below with a `— done`
> note and the corrected `by_design.md` entries. **Always verify the actual
> code state before implementing a line here.**

Legend — type: **[gap]** real fixable gap · **[gap-test]** missing test for
already-correct code · **[planned]** intentional future / trigger-driven.
Priority **P1/P2/P3**. Effort **S/M/L** (hours/days/weeks). Intentional
divergences live in `docs/parity/by_design.md` and are not tracked here.

## 1. Release / Ops

- [ ] **Tag & release `v0.14.6` / current `main`** — merged, untagged. `[planned]` P2 S — operator decision.
- [ ] **openccu-data resync after upstream releases** — `make update-ccu-data`. `[planned]` P3 S (standing).

## 2. North — REST / WebSocket / MQTT / SPA

- [x] **Device-removal WS broadcast** — done; `DeviceLifecycleSubscriber.Start()` runs at boot (`daemon_sysstatus.go`).
- [x] **MQTT `last_event_age` discovery + publish** — done; metric + discovery + publish wired.
- [x] **5 WS commands** (`schedules.set_enabled`, `links.get_form_schema`, `links.get_profiles`, `links.test_profile`, `paramset.determine`) — done; real handlers wired in `ws_adapters.go` (the "stub" only applies when a provider is nil).
- [x] **Per-interface install-mode REST** — done; `GET/POST /install-mode/interfaces` routed.
- [x] **Manual device add** — done; `POST /devices/{addr}/accept` + adapter + coordinator.
- [x] **`firmware.refresh` WS command** — done (0.15.0); `FirmwareDomain` wired.
- [x] **Link-peer refresh** — done (0.15.0); folded into `config.reload_device_config` (on demand, no boot RPC sweep).
- [ ] **OpenAPI ↔ TypeScript reconciliation** — hand-written types in `assets/ui/src/lib/api/types.ts` diverge from the generated spec types (optional `central`, capitalised keys). Align the SPA to the spec (source of truth) + guard composite-key call sites. `[gap]` P2 M.
- [ ] **SPA schedule editor add/edit/delete-slot UI** — REST/WS paths exist; verify whether the Svelte slot-editor is wired (BD-HubDataPoint-EmptySimpleEntry). `[gap]` P2 M — verify first.

## 3. Matter bridge (config-flag-gated, default OFF — no pairing risk)

> Verify each cluster's real state first. Mount only behind an opt-in flag:
> extra RootNode clusters can make Apple Home reject the bridge at pairing.

- [ ] **AccessRestrictionList (ARL, 0x002B)** — skeleton present, not mounted. `[planned]` P3 L.
- [ ] **TimeSynchronization (0x0038)** — impl exists, not mounted. `[planned]` P3 M.
- [ ] **Actions (0x0025)** on the Aggregator. `[planned]` P3 M.
- [ ] **AccessControl Extension store** — fabric-scoped + conflict + fan-out. `[planned]` P3 M.
- [ ] **Event-driven re-announce + matter.js backoff** — fixed 30-min cadence today. `[planned]` P3 S–M.
- [ ] **Cluster-revision parity tests (8)** — Thermostat, DoorLock, WindowCovering, … `[gap-test]` P2 M.

## 5. Persistence / Config

- [ ] **Per-central feature-flag layer** — single daemon config today. `[planned]` P3 M — verify first.

## 6. Testing (targeted — the 4 named risk areas, not a blanket LOC push)

- [ ] **Reliability timing tests** via `internal/clock` (backoff/jitter). `[gap]` P1 M.
- [ ] **Coordinator integration scenarios** (recovery, failover). `[gap]` P2 M–L.
- [ ] **Store/cache coherency** (paramset patches + invalidation). `[gap]` P2 M.
- [ ] **Consolidated event-bus race battery** (~38 scenarios). `[gap]` P2 M.
- [ ] **Pre-release load/soak harness** — `tests/loadtest` (`-tags=loadtest`); the 60-min ≥1000-device run is operator-executed. `[planned]` P1 L.

## 7. Docs / ADRs

- [ ] **SPA user-guide screenshots (3)** + **Matter commissioning screenshot** — generated via the Playwright pipeline. `[gap]` P3 S.
- [ ] **Backfill missing ADRs** — recent (post-0.12) decisions not yet formal ADRs. `[gap]` P3 M.
