# Getting Started

This page gets a single CCU bridged and the daemon reachable. For the
full operator walkthrough see the [User Guide](user-guide.md).

## Prerequisites

- A reachable Homematic / HomematicIP CCU (CCU2, CCU3, RaspberryMatic,
  or OpenCCU) on your network.
- Docker, **or** a Go 1.26+ toolchain to build the binary — neither is
  needed for the two add-on installs below.

## Install and run

=== "Docker"

    ```sh
    docker run -d --restart unless-stopped \
      -p 8119:8119 -p 8120:8120 -p 8129:8129 \
      -v $(pwd)/config.yaml:/app/config.yaml:ro \
      -v openccu-loom-data:/app/var \
      ghcr.io/sukramj/openccu-loom:latest run --config /app/config.yaml
    ```

    `--restart unless-stopped` is what makes the Config UI's **Restart**
    action work: the daemon exits and Docker brings the container back.
    Without it, a restart from the UI leaves the daemon down.

=== "Binary"

    ```sh
    make build
    ./bin/openccu-loom run --config config.yaml
    ```

=== "Home Assistant add-on"

    Add `https://github.com/SukramJ/openccu-loom` as a repository under
    **Settings → Add-ons → Add-on Store → ⋮ → Repositories**, then
    install **OpenCCU-Loom**. The Config UI appears as a sidebar panel
    (Ingress) and on `:8119`; state persists in the add-on's `/data`.

    A second add-on, **OpenCCU-Loom Remote**, proxies a daemon running
    elsewhere into the same sidebar. See
    [`packaging/ha-addon/README.md`](https://github.com/SukramJ/openccu-loom/blob/main/packaging/ha-addon/README.md).

=== "CCU / RaspberryMatic add-on"

    Download `openccu-loom-ccu-<version>.tar.gz` from the
    [releases page](https://github.com/SukramJ/openccu-loom/releases)
    and install it on the CCU under **Settings → Control panel →
    Additional software**. Supported platforms are CCU3 (armv7l) and
    RaspberryMatic / OpenCCU (armv7l, aarch64, x86-64); CCU1 and CCU2
    are not supported.

    This install defaults to CCU-delegated login and can update itself
    from the project's GitHub releases. See
    [`packaging/ccu-addon/README.md`](https://github.com/SukramJ/openccu-loom/blob/main/packaging/ccu-addon/README.md).

The daemon auto-discovers a config if you omit `--config` (first
existing wins): `$OPENCCU_LOOM_CONFIG`, `./config.yaml`,
`~/.config/openccu-loom/config.yaml`, `/etc/openccu-loom/config.yaml`.

## Ports

| Port | Purpose |
| --- | --- |
| `8119` | REST + WebSocket API, Config UI (Svelte SPA), MCP route, and a minimal no-JS `/health` + `/about` diagnostic surface |
| `8120` | XML-RPC push callback server (HmIP-RF, BidCos, …) |
| `8129` | BIN-RPC push callback server (CUxD) |
| `5540` | Matter bridge (UDP; **off by default**) |

The two callback ports are how the CCU pushes value changes back to the
daemon; they must be reachable **from** the CCU. The Matter listener
only binds when `north.matter.enabled` is `true`.

## First-run setup

1. Start the daemon with no user pre-configured.
2. Open `http://localhost:8119/` — the SPA redirects to `/setup` while
   no admin user exists. Create the first admin account there.
3. Sign in at `/login`. OIDC is supported when configured.

On the CCU / RaspberryMatic add-on the default is CCU-delegated login:
you sign in with your existing CCU account instead of creating a
separate account. See [Authentication](admin/auth.md).

From the SPA's **Settings** tab you can add CCUs and configure MQTT,
Matter, REST auth, and more. Settings are persisted to the SQLite
database at `<data_dir>/openccu-loom.db`.

## Configuration model

A small `config.yaml` covers the **bootstrap tier** — values the daemon
needs before its database opens (data dir, bind addresses, log handler,
default UI language). On a fresh install, anything you list there is
seeded into the database on first start; after that the database wins
and the SPA is the place to make changes.

- `example.config.yaml` — the minimal reference config (bootstrap tier).
- `example.config.full.yaml` — an annotated reference of every option.
- `example.env` — every environment variable; prefer env for secrets.

!!! warning "Secrets at rest"
    Secret-classed fields (passwords, OIDC client secret, …) are
    encrypted in the database. The master key comes from
    `OPENCCU_LOOM_SECRET_KEY` (base64, 32 bytes) or an auto-generated
    `<data_dir>/secret.key` (mode `0600`). **Back up `secret.key`
    together with the database** — without it, restored secrets cannot
    be decrypted.

## Next steps

- **[Installation & First Steps](user-guide.md)** — the administrator
  install walkthrough (install methods, ports, the bootstrap config tier).
- **[Configuration reference](admin/configuration.md)** — every config
  key, grouped by area.
- **[MQTT Topics](mqtt-topic-schema.md)** — the topic schema for Home
  Assistant Discovery and the raw plane.
- **[Matter](user/matter.md)** — bring your CCU devices into Apple Home,
  Google Home, and Alexa.
- **[Multi-CCU](user/multi-ccu.md)** — run many CCUs from one daemon.
