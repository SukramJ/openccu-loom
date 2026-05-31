# Matter bridge — conformance plan

**Status:** 2026-05-05 · **Target version:** Matter 1.5.1 ·
**Related:** [ADR 0012](./adr/0012-matter-pure-go-implementation.md),
[`internal/north/matter/conformance`](../internal/north/matter/conformance)

This document collects the tests the v1.1 Matter bridge has to pass
before every release. It mixes automated Go tests with manual smoke
runs against real controller hardware.

---

## 1. Automated tests (CI)

### 1.1 Unit + round-trip (every PR)

`go test ./internal/north/matter/...` — every unit test written in
phases 1–9. ~700 tests today, `-race` clean.

### 1.2 Golden-vector conformance (every PR)

`go test ./internal/north/matter/conformance/... -run Vectors` — pinned
wire bytes for the TLV codec and the application-cluster wire codecs.
Drift in encoder ordering or decoder tolerance fails the test.

### 1.3 Load tests (nightly)

`go test ./internal/north/matter/conformance/... -run Load -timeout 5m`
— drives the subscription manager at 600 endpoints (200 × 3
channels). Failure modes: linearity drift in `OnAttributeChanged` /
`Tick` sweep or mutex hot-spotting.

---

## 2. chip-tool smoke (build-tag-gated)

`go test -tags chiptool ./internal/north/matter/conformance/...`

Prerequisites:

- `chip-tool` from the `connectedhomeip` repo installed locally
  (Apple-CSA reference implementation).
- A bridge instance running on the loopback multicast zone (`lo` or
  `dummy0` via `ip link add dummy0 type dummy`).
- Operator has set `passcode=20202021`, `discriminator=3840` in the
  bridge config.

The test is a ship blocker: no green chip-tool pairing run, no
release. CI runs it on a Linux runner with multicast permission;
locally a developer can skip it because the `LookPath` check makes
chip-tool optional.

---

## 3. Manual smoke tests (release checklist)

These tests run on release day against real controller hardware. Each
entry is recorded in `CHANGELOG.md` as `[manually verified]`.

### 3.1 Home Assistant Matter Server

1. HA Core ≥ 2025.3 with the Matter add-on active (Matter Server
   ≥ 7.0.0).
2. `/api/v1/matter/commissioning:open` (REST) → open the pairing
   window, scan the QR code from the response in the HA UI.
3. Expectation: HA lists the bridge under "Bridges" plus every
   bridged endpoint individually.
4. Toggle a light endpoint → the CCU side mirrors it in MQTT.
5. Stop / restart the bridge → HA shows "Reachable" green again
   within 60 s.

### 3.2 Apple Home

1. iPhone iOS ≥ 17, "Add accessory" → "More options" → QR code from
   the bridge pairing window.
2. Expectation: Apple Home accepts vendor / product / NodeLabel and
   offers room assignment.
3. Lights / covers / thermostat all toggle individually from the
   Apple Home UI.
4. "Share bridge" with a second Apple Home account → CASE multi-
   fabric works.

### 3.3 Google Home

1. Google Home app ≥ 3.4, "Add device" → Matter → scan QR.
2. Expectation: pairing completes within 30 s.
3. Routines work against the bridge endpoints; voice control via
   Google Assistant works.

### 3.4 SmartThings (best-effort)

Not a ship blocker for v1.1 GA, but a single smoke run on a
SmartThings hub before release surfaces early signals about
mandatory-attribute drift.

---

## 4. Known limitations

- **No BLE pairing paths** in v1.1 — see ADR 0012 §"Risks #2".
  The manual smoke runs go through on-network mDNS exclusively.
- **Apple Home multi-hub setups** with ≥ 3 hubs are not covered —
  the use cases are rare and HA Server conformance is enough for
  the release decision.
- **Subscription resumption** across reboot is missing in v1.1 (see
  the phase-7 docs) — smoke tests must factor in a fresh subscribe
  phase after every reboot.
