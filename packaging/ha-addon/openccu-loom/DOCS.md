# OpenCCU-Loom — Home Assistant Add-on

OpenCCU-Loom is a standalone bridge that connects Homematic CCUs (CCU3,
RaspberryMatic) to MQTT, a REST + WebSocket API, and a built-in Config UI.
It runs as a native HA add-on and is accessible directly from the HA sidebar.

> **Which integration path should you run?** The daemon can feed Home
> Assistant through MQTT Discovery, through *Homematic(IP) Local*'s loom
> backend, or through Matter — and each creates its own HA entities, so
> exactly one of them per device. The scenario guide is
> [`docs/user/home-assistant.md`](https://github.com/SukramJ/openccu-loom/blob/main/docs/user/home-assistant.md).

## Installation

1. In Home Assistant go to **Settings → Add-ons → Add-on Store**, click
   the three-dot menu (top right), and choose **Repositories**.
2. Add `https://github.com/SukramJ/openccu-loom` and confirm.
3. Find **OpenCCU-Loom** in the store, click **Install**, and wait for the
   image to pull.
4. Start the add-on and open the panel via the **OpenCCU-Loom** entry in
   the HA sidebar (or navigate to the **Web UI** link in the add-on page).
5. Complete the first-run setup: create an admin account, then go to
   **Centrals** and add a central pointing at your CCU's IP address.

## Accessing the UI

| Access path | Notes |
|---|---|
| HA sidebar / Ingress | Recommended — authentication is handled by the HA Ingress proxy. Works from any HA frontend (browser, companion app). |
| `http://<ha-host>:8119/app/` | Direct access — bypasses Ingress; useful when Ingress is unavailable or for API clients. Opens the full SPA. Uses `rest_port` (default 8119). |
| `http://<ha-host>:8119/` | Server-rendered diagnostics (`/health`, `/about`) — same port as the SPA. |

## Why `host_network: true`

The daemon resolves the callback host **per central**: it picks the egress
interface toward each configured CCU and advertises that IP to the CCU so the
CCU can push XML-RPC and BIN-RPC events back (ports 8120 and 8129). With
network isolation (bridge mode) the daemon would advertise the container IP,
which is unreachable from the CCU's perspective, breaking event delivery.
`host_network: true` ensures the daemon sees the host's LAN interfaces and
can advertise the correct IP.

## Ports

| Option | Default | Purpose |
|---|---|---|
| `rest_port` | 8119 | REST API + Config UI (SPA) + diagnostics. Also the Ingress port. |
| `xmlrpc_callback_port` | 8120 | XML-RPC callback — the CCU pushes events here. |
| `binrpc_callback_port` | 8129 | BIN-RPC callback — CUxD pushes events here. |

Because the add-on runs with `host_network`, the daemon binds these ports
directly on the Home Assistant host. They are **operator-configurable** via the
options above — change them to avoid collisions with other host services. Make
sure the callback ports are reachable from your CCU's network.

> **Ingress + `rest_port`:** the Ingress panel proxies to the static
> `ingress_port` (8119). If you change `rest_port` away from 8119 the sidebar
> panel stops working — reach the UI directly at `http://<ha-host>:<rest_port>/app/`.
> Keep `rest_port` at 8119 to use the panel.

## Configuration options

| Option | Default | Values | Description |
|---|---|---|---|
| `log_level` | `info` | `debug`, `info`, `warn`, `error` | Daemon log verbosity. |
| `rest_port` | `8119` | `1`–`65535` | Host port for the REST API + Config UI. Must equal `ingress_port` (8119) for the sidebar panel. |
| `xmlrpc_callback_port` | `8120` | `1`–`65535` | Host port the CCU pushes XML-RPC events to. |
| `binrpc_callback_port` | `8129` | `1`–`65535` | Host port CUxD pushes BIN-RPC events to. |

All other daemon settings (centrals, MQTT, auth, Matter) are configured
through the Config UI after the add-on starts.

## Persistent data

Everything written to `/data` inside the container persists across add-on
updates and restarts. This includes:

- The SQLite database (sessions, paramsets, device metadata, audit log).
- Uploaded TLS certificates and key files.
- Any filesystem-side persistent state.

Do **not** delete the `/data` directory unless you intend a full factory reset.

## Restart button

The SPA's **Restart** action sends a `SIGTERM` to the daemon process. Under
s6-overlay the process is immediately relaunched by the supervisor, so the
restart is clean and self-contained — a brief downtime of a few seconds is
expected. The button is enabled because `OPENCCU_LOOM_SUPERVISOR=1` is set
in the container environment; without that signal the daemon disables the
restart option to avoid leaving itself offline.
