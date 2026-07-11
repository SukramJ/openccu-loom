# SPA-E2E against godevccu

End-to-end validation of the Svelte SPA's operating surfaces without a
physical CCU. Addresses the visibility gap for device types that are
missing from a given developer's own device inventory (cover, lock,
RGBW, …).

## Motivation

Three layers have to work together for a single tile click:

```
Svelte tile ─── HTTP/WS ─── daemon CDP dispatcher ─── ChannelWriter ─── CCU
```

Against a real CCU, a developer only exercises the devices in their own
household. Cover tiles on HmIP-FBL, lock on HmIP-DLD, RGBW on
HmIP-RGBW — if those are missing from the household, a regression
there goes undetected through every stage until it reaches a user.

aiohomematic solved this problem on the backend: every custom DP has
unit tests that show, against pydevccu, that a call against the DP
produces the expected wire calls. OpenCCU-Loom has structurally
equivalent backend-side coverage (per-custom-DP tests plus the
cross-stack snapshot against aiohomematic).

This document describes the extension to the SPA layer: a test setup
that drives the Svelte tiles' REST calls against the daemon-with-
godevccu and verifies that the wire-side response matches expectation.

## Architecture (as implemented)

Unlike the original proposal below, the implemented harness does not
start a separate daemon process or an HTTP client — it builds the
Central → Pipeline → CustomDPDispatcher stack in-process and drives it
through the same interfaces the REST handler uses, then reads back the
wire-side state from godevccu via `getValue` / `getParamset`:

```
┌──────────────────────────────────────────────────────────────────┐
│ Go test process (tests/integration/, build tag `integration`)    │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │ godevccu (in-process VirtualCCU)                           │  │
│  │   • XML-RPC + JSON-RPC on ephemeral ports                  │  │
│  │   • full OCCU device variety (~399 models)                 │  │
│  │   • device logic writes SetValue / PutParamset through     │  │
│  │     and emits event() push callbacks                       │  │
│  └────────────────────────────────────────────────────────────┘  │
│                  ▲ XML-RPC                                       │
│                  │                                               │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │ In-process daemon stack (spaHarness)                        │  │
│  │   • central.CentralUnit points at godevccu                 │  │
│  │   • Pipeline + CustomDPDispatcher built directly, no HTTP   │  │
│  │     listener — the test drives the same interfaces the     │  │
│  │     REST handler calls                                     │  │
│  └────────────────────────────────────────────────────────────┘  │
│                  ▲                                                │
│                  │                                               │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │ Plan runner (spaPlan.execute)                               │  │
│  │   • dispatches a CDP operation plan (spaAction)             │  │
│  │   • verifies the round trip (dispatch result + wire-side    │  │
│  │     effect + emitted event)                                 │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

The SPA itself is not started — the test drives the CDP operations
directly. The tests model the tiles' *operation plans* 1:1 (what a
user click triggers), not rendering. Rendering bugs are covered by
`svelte-check` and the Playwright visual-regression suite
(`assets/ui/tests/e2e/`), which are out of scope for this document.

## Operation plan per CDP kind

Each CDP kind gets a plan built from a `spaPlan` (setup + a list of
`spaAction` steps + expectations):

| Field | Meaning |
|---|---|
| setup | Device model + which channel |
| pre-state | Wire values that hold as the starting state; set directly via godevccu where needed |
| actions | List of (operation, params) — what the tile sends on click X |
| expected wire effect | Which wire calls (`setValue` / `putParamset`) godevccu must have observed |
| expected state | Which custom-DP state-payload fields must carry the expected value after the call |
| expected event | Which emitted events (`data_point` / `custom_data_point`) must have fired |

## Coverage (Slice 1, ADR 0016 follow-up)

Slice 1 covers the kind families that render in the overview view
today, one Go test function per device model — see
`tests/integration/spa_e2e_{climate,cover,light,lock,siren,switch}_test.go`:

| Tile | godevccu model | Test function |
|---|---|---|
| ClimateTile / `climate_hmip` | HmIP-BWTH | `TestSPAE2E_Climate_HmIP_BWTH` |
| ClimateTile / `climate_rf` | HM-CC-RT-DN | `TestSPAE2E_Climate_RF_HMCCRTDN` |
| CoverTile / `cover_blind` | HmIP-FBL | `TestSPAE2E_Cover_Blind_HmIPFBL` |
| CoverTile / `cover_rf_blind` | HM-LC-Bl1-FM | `TestSPAE2E_Cover_RfBlind_HMLCBl1FM` |
| CoverTile / `cover_garage` | HmIP-MOD-HO | `TestSPAE2E_Cover_Garage_HmIPMODHO` |
| LightTile / dimmer | HmIP-BDT | `TestSPAE2E_Light_Dimmer_HmIPBDT` |
| LightTile / `light_fixed_color` | HmIP-BSL | `TestSPAE2E_Light_FixedColor_HmIPBSL` |
| LightTile / `light_rgbw` | HmIP-RGBW | `TestSPAE2E_Light_RGBW_HmIPRGBW` |
| LightTile / RF dimmer | HM-LC-Dim1T-FM | `TestSPAE2E_Light_RfDimmer_HMLCDim1TFM` |
| LockTile | HmIP-DLD | `TestSPAE2E_Lock_HmIPDLD` |
| SwitchTile | HmIP-BSM | `TestSPAE2E_Switch_HmIPBSM` |
| SirenTile | HmIP-ASIR | `TestSPAE2E_Siren_HmIPASIR` |

Extensions (boundary values, error paths, disallowed operations) are
open follow-up work, not yet implemented.

## Test layout (as implemented)

```
tests/integration/
├── spa_e2e_harness_test.go   (build tag integration) — in-process Central/Pipeline/Dispatcher stack, godevccu wiring
├── spa_e2e_plans_test.go     spaPlan / spaAction types + the plan runner (execute)
├── spa_e2e_climate_test.go   per-kind test functions
├── spa_e2e_cover_test.go
├── spa_e2e_light_test.go
├── spa_e2e_lock_test.go
├── spa_e2e_siren_test.go
└── spa_e2e_switch_test.go
```

Build tag `integration` keeps the tests out of the normal `make test`
run. Run the whole suite (including these) via:

```sh
make integration
```

or scope to just the SPA-E2E files:

```sh
go test -tags=integration -run TestSPAE2E ./tests/integration/...
```

There is no separate `tests/spa_e2e/` directory and no `make e2e-spa`
target — the original proposal below (test layout under
`tests/spa_e2e/` with a dedicated `e2e` build tag and CI stage) was
implemented directly inside `tests/integration/` under the existing
`integration` build tag instead.

## Wire-side verification

The harness reads wire-side state back from godevccu directly via
`getValue` / `getParamset` calls issued by the test after each plan
step, rather than through a separate call-recording hook or sniffing
proxy on the XML-RPC transport.

## Migration vs. extension

The existing integration tests under `tests/integration/` (godevccu)
test the **daemon model**: that the daemon builds the correct custom-DP
model from godevccu events. Those tests remain. The SPA-E2E tests sit
*above* the daemon's dispatch surface and specifically cover the SPA
operation paths.

The separation is clean: daemon-model tests never see the CDP
dispatch surface; SPA-E2E tests never reach into custom-DP internals
directly — both drive the same in-process stack from different angles.

## Acceptance criterion

Slice 1 is successful when:

- `go test -tags=integration -run TestSPAE2E ./tests/integration/...`
  passes.
- Every kind from the overview view has at least one plan.
- A deliberately introduced regression bug (e.g. `set_mode` with the
  wrong `CONTROL_MODE` value) is reliably caught by the test setup.

Both conditions hold for Slice 1 as implemented; see the coverage
table above for source-of-truth kind ↔ test mapping.
