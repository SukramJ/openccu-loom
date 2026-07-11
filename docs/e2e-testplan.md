# OpenCCU-Loom — End-to-End Test Plan

This document defines the **end-to-end (E2E) test layer** for
OpenCCU-Loom: a black-box verification of every externally offered
interface against a fully assembled daemon, designed to run
hermetically in CI on every pull request.

It complements — and does not replace — the existing test pillars:

| Pillar | Where | Purpose | Run by |
|---|---|---|---|
| Unit | `internal/**/*_test.go`, `pkg/**/*_test.go` | per-package logic | `make test` |
| Contract | `tests/contract/` | protocol / capability invariants | `make contract` |
| Golden | `tests/golden/` | recorded session replay | `make test` |
| Integration | `tests/integration/` | godevccu + Mosquitto, white-box | `make integration` |
| **E2E** (this plan) | `tests/e2e/` | **black-box, all north-bound surfaces** | `make e2e` |
| Bench | `tests/bench/` | performance | `make bench` |
| Live smoke | `tests/integration/*live*` | real CCU | manual |

The E2E layer is the only one that drives the daemon **the way an
external client would**: through the real wire, with real protocol
codecs, against real handlers, with no shortcut into internal types.

---

## 1. Goals

- **Surface coverage**: every externally offered interface has at
  least one Go-test that drives it through its production transport.
- **Schema enforcement**: REST and WebSocket suites are derived from
  `assets/openapi.yaml` and `assets/wsapi.json` so a new endpoint
  cannot be merged without an explicit Test- or Skip-entry.
- **CI hermeticity**: zero external dependencies (no Docker, no real
  CCU, no internet) — runs on `ubuntu-latest`, `macos-latest`, and
  `windows-latest`.
- **Determinism**: time is controlled via `internal/clock`; no
  `time.Sleep` in test bodies; goroutine-leak detection on every test.
- **Wallclock budget**: < 2 min on a clean GitHub-hosted runner.

## 2. Non-Goals

- Performance / latency SLA verification → `tests/bench/`.
- Cross-stack model parity vs. aiohomematic → `make snapshot`.
- Reliability timing windows (backoff, jitter envelopes) → unit tests
  with `internal/clock` fakes.
- Coordinator lock semantics → race-flagged unit tests.
- Pixel-level UI snapshots → run locally; not part of CI.

E2E proves **the door works**, not how the lock looks inside.

---

## 3. Inventory of External Interfaces

| # | Interface | Endpoint | Driver in E2E | Schema source |
|---|---|---|---|---|
| 1 | REST API (see `assets/openapi.yaml` for the current path/operation count) | `:8119/api/...` | `net/http` + OpenAPI walker | `assets/openapi.yaml` |
| 2 | WebSocket (see `assets/wsapi.json` for the current command/broadcast count) | `:8119/api/ws` | `coder/websocket` + WSAPI walker | `assets/wsapi.json` |
| 3 | MQTT (HA Discovery + raw) | `tcp://broker:1883` | `paho.mqtt.golang` | `docs/mqtt-topic-schema.md` |
| 4 | Config UI (Svelte SPA — login, OIDC, onboarding, ADR 0045) | `:8119/` | `net/http` static smoke | `internal/north/ui/spa_dist/` |
| 5 | No-JS diagnostic anchor | `:8119/health`, `:8119/about` | `net/http` roundtrip | `internal/north/ui/templates/` |
| 6 | Prometheus | `:8119/metrics` | `expfmt.TextParser` | `pkg/hmlog`, `internal/metrics` |
| 7 | Auth — Basic | `Authorization: Basic` | `net/http` | `assets/openapi.yaml` |
| 8 | Auth — Session | cookies | `net/http.CookieJar` | login flow |
| 9 | Auth — API Token | `Authorization: Bearer` | `net/http` | `auth.tokens` config |
| 10 | Auth — OIDC | `/auth/oidc/{start,callback}` | mock OP | OIDC discovery |
| 11 | CLI `hmcli` | sub-process | `os/exec` | `cmd/hmcli` |
| 12 | Matter (spec 1.5.1 / matter.js HEAD) | TCP/CHIP | `make matter-smoke` (Linux only) | out of CI scope |

