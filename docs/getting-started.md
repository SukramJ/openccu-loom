# Getting Started

This page gets a single CCU bridged and the daemon reachable. For the
full operator walkthrough see the [User Guide](user-guide.md).

## Prerequisites

- A reachable Homematic / HomematicIP CCU (CCU2, CCU3, or
  RaspberryMatic) on your network.
- Docker, **or** a Go 1.26+ toolchain to build the binary.

## Install and run

=== "Docker"

    ```sh
    docker run -d \
      -p 8080:8080 -p 8081:8081 -p 8120:8120 -p 8129:8129 \
      -v $(pwd)/config.yaml:/app/config.yaml:ro \
      -v openccu-loom-data:/app/var \
      ghcr.io/sukramj/openccu-loom:latest run --config /app/config.yaml
    ```

=== "Binary"

    ```sh
    make build
    ./bin/openccu-loom run --config config.yaml
    ```

The daemon auto-discovers a config if you omit `--config` (first
existing wins): `$OPENCCU_LOOM_CONFIG`, `./config.yaml`,
`~/.config/openccu-loom/config.yaml`, `/etc/openccu-loom/config.yaml`.

## Ports

| Port | Purpose |
| --- | --- |
| `8080` | REST + WebSocket API (the MCP route mounts here too) |
| `8081` | Config UI (Svelte SPA + HTMX bootstrap) |
| `8120` | XML-RPC push callback server (HmIP-RF, BidCos, …) |
| `8129` | BIN-RPC push callback server (CUxD) |

The two callback ports are how the CCU pushes value changes back to the
daemon; they must be reachable **from** the CCU.

## First-run setup

1. Start the daemon with no user pre-configured.
2. Open `http://localhost:8081/setup` and create the first admin
   account.
3. Sign in at `/login`. OIDC is supported when configured.

From the SPA's **Settings** tab you can add CCUs and configure MQTT,
Matter, REST auth, and more. Settings are persisted to the SQLite
database at `<data_dir>/openccu-loom.db`.

## Configuration model

A small `config.yaml` covers the **bootstrap tier** — values the daemon
needs before its database opens (data dir, bind addresses, log handler,
default UI language). On a fresh install, anything you list there is
seeded into the database on first start; after that the database wins
and the SPA is the place to make changes.

- `config.example.yaml` — the minimal reference config (bootstrap tier).
- `config.example.full.yaml` — an annotated reference of every option.
- `example.env` — every environment variable; prefer env for secrets.

!!! warning "Secrets at rest"
    Secret-classed fields (passwords, OIDC client secret, …) are
    encrypted in the database. The master key comes from
    `OPENCCU_LOOM_SECRET_KEY` (base64, 32 bytes) or an auto-generated
    `<data_dir>/secret.key` (mode `0600`). **Back up `secret.key`
    together with the database** — without it, restored secrets cannot
    be decrypted.

## Next steps

- **[User Guide](user-guide.md)** — the complete install + config
  walkthrough.
- **[MQTT Topics](mqtt-topic-schema.md)** — the topic schema for Home
  Assistant Discovery and the raw plane.
- **[MCP Server](external-clients/mcp.md)** — expose your CCU to LLM
  agents over the Model Context Protocol.
- **[Multi-CCU](user/multi-ccu.md)** — run many CCUs from one daemon.
