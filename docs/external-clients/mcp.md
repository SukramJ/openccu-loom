# Using the OpenCCU-Loom MCP Server

!!! info "Who this page is for"
    Integrators connecting an LLM agent (Claude Desktop, Claude Code, or
    any MCP client) to a running daemon. Administrators enable and scope
    the server; see [Authentication](../admin/auth.md) for the auth chain
    it inherits.

OpenCCU-Loom ships a **Model Context Protocol (MCP)** server as a
north-bound adapter. It lets LLM agents (Claude Desktop, Claude Code,
or any MCP-capable client) read — and, if you opt in, write — your
Homematic CCU domain through a small, typed tool surface.

The MCP adapter is a thin projection of the same domain the REST API
serves: every tool is scoped per central, reads are always available,
and writes require a **second, explicit opt-in**. Authorization is the
REST listener's auth chain: the mount authenticates every request and
gates the whole tool set at one role — viewer, or operator once writes
are allowed. A tool whose REST twin is admin-only (`list_audit`) checks
the caller's role itself, so both surfaces draw the same boundary.

- Design rationale: [ADR 0025 — MCP north-bound adapter](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0025-mcp-northbound-adapter.md)
- Dev-mode surface: [ADR 0026 — MCP dev mode](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0026-mcp-dev-mode.md)
- Implementation: `internal/north/mcp/` (`server.go`, `tools.go`, `tools_hub.go`, `tools_alarm.go`, `tools_ops.go`, `tools_fleet.go`)

---

## 1. Quick start

### 1.1 Enable the server

The MCP route is **off by default**. The quickest way to turn it on is
the Config UI: open **Settings → MCP**, tick **Enabled** (and, if you
want agent-driven control, **Allow writes**), then restart the daemon —
the route is mounted at boot, so the change is restart-required.

Prefer YAML? Set it in your config (`config.yaml`):

```yaml
north:
  rest:
    listen: ":8119"        # the MCP route is mounted on the REST listener
  mcp:
    enabled: true          # master switch — false = no MCP route at all
    allow_writes: false    # keep read-only for now (see §4)
    path: /mcp             # HTTP mount path on the REST listener
```

Defaults: `enabled: false`, `allow_writes: false`, `path: /mcp`. The
REST listener defaults to `:8119`.

Restart the daemon. On startup you'll see:

```
INFO north.mcp.enabled path=/mcp allow_writes=false
```

The MCP endpoint is now served at:

```
http://<host>:8119/mcp
```

> **Note:** MCP does **not** get its own listener or port. It is mounted
> on the existing REST listener at `path`; every other URL falls through
> to the normal REST router.

### 1.2 Transport

The server speaks **Streamable-HTTP** — the official MCP HTTP transport
(`github.com/modelcontextprotocol/go-sdk`). There is no stdio transport;
point your client at the HTTP URL above. For stdio-only clients, bridge
with a small proxy such as `mcp-remote` (see §3.2).

### 1.3 Authentication

The MCP endpoint sits **behind the same auth chain as REST**. Send a
credential on every request:

```
Authorization: Bearer <api-token>
```

API tokens (Bearer) are the recommended path for agents and CI; Basic
auth also works. Create a token the same way you would for any REST
client. A request without a valid credential gets `401` before it ever
reaches a tool.

### 1.4 Discover the posture from `GET /info`

The daemon advertises its MCP posture as capability tokens so a client
can reason about what's available before connecting:

| Capability token | Meaning |
| --- | --- |
| `mcp.v1` | MCP server is enabled (read tools available) |
| `mcp.write.v1` | Write tools are also enabled (`allow_writes: true`) |

```sh
curl -s -H "Authorization: Bearer $TOKEN" http://host:8119/info \
  | jq '.capabilities'
# [..., "mcp.v1", "mcp.write.v1"]
```

---

## 2. The tool surface

Thirty-nine tools, in two tiers: 31 read tools + 8 write tools. **Read
tools** are always registered (each gated on its backing subsystem
being wired). **Write tools** are registered only when
`allow_writes: true` — and that flag includes arming and disarming the
alarm system; see §4.