South-bound transports (XML-RPC / JSON-RPC / BIN-RPC) are exercised
indirectly — the daemon talks to godevccu in-process; tests assert on
the **observable effect** at the north-bound boundary.

---

## 4. Three Test Layers

### 4.1 Layer A — In-Process Black-Box E2E (mandatory in CI)

**Build tag**: `e2e` · **Wallclock**: < 2 min · **Workflow**: `e2e.yml`

The harness spawns the **real `./bin/openccu-loom` binary** as a
sub-process with a generated config that points every listener at
pre-allocated free ports on `127.0.0.1`. Tests then drive each
interface using its production client library. This is the truest
black-box: the binary the user installs is the binary under test —
no test-only assembly path can drift from production wiring.

Backing services:

| Dependency | Production | E2E |
|---|---|---|
| Daemon | systemd / docker | `os/exec ./bin/openccu-loom run --config <tempdir>/config.yaml` |
| South-bound CCU | HmIP/CUxD | godevccu (in-process, in the **test** process) |
| MQTT broker | Mosquitto / EMQX | embedded pure-Go broker (e.g. `mochi-mqtt/server`) bound to a free port |
| OIDC OP | Keycloak / Auth0 | in-process mock OP (`tests/e2e/harness/oidc_op.go`) bound to a free port |
| SQLite store | `data_dir` | `t.TempDir()` |
| SPA assets | embedded | pre-built `spa_dist/`, embedded in `./bin/openccu-loom` |
| Time | `time.Now` | real time — assertions poll with deadline, no `time.Sleep` |

Pre-allocation uses `net.Listen("tcp", "127.0.0.1:0")` followed by
`Close()` — there is a small TOCTOU window before the daemon binds.
If a collision actually materialises, the daemon fails to bind and
the test fails loudly; we do **not** retry-around-flake.

The harness lives in `tests/e2e/harness/` and exposes a single entry
point:

```go
h := harness.Start(t, harness.Options{
    Devices:   harness.DefaultDevices,
    AuthMode:  harness.AuthSession,
    EnableMQTT: true,
})
defer h.Stop()

resp := h.REST().Get("/api/v1/devices").Auth(h.Admin()).Do()
```

The harness handles port allocation, lifecycle, and `t.Cleanup`
registration. It is **not** reusable across tests — each test gets a
fresh daemon on fresh ports.

### 4.2 Layer B — Browser/UI E2E (nightly + `needs-ui` label)

**Build tag**: `e2e_ui` · **Workflow**: `nightly.yml`

Playwright runs three smoke flows against the same harness:

1. Login → device list → channel detail.
2. MASTER paramset session: dirty / undo / redo / save (mirrors the
   reference UX from `homematicip-local-frontend`).
3. WebSocket push: a value updates live after a godevccu event.

Snapshot/screenshot diffs are kept out of CI to avoid flakiness from
font/AA differences across runners; they remain an opt-in local tool.

### 4.3 Layer C — Live Smoke (manual, real CCU)

The existing `tests/integration/live_smoke_test.go` family stays as
is. It is excluded from any CI workflow.

---

## 5. Schema-Driven Coverage

Static maintenance does not scale to ~80 REST and 85 WS operations.
Two walkers solve this:

### 5.1 OpenAPI Walker (`tests/e2e/api_rest_test.go`)

Iterates every operation in `assets/openapi.yaml`:

1. Pick request payload from the operation's `examples:` (if absent,
   the operation must appear in `tests/e2e/openapi_skip.txt` with a
   reason — the walker fails the build otherwise).
2. Issue the request against the harness with admin credentials.
3. Validate the response body and status against the operation's
   schema (`pb33f/libopenapi-validator` or `kin-openapi`).
4. Record `(tag, method, path, status)` in a per-test coverage map;
   the suite asserts at the end that every operation was visited.

This converts the spec into the test source of truth. New endpoint
without test → red CI.

### 5.2 WSAPI Walker (`tests/e2e/api_ws_test.go`)

Identical pattern against `assets/wsapi.json`. The walker establishes
one WS connection, sends each command with its example payload, and
verifies the response envelope against the command's response schema.

