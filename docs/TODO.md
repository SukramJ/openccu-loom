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
- [x] **OpenAPI ↔ TypeScript reconciliation** — done (0.15.0, #192); `openapi.yaml` corrected to the server JSON, generated types regenerated, SPA overrides removed, REST APIVersion → 2.1.0.
- [x] **SPA schedule editor add/edit/delete-slot UI** — done; the Svelte slot editor is wired (verified, by_design A1-BD03). The Go `EmptySimpleEntry` helper stays uncalled (minor, noted in by_design).

## 3. Matter bridge (config-flag-gated, default OFF — no pairing risk)

> Verify each cluster's real state first. Mount only behind an opt-in flag:
> extra RootNode clusters can make Apple Home reject the bridge at pairing.

- [x] **TimeSynchronization (0x0038)** — done; flag-gated mount via `north.matter.enable_time_sync` (default off).
- [x] **AccessControl Extension** — already implemented (per-fabric extension list, mounted with the AccessControl cluster).
- [x] **Event-driven re-announce** — already wired: `RemoveFabric` triggers an immediate mDNS re-announce (`TriggerReannounce`) on top of the 30-min cadence.
- [x] **Cluster-revision parity tests** — already present in `parityCases()` (TimeSync, AccessControl, GroupKeyManagement, PowerSource, ICD…).
- [ ] **AccessRestrictionList (ARL, 0x002B)** — intentionally deferred: no Managed-Aggregator use-case; a full matter.js-grade port (fabric store + AddNOC review + enforcement) is weeks of sensitive work. `[planned]` P3 L.
- [ ] **Actions (0x0025)** — intentionally deferred: the bridge has no scene/action surface to model. `[planned]` P3 M.

## 4. Southbound — Homegear — CONCLUDED (at parity)

- [x] **Homegear backend** — concluded (2026-06). At parity with the defined
  target (aiohomematic's Homegear support): the `HomegearBackend` implements the
  full operations surface (devices, paramsets, get/set value, links, sysvars,
  metadata, device name, `determineParameter`); ReGa-only ops (programs, rooms,
  functions, inbox, system-update, sysvar-create) return `ErrUnsupported`,
  matching aiohomematic. Going beyond (full CCU-like depth, non-HomeMatic
  families, live-Homegear validation) is the explicit non-goal in
  `SPECIFICATION.md` §2.2 — not planned. See `docs/roadmap.md` Phase 3.

## 5. Persistence / Config

- [x] **Per-central feature-flag layer** — no genuine gap: the TODO mis-attributed `A5-P04` (an unrelated by-design item); no per-central flag scaffolding exists and there is no operator demand.

## 6. Testing (targeted — the 4 named risk areas, not a blanket LOC push)

- [x] **Reliability timing tests** — done; circuit-breaker OPEN→HALF_OPEN boundary + retry backoff timeline via the clock seam.
- [x] **Coordinator integration scenarios** — done; readiness-gate re-entry, multi-interface concurrent failover, classification-reason chains, event-driven recovery.
- [x] **Store/cache coherency** — done; patch→upsert cache coherency, multi-paramset-key isolation, device-model-change (additive-cache limitation pinned).
- [x] **Consolidated event-bus race battery** — extended; cross-priority reentrancy, self-unsubscribe, clear+resubscribe, panic cascade (all under `-race`).
- [x] **Pre-release load/soak harness** — done; `tests/loadtest` (`-tags=loadtest`), env-scaled smoke + operator-run ≥1000-device/60-min soak.

## 7. Docs / ADRs

- [x] **SPA user-guide screenshots (3)** + **Matter commissioning screenshot** — done; generated via the Playwright pipeline into `docs/user/img/` and embedded in `web-ui.md` / `matter.md`.
- [x] **Backfill missing ADRs** — no genuine gap: ADRs are complete through 0044; recent post-0.12 work is bug-fixes / small features recorded in `CHANGELOG.md` + `by_design.md`, none an architectural shift warranting a new ADR.
