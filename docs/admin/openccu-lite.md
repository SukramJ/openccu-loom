# Connecting an openccu-lite system

How to add a box running **openccu-lite** (a CCU firmware without
ReGaHss, managed by the daemon `occulited`) as a central, what it can
and cannot do compared to a full CCU, and how to read the reasons the
daemon reports when something is not available.

!!! info "Who this page is for"
    Administrators adding an openccu-lite box to the fleet. For the
    config keys themselves see the
    [configuration reference](configuration.md#centrals); for running a
    mix of CCUs and openccu-lite boxes see the
    [multi-CCU handbook](../user/multi-ccu.md).

## What works

Everything a device needs is served through the box's `occulited` API:
devices, channels, paramsets, values, links, MASTER configuration,
install mode, device firmware, and delete/replace/config-restore, all
proxied through the box's XML-RPC interfaces. Live events arrive on the
box's event stream instead of an XML-RPC callback, so a value change,
a new or removed device, or an interface going down or coming back
shows up exactly as it would from a CCU. Names, rooms and functions
come from the box's own metadata store and follow its change stream, so
a rename made on the box appears live. System management — reboot,
power off, recovery mode, backup and restore, firmware updates, service
messages, install mode, duty cycle, heating groups (list and delete) —
is available wherever the API token carries the scope for it.

Device pictures work too, even though the box has no WebUI to serve
them: the daemon ships the CCU's device artwork in its embedded data
snapshot and serves it from there.

## What is absent, and why

openccu-lite has no ReGaHss and no script interpreter, so a handful of
CCU features simply do not exist on it:

| Absent | Reason |
|---|---|
| System variables, programs, HM-Script, the CCU inbox | No ReGa, no script interpreter |
| Acknowledging service or alarm messages | The box's API has no acknowledge endpoint |
| Communication test, astro position, safe mode | No endpoint on the box |
| CUxD | The box's event stream carries only its own XML-RPC interfaces, never BIN-RPC |

None of these fail silently: the daemon reports each one as an
unavailable **feature**, with a reason (see
[Feature keys](#feature-keys-and-what-they-need) below), and the web UI
hides the corresponding views and actions instead of offering something
that would fail.

Creating and editing heating groups (their members and the member
candidates) are not offered yet — listing groups and deleting one work,
but the shape of the box's groups-membership API is not yet known well
enough to map member ids without risking a device landing in the wrong
group.

## Adding an openccu-lite box

Both the setup wizard and **Settings → CCUs → Add CCU** walk the same
steps:

1. **Identify system** — enter the host (or pick one SSDP already
   found and labelled `openccu-lite`). The daemon asks the address what
   it is; over HTTPS with a certificate no authority signed, it shows
   the certificate's SHA-256 fingerprint for you to compare against the
   box's own display.
2. Choose how to authenticate:
   - **Pair** — pick an access level (**full**, **control-only** or
     **read-only**); the web UI shows a six-digit code, which the box's
     administrator enters on the box to approve the request. The pairing session is cancellable and
     shows its live state while you wait. A token from pairing can
     never carry the `backup`, `power`, `radio:keys`, `addons:write`,
     `led` or `auth:admin` scopes — pair for everyday operation, and
     use a manual token when you need those.
   - **Paste a token** — a token created on the box's own console:
     ```sh
     occulited token openccu-loom --scope rpc:admin --scope meta:write --scope system:write --scope logs:read --scope backup --scope power
     ```
     lists every scope this daemon can use. Grant a narrower set and
     the corresponding features are reported as missing the scope,
     never as a hard failure.
3. Pick the interfaces; `CUxD` is never offered for an openccu-lite
   system. Username and password fields are hidden — the box
   authenticates by token, not by a CCU account.

The token itself never reaches the browser: for a pairing, the daemon
holds the approved token server-side and a central created with the
pairing's id takes it directly.

After the box is adopted, its admin page shows the token's effective
scopes and names which features each missing scope would unlock.

## TLS pinning

An openccu-lite box on the LAN typically serves a self-signed
certificate. Rather than skip verification outright, set
`tls_fingerprint` (the lower-case hex SHA-256 of the certificate's DER
encoding): the handshake then succeeds exactly when the served leaf
certificate matches that fingerprint, whoever issued it — the chain
itself is not checked. The wizard pre-fills this from the probe step;
confirm it against the box's own display before accepting. This is
mutually exclusive with `tls_insecure_skip_verify`, and only applies
with `tls: true`.

## One event stream, two slots per token

The daemon holds **one** SSE stream per openccu-lite central, carrying
every configured interface. The box limits each token to two
concurrent streams (SSE and WebSocket combined, 16 in total). Pair or
create one token per Loom instance — a token shared with another
client (a second Loom instance, a browser tab against the box's own
API) can starve the daemon of its stream slot: the box answers `429`,
the daemon backs off and retries (honouring the box's `Retry-After`, 30
seconds otherwise) and logs `lite.stream.dropped` naming the cause,
until the other client's stream ends and a slot frees up. Events still
arrive once the stream reconnects; while it is down, values fall back
to whatever the last reconciliation seeded plus on-demand reads.

## Feature keys and what they need

`GET /api/v1/system/ccu` reports, per central, a `features` map: every
key the daemon knows, whether it is available right now, and if not,
why (`not_supported_by_system` — the system has no such thing;
`missing_scope` — the token lacks the named scope; `not_ready` — the
central has not finished its first bring-up). On openccu-lite, each
scope-gated feature needs the scope named below (already expanded —
`rpc:admin` implies every lower `rpc:*` tier, `system:write` implies
`system:read`, and so on):

| Feature | Needs | Never available |
|---|---|---|
| `hub.sysvars`, `hub.programs`, `hub.alarm_messages`, `hub.inbox` | — | always |
| `hub.service_messages` | `system:read` | |
| `hub.service_messages.ack` | — | always |
| `hub.service_messages.suppress` | `rpc:admin` | |
| `hub.system_update` | `system:read` | |
| `hub.system_update.install` | `power` | |
| `system.backup.create` | `backup` | |
| `system.backup.restore` | `power` | |
| `system.reboot`, `system.poweroff`, `system.recovery_mode` | `power` | |
| `system.safe_mode`, `system.position` | — | always |
| `system.auth_delegation` | — | uses the logging-in operator's own account |
| `device.control` | `rpc:operate` | |
| `device.configure` | `rpc:configure` | |
| `device.admin`, `device.firmware_update` | `rpc:admin` | |
| `device.communication_test` | — | always |
| `device.rename` | `meta:write` | |
| `taxonomy.read` | `meta:read` | |
| `taxonomy.assign`, `taxonomy.edit`, `taxonomy.tree` | `meta:write` | |
| `heating_groups.read` | `system:read` | |
| `heating_groups.write` | `system:write` | (lists and deletes; create/update are not offered yet, see above) |
| `install_mode` | `rpc:configure` | |
| `install_mode.local` | `rpc:admin` | |
| `radio.duty_cycle`, `connectivity` | `rpc:read` | |

A token with only `rpc:read` and `meta:read` therefore gives you a
read-only view: devices, values, names and rooms, but no writes and no
system management. See [REST + WebSocket](../integrations/rest-ws.md)
for how a request answers when a feature is missing.

## Troubleshooting: readiness reasons

While an openccu-lite central is not yet serving, `/system/ccu`
reports a plain-language reason instead of just "not ready":

| Reason | Meaning |
|---|---|
| `occulited starting (503)` | The box's own service is still coming up. |
| `token rejected (401)` | The API token is invalid or was revoked. Re-pair or paste a fresh token. |
| `token lacks <scope> (403)` | The token cannot even read the box's health/interfaces; grant it at least `rpc:read`. |
| `occulited reports itself unhealthy` | The box answered but flags its own health as not OK. |
| `no configured interface is running` | None of the interfaces this central is configured for are up on the box. |
| `occulited answered <status>` | An HTTP status other than the ones above; check the box directly. |
| `unreachable: …` | A network-level failure reaching the box. |

A rejected token is a credential problem: re-pair, or paste a token
that the box accepts.

## Running OpenCCU-Loom on the box itself

OpenCCU-Loom can also run as an add-on on the openccu-lite box, installed
from the box's Addons page. A few things differ from a CCU:

- **The local box onboards itself.** The box mints the add-on an API
  token at every start (the manifest's declared scopes:
  `rpc:admin`, `meta:write`, `system:write`, `logs:read`), and a first
  boot with no centrals configured adopts the local system with it —
  named after the box's hostname, no pairing step, no token to paste.
  The token is read from its file on every request, so the rotation at
  each occulited start needs nothing from you (the central's
  `api_token_file` field; also useful for container secret mounts).
  `backup` and `power` are never part of an add-on token: pair with the
  box when you want backups or reboots from Loom, exactly as the
  feature table's `missing_scope` reasons say. Deleting the
  auto-onboarded central sticks for as long as any central exists; an
  empty list on the box re-onboards at the next start (ADR 0077).

- **The Config UI is served through the box's web server.** The add-on
  ships a validated lighttpd fragment (ADR 0078): the box proxies
  `https://<box>/addons/loom/` to the daemon on the loopback — TLS and
  the certificate you configured on the box included, the WebSocket
  event stream included — and the box shell lists the UI in its
  navigation. Nothing to open in the firewall for this path; port 8119
  is only needed for direct access from other machines (a Home
  Assistant backend, MQTT-less REST clients).
- **Reusing the box's certificate on the direct port** (optional): both
  a CCU and an openccu-lite box keep their web-server certificate in
  one combined file, `/etc/config/server.pem`, which the add-on may
  read. Set `north.rest.tls_cert_file` **and** `tls_key_file` both to
  that path and the daemon serves HTTPS on 8119 with the same
  certificate; a rotation (ACME renewals included) is picked up on the
  next connection, and on openccu-lite an ACME renewal restarts the
  add-on anyway. This stays opt-in — switching 8119 to HTTPS breaks
  clients that talk `http://` to it today.
- **Open port 8119 on the Addons page** (only for direct access). The add-on's manifest declares
  its ports, so they appear as switches under *Addon ports*: 8119
  (Config UI, REST/WebSocket, MCP) is the one to open; 8120/8129 (the
  XML-RPC/BIN-RPC callbacks a *remote* CCU would push to) and 5540
  (Matter) stay closed until you use those features. An opened switch is
  a named firewall rule owned by the add-on. mDNS needs no opening — the
  box's own discovery rules already accept multicast to the mDNS groups.
  On a version installed before the manifest declared ports, open 8119
  on the Firewall page instead and **confirm it**: openccu-lite loads a
  firewall change as a draft and puts the previous rules back after one
  minute unless the change is confirmed on the page.
- **Updates come from the Addons page.** The add-on's own self-update is not
  offered on openccu-lite; the box's catalogue carries new versions.
- **No session in the add-on's page URL.** The add-on ships an
  `openccu-lite.json` declaring that its settings page takes no session in
  the URL (`ui.session_header`); the page is a landing card and reads no
  session at all. On a version without that file (0.80.0 and earlier),
  openccu-lite warns that the page receives your session in its URL; you can
  switch the old session handover off for OpenCCU-Loom on the Addons page
  without losing anything.
- **Downloaded archives stay in the add-on's directory.** The box mounts
  `/usr/local` read-only for add-ons apart from their own directories, so
  archives land in the add-on's `var/backups` rather than
  `/usr/local/sdcard/backup`.

## Trying it without a box

The [godevccu](https://github.com/SukramJ/godevccu) simulator can play a
complete openccu-lite box — API token, XML-RPC proxy, event stream and
all — loaded with its full embedded device fleet:

```sh
godevccu -mode lite -lite-listen 127.0.0.1:2121
```

The startup log prints the box URL and the API token; add a central with
`system_type: openccu-lite` pointed at it, exactly as above. The full
recipe, including the central's YAML block, is in
[Testing → Running against a simulated system](../developer/testing.md#running-against-a-simulated-system).

## See also

- [Configuration reference](configuration.md#centrals) — `system_type`, `api_token`, `tls_fingerprint`, `json_rpc_port`.
- [REST + WebSocket](../integrations/rest-ws.md) — the `feature_unavailable` problem, taxonomy endpoints, onboarding routes.
- [Backup & restore](backup.md) — what a lite backup/restore can and cannot do.
- [Multi-CCU handbook](../user/multi-ccu.md) — running CCUs and openccu-lite boxes side by side.
- [Architecture](../developer/architecture.md#south-profiles) — how a central picks its south-bound profile.
