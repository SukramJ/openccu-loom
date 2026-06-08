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
3. Open the **OpenCCU-Loom** entry (or `http://<ccu>:8080/app/`) and
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

- `:8080` REST API + Config UI (SPA) · `:8081` pre-auth bootstrap
- `:8120` XML-RPC callback · `:8129` BIN-RPC callback
- Persistent state (SQLite DB + filesystem): `/usr/local/addons/openccu-loom/var`

Ports and data dir are set via `OPENCCU_LOOM_*` env vars in
`rc.d/openccu-loom`; edit there to resolve a port clash with another
add-on.

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
