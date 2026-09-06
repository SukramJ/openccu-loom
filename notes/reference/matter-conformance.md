# Matter bridge — conformance plan

**Status:** the test list below is maintained; the version it targets is not
pinned here. The authoritative Matter revision is whatever
`notes/parity/matter/matter-schema-snapshot.json` records under `matter`
(`revision`, `specificationVersion`, `sourceCommit`) — it moves with every
`make sync-matter-schema`, so a number written into this page goes stale
within a release. At the time of writing the snapshot pins revision 1.6.0.
**Related:** [ADR 0012](../../docs/adr/0012-matter-pure-go-implementation.md),
[go-fabric `conformance`](https://github.com/SukramJ/go-fabric/tree/main/conformance)

This document collects the tests that exercise the v1.1 Matter bridge:
automated Go tests plus manual smoke runs against real controller
hardware. Sections 1 and 2 describe what CI actually runs and when;
section 3 is a manual checklist that no release currently gates on.
Where a test is a gate, the section says which workflow enforces it.

---

## 1. Automated tests (CI)

The Matter stack is no longer part of this repository: it lives in the
[go-fabric](https://github.com/SukramJ/go-fabric) module, and its tests run in
that module's CI, not this one. The commands below say which repository each
one belongs to, because running them here finds nothing — `internal/north/matter/`
has not existed since the extraction, and a command naming it is not a test
that is failing, it is a test that is absent.

What stayed here is the host side: the model walk that turns CCU devices into
endpoints (`internal/north/matteradapter/`, 17 test files), the per-device
projections under `internal/model/`, and the chip-tool suite in
`tests/chiptool/`. Those run in this repository's `test` job on every pull
request, and section 2 covers the chip-tool layer separately because it is
gated differently.

### 1.1 Unit + round-trip (every PR, go-fabric)

`make race` in go-fabric — `go test -race -count=1 ./...` over the whole
module. There is no Matter-only invocation because the module is Matter.

### 1.2 Golden-vector conformance (every PR, go-fabric)

`go test ./conformance/ -run TestTLVCoreVectors` in go-fabric — pinned wire
bytes for the TLV codec. It carries no build tag, so it also runs as part of
1.1; naming it separately is for the reader who wants to run just this one.
Drift in encoder ordering or decoder tolerance fails it.

### 1.3 Subscription fan-out under load (every PR, go-fabric)

`go test ./conformance/ -run TestLoadSubscriptionFanout` in go-fabric. It
carries no build tag either, so it runs on every pull request rather than
nightly — the "nightly" this section used to claim was never configured
anywhere. Failure modes: linearity drift in the attribute-change sweep, or
mutex hot-spotting.

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

**The suite is not a release gate.** `.github/workflows/chiptool.yml`
runs its three jobs — `chip-tool capability suite`,
`chip-tool control leg (CHIP app, no bridge)` and
`matter-smoke (compose PASE)` — nightly (`cron: "47 3 * * *"`), on
`workflow_dispatch`, on the `needs-chiptool` pull-request label, and
automatically on any pull request whose diff matches the path list in
that workflow's `changes` job (the matteradapter, the endpoint store,
`*matter*.go` model and daemon files, `tests/chiptool/`, the compose
smoke file, the workflow itself, and `go.mod` / `go.sum`).

Nothing makes a release depend on it. `release.yml` is the only
tag-triggered workflow (`on: push: tags: ["v*"]`); its jobs are
`goreleaser` and `notify-client-repo`, and neither references chip-tool
or waits on the chiptool workflow. No other workflow references it
either. The release procedure's own gate is
`make lint && make test && make contract`, which does not build the
`chiptool` tag. So a tag can be pushed and a release published while the
last chip-tool run is red or has never run for that commit.

One thing this page cannot settle from the repository: whether a
chiptool job is configured as a **required status check** in GitHub
branch protection. That setting lives in repository configuration, not
in `.github/`, and was not queried. It would gate *merging a pull
request*, never *publishing a tag* — so even a required check there
would not make the sentence above wrong.

Locally a developer can skip the suite because the `LookPath` check
makes chip-tool optional (`tests/chiptool/harness/skip.go`);
`OPENCCU_LOOM_CHIPTOOL_BIN` pins the binary in CI so the job cannot
pass by skipping.

---

## 3. Manual smoke tests (release checklist)

This is a checklist to work through against real controller hardware —
not a record of anything that has happened. **No release has recorded a
result of it.** `CHANGELOG.md` holds 191 release sections (`^## \[`) and
zero occurrences of `manually verified`; a grep for equivalent markers
(`verified`, `smoke`, the controller names) returns only prose about
code changes, never a per-release verification stamp. There is likewise
no manual-verification step in the release procedure, whose gate is
`make lint && make test && make contract`.

Treat the runs below as optional hardware checks a maintainer may
perform. If one is performed and its outcome is meant to be durable, it
has to be written down somewhere — nothing currently writes it down.

### 3.1 Home Assistant Matter Server

1. HA Core ≥ 2025.3 with the Matter add-on active (Matter Server
   ≥ 7.0.0).
2. `POST /api/v1/matter/commissioning/window` (REST, body
   `{"duration_seconds": N}`) → open the pairing window, scan the QR
   code from the response in the HA UI. Early-close is
   `POST /api/v1/matter/commissioning/window/close`.
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

**Subscription resumption** across reboot is implemented: persistent
subscriptions survive restart via migration
`018_matter_persistent_subscriptions.sql` +
`internal/north/matter/store/subscriptions.go`, and CASE session
resumption (Sigma2Resume) is wired in `secure/sigma`, so a controller
re-attaches without a fresh subscribe phase after a bridge reboot.
