# OpenCCU-Loom — CCU / RaspberryMatic Add-on

This directory holds the CCU / RaspberryMatic add-on packaging for
OpenCCU-Loom. The release pipeline assembles it into
`openccu-loom-ccu-<version>.tar.gz`, attached to each GitHub release.

## Install on the CCU

1. Download `openccu-loom-ccu-<version>.tar.gz` from the
   [releases page](https://github.com/SukramJ/openccu-loom/releases).
2. On the CCU web UI: **Settings → Control panel → Additional software**,
   choose the tarball, and install. CCU3 / RaspberryMatic reboot to
   finalise the add-on registration.
3. Open the **OpenCCU-Loom** entry (or `http://<ccu>:8119/app/`) and
   complete the first-run setup. Everything — centrals, MQTT, auth — is
   configured through the UI; add a central pointing at `127.0.0.1` to
   bridge the local CCU.

Supported platforms: **CCU3** (armv7l) and **RaspberryMatic** in all its
flavours — armv7l (32-bit Pi), aarch64 (64-bit Pi), and x86-64 (OVA /
generic / Proxmox). CCU1 / CCU2 are not supported.

## Layout

```
ccu/
├── update_script                 install/update hook (arch select, menu reg)
├── rc.d/openccu-loom             start/stop service (start-stop-daemon)
├── etc/monit-openccu-loom.cfg    monit process supervision
└── www/
    ├── config.cgi                "Settings" → redirect to the Config UI
    └── update-check.cgi          "Update"   → latest release tag
```

The daemon binaries (`openccu-loom.amd64` / `.arm64` / `.armv7`), a
`VERSION` file, and `assets/openapi.yaml` are added under `addon/` by
`script/build_ccu_addon.sh` at package time; the matching binary is
installed to `/usr/local/addons/openccu-loom/` per `uname -m`.

## Ports & data

- `:8119` REST API + Config UI (SPA) + bootstrap surface (login, `/setup`, `/health`, `/about`)
- `:8120` XML-RPC callback · `:8129` BIN-RPC callback
- Persistent state (SQLite DB + filesystem): `/usr/local/addons/openccu-loom/var`

Ports and data dir are set via `OPENCCU_LOOM_*` env vars in
`rc.d/openccu-loom`; edit there to resolve a port clash with another
add-on.

The daemon resolves the callback host **per central** (the egress
interface toward each CCU), so a co-located CCU gets `127.0.0.1` and an
external CCU gets the LAN IP automatically — no manual callback host
needed. This is what makes the CCU push events back reliably (otherwise:
no events, "central heartbeat degraded").

## Behind a reverse proxy

The add-on's **Settings** page (the "OpenCCU-Loom" entry on *Additional
software*) has an **Open Config UI** button. By default it links the
browser straight at the daemon on `http://<same-host>:8119/app/` — correct
when you reach the CCU directly on the LAN, but unreachable from behind a
reverse proxy (Traefik, nginx, …) that terminates TLS and only routes
`:443`, not `:8119`.

For a proxied deployment, give the daemon its externally-reachable base URL
via **`north.rest.public_url`** (Settings tab in the SPA, or YAML):

```yaml
north:
  rest:
    public_url: "https://loom.example.de"   # no path suffix; /app/ is appended
    csrf_secure: true                        # Secure flag on the CSRF cookie (HTTPS)
```

The daemon writes the resolved URL to `<data_dir>/public_url`, and
`config.cgi` then links the button at `<public_url>/app/` instead of the
direct-host heuristic. `public_url` is **restart-required** — set it, then
use the SPA's **Restart** action (see below). Leave it empty for a
LAN-direct install; the heuristic stays in effect.

Point a router at the daemon on the CCU. The CCU is an external service
(not a container), so use the file provider rather than Docker labels, and
prefer a dedicated host over a path prefix — the daemon serves its surfaces
(`/app`, `/api`, `/ws`) at the root:

```yaml
# Traefik dynamic config (file provider)
http:
  routers:
    loom:
      rule: "Host(`loom.example.de`)"
      service: loom
      tls: {}
  services:
    loom:
      loadBalancer:
        servers:
          - url: "http://<ccu-lan-ip>:8119"
```

The SPA is then reachable at `https://loom.example.de/app/`, and the
add-on button links there.

## Restart from the UI

The add-on runs the daemon under **monit** (active mode) and sets
`OPENCCU_LOOM_SUPERVISOR=1`, so the SPA's **Restart** action is enabled:
it SIGTERMs the daemon, which exits and is brought back up by monit on
its next cycle (a brief gap is expected — it is a restart, not a
hot-reload). Without the supervisor signal the daemon disables restart
to avoid leaving itself offline.

## Build locally

```sh
script/build_ccu_addon.sh            # version from git describe
script/build_ccu_addon.sh 1.2.3      # explicit version
```