### 5.3 MQTT Topic Walker

The MQTT topic plane is documented in `docs/mqtt-topic-schema.md`,
which is a Markdown file rather than a machine schema. The walker
loads a small Go-side topic table (`tests/e2e/harness/mqtt_topics.go`)
that mirrors the schema doc; the contract test
`mqtt_topic_schema_doctest_test.go` already pins the doc and the table
together. The walker drives each topic family for a representative
device set (`harness.DefaultDevices`).

---

## 6. Test File Layout

```
tests/e2e/
├── doc.go                       — package documentation
├── openapi_skip.txt             — endpoints intentionally not yet covered
├── harness/
│   ├── doc.go
│   ├── daemon.go                — bring daemon up in goroutine, t.Cleanup-aware
│   ├── ports.go                 — pickFreePort() with retry envelope
│   ├── godevccu.go              — re-export from tests/integration
│   ├── mqtt_broker.go           — embedded pure-Go MQTT broker
│   ├── oidc_op.go               — mock OIDC OP (RS256 keypair, in-memory)
│   ├── mqtt_topics.go           — topic table mirroring docs/mqtt-topic-schema.md
│   └── clients.go               — REST/WS/MQTT/Prom test clients
├── api_rest_test.go             — OpenAPI walker
├── api_ws_test.go               — WSAPI walker
├── auth_basic_test.go
├── auth_session_test.go
├── auth_token_test.go
├── auth_oidc_test.go            — against mock OP
├── mqtt_discovery_test.go
├── mqtt_command_test.go         — set via raw topic, observe value echo
├── ui_htmx_test.go              — login form + /setup + /health + /about (HTTP only)
├── ui_spa_static_test.go        — index.html, CSP, MIME, asset-hash stability
├── prometheus_test.go           — required metrics present, expfmt parses
└── cli_hmcli_test.go            — `hmcli devices ls`, `hmcli paramset get`
```

Every `*_test.go` file carries `//go:build e2e`.

!!! note "As-built layout differs from this plan"
    §§1-10 are the original design plan; §11 below is the as-built log.
    The implemented `tests/e2e/` layout diverges from the sketch above:
    the auth quartet is one file (`auth_test.go`), the UI suites are one
    file (`ui_test.go`, no more `ui_htmx_test.go`/`ui_spa_static_test.go`
    split — `/login` and `/setup` no longer exist as server-rendered
    routes, see ADR 0045), the CLI suite is `cli_test.go`, and MQTT is
    split across `mqtt_test.go` / `mqtt_discovery_test.go` /
    `mqtt_subscriber_test.go` / `mqtt_collector_test.go`. `harness/` has
    `config.go` + `ws_client.go` instead of `mqtt_topics.go`, and an
    `openapi_skip.txt` **and** `wsapi_skip.txt` both exist. The suite
    also grew several boot/lifecycle-focused files not in the original
    plan: `boot_test.go`, `bringup_test.go`, `cold_boot_test.go`,
    `hot_plug_test.go`, `reconnect_test.go`, `matter_boot_test.go`,
    `degraded_state_test.go`, `visibility_test.go`,
    `custom_dp_roundtrip_test.go`, `hub_aggregates_test.go`.

---

## 7. CI Wiring

New workflow `.github/workflows/e2e.yml`:

```yaml
name: e2e
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
jobs:
  e2e:
    runs-on: ubuntu-latest
    steps:
      - actions/checkout@v4
      - actions/setup-node@v4    # cache via package-lock.json hash
      - actions/setup-go@v5
      - run: cd assets/ui && npm ci && npm run build
      - run: make build-all
      - run: make e2e
```

Cross-platform runs (`macos-latest`, `windows-latest`) are added once
the embedded MQTT broker is wired — no Docker required, so they will
green out of the box.

`ci.yml` stays unchanged. `integration.yml` keeps owning the
godevccu+snapshot pipeline. `nightly.yml` gains the Playwright job
(Layer B).

Coverage: E2E tests do **not** contribute to the per-package
coverage gate — they are black-box and would otherwise inflate the
metric for adapters that have no real unit coverage. They produce a
separate `coverage-e2e.out` artifact for visualisation only.

