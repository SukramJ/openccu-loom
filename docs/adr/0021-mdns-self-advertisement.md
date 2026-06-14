# ADR 0021 — mDNS self-advertisement for LAN auto-discovery

- **Status**: accepted
- **Date**: 2026-05-24
- **Related**:
  [ADR 0020 — external-client wire contract](./0020-external-client-wire-contract.md),
  [`docs/external-clients/asks.md`](../external-clients/asks.md) (closes E1),
  `internal/north/discovery/mdns/`,
  `internal/north/matter/mdns/` (Matter-side mDNS — different consumer, same library)

## Decision

OpenCCU-Loom advertises its north-bound REST surface on the local
network via mDNS so zeroconf-aware clients (Home Assistant config
flow, generic mDNS browsers, an SPA hosted on a separate host) can
auto-discover the daemon without manual host/port entry.

One service is published:

- **Service type**: `_openccu-loom._tcp.local.`
- **Port**: `North.REST.Listen` (parsed from the configured listen
  address; the daemon skips advertisement when the address has no
  numeric port — Unix sockets, malformed strings).
- **Instance name**: `North.Discovery.MDNS.InstanceName`, falling
  back to the OS hostname (with any `.local` suffix stripped).
- **TXT records** (always-emitted shape):
  - `path=/api/v1` — REST mount path
  - `api_version=<handlers.APIVersion>` — wire-contract version
    (mirrors `GET /info.api_version`)
  - `tls=0` — TLS-in-front flag; `0` until the daemon grows a TLS
    listener (or a documented reverse-proxy pattern surfaces the
    flag separately)
  - `instance=<resolved instance name>` — the friendly label a
    discovering client shows in its daemon picker (the SRV instance
    label carries the same value; the explicit key is robust across
    resolvers that do not surface the label conveniently)
  - `centrals=<count>` — number of CCUs this daemon serves, a cheap
    pre-auth hint for the picker. The CCU names/serials themselves are
    deliberately NOT advertised (volatile, only known post-connect,
    and TXT is size-limited); a client reads them from
    `GET /api/v1/system/ccu` after it authenticates. The intended
    discovery flow: browse `_openccu-loom._tcp` → pick a daemon
    (label `instance`, host/IP from A/AAAA, port from SRV) → enter a
    token → `GET /system/ccu` → pick the CCU by name/serial — no
    manual host or instance entry.

The implementation lives in `internal/north/discovery/mdns/` —
deliberately a separate package from `internal/north/matter/mdns/`
even though both share the same underlying library (`grandcat/zeroconf`).
Matter has tuning concerns specific to commissioner interop
(curated address list via `RegisterProxy`, subtype responder,
OS-pinned hostname to avoid duplicate-A-record drowning) that don't
apply to daemon-discovery; consolidating them under one Advertiser
abstraction would muddy both surfaces.

Default is **on** — opt-out via `North.Discovery.MDNS.Enabled: false`.

## Context

`docs/external-clients/asks.md` E1 captured the request from the
`py-openccu-loom-client` / `homematicip_local` migration: the HA
config flow currently asks the user for host + port + interface list
on every install. Once the daemon advertises itself, HA's
`zeroconf:` manifest entry can prefill the host + port, leaving the
user with only the token (or credentials) to enter.

The same advertisement helps any other LAN client (Node-RED, Magic
Mirror, an SPA hosted off-box) find the daemon, which is exactly
the failure mode an operator hits today: "where is the daemon
running, again?"

The decision to ship E1 separately from the wire-contract foundation
(ADR-0020) was deliberate — E1 is a runtime feature touching the
daemon lifecycle and dependency tree; it deserved its own focused
PR rather than getting bundled into a contract-surface change.

## Consequences

### What ships

- `internal/north/discovery/mdns/advertiser.go` — `Advertiser`
  interface, `Noop` and `Multicast` implementations, `Service`
  struct, validation.
- `internal/north/discovery/mdns/advertiser_test.go` — Noop
  round-trip + idempotency + instance-name resolution coverage.
  Multicast is exercised at the daemon-integration level rather
  than unit-tested here (mDNS over UDP 5353 is flaky in CI).
