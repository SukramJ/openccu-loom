# Matter conformance smoke (chip-tool)

`make matter-smoke` runs the official CSA `chip-tool` against a
running OpenCCU-Loom Matter bridge inside Docker Compose and asserts
on a `Pairing Success` marker. It is a **pre-release gate**, not a
per-PR check — the chip-tool image is ~2.5 GiB compressed and the
smoke takes ~30 s after the image is cached locally.

When to run:

- Before tagging an `RC` or `final` release.
- After non-trivial changes under `internal/north/matter/` (cluster
  set, IM dispatch, secure channel, mDNS).
- After bumping the `connectedhomeip` chip-tool pin (see below).

In-process tests (`go test ./internal/north/matter/...`,
`tests/contract/`, `tests/golden/`) cover the daily loop. Reach for
this smoke when you need an external commissioner's verdict.

---

## Prerequisites

- **Linux host.** Matter discovery rides on UDP/IPv6 multicast; Docker
  Desktop on macOS / Windows does not bridge multicast into containers.
  On macOS, run the smoke from a Linux VM (UTM, Multipass, Lima) — or
  push the branch and let the `chiptool` CI workflow run it on a Linux
  runner (see [CI](#ci) below).
- Docker Engine ≥ 24 with the Compose plugin.
- IPv6 enabled on the host network interface (`sysctl
  net.ipv6.conf.all.disable_ipv6 == 0`).

No host avahi/dbus setup is needed: the chip-tool container runs its
own `dbus-daemon` + `avahi-daemon` (chip-tool's platform-mdns build
hard-requires the bus at init, even for `pairing already-discovered`).
A host avahi may coexist; both stacks answer multicast independently.

---

## Run

```sh
make matter-smoke
```

The target:

1. Builds the local `openccu-loom-matter-smoke:local` image from the
   repo `Dockerfile`.
2. Pulls the pinned `connectedhomeip/chip-cert-bins` image (cached
   after the first run).
3. Brings both services up under host networking.
4. Execs `chip-tool pairing already-discovered 0x1234 20202021
   ::1 5540 --pase-only true --bypass-attestation-verifier
   true` and tees the output to `tmp/matter-smoke.log`. The target is
   the IPv6 loopback because the `chip-cert-bins` chip-tool is an
   ipv6only build that rejects IPv4 literals.
5. Greps for `Pairing Success` and tears the stack down.

A failed run prints the last 80 OpenCCU-Loom log lines and leaves the
chip-tool log at `tmp/matter-smoke.log` for inspection. Use
`make matter-smoke-down` to clean up if a run was interrupted before
the teardown step.

---

## CI

The `chiptool` workflow (`.github/workflows/chiptool.yml`) runs both
chip-tool layers on `ubuntu-24.04-arm` runners (free for public
repositories). GitHub's Linux runners support host networking and
loopback UDP, so neither the Docker Desktop multicast limitation nor
the macOS container gap applies there. arm64 is not a choice but a
constraint: Docker Hub's `connectedhomeip/chip-cert-bins` publishes
arm64-only manifests — every recent tag lacks a `linux/amd64` image —
so an amd64 runner cannot pull the image at all:

- **chip-tool capability suite** (`make chiptool-test`) — the Go suite
  under `tests/chiptool/` against a runner-native chip-tool binary.
  CI extracts `/root/apps/chip-tool` from the pinned
  `connectedhomeip/chip-cert-bins` image (the pin is read from
  `compose/matter-smoke.yml`, so both layers always use the same
  chip-tool build) and caches the extracted binary keyed on the pin —
  the ~2.5 GiB image pull happens only after a pin bump. The binary
  location is passed via `OPENCCU_LOOM_CHIPTOOL_BIN` so the harness
  never silently skips on a PATH miss.
- **matter-smoke** (`make matter-smoke`) — the compose PASE smoke,
  identical to a local Linux run. On failure the chip-tool log is
  uploaded as the `matter-smoke-log` workflow artifact.

Triggers: nightly (03:47 UTC), the `needs-chiptool` PR label, and
manual `workflow_dispatch`. It is deliberately not a per-PR gate —
chip-tool runs are slow (the Go suite alone budgets up to 10 min).
The opt-in mDNS discovery test (`OPENCCU_LOOM_CHIPTOOL_MDNS=1`) stays
disabled in CI; multicast on shared runners is the flakiness the
suite's loopback design avoids.

---

## Configuration knobs

The smoke fixture lives at `compose/fixtures/matter-smoke-cfg.yaml`.
Adjust:

- `north.matter.discriminator` + `north.matter.commissioning.passcode`
  — must match the chip-tool invocation in the Makefile target.
- `north.matter.case.node_id` — leave at `0` for the PASE-only smoke.
  Setting non-zero plus a `fabric_id` would exercise CASE pickup, but
  needs a fabric-identity fixture (NOC + ICAC + RCAC + key) that does
  not yet ship in the repo.
- `mdns_advertise: zeroconf` — required so chip-tool's discovery
  finds the bridge by service-type. The default `noop` advertiser is
  for unit tests only.

---

## Bumping the chip-tool pin

The pin lives in `compose/matter-smoke.yml` under the `chip-tool`
service:

```yaml
image: connectedhomeip/chip-cert-bins:<commit-sha>
```

Tags on `connectedhomeip/chip-cert-bins` are commit SHAs from the
upstream `connectedhomeip` master at build time. To bump:

1. Look up the current latest tag:
   `curl -s "https://hub.docker.com/v2/repositories/connectedhomeip/chip-cert-bins/tags?page_size=5" | jq -r '.results[].name'`.
   Note the platform constraint: recent tags ship `linux/arm64`
   manifests only (no amd64), so the smoke needs an arm64 Linux host —
   an Apple-Silicon VM locally, `ubuntu-24.04-arm` in CI.
2. Cross-check that the SHA exists in
   `https://github.com/project-chip/connectedhomeip/commits/master`
   and that no spec-breaking changes landed between the old and new
   pin. (`SPECIFICATION.md` pins the bridge to Matter 1.5.1; chip-tool
   from a 1.5.x or later master is forward-compatible for our PASE +
   IM smoke.)
3. Update the `image:` line and run `make matter-smoke` locally to
   confirm the new chip-tool still passes.
4. Commit the bump on its own.

---

## Pre-release commissioner matrix

`make matter-smoke` covers chip-tool on loopback, but Apple Home /
Google Home / HA Matter Server have controller-specific quirks
(`Reachable=false` handling, subscription cadence floor, root-node
requirements) that no automated test catches. Before tagging an `RC`
or `final`, walk this checklist on a Linux host with avahi advertising:

### 1. chip-tool loopback (automated)

```sh
make matter-smoke
```

Must report `Pairing Success`.

### 2. Home Assistant Matter Server

- Spin up HA core (or HA OS) with the Matter Server add-on.
- In HA UI: *Settings → Devices & Services → Add Integration → Matter*.
- Tap *Add Matter device → Add device using setup code* and paste the
  bridge's manual pairing code (printed by `openccu-loom info` once
  the matter REST API is wired, or read directly from the smoke log).
- **Pass criteria**: bridge appears as a device, every channel
  surfaces as a sub-device under it, OnOff / LevelControl / Thermostat
  controls actually drive the underlying CCU.
- **Watch for**: `Reachable=false` flicker (HA polls aggressively;
  fast-cycling sensors should not toggle reachability).

### 3. Apple Home

- iOS device with Home app, on the same LAN as the daemon.
- *Add Accessory → More options → bridge name* (mDNS discovery).
- Enter the bridge's setup code.
- **Pass criteria**: bridge commissioned, every endpoint shows up as
  an accessory, scenes can be built from the exposed clusters.
- **Watch for**: ColorControl HS-mode handling on Apple's side; some
  controllers refuse non-1.3 ClusterRevisions silently.

### 4. Google Home

- Google Home app on a paired Android device.
- *+ Add → Set up device → New device → matter logo*.
- Scan the QR code from `GET /api/v1/matter/setup-payload`.
- **Pass criteria**: bridge + endpoints discovered + controllable.
- **Watch for**: Google's Smoke / CO clusters have stricter feature-map
  validation than HA; SmokeCOAlarm endpoints with FeatureMap=0 are
  rejected.

### 5. Sign-off

Record the run in the release CHANGELOG entry. Any controller-specific
issue is filed against the controller, not patched in OpenCCU-Loom
silently — see ADR 0012 §Risks #4.

---

## Troubleshooting

- **`No such network: host` on macOS** — Docker Desktop. Use a Linux
  VM; this smoke cannot run on macOS hosts directly.
- **chip-tool dies at init with `CHIP Error 0x000000AD: Open file
  failed` (DnssdImpl)** — the in-container dbus/avahi did not come up;
  check `docker compose -f compose/matter-smoke.yml logs chip-tool`.
- **chip-tool times out at "Discovering devices"** — mDNS advertising
  is not reaching the resolver, or IPv6 is disabled. Verify from
  inside the chip-tool container with
  `docker compose -f compose/matter-smoke.yml exec chip-tool avahi-browse -art | grep _matter`.
- **`Pairing Success` missing but no error** — the smoke may be
  matching the wrong commissioning attempt. Check
  `tmp/matter-smoke.log` for the actual `chip-tool` exit message.
- **Image pull stalls** — `connectedhomeip/chip-cert-bins` is large
  (~2.5 GiB). First-time pull on a slow link can take 5–10 min.