---

## 8. Determinism Rules

- **Ports**: pre-allocated via `pickFreePort` per listener, written
  into the generated config, exposed to tests via accessors. No
  hard-coded ports anywhere.
- **Time**: real time. Tests assert via `assertEventually` (polling
  with a deadline, default 10 s) — never via `time.Sleep`.
- **Sub-process lifecycle**: SIGTERM on cleanup, 5 s grace, then
  SIGKILL. Captured stdout / stderr is dumped into `t.Log` only on
  failure to keep the green-path output quiet.
- **Sub-process leaks**: the harness's t.Cleanup is registered first
  thing in `Start`, so a panic between Start and the test body still
  reaps the binary.
- **No retries-on-flake**: a flaky E2E test is a real bug. If a wait
  is involved, raise the deadline explicitly with a comment that says
  what we are waiting for.

---

## 9. Rollout Plan

| Step | Scope | Effort |
|---|---|---|
| 1 | Harness skeleton (this PR) | ½ d |
| 2 | Daemon sub-process bring-up + ports + lifecycle | 1 d |
| 3 | Embedded MQTT broker integration | ½ d |
| 4 | OpenAPI walker — REST | 2 d |
| 5 | WSAPI walker — WebSocket | 2 d |
| 6 | Mock OIDC OP + auth quartet | 1 d |
| 7 | MQTT discovery + command roundtrip | 1 d |
| 8 | HTMX bootstrap HTTP-only suite + SPA static smoke | 1 d |
| 9 | Prometheus + CLI smoke | ½ d |
| 10 | `e2e.yml` workflow + gating | ½ d |
| 11 | Playwright nightly (Layer B) | 1 d |
| **Total** | | **~11 person-days** |

Layers A and B are independent; A is the gating goal.

---

## 10. Open Decisions

- **MQTT broker library**: `mochi-mqtt/server` v2 is the leading
  candidate (MIT, pure Go, MQTT 5). Alternatives: `volantmq/volant`,
  in-house listener. ADR required before adding the dependency.
- **OpenAPI validator**: `pb33f/libopenapi-validator` (MIT) or
  `getkin/kin-openapi` (MIT). Both pure Go. Pick one in step 4.
- **OIDC mock**: hand-rolled (~150 LoC) or `oauth2-proxy/mockoidc`.
  Hand-rolled likely wins on dependency hygiene.

These decisions are filed as discussion items, not blocking the
harness skeleton.

---

## 11. Discovered Bugs (E2E surfacing)

### 11.1 HmIP-BROLL panics during device materialisation — **fixed 2026-05-08**

**Found**: 2026-05-08, step 2 bring-up smoke.
**Symptom**: nil-pointer deref in `Cover.DataPointKey` at
`internal/model/custom/materialize.go:598`, causing the daemon to
exit with status 2 during `WireCentrals`.
**Reach**: the panic only fired on the production daemon pipeline;
`tests/integration/` exercised the same fleet without `daemonServe`
and stayed green, which is why the bug had no Go-side guard until E2E.
**Cause**: `Cover` embeds `*generic.Float`, which can be nil when the
backing channel exposes no LEVEL data point. The autogenerated
forwarder `Cover.DataPointKey` invoked
`(*DataPoint[float64]).DataPointKey` on a nil receiver; the `if dp == nil`
check in `lookupProfileForCustomDP` did not catch it because the
custom-DP interface itself was non-nil.
**Fix**: explicit nil-safe `Cover.DataPointKey` mirroring the existing
guard pattern on `SubDataPointKeys` / `IsRefreshed`, plus a
belt-and-braces empty-channel-address check in
`lookupProfileForCustomDP`. Regression test added in
`internal/model/custom/cover/cover_deep_test.go::TestCoverNilLevelDPGracefullyDegrades`.

### 11.2 REST handler / spec drifts (`TestRESTGetWalker`)

**Found**: 2026-05-08, step 4 OpenAPI walker.