- `internal/config/config.go` — `NorthDiscovery.MDNS` config struct
  with `Enabled *bool` (default-true via `IsEnabled()`) and
  `InstanceName string` (default = OS hostname).
- `cmd/openccu-loom/daemon.go` — `startMDNSAdvertiser` helper +
  Start/Stop wiring inside the REST-enabled branch. Failure to
  register is logged at WARN and does not abort daemon startup —
  mDNS is convenience, not a hard dependency.
- `docs/external-clients/asks.md` — E1 marked closed.

### Default-on rationale

The asks.md TL;DR motivation behind E1 was HA out-of-box auto-discovery.
Default-off would re-impose the manual host/port entry on every fresh
install — the exact friction we're removing. Operators who don't want
LAN visibility can opt out with one config line.

Concretely: the daemon already exposes a TLS-less REST surface on
all configured interfaces in default deployments. Adding an mDNS
TXT record that names the port is a marginal information disclosure
(`netstat -tlnp` would surface the same port to any LAN-resident
attacker); the actual security boundary is the auth layer (basic /
bearer / OIDC), not the discoverability of the listen address. For
deployments where even the discoverability matters (segmented LANs,
multi-tenant networks), the opt-out flag is one YAML line.

### Library reuse

`grandcat/zeroconf v1.0.0` was already pulled in for the Matter
mDNS layer; reusing it here adds no new dependency. The daemon
calls `zeroconf.Register` (not `RegisterProxy`) — interface
filtering and curated address lists are unnecessary for the simpler
daemon-discovery shape, and `Register`'s "all multicast-capable
interfaces" default is exactly what HA expects when the daemon is
multi-homed.

### Multi-central + multi-instance

The advertisement is per-daemon, not per-central. A host running
two openccu-loom instances (different REST ports) would need
distinct `instance_name` values to avoid mDNS record collision.
The default hostname-based name handles the common one-instance
case; multi-instance operators must configure `instance_name`
explicitly. Documented in the config struct comment; not enforced
by validation (an operator running two instances has bigger
problems than name collision).

### What the SDK author can rely on

The `py-openccu-loom-client` integration into `homematicip_local`
can now wire HA's `zeroconf:` manifest entry to:

```json
{
  "zeroconf": [
    {
      "type": "_openccu-loom._tcp.local.",
      "properties": { "api_version": "1.*" }
    }
  ]
}
```

The TXT `api_version` field uses semver, so prefix-matching on
`1.*` admits future minor bumps without manifest churn. Major
bumps (`2.*`) will require a manifest update — which is correct:
the SDK at that point also needs work.

## Alternatives considered

### A. Default-off (opt-in)

Considered for consistency with `cfg.North.Matter.Enabled = false`
default. Rejected because Matter requires hardware certs + a
commissioning device + Apple/Google/Alexa ecosystem setup —
default-off matches the operator effort required. mDNS
self-advertisement requires no setup at all; default-off would
re-impose the friction E1 was raised to remove.

### B. Bake mDNS into `internal/north/matter/mdns/`

Considered for code reuse — one mDNS package, two service types.
Rejected because the Matter package has accumulated commissioner-
interop tuning (subtype responder, hostname proxy, primary-IP
curation) that complicates daemon-discovery; the simpler shape
deserves its own surface. The shared library underneath is
sufficient code reuse.

### C. Expose mDNS through the capability handshake

Considered surfacing `discovery.mdns.v1` as a capability in
`GET /info.capabilities`. Rejected because mDNS is *the discovery
mechanism* — by the time a client reads `/info`, it has already
found the daemon. Surfacing the capability would be circular.
External clients that need to know "should I keep advertising
locally?" can look for the daemon's TXT records directly; the
capability list stays focused on wire-protocol features.

## Migration impact

No breaking change for existing operators:

- Default-on means existing fresh installs auto-advertise.
- The opt-out flag is additive; operators who don't set it inherit
  the new default.
- Upgrades from 0.1.0 see the daemon start advertising on the next
  restart; nothing in the existing setup changes.
- mDNS register failures (port conflict on UDP 5353 with another
  responder) are logged at WARN and do not abort startup.

HA `homematicip_local` config flow can wire the `zeroconf:` manifest
entry in the next SDK release. Until it does, the advertisement is
visible to any other zeroconf browser and is otherwise inert.
