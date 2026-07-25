# OpenCCU-Loom — Home Assistant Add-on Repository

This directory contains the Home Assistant add-on packaging for OpenCCU-Loom.

## Adding the repository to Home Assistant

1. In Home Assistant open **Settings → Add-ons → Add-on Store**.
2. Click the three-dot menu (top right) and choose **Repositories**.
3. Enter `https://github.com/SukramJ/openccu-loom` and click **Add**.
4. Find **OpenCCU-Loom** in the store and click **Install**.

## Layout

```
ha-addon/
├── README.md                     this file
├── openccu-loom/                 the daemon add-on (runs OpenCCU-Loom on the HA host)
│   ├── config.yaml               HA add-on manifest (name, slug, ports, options, …)
│   ├── build.yaml                HA builder config (base images, build args)
│   ├── Dockerfile                copies daemon + spec from the release image
│   ├── DOCS.md                   shown in the HA add-on UI
│   ├── CHANGELOG.md              per-add-on release notes
│   ├── icon.png                  add-on icon (square brand mark)
│   ├── logo.png                  add-on logo (wordmark)
│   └── rootfs/
│       ├── etc/s6-overlay/s6-rc.d/
│       │   ├── openccu-loom/
│       │   │   ├── type          "longrun"
│       │   │   └── run           s6 run script → exec /usr/bin/run.sh
│       │   └── user/contents.d/
│       │       └── openccu-loom  empty — registers service in the user bundle
│       └── usr/bin/
│           └── run.sh            reads add-on config, sets env, execs daemon
└── openccu-loom-remote/          ingress proxy for REMOTE instances (ADR 0054)
    └── (same layout; the binary is the openccu-loom-remote proxy,
         no host_network, ingress only)
```

## Pre-built images

The add-on image (`ghcr.io/sukramj/openccu-loom-ha-{arch}`) is built and
published automatically by the release pipeline when the `BUILD_HA_ADDON`
flag is set in `.github/workflows/release.yml`. The binary inside the image
is copied directly from the corresponding release image
(`ghcr.io/sukramj/openccu-loom:<version>`), so there is no separate source
build step in the add-on Dockerfile.