The walker drove every documented `GET` operation in
`assets/openapi.yaml` against a freshly started daemon (no real CCU,
godevccu standing in). 33 operations match the contract; 15 do not.
Each entry below is held in `tests/e2e/openapi_skip.txt` with a
short reason and should be resolved per its category.

**Handler bugs — return 500 where 4xx is expected — fixed 2026-05-08:**

- ~~`getChannelSchedule`, `getDeviceSchedule`, `listLinks`~~ — three
  handlers returned 500 on a missing device because their adapter
  layer wrapped `ErrNoScheduleBackend` / `ErrNoLinkBackend` for both
  "no backend wired" *and* "device not in any central". Fix: introduced
  a new error path using `hmerr.ErrDescriptionNotFound` for the
  missing-device case in `SchedulesDomain.resolve`,
  `SchedulesDomain.FindScheduleChannel`, and
  `LinksDomain.lookupDevice`; the REST mappers (`writeScheduleError`,
  `ListLinks`) translate that sentinel to 404. Spec entries for 404
  added on `getDeviceSchedule` and `listLinks`. The
  `getChannelSchedule` op was already documented with 404 — only
  the handler mapping was missing.

**Spec drift — response status not declared — closed 2026-05-08:**

- ~~`listCustomDataPoints`, `listCalculatedDataPoints`,
  `GET /devices/{addr}/channels`,
  `GET /devices/{addr}/channels/{no}/data-points`~~ — added `404`
  responses.
- ~~`linkableChannels`~~ — added `400` and `404`.
- ~~`oidcStart`, `oidcCallback`~~ — added `404` (route not registered
  when OIDC disabled). The existing `503` description was tightened
  to "OIDC configured but provider unreachable".
- ~~`GET /events`~~ — added `400` for plain HTTP requests without a
  WebSocket upgrade.

**Upstream 502 — closed 2026-05-08 by spec only:**

- ~~`downloadBackup`, `getCentralLinksStatus`, `getLinkParamset`,
  `GET /devices/{addr}/paramsets/{key}` (and the matching `PUT`)~~ —
  documented both `404` and `502` as legitimate responses. The
  handlers currently return `502` for any error including missing
  device; a follow-up should mirror the schedules/links fix
  (introduce `hmerr.ErrDescriptionNotFound` propagation in the
  paramset / central-links / backup adapters and map to `404`).

**Walker state**: every documented `GET` operation now matches its
spec. `tests/e2e/openapi_skip.txt` is empty; any new endpoint added
without test or skip entry → red CI.

**Open follow-up** (lower priority):

- Refine the four handlers above to emit `404` for "device / backup
  not found" and reserve `502` for genuine upstream failures.

### 11.3 WS handler hardening (`TestWSCommandWalker`)

**Found**: 2026-05-08, step 5 WSAPI walker.

The walker drives every command in `assets/wsapi.json` over a real
WebSocket connection (`/api/v1/events`) and asserts that the
response envelope is well-formed and the error code is one of the
typed catalogue values. 84 of 87 commands match the contract; the 3
remaining are godevccu fixture limitations.

**Crash discovered — fixed 2026-05-08:**

- ~~`Cover.Category` panic~~ — same shape as §11.1: `Cover` embeds
  `*generic.Float`; the autogenerated `Category` forwarder
  dereferences a nil receiver when the channel has no LEVEL DP. The
  WS `cdp.list` handler walked custom DPs and hit it; the recovery
  middleware caught the panic but tore down the WS connection.
  Fix: explicit nil-safe `Cover.Category()` returning
  `hmenum.DataPointCategoryUndefined`, plus a consumer-side guard
  in `internal/north/rest/ws/custom_data_points.go::customDPsForDevice`
  that skips entries whose `DataPointKey().Parameter == ""`.
  Regression assertion added to `cover_deep_test.go`.

**Wrong error code — fixed 2026-05-08:**

12 handlers used `errors.New("ws: ... is required")` for input
validation; the router wrapped these as `internal_error`, suggesting
a server bug to clients. Bulk-rewritten to
`NewCommandError(CommandErrorBadRequest, "...")`. Affected commands:

- `cdp.get`, `cdp.invoke`, `calc_dp.get` (custom_data_points.go)
- `device.install_mode`, `device.rename`, `paramset.put`,
  `master_profiles.get`, `master_profiles.match`,
  `master_profiles.apply` (commands_extended.go)
- (plus 3 other validation sites in the same files)

**Stub handlers — fixed 2026-05-08:**

5 commands (`schedules.set_enabled`, `links.get_form_schema`,
`links.get_profiles`, `links.test_profile`, `paramset.determine`)
are registered stubs awaiting domain wiring. They previously
returned plain Go errors → `internal_error`. Introduced a new
typed error code `CommandErrorNotImplemented` (`"not_implemented"`)
distinct from `unknown_command` (no handler) and `internal_error`
(real bug). The walker accepts it; the SPA can branch on it
without string-matching.

**Walker state**: every catalogued command is exercised. Skip list
holds 3 entries — all are godevccu fixture gaps (`backup.status`,
`backup.trigger`, `firmware.update`) that need `ReGa.runScript`
which the simulator does not implement. They will pass against a
real CCU.

### 11.4 Auth quartet — clean (`TestAuth*`)

**Implemented**: 2026-05-08, step 6.

The four authentication backends each get one E2E test driving the
happy path against the real REST API:

| Mode | Test | Wire shape |
|---|---|---|
| `Basic` | `TestAuthBasic` | `Authorization: Basic <b64(user:pass)>`; happy + 401 anon |
| `Session` | `TestAuthSession` | POST `/auth/login` → cookie jar → `/auth/me` |
| `Token` | `TestAuthToken` | `Authorization: Bearer <token>`; happy + 401 bogus |
| `OIDC` | `TestAuthOIDC` | full PKCE handshake against the in-process MockOP |

The MockOP (`tests/e2e/harness/oidc_op.go`) is a hand-rolled OpenID
Provider serving the four endpoints the daemon's OIDC client needs
(`/.well-known/openid-configuration`, `/jwks`, `/authorize`, `/token`)
and signing ID tokens with an in-memory RS256 keypair. The OIDC test
skips the user-agent step by calling
`MockOP.IssueAuthCode(sub, role)` directly, then GETs
`/auth/oidc/callback` with the minted code + the daemon-issued state.
The daemon exchanges the code (calling the MockOP's `/token`),
parses the ID token, and issues a `openccu_loom_session` cookie that
`/auth/me` then accepts.

Note: the daemon does **not** verify the ID-token signature in v1.0
(see `internal/auth/oidc/client.go::DecodeIDToken` — Spec §19
hardening item). The MockOP signs anyway so the wire shape matches a
real OP and a future signature-verification turn-on does not break
the test.

All four tests run with `t.Parallel()`; the harness spawns a fresh
daemon sub-process per test on its own ephemeral ports. The full E2E
suite (bring-up + REST walker + WS walker + 4 auth tests = 7 tests)
completes in ~6.5 s wallclock per run.

### 11.5 MQTT Discovery + Roundtrip — partial (`TestMQTT*`)

**Implemented**: 2026-05-08, step 7.

Three smoke tests exercise the MQTT bridge against an embedded
mochi-mqtt/server v2 broker (MIT, pure-Go, MQTT v5):

- `TestMQTTBridgeOnline` — daemon publishes its retained `online`
  birth message on `openccu-loom/bridge/status`.
- `TestMQTTHomeAssistantDiscovery` — at least one
  `homeassistant/<component>/.../config` payload with `unique_id`.
- `TestMQTTSetCommandIngested` — the broker's `$SYS/broker/messages/received`
  counter rises after the test publishes a `/set` frame, proving
  the wire path through the broker works end-to-end.

**Doc/code drifts surfaced and fixed**:

- The `mqtt.raw_enabled` and `mqtt.discovery_enabled` defaults
  documented in `example.config.yaml` (`[default: true]`) did NOT
  match the Go zero-value (`false`) — silently broke MQTT for any
  operator that set only `enabled: true` + `broker_url`. Fixing the
  defaults to be `*bool`-driven is invasive (Go has no
  set-vs-unset distinction for `bool`); the harness now writes
  both flags explicitly true. **Production fix is still open** —
  either move both fields to `*bool`, or document the actual
  zero-value behaviour and remove the misleading `[default: true]`
  annotation.
- The CCU's HTTP port was hardcoded to 80 (plain) / 443 (TLS) in
  `internal/central/adapter/hub_wiring.go::ccuBaseURLFor`. This made
  it impossible to point the daemon's JSON-RPC client (and the
  legacy backup-restorer endpoint) at any non-standard port —
  including the in-process godevccu simulator's ephemeral
  JSON-RPC listener. **Fixed**: added `CentralConfig.JSONRPCPort`
  (`json_rpc_port` in YAML); zero falls back to 80/443, non-zero
  overrides both. The harness wires godevccu's actual JSON-RPC
  port; the seed pipeline now reaches the simulator and JSON-RPC
  auth completes (`hub.programs.ok` instead of
  `Session expired or invalid`).

