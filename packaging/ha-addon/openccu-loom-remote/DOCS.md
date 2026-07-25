# OpenCCU-Loom Remote — Home Assistant Add-on

This add-on does **not** run OpenCCU-Loom itself. It is a small ingress
proxy that brings the Config UI of one or more **remote** OpenCCU-Loom
instances — running next to the CCU, on a NAS, or at another site behind
a VPN — into the Home Assistant sidebar, protected by the HA login.

If you want to run the daemon **on this Home Assistant host**, install
the main **OpenCCU-Loom** add-on instead; the two add-ons coexist.

## Installation

1. In Home Assistant go to **Settings → Add-ons → Add-on Store**, click
   the three-dot menu (top right), and choose **Repositories**.
2. Add `https://github.com/SukramJ/openccu-loom` and confirm.
3. Find **OpenCCU-Loom Remote** in the store and click **Install**.
4. Open the **Configuration** tab and add your instance(s) — see below.
5. Start the add-on and open the panel via the **OpenCCU-Loom Remote**
   entry in the HA sidebar.

## Configuration

```yaml
log_level: info
instances:
  - name: main
    url: "http://192.168.1.10:8119"
    token: "<api token created on that instance>"
  - name: garden-house
    url: "https://loom.garden.example:8119"
    tls_insecure: true
```

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | Short slug (`A-Z`, `a-z`, `0-9`, `-`, `_`). Becomes the URL path segment and the tile label. |
| `url` | yes | Base URL of the remote instance's REST/UI port (default `8119`). `http://` for LAN, `https://` for TLS. |
| `token` | no | API token minted on the remote instance. When set, HA admins land in the UI **without a second login**. When empty, the remote login page is shown instead. |
| `tls_insecure` | no | Accept a self-signed certificate for this instance's `https` URL. Default `false`. |

With **one** instance the panel opens the remote UI directly. With
**several**, the panel shows an overview with live status tiles
(reachability, health, version) — click a tile to open that instance.

## Creating an API token (recommended)

On the remote instance open its Config UI and mint the token under
**Access control** (or via `POST /api/v1/auth/tokens/v2`; bearer auth
is enabled by default). Create a dedicated token per HA installation:
it is individually revocable and shows up as its own subject in the
remote audit log. The token's role decides what the HA-side user may
do — mint an admin token for full configuration access.

The token is stored in the Supervisor's add-on options like any other
add-on secret (MQTT passwords etc.). If you do not want that, leave
`token` empty and sign in manually through the proxied login page.

## Security notes

- The Ingress panel is restricted to **HA administrators**
  (`panel_admin`), and that gate is what authorizes the token
  injection — the add-on has no other authentication layer of its own.
- For connections across the internet use `https://` or a VPN;
  `tls_insecure` is meant for self-signed certificates on your own LAN.
- MQTT and Matter of the remote instance are **not** proxied — this
  add-on bridges the UI/REST/WebSocket surface only.

## Troubleshooting

- **Tile shows "Unreachable"** — the proxy cannot reach `url` from the
  HA host. Check routing/VPN and that the remote daemon's REST port is
  open.
- **Login page appears although a token is set** — the token was
  rejected by the remote instance (revoked or wrong). Mint a new one.
- **Add-on refuses to start** — at least one instance must be
  configured; the log names the invalid field otherwise.
