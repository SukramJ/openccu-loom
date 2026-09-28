# Privacy: what the daemon sends to outside sources

OpenCCU-Loom has no telemetry, no vendor account and no cloud service of
its own. This page lists every connection the daemon opens by itself,
field by field: where it goes, when it happens, what it sends, and how to
switch it off. Everything else stays on your local network, or goes only
to destinations you configure yourself.

The requests the daemon builds towards the internet are pinned by tests
(`internal/addonupdate/outbound_test.go`): the test compares every
request verbatim — method, path and each header — so a change to what
leaves the daemon fails the test, and this page is changed with it.

## In short

| Connection | Destination | When | What identifies the system | Switch |
| --- | --- | --- | --- | --- |
| [Release check](#release-check-and-add-on-self-update) | GitHub (`api.github.com`) | CCU add-on builds only: shortly after boot, then daily | nothing but the IP address | `addon_update.enabled` |
| [Add-on download](#release-check-and-add-on-self-update) | GitHub release assets | only when an update is installed | nothing but the IP address | manual action |
| [Login with OpenID Connect](#login-with-openid-connect) | the provider you configure | only when configured: at login and key refresh | the client id, the redirect URI | `north.rest.auth.oidc.enabled` (default off) |

Everything below the table's line goes only where **you** point it — see
[Destinations you configure](#destinations-you-configure) and
[On the local network](#on-the-local-network).

## Release check and add-on self-update

Only the CCU add-on build performs this check — on every other platform
(binary, Docker, Home Assistant add-on) the capability probe fails and
the section has no effect. On an **openccu-lite** host the probe also
reports unsupported (the `VARIANT=lite` marker in `/VERSION`): add-on
updates there belong to occulited's own catalogue, which downloads and
installs releases itself. When active, the daemon asks
`https://api.github.com/repos/SukramJ/openccu-loom/releases/latest`
shortly after boot and then once a day whether a newer release exists
(ADR 0057).

- **Sent**: a plain `GET` with `Accept: application/vnd.github+json` and
  Go's own `User-Agent`. No version, serial number, hostname or any
  other identifier of the running system is in the request.
- **On install** (a manual action, or the staged update path): the
  release's checksum file and the add-on tarball are downloaded from
  GitHub's release assets; the tarball is verified against its SHA-256
  before it is staged.
- **Switch**: `addon_update.enabled: false` stops the background check;
  the manual check stays available.

## Login with OpenID Connect

Only when you configure an OIDC provider (`north.rest.auth.oidc`): the
daemon fetches the provider's discovery document and JWKS keys, and
exchanges the login code (with PKCE). The requests carry the client id
and the redirect URI — they identify the daemon *to your provider*,
which you chose. Default: off.

## Destinations you configure

These connections exist only when you configure them, and go only to the
host you name:

| Destination | Config | What is sent |
| --- | --- | --- |
| Your CCU / openccu-lite systems | `centrals[]` | the daemon's whole purpose: device reads/writes, events, names — plus the callback address the CCU pushes events to |
| Your MQTT broker | `north.mqtt` (default off) | device state, Home Assistant discovery, command topics |
| Your webhook endpoint | `north.webhook` (default off) | datapoint / status / incident events as HMAC-signed JSON |
| Your InfluxDB | `history.export` (default off) | recorded measurement samples, line protocol |
| Your OTLP collector | `north.rest.tracing.otlp_endpoint` (default off) | trace spans of the daemon's own request handling |

What these payloads contain is device data from your CCUs — treat the
receiving systems accordingly.

## On the local network

- **mDNS advertisement** (`north.discovery.mdns`, default on): the daemon
  announces itself as `_openccu-loom._tcp.local.` — hostname, REST port,
  API version, TLS flag — so Home Assistant and other zeroconf clients
  find it. Multicast only; nothing leaves the LAN.
- **SSDP search** (`north.discovery.ssdp`, default on): an `M-SEARCH`
  multicast probe every 60 seconds to find CCUs on the LAN for the
  adopt-a-central UI. The probe carries no data about the daemon beyond
  its source address.
- **Matter bridge** (`north.matter.enabled`, default off): when enabled,
  the bridge announces itself over mDNS so Matter controllers can
  commission and reach it. LAN-scoped.

## What never leaves the system

Device data, datapoint values, names, rooms, credentials, backups and
the measurement history are served to *your* clients (REST/WebSocket,
MQTT, Matter, the web UI) and stored locally. The daemon fetches no
fonts, scripts or other resources from the internet — the web UI is
fully embedded in the binary — and it makes no NTP, geolocation or
crash-reporting calls. There is no "phone home" beyond the release check
above, and none at all outside the CCU add-on build.