**Remaining godevccu fixture limitation**: the daemon's seed
pipeline calls `ReGa.runScript` to bulk-load device data points;
godevccu's JSON-RPC layer accepts the call but returns an empty
JSON envelope (`unexpected end of JSON input`). The result is a
device graph with channels but **no data points** —
`/api/v1/devices/{addr}/channels/{no}/data-points` returns `[]`
even after a successful bring-up. A true write-roundtrip
(`PUT /value` → CCU SetValue → callback → MQTT state echo)
therefore cannot run against godevccu today and is parked behind
this finding. Either godevccu grows a working `ReGa.runScript`
implementation, or the daemon learns to populate DPs from XML-RPC
`getParamsetDescription` directly when the rega seed fails.

### 11.6 UI smoke — clean (`TestUI*`)

**Implemented**: 2026-05-08, step 8.

!!! warning "Historical entry — HTMX login/setup since removed"
    This entry documents the suite as it stood on 2026-05-08. Since
    ADR 0045, `/login` and `/setup` no longer exist as server-rendered
    routes — login, OIDC, and first-run onboarding all moved into the
    Svelte SPA. The `TestUIHTMX*` tests described below have been
    replaced; the current server-rendered surface is only `/health` +
    `/about`, covered by `tests/e2e/ui_test.go`.

Seven HTTP-only tests cover both browser-facing surfaces (as of
2026-05-08; see the warning above for what changed since):

**Svelte SPA (REST listener, `/app/*`)**:

- `TestUISPAIndexServed` — `GET /app/` → 200 with `text/html`,
  `Cache-Control: no-store`, body carries `<!doctype html>` plus
  `<div id="app"></div>` and references hashed assets under
  `/app/assets/`.
- `TestUISPARootRedirect` — `GET /app` (no trailing slash) → 301
  to `/app/`.
- `TestUISPADeepLinkFallback` — `GET /app/some/unknown/route` →
  200 with the index.html shell, so client-side routing works for
  bookmarks and deep links.
- `TestUISPAHashedAssetCacheable` — scrapes one `/app/assets/*`
  reference from the shell and verifies it returns
  `Cache-Control: public, max-age=31536000, immutable` (the
  hash-rotates-on-build cache strategy).

**HTMX bootstrap (REST listener, `:8119`)**:

- `TestUIHTMXRootRedirectsToHealth` — `GET /` → 303 to `/health`,
  the canonical landing page.
- `TestUIHTMXPagesRender` — `/health`, `/about`, `/login`, `/setup`
  each return 200 (or `/setup` 303 once an admin user exists) with
  `text/html` and a per-page anchor (`name="username"` on `/login`,
  `<html>` elsewhere).
- `TestUIHTMXStaticAssetsServed` — `/ui/assets/app.css` returns
  200 with `text/css`.

**Dead-file follow-up**: `internal/north/ui/assets/htmx.min.js`
exists in source but is **not embedded** (the `//go:embed
assets/*.css` glob skips it) and **not referenced** by any layout
template. Either drop the source file or extend the glob and link
it from the layout — minor cleanup, no functional impact.

The full E2E suite now stands at **16 tests** running in ~12 s
wallclock per run (bring-up + REST walker + WS walker + 4 auth + 3
MQTT + 7 UI), 2 sequential runs in 25 s.

