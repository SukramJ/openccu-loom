<p align="center">
  <img src="assets/logo/wordmark.svg" alt="OpenCCU-Loom" height="64">
</p>

# OpenCCU-Loom

A standalone Go daemon that talks to Homematic and HomematicIP CCUs
(CCU2, CCU3, RaspberryMatic) over XML-RPC, BIN-RPC, and JSON-RPC, and
bridges them to MQTT (Home Assistant Discovery + raw topic plane),
a REST + WebSocket API, and a web-based configuration UI.

`OpenCCU-Loom` is a Go port of the Python library
[`aiohomematic`](https://github.com/SukramJ/aiohomematic) that adds a
standalone-daemon surface (MQTT, REST + WebSocket, Config UI, Matter)
on top of CCU connectivity. The two projects coexist: `aiohomematic`
remains the library powering the Home Assistant integration
*Homematic(IP) Local*; `OpenCCU-Loom` is the self-contained daemon for
users who want MQTT / REST / Web-UI / Matter access without running
Home Assistant.

## Why "OpenCCU-Loom"?

Two parts, both deliberate:

- **OpenCCU** is the upstream this project extends:
  [github.com/OpenCCU/OpenCCU](https://github.com/OpenCCU/OpenCCU)
  ([openccu.de](https://openccu.de)) — a Buildroot-based, cloud-free
  smart-home OS for the HomematicIP CCU (CCU3 / ELV-Charly) that
  runs on Raspberry Pi, x86/ARM, Proxmox, Docker, LXC, Kubernetes.
  OpenCCU-Loom is an extension that adds the daemon surface OpenCCU
  itself doesn't ship: MQTT (HA Discovery + raw plane), a REST +
  WebSocket API, a Svelte-based Config UI, and a native-Go Matter
  bridge. The same OpenCCU tree is also the **source** for the
  [openccu-data](https://github.com/SukramJ/openccu-data) catalogue
  (extracted translations, easymodes, device profiles), which
  OpenCCU-Loom embeds at build time — so OpenCCU is both the
  runtime host the daemon ships alongside *and* the upstream the
  daemon's metadata is derived from.
- **Loom** describes what the daemon actually does. A *wiring loom*
  is the bundled, ordered cable harness that runs through any car
  or aircraft — many individual conductors gathered into one routed
  path. That is the daemon's role exactly: many CCU wire protocols
  (XML-RPC, BIN-RPC, JSON-RPC, push callbacks across HmIP-RF,
  BidCos-RF, BidCos-Wired, VirtualDevices, CUxD) come
  in on one side; multiple north-bound protocols (MQTT, REST,
  WebSocket, Matter) come out on the other. The mark — four
  parallel strands held by a bundling band — depicts that
  cross-section directly.

Together: a **loom** for **OpenCCU** that weaves the proprietary
wire side into the standard-protocol side.

## Status

**0.15.0** (in development; latest release tag **v0.14.5**). All four
north-bound bridges work end-to-end against a real CCU and the `godevccu`
simulator. The 0.14.x line added CCU-delegated authentication (ADR 0043),
single-port onboarding + opt-in HA Ingress auth passthrough (ADR 0044), and a
critical HA-add-on data-persistence fix (`OPENCCU_LOOM_DATA_DIR` honoured
without a config file); 0.15.0 reconciles the OpenAPI contract with the server
(REST `APIVersion` 2.1.0), wires the `firmware.refresh` command + on-reload
link-peer refresh, and adds a flag-gated Matter TimeSynchronization cluster.
The earlier 0.2.0 baseline built on 0.1.0
with deeper Home Assistant parity (MQTT discovery for the hub layer —
system variables, programs, per-interface install-mode and
virtual-remote buttons — usage-gated per-parameter discovery, and
per-device firmware-update entities), a versioned external-client
contract (`schema_digest` + `api_version` on `GET /api/v1/info`, see
[ADR 0028](./docs/adr/0028-contract-digest-and-version-guard.md)),
Home Assistant and CCU/RaspberryMatic add-on packaging, and a round
of multi-CCU connection-reliability fixes.

- MQTT (HA Discovery + raw plane), REST + WebSocket, Svelte SPA + HTMX
  bootstrap, and a native-Go Matter bridge with PASE/CASE, IM
  Read/Write/Invoke/Subscribe/TimedRequest, 12 generic measurement
  cluster servers, 12 bridge-core clusters, and Custom-DP projections
  for switch / light / cover / climate / lock / siren / valve.
- Production-grade Matter attestation requires vendor-supplied
  DAC/PAI/CD bundles via config; the bundled CSA Test PAA chain
  works for development and Apple-/Google-/chip-tool-driven testing.
- Reliability layer (circuit breaker, retry, throttle, request
  coalescer, ping/pong) is parity-tested against the upstream
  reference values.

Canonical references:

- [`SPECIFICATION.md`](./SPECIFICATION.md) — complete specification
- [`CHANGELOG.md`](./CHANGELOG.md) — release history
- [`docs/user-guide.md`](./docs/user-guide.md) — operator install + config walkthrough
- [`docs/SECURITY.md`](./docs/SECURITY.md) — threat model + audit checklist
- [`docs/caching.md`](./docs/caching.md) — every cache layer + boot-time radio cost
- [`docs/adr/`](./docs/adr/) — architecture decisions (Matter bridge in [ADR 0012](./docs/adr/0012-matter-pure-go-implementation.md), commissioning bring-up in [ADR 0013](./docs/adr/0013-matter-commissioning-bring-up.md))
- [`assets/openapi.yaml`](./assets/openapi.yaml) — REST API contract

## Quickstart

### Docker

```sh
docker run -d --restart unless-stopped \
  -p 8119:8119 -p 8120:8120 -p 8129:8129 \
  -v $(pwd)/config.yaml:/app/config.yaml:ro \
  -v openccu-loom-data:/app/var \
  ghcr.io/sukramj/openccu-loom:latest run --config /app/config.yaml
```

> `--restart unless-stopped` (already set in `docker-compose.yaml`) lets the
> Config UI's **Restart** action work: the daemon exits on restart and Docker
> brings the container back. Without a restart policy the container would stay
> stopped. (The Home Assistant add-on handles this in-container via s6-overlay.)

### Binary

```sh
make build
./bin/openccu-loom run --config config.yaml
```

### Home Assistant add-on

Add `https://github.com/SukramJ/openccu-loom` as a repository under
*Settings → Add-ons → Add-on Store → ⋮ → Repositories*, then install
**OpenCCU-Loom**. The Config UI appears as a sidebar panel (Ingress) and
on `:8119`; state persists in the add-on's `/data`. See
[`packaging/ha-addon/README.md`](packaging/ha-addon/README.md). For native
CCU3 / RaspberryMatic installs, see
[`packaging/ccu-addon/README.md`](packaging/ccu-addon/README.md).

First-run setup:

1. Start the daemon without a user pre-configured.
2. Open `http://localhost:8119/setup` and create the first admin.
3. Sign in at `/login` — OIDC is supported when configured.

## Configuration model

OpenCCU-Loom splits its configuration into three tiers so the
SPA can drive almost everything at runtime without forcing
operators to hand-edit YAML for every change.

| Tier | Lives in | What goes there | Edit via |
|------|----------|-----------------|----------|
| **Bootstrap** | `config.yaml` | `data_dir`, `north.rest.listen`, `logging.{level,format}`, `bootstrap.allow_first_run_setup`, `env_file` | Edit the YAML + restart |
| **Live** | SQLite (`<data_dir>/openccu-loom.db`) | Everything else — CCUs, MQTT, Matter, mDNS, CORS, OIDC, rate-limit, reliability tunables, users, API tokens | SPA Settings tab, or REST `PUT /api/v1/config/sections/{section}` |
| **Secrets** | environment (process or `.env` file) | CCU passwords, MQTT password, OIDC client secret | Operator-owned; daemon never writes them back |

The runtime daemon overlays the live tier on top of whatever the
YAML provides, so:

* an empty database starts from the YAML — that's the seed.
* edits in the SPA win on the next restart.
* deleting a section row in the database via
  `DELETE /api/v1/config/sections/{section}` reverts to the YAML
  fallback.

The shipped `example.config.yaml` is intentionally short — see
[`example.config.yaml`](./example.config.yaml). Everything in the
"live" tier can still be set declaratively in YAML if you prefer
GitOps + restart over live-edit; the SPA is just an additional
surface, not a replacement.

For the complete field schema (every available knob with its
classification basic / expert / secret), call
`GET /api/v1/config/schema` once the daemon is up; the SPA reads
the same endpoint to build its editors.

## Secrets

OpenCCU-Loom never persists CCU / MQTT / OIDC passwords to YAML
unless you put them there yourself. The recommended path is:

* type the password directly into the SPA's CCU dialog → stored
  encrypted-at-rest by the OS (SQLite file is mode 0600), redacted
  from backup tarballs unless `--include-secrets` is passed; or
* keep the password in your shell / systemd / Docker / Kubernetes
  secret store and reference it via `password_env: MY_VAR_NAME` in
  the SPA's CCU dialog (expert mode).

The supported env hooks are:

| Secret | Resolution |
|---|---|
| CCU password (per-CCU) | `centrals[].password_env` in the SPA / DB. Daemon reads `os.Getenv(<that-name>)` when it dials the CCU. |
| MQTT broker password | `OPENCCU_LOOM_MQTT_PASSWORD` |
| OIDC client secret | `OPENCCU_LOOM_OIDC_CLIENT_SECRET` |

### Setting them locally

```sh
export CCU_HOME_PASSWORD='your-ccu-password'
export OPENCCU_LOOM_MQTT_PASSWORD='your-mqtt-password'
./bin/openccu-loom run --config config.yaml
```

Then in the SPA's CCU dialog, set **Password env-var** to
`CCU_HOME_PASSWORD`. The daemon resolves it on every CCU dial; the
password never lands in `config.yaml`, the SQLite database, or a
backup tarball.

### systemd

`EnvironmentFile=` is the cleanest way to pin secrets to a
service unit without exposing them on the command line:

```ini
# /etc/systemd/system/openccu-loom.service
[Service]
EnvironmentFile=/etc/openccu-loom/secrets.env
ExecStart=/usr/local/bin/openccu-loom run --config /etc/openccu-loom/config.yaml
```

```sh
# /etc/openccu-loom/secrets.env  (chmod 0600, owner root)
CCU_HOME_PASSWORD=your-ccu-password
OPENCCU_LOOM_MQTT_PASSWORD=your-mqtt-password
OPENCCU_LOOM_OIDC_CLIENT_SECRET=your-oidc-secret
```

### Docker / Compose

```yaml
services:
  openccu-loom:
    image: ghcr.io/sukramj/openccu-loom:latest
    env_file:
      - secrets.env
    volumes:
      - ./config.yaml:/app/config.yaml:ro
      - openccu-loom-data:/app/var
    command: run --config /app/config.yaml
```

Mount the `secrets.env` file outside the image, never bake it in.
For Kubernetes use a `Secret` with `envFrom:` — same shape.

### Operator-facing escape hatch (test rigs only)

If you cannot use env variables (one-off dev installs), set
`security.allow_plaintext_secrets: true` via the Settings section
in the SPA. With the toggle on, the per-CCU dialog accepts a
plaintext fallback in addition to `password_env`. The plaintext
value is then stored in the SQLite `centrals` table; do not enable
this on a production host.

## Features

- **Full south-bound parity** with `aiohomematic` on the wire — every
  CCU interface, every device profile, every reliability invariant
  (circuit breaker, retry, throttle, request coalescer, ping/pong).
- **MQTT bridge** with Home Assistant Discovery **and** a raw topic
  plane; bidirectional control via `/set` topic subscriptions. Pure
  Go MQTT 3.1.1 client (no `paho` dependency).
- **REST + WebSocket API** — ~90 REST endpoints (full catalogue in
  [`assets/openapi.yaml`](./assets/openapi.yaml)) and 85 WebSocket
  commands, OpenAPI 3.1 contract, RFC 9457 problem+json,
  Idempotency-Key middleware.
- **Configuration UI** — Svelte 5 SPA (Tailwind 4 + Vite, embedded
  via `go:embed`) as the primary surface; an HTMX + `html/template`
  fallback covers login, setup wizard, and status dashboards without
  JavaScript. Locale-aware i18n (de + en).
- **Multi-CCU**: one daemon, many CCUs, all scoped cleanly.
- **Authentication**: HTTP Basic, Bearer tokens, session cookies with
  CSRF, OpenID Connect (PKCE + JWKS-verified RS256 signatures).
- **Static single binary** (`CGO_ENABLED=0`) for Linux
  amd64 / arm64 / armv7, plus macOS, Windows (best-effort). Docker
  image published to `ghcr.io/sukramj/openccu-loom`.

## Differences from `aiohomematic`

The two projects are designed for different consumers and therefore
diverge on the north-bound side. Wire-level behaviour and the device
profile catalogue are kept in lockstep.

| Area | aiohomematic | OpenCCU-Loom |
|---|---|---|
| Language | Python 3.14 (asyncio) | Go 1.26+ |
| License | MIT | MIT |
| Primary consumer | Home Assistant integration | Standalone daemon (MQTT / REST / UI / Matter) |
| CUxD transport | JSON-RPC via CCU facade + MQTT workaround | **Native BIN-RPC** + BIN-RPC callback server |
| Multi-CCU | one `CentralUnit` per process | **many** `CentralUnit`s per process |
| Config | programmatic (Pydantic) | YAML (with defaults) |
| Persistence | JSON files | SQLite WAL + filesystem under `data_dir/` |
| UI | none (HA provides one) | built-in Svelte 5 SPA (HTMX fallback for no-JS) |
| Decorators (`@state_property`, `@inspector`) | Python runtime | Go struct tags; `@inspector` wrapping handled inline at call sites |
| Device profiles | hand-written Python | generated from the `aiohomematic` registry, plus hand-written Go wrappers |

## Quality Assurance — Strukturelle Säulen

OpenCCU-Loom hat vier strukturelle Säulen, die Architektur-Drift
verhindern:

| Säule | Tool | Was wird detektiert |
|---|---|---|
| 1. Reachability | `make reachability` | Dead-Code (exported ohne Production-Caller) |
| 2. Pin-Tests | `tests/contract/wiring_pins/` | Wiring-Regressionen |
| 3. Wire-Snapshots | `make wire-snapshots` | Custom-DP-Wire-Encoding-Drifts |
| 4. E2E-Smoke | `make e2e` | Feature-Drifts zur Laufzeit |

Details: [`docs/parity/structural-approach.md`](docs/parity/structural-approach.md)

## Building

```sh
make build        # produces ./bin/openccu-loom
make test         # unit + contract tests
make integration  # godevccu + Mosquitto integration tests (Mosquitto needs Docker)
make bench        # benchmarks
make lint         # golangci-lint (null findings required)
make docker       # multi-arch Docker image via buildx
```

Prerequisites: Go 1.26+, `golangci-lint`, `gofumpt`, `goreleaser`,
Docker (+ buildx) for the Mosquitto-backed integration tests.
Integration runs use [`godevccu`](https://github.com/SukramJ/godevccu),
a pure-Go HomeMatic CCU simulator embedded as a regular module
dependency — no Python toolchain required.

## License

The OpenCCU-Loom **source code** is licensed under the
[MIT License](./LICENSE) — aligned with the rest of the aiohomematic
ecosystem (aiohomematic, aiohomematic-config, openccu-data).

The **binary distribution** additionally ships CCU metadata archives
sourced from [openccu-data](https://github.com/SukramJ/openccu-data).
Those files (`internal/ccudata/embedded/*.json.gz`,
`internal/ccudata/embedded/profiles/*.json.gz`) are governed by the
**eQ-3 HomeMatic Software License** — free for private and
non-commercial use; commercial redistribution requires written
permission from eQ-3 AG. See
[`internal/ccudata/embedded/NOTICE`](./internal/ccudata/embedded/NOTICE)
for the full terms and
[`docs/adr/0003-embed-occu-extracts.md`](./docs/adr/0003-embed-occu-extracts.md)
for the rationale.

Operators with commercial use-cases can swap the embedded archives
out via `cfg.CCUData.{translations_path,easymode_path}` for
self-licensed equivalents — the daemon degrades gracefully.

Third-party prior art (the reference projects this port is built on and the
Go module dependencies, with their licenses and verbatim copyright notices)
is recorded in [`THIRD-PARTY-NOTICES.md`](./THIRD-PARTY-NOTICES.md). Full
license texts live under [`licenses/`](./licenses/).

## Documentation

- [`SPECIFICATION.md`](./SPECIFICATION.md) — design intent, hard
  constraints, resolved decisions.
- [`CLAUDE.md`](./CLAUDE.md) — entry point for AI assistants and
  fresh contributors.
- [`docs/adr/`](./docs/adr/) — architecture decisions.
- [`docs/external-clients/`](./docs/external-clients/) — wire
  contract for Python / TypeScript / Rust clients. Start with the
  [topic hierarchy](./docs/external-clients/topic-hierarchy.md) for
  the WebSocket surface and the
  [asks backlog](./docs/external-clients/asks.md) for the
  closure-index. The contract architecture itself is locked in
  [ADR 0020](./docs/adr/0020-external-client-wire-contract.md),
  [ADR 0021](./docs/adr/0021-mdns-self-advertisement.md) (mDNS
  auto-discovery), and
  [ADR 0022](./docs/adr/0022-ws-resume-and-kind.md) (WS resume +
  envelope kind).

## Contributing

[`CONTRIBUTING.md`](./CONTRIBUTING.md) covers local setup, PR
expectations, and the release workflow. Before opening a PR, please
open an issue so we agree on scope, especially when the change touches
the wire layer or the device profile catalogue.

## Acknowledgements

OpenCCU-Loom stands on the prior art of several other projects. The full
list with licenses and verbatim copyright notices is in
[`THIRD-PARTY-NOTICES.md`](./THIRD-PARTY-NOTICES.md); the narrative credits
are in [`docs/attribution.md`](./docs/attribution.md). With particular thanks:

- [aiohomematic](https://github.com/SukramJ/aiohomematic) (MIT) — the
  reference implementation this Go port follows for wire behaviour and the
  device profile catalogue. Authored by **SukramJ** and **Daniel Perna**.
- [aiohomematic-config](https://github.com/SukramJ/aiohomematic-config) (MIT)
  — the form-schema, grouping and label logic ported into the Config UI.
- [pydevccu](https://github.com/danielperna84/pydevccu) (MIT, Daniel Perna &
  SukramJ) — the CCU simulator that
  [godevccu](https://github.com/SukramJ/godevccu) (and thus the integration
  test suite) is a Go port of.
- [matter.js](https://github.com/project-chip/matter.js) (Apache-2.0) — the
  gold standard for the entire Matter bridge; cluster schema, defaults and
  wire shape are mirrored from it.
- [homematicip-local-frontend](https://github.com/SukramJ/homematicip-local-frontend)
  (MIT) and the [Home Assistant frontend](https://github.com/home-assistant/frontend)
  (Apache-2.0) — the UI interaction and control-primitive references for the
  Svelte SPA.
- The Homematic / HomematicIP community and eQ-3 for the devices and protocol
  knowledge that make any of this possible.