Tool names follow one taxonomy across the whole surface: `list_<plural>`
enumerates like entities, `get_<singular>` fetches one record or an
overall view, `read_<noun>` reads a keyed sub-structure, and
`<verb>_<noun>` is a write/action. Names use the project's compact
domain vocabulary (`central`, `datapoint`, `paramset`, `sysvar`).

Every tool that touches a specific CCU takes a `central_name`. It is
**optional on reads** (omit to span all centrals) and **required on
writes** — and on a write the named central *must own* the target
device, or the call is rejected (ADR 0002, multi-CCU safety).

### 2.1 Read tools (always available)

| Tool | Arguments | Returns |
| --- | --- | --- |
| `list_centrals` | — | The configured CCU names. These are the scoping dimension for every other tool. |
| `list_devices` | `central_name?` | Device summaries (address, model, name, interface, central). Omit `central_name` to list all. |
| `get_device` | `address` | A single device summary + its owning central. |
| `list_channels` | `address` (device-level) | The device's channels (address, number, type, name, room, data-point count). Use it to discover channel addresses (`<device>:<n>`) before `read_paramset`. |
| `get_device_schedule` | `address` (device-level) | The device's weekly schedule (week profile) per channel: schedule type (`climate`/`default`), active/available profiles, entry counts, schedule-enabled state. |
| `read_paramset` | `address` (channel, e.g. `ABC:1`), `key` (`MASTER` or `VALUES`) | The parameter→value map. `MASTER` = configuration, `VALUES` = current state. |
| `list_rooms` | `central_name?` | Configured rooms with the device count for each. |
| `list_functions` | `central_name?` | Configured functions (Gewerke) with the device count for each. |
| `list_programs` | `central_name?` | CCU automation programs (id, name, last-execution state). The `id` is what `trigger_program` takes; internal `Tmp_*` programs are omitted. |
| `list_sysvars` | `central_name?` | CCU system variables (name, type, current value, unit). Internal sysvars omitted. |
| `list_service_messages` | `central_name?` | Active service messages (low battery, sabotage, comms errors) — device-maintenance conditions `get_health` does not report. |
| `list_alarm_messages` | `central_name?` | Active alarm messages (the alarm set, distinct from service messages). |
| `list_inbox` | `central_name?` | Devices in the inbox — newly detected, not yet accepted into the configuration. |
| `list_audit` | `limit?` (default 50, max 1000) | Recent config change-log, newest first (who changed what, when). |
| `list_incidents` | `central_name?`, `limit?` (default 50, max 1000) | Recent reliability incident journal (circuit-breaker trips, ping/pong mismatches, retry exhaustion), newest first. Registered only when the daemon's `Incidents` dependency is wired. |
| `get_health` | — | Overall daemon status + per-component status (CCU connectivity, subsystems). |
| `get_system_info` | `central_name?` | Daemon version, plus per-central program/sysvar counts and CCU firmware-update state. |
| `list_alarm_zones` | — | Every alarm zone with its arm state, active countdown and the number of latched motion detectors. The `id` is what `arm_alarm_zone` / `disarm_alarm_zone` take. Registered only when the alarm domain is wired. |
| `list_triggered_motion` | `zone_id?` | The motion detectors that are currently latched and can be cleared. Omit `zone_id` for every zone. Registered only when the alarm domain is wired. |
| `get_security_status` | — | Overall Security & Safety state: the folded severity, the hazard classes that have known sources, and the active faults. Registered only when the security domain is wired. |
| `get_matter_status` | — | The Matter bridge's runtime state: enabled/listening, paired-controller (fabric) and bridged-endpoint counts, and whether a commissioning window is open. Registered only when the Matter bridge dependency is wired. |
| `list_backups` | `central_name?` | Locally-stored CCU backup archives (id, owning central, size, creation time, download filename). Registered only when the backup store is wired. |
| `get_addon_update_status` | — | The CCU add-on self-updater's status: current/available version, whether an update is available, and whether a download or install is currently running. Registered only when the add-on self-updater is wired. |
| `list_groups` | `central_name?` | CCU heating groups (roster and members) per central. Registered only when the groups reader is wired. |
| `list_areas` | `central_name?` | Operator-defined areas (room groupings one level above the CCU's flat room list) with their assigned rooms; `central_name` scopes which rooms show. Registered only when the area store is wired. |
| `list_interfaces` | — | Configured CCU interfaces with connectivity state (connected, duty cycle, carrier sense). Read-only: reconnecting an interface actuates the radio link and is deliberately not exposed, the same argument that keeps `install-mode` off the surface. Registered only when the interface index is wired. |
| `get_measurements` | `central`, `interface_id`, `channel`, `parameter`, `from`, `to` (all required), `buckets?` (default 200, max 2000) | A data point's recorded measurement history, server-bucketed into evenly spaced points over the given window. There is no default window — a caller must name one. Registered only when the history service is wired. |
| `list_hidden_parameters` | `central_name?` | The persisted un-ignore patterns that promote otherwise-hidden parameters into the visible data-point surface. Registered only when the visibility store is wired. |
| `get_energy` | `central` (required), `from`, `to` (required), `group?` (`hour`/`day`/`month`, default `day`), `device?` | Per-device power/energy aggregation over the given window; omit `device` for every energy device on the central. Registered only when the energy service is wired. |
| `list_links` | `central_name?` | Direct device-to-device links across every configured central. Registered only when the links service is wired. |
| `list_schedules` | — | Every device across the fleet that carries a week schedule, with its schedule kind (`week_profile` or `climate`). Registered only when the schedule service is wired. |

The central-spanning read tools (`central_name?`) span every configured
central when `central_name` is omitted, or scope to the named one when
set — the same multi-CCU rule the rest of the surface follows.

### 2.2 Write tools (only when `allow_writes: true`)

| Tool | Arguments | Effect |
| --- | --- | --- |
| `set_datapoint` | `central_name`, `address` (channel), `parameter` (e.g. `STATE`, `LEVEL`), `value` | Writes a value to a device data point. Recorded to the audit log with a `via mcp` note. |
| `write_paramset` | `central_name`, `address` (channel), `key` (`MASTER`/`VALUES`), `values` (map), `edit_token?` | Writes a paramset. Recorded to the audit log. A `MASTER` write requires `edit_token` from `open_edit_session` — see the note below. |
| `open_edit_session` | `address` (channel), `key` (`MASTER`) | Acquires the per-channel edit lock a `MASTER` `write_paramset` call needs. Returns `token` (pass as `edit_token`) and `expires`. Fails when another session already holds the lock. Registered only when the daemon's edit-lock registry is wired. |
| `close_edit_session` | `address` (channel), `key` (`MASTER`), `edit_token` | Releases a lock `open_edit_session` opened. Registered only when the daemon's edit-lock registry is wired. |
| `trigger_program` | `central_name`, `program_id` (CCU ISE object id) | Runs a CCU automation program. Recorded to the audit log. |
| `arm_alarm_zone` | `zone_id`, `mode` (`perimeter`, `full`, `night`, `vacation`, `custom`) | Arms one alarm zone. Fails when a sensor blocks the arm — read `list_alarm_zones` first. |
| `disarm_alarm_zone` | `zone_id` | Disarms one alarm zone. Zones whose policy requires a disarm code cannot be disarmed from here. |
| `reset_motion` | `zone_id?` | Clears the latched motion detectors of one zone, or of every zone when `zone_id` is omitted. |

Notes:

- `set_datapoint` writes at `CommandPriorityHigh` — the same priority
  the REST API uses for user-initiated writes.
- `LINK` paramsets are intentionally **not** exposed (they need a peer
  address and a different tool shape). Only `MASTER` and `VALUES`.
- A `MASTER` `write_paramset` call is a configuration change and is
  gated behind the same per-channel edit lock REST and WebSocket
  clients use: call `open_edit_session` first, pass the returned
  `token` as `write_paramset`'s `edit_token`, and call
  `close_edit_session` when done (or let the lock expire on its own).
  `VALUES` writes are ungated and need no token.
- Channel addresses use the `<device>:<channel>` form (e.g.
  `0001D3C99C1234:4`). Device-level addresses (no `:channel`) are used
  by `get_device`.

---

## 3. Connecting a client

### 3.1 Claude Code (this CLI)

Add the server with the HTTP transport and a bearer token:

```sh
claude mcp add --transport http openccu-loom http://host:8119/mcp \
  --header "Authorization: Bearer $TOKEN"
```

By default the server is registered at `local` scope (this project /
machine only). Add `--scope user` to make it available across all your
sessions, or `--scope project` to share it with collaborators via the
project's `.mcp.json`:

```sh
claude mcp add --scope user --transport http openccu-loom \
  http://host:8119/mcp --header "Authorization: Bearer $TOKEN"
```

> Avoid `--scope project` if the header carries a real token — a
> project-scoped entry is checked in. Prefer `user` scope, or reference
> the token via an environment variable.

Then in a session: *"List my CCUs"* → the agent calls `list_centrals`.

### 3.2 Claude Desktop / stdio-only clients

Claude Desktop currently expects stdio servers, so bridge to the HTTP
endpoint with `mcp-remote`:

```jsonc
// claude_desktop_config.json
{
  "mcpServers": {
    "openccu-loom": {
      "command": "npx",
      "args": [
        "-y", "mcp-remote",
        "http://host:8119/mcp",
        "--header", "Authorization:${AUTH_HEADER}"
      ],
      "env": { "AUTH_HEADER": "Bearer <your-api-token>" }
    }
  }
}
```

> **No spaces inside `args` entries.** Claude Desktop currently mangles
> arguments containing spaces, so the header value must arrive through
> the environment variable (`mcp-remote` expands `${AUTH_HEADER}`
> itself). Writing `"Authorization: Bearer …"` directly into `args`
> breaks the header, mcp-remote then falls back to an OAuth discovery
> flow, and the connection fails with confusing `/register` 404 errors.
> Use an absolute `command` path (e.g. `/opt/homebrew/bin/npx`) if
> Claude Desktop cannot find `npx` on its minimal PATH.

### 3.3 Raw protocol smoke test

Initialize a session against the endpoint to confirm reachability and
auth:

```sh
curl -s http://host:8119/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize",
       "params":{"protocolVersion":"2025-06-18",
                 "capabilities":{},
                 "clientInfo":{"name":"curl","version":"0"}}}'
```

A `401` means the token was missing/invalid; a `200` with a server
`initialize` result means you're through the auth chain.

---

## 4. Read-only vs. write posture (the two opt-ins)

The server is designed to be **safe to enable**:

1. `enabled: true` alone → **read-only**. The agent can inventory
   devices, read paramsets, inspect health, and read the audit log, but
   cannot change anything on the CCU.
2. `allow_writes: true` *in addition* → the write tools of §2.2 are
   registered (each still gated on its own dependency being wired).
   This is a **separate, deliberate decision** — enabling MCP never
   silently grants write access.

   Most of those tools act on devices (`set_datapoint`, `write_paramset`
   plus its `open_edit_session` / `close_edit_session` lock pair,
   `trigger_program`); the rest act on the **alarm system**
   (`arm_alarm_zone`, `disarm_alarm_zone`, `reset_motion`). There is no
   separate opt-in for the alarm tier — the one flag grants both, so an
   agent that can switch a lamp can also disarm a zone. Zones whose
   policy requires a disarm code are still refused, which is the only
   distinction the daemon draws here.

Why this matters for agents: an LLM exploring your home should be able
to *answer questions* without any risk of toggling a real device. You
flip `allow_writes` only when you actively want the agent to act.

Every write is **recorded to the audit log** with a `via mcp` origin
tag, so `list_audit` (or the REST audit surface, or the Config UI)
shows exactly what the agent changed and when.

> **Operational guidance for real CCUs.** A write tool drives the same
> `setValue` / paramset path as the REST API — i.e. it actuates real,
> in-use devices — and the alarm write tools drive the same engine the
> panel and the SPA drive. Treat `allow_writes: true` as you would
> handing an automation script write access to your home *and* to your
> alarm system. Start read-only, scope the API token tightly, and turn
> writes on only against a CCU/devices you're comfortable letting an
> agent move.

---

## 5. Use cases

### 5.1 "What's in my house?" — natural-language inventory (read-only)

> *"How many HmIP switches do I have, and which room names did I give
> them?"*

The agent calls `list_centrals` → `list_devices`, then groups the
returned summaries by `model` / `name`. No config change, no risk —
this works with `allow_writes: false`.

### 5.2 Triage a flaky device (read-only)

> *"My bathroom thermostat dropped off — what's its current state and
> last-seen config?"*

`get_device` to confirm the address and interface → `read_paramset`
with `VALUES` for the live state → `read_paramset` with `MASTER` to see
its configured cycle/temperature parameters → `get_health` to check
whether the whole CCU link is degraded vs. just that device.

### 5.3 Change-log forensics (read-only)

> *"Did anything change the living-room dimmer config in the last day,
> and who did it?"*

`list_audit` with a `limit`, filtered by the agent on `device_address`
/ `parameter`. Useful for "why did this device behave differently
today?" investigations across REST, UI, and MCP-origin changes alike
(MCP writes carry the `via mcp` note).

### 5.4 Health watchdog / status summarizer (read-only)

> *"Give me a one-line health summary of all my CCUs every morning."*

`get_health` → the agent renders `overall` plus any non-OK components.
Pairs naturally with a scheduled agent run; the read-only posture means
you can leave this running unattended.

### 5.5 Voice/chat-driven control (writes on)

> *"Turn off the bookshelf lamp."*

The agent resolves the device with `get_device` / `list_devices`, then
`set_datapoint` with `central_name`, the channel `address`, parameter
`STATE`, value `false`. The owning-central check stops it from writing
to the wrong CCU; the audit log records the action.

### 5.6 Scene / routine kick-off (writes on)

> *"Run my 'Leaving home' routine."*

`trigger_program` with the CCU program's `program_id`. Lets you expose
existing CCU-side automations to an agent without re-implementing them
north-bound.

### 5.7 Guided re-configuration (writes on)

> *"Set the staircase light's on-time to 90 seconds."*

`read_paramset` (`MASTER`) to discover the current values and parameter
names → the agent proposes the change → `open_edit_session` for the
channel to obtain an `edit_token` → `write_paramset` (`MASTER`) with
just the changed keys and that token → `close_edit_session` once done.
Because the write goes through the same validated paramset path as
REST, invalid values are rejected at the boundary, and the change lands
in the audit log.

### 5.8 Cross-CCU operations (multi-CCU)

> *"List every device across all my CCUs, then turn off all switches in
> the 'Garage' central."*

`list_devices` with no `central_name` spans every central; the write
step names `central_name: garage` explicitly. The required-and-checked
`central_name` on writes is what makes "do X on CCU B" safe in a
multi-CCU deployment.

---

## 6. Troubleshooting

| Symptom | Likely cause |
| --- | --- |
| No `/mcp` route (404) | `north.mcp.enabled` is `false`, or the client is pointed at the wrong `path`. |
| `401 Unauthorized` | Missing/invalid `Authorization` header. Check the API token and that you send it on every request. |
| Write tools missing from `tools/list` | `allow_writes` is `false`, or the relevant subsystem (writer/paramsets/hubs) isn't wired. |
| `device X belongs to central "A", not "B"` | The `central_name` you passed doesn't own that device. Fix the name (see `get_device`'s reported central). |
| `key must be MASTER or VALUES` | `read_paramset` / `write_paramset` only accept those two keys; `LINK` is not exposed. |
| `mcp.v1` absent from `GET /info` | Server isn't enabled, or the daemon wasn't restarted after the config change. |

---

## 7. Where to read more

- **Architecture & decisions:** [ADR 0025 — MCP north-bound adapter](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0025-mcp-northbound-adapter.md)
- **Tool definitions (source of truth):** `internal/north/mcp/tools.go`, `tools_hub.go`, `tools_ops.go`, `tools_alarm.go`
- **Wiring / mount point:** `cmd/openccu-loom/daemon_rest_mount.go` (`mountMCP`)
- **Config reference:** [`example.config.full.yaml`](https://github.com/SukramJ/openccu-loom/blob/main/example.config.full.yaml) (`north.mcp` section)
- **Capability tokens:** `internal/north/rest/handlers/info.go`
- **Authentication model:** [Authentication](../admin/auth.md)