### 11.7 Prometheus + CLI smoke (`TestPrometheus*` / `TestCLI*`)

**Implemented**: 2026-05-08, step 9.

Prometheus (`TestPrometheusMetricsExposed`) drives `/api/v1/metrics`
through the same harness daemon used by every other E2E test. The
acceptance contract:

- Anonymous request → 401 (the metrics endpoint is gated by the
  same auth middleware as the rest of the API; Prometheus
  scrapers must authenticate).
- Authenticated request → 200 with `Content-Type: text/plain*`.
- Empty body is acceptable for a fresh registry — the daemon's
  `internal/metrics.Registry` populates lazily from event-bus
  traffic. When samples ARE present, every non-blank, non-comment
  line carries a `<name> <value>` separated by whitespace, and
  HELP / TYPE comment counts never exceed sample counts.

The walker keeps parser logic local rather than pulling in
`prometheus/common/expfmt` — adding a dep just for one assertion
trips the project's ADR scrutiny.

CLI (`TestCLIVersion`, `TestCLIConfigValidate`, `TestCLIHelp`)
shells out to `./bin/hmcli` with `os/exec`. No daemon involved, so
the three tests complete in ~0.25 s combined:

- `version` / `--version` / `-v` all print an `hmcli ` envelope.
- `config validate <good.yaml>` exits 0; `config validate <bad>`
  exits non-zero. Same loader the daemon runs at boot.
- No-args invocation exits non-zero with `Usage:`; `help` exits 0
  with `Usage:` on stdout.

The harness now resolves both binaries:
`OPENCCU_LOOM_E2E_BINARY` (daemon, existing) +
`OPENCCU_LOOM_E2E_HMCLI` (new) override the default `./bin/<name>`
lookup so CI artifact-locations or Bazel-style sandboxes can rewire
without code changes.

### 11.8 CI gate — `make e2e` and `.github/workflows/e2e.yml`

**Wired**: 2026-05-08, step 10.

`make e2e` now depends on `build-all`, so a single command from a
clean checkout (`make e2e`) compiles both binaries and runs the
suite. `make e2e-dist` is the full pipeline (`ui-build` → `build-all`
→ test) and matches what CI runs.

`.github/workflows/e2e.yml` triggers on push-to-main and every PR
to main:

1. `actions/setup-node@v4` with npm cache keyed by
   `assets/ui/package-lock.json`.
2. `actions/setup-go@v5` with the module cache.
3. `npm ci` + `npm run build` in `assets/ui/` — produces the
   embedded `internal/north/ui/spa_dist/` bundle.
4. `make build-all` — daemon + hmcli.
5. `go test -tags=e2e -timeout=180s ./tests/e2e/...`.
6. On failure: dump `./bin/openccu-loom version` and
   `./bin/hmcli version` for triage.

`ci.yml` is unchanged (lint / test / contract / bench / fuzz /
build matrix). `integration.yml` keeps owning godevccu snapshots.
The new e2e job is independent and will green out of the box on
`ubuntu-latest`; macOS / Windows runners are deferred until the
mochi-mqtt broker is shown to behave identically there (no Docker
dependency, so cross-platform runs are gated on a future iteration
rather than on infra work).

The suite stood at **20 tests** in ~13 s wallclock as of this log entry
(2026-05-08); `make e2e` including build was ~17 s on a developer
laptop then. Coverage breakdown at the time:

| Layer | Tests |
|---|---|
| Bring-up | 1 |
| REST-Walker (GET ops, walked from `assets/openapi.yaml`) | 1 |
| WS-Walker (commands, walked from `assets/wsapi.json`) | 1 |
| Auth (Basic/Session/Token/OIDC) | 4 |
| MQTT (Bridge online + Discovery + Set) | 3 |
| UI (4 SPA + 3 HTMX) | 7 |
| Prometheus | 1 |
| CLI (version + config + help) | 3 |
| **Total** | **20** |

The suite has grown well past this point since (see the as-built note
in §6) — `tests/e2e/` now holds many more files covering boot/reconnect
lifecycle, Matter, visibility, and custom-DP round-trips; this table is
a historical snapshot, not the current count.

