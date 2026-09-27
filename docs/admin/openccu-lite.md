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

## See also

- [Configuration reference](configuration.md#centrals) — `system_type`, `api_token`, `tls_fingerprint`, `json_rpc_port`.
- [REST + WebSocket](../integrations/rest-ws.md) — the `feature_unavailable` problem, taxonomy endpoints, onboarding routes.
- [Backup & restore](backup.md) — what a lite backup/restore can and cannot do.
- [Multi-CCU handbook](../user/multi-ccu.md) — running CCUs and openccu-lite boxes side by side.
- [Architecture](../developer/architecture.md#south-profiles) — how a central picks its south-bound profile.
