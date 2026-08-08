# ADR 0046 — Active SSDP/UPnP discovery of CCUs on the LAN

- **Status**: Accepted
- **Date**: 2026-06-29
- **Related**:
  [ADR 0021 — mDNS self-advertisement](./0021-mdns-self-advertisement.md),
  [ADR 0002 — multi-CCU first class](./0002-multi-ccu-first-class.md),
  [SPECIFICATION.md](https://github.com/SukramJ/openccu-loom/blob/main/SPECIFICATION.md) §4.1

## Context

Adding a CCU today means typing its host address by hand. Homematic and
OpenCCU central units already announce themselves on the LAN over SSDP/UPnP —
a CCU answers an M-SEARCH and serves a UPnP device description at
`http://<ip>/upnp/basic_dev.cgi` carrying its `friendlyName`, `manufacturer`,
`modelDescription`, and `UDN`. The Home Assistant integration
(`homematicip_local`) relies entirely on Home Assistant's SSDP integration for
this — it filters discovery results on `manufacturer == "OpenCCU"`, maps
`friendlyName` → name, `modelDescription` → serial, and the location host → the
CCU address. `aiohomematic` itself has **no** discovery; it only speaks to an
already-known host.

OpenCCU-Loom is a standalone daemon with no Home Assistant underneath, so it
has to run discovery itself.

## Decision

Implement **active SSDP discovery** in a new package
`internal/north/discovery/ssdp`, surface the results through REST + the SPA, and
let the operator **adopt** or **ignore** each find.

1. **Scanner.** A long-lived `Discoverer` periodically multicasts an M-SEARCH
   (generic `ssdp:all` target — like homematicip_local we filter on the device
   description, not the SSDP ST) on every routable, non-virtual LAN interface,
   follows each responder's `basic_dev.cgi`, and parses it. The interface
   filtering reuses the mDNS advertiser's virtual-bridge exclusion so the probe
   leaves via a real LAN link in container setups (HA add-on host networking).
   Implemented with the Go stdlib + `golang.org/x/net/ipv4` — **no new
   dependency**. Lifecycle mirrors the mDNS advertiser (start at boot, stop on
   exit); stale entries expire after a few missed scans.

2. **Manufacturer scope.** Accept `OpenCCU` **and** classic eQ-3 / HomeMatic /
   RaspberryMatic centrals (a superset of homematicip_local's OpenCCU-only
   filter), so a real CCU2/CCU3 is found too.

3. **Identity.** The stable id is the UDN serial
   (`uuid:upnp-BasicDevice-1_0-<SERIAL>`), falling back to the last 10
   characters of `modelDescription` (homematicip_local's convention). It dedupes
   responses and keys the ignore list.

4. **Ignore list.** Persistent, keyed by serial
   (`discovery_ignored_ccus` table, migration 023, modelled on the
   `visibility_unignore` store). Ignored CCUs are filtered out of the discovery
   surface so an unwanted one stops reappearing; an operator can un-ignore one.

5. **REST.** `GET /api/v1/centrals/discovered` (each entry flagged
   `already_configured` by host match against the configured centrals),
   `GET …/discovered/ignored`, and admin-gated
   `POST|DELETE …/discovered/{serial}/ignore`. Adoption reuses the existing
   `POST /api/v1/centrals`. `APIVersion` → 2.8.0.

6. **UI.** A "discovered CCUs" section in the Settings → CCUs view (adopt /
   ignore) and a pick-list in the first-run onboarding wizard's CCU step.

7. **Default on.** Discovery is enabled by default
   (`cfg.North.Discovery.SSDP`), opt-out via config. It only *reads* the network
   (a multicast probe — nothing about the daemon leaves the LAN) and simply
   finds nothing where multicast is unavailable.

## Consequences

- One-click CCU adoption instead of hand-typing an address; better first-run UX.
- A new always-on multicast scan loop. Bounded: one small datagram per
  interface per interval, results capped by what answers on the LAN.
- The scope deliberately diverges from homematicip_local (OpenCCU-only) by
  also accepting classic eQ-3 / HomeMatic / RaspberryMatic centrals.
- Multicast reachability varies by network topology (some container bridges
  drop it); the feature degrades to "no results", never to an error.

## Update (0.21.0): serial-keyed "already configured" + suggested adoption host

The first cut keyed the "already configured" flag and the adoption pre-fill on
the discovered **host** (the device-description URL's IP). That IP is not
stable — a DHCP lease or a rotating docker address makes a configured CCU look
new, and pre-filling a docker IP yields a connection that breaks on the next
add-on restart. Two refinements:

- **Match by serial.** Adopting a CCU now persists its hardware serial (a
  `serial` column on the centrals store, migration 024); the discovery list
  flags "already configured" by serial, falling back to host only for rows that
  predate serial capture (YAML / manual / pre-migration).
- **Suggest a stable host.** A server-computed `suggested_host` per discovered
  CCU (the SPA pre-fills it; the raw host is still shown):
  1. the discovered IP is one of the daemon's own interface IPs → `localhost`;
  2. supervised (HA add-on) **and** the IP is in the docker range
     `172.16.0.0/12` → reverse-DNS the IP to its stable container hostname;
  3. otherwise the raw host, unchanged.

  LAN ranges (192.168/16, 10/8) are deliberately left untouched, so a normal
  CCU keeps its address. The reverse lookup is best-effort: no PTR → raw host.
