# ADR 0054 — Remote ingress proxy add-on (OpenCCU-Loom Remote)

Date: 2026-07-19
Status: accepted
Related:
[ADR 0044 — single-port onboarding and HA Ingress auth passthrough](./0044-single-port-onboarding-and-ha-ingress-auth.md),
[ADR 0051 — north-bound authorization model](./0051-northbound-authorization-model.md)

## Context

The existing Home Assistant add-on runs the daemon **on the HA host**
and surfaces its SPA through HA Ingress. Operators increasingly run
OpenCCU-Loom **elsewhere** — next to the CCU, in a server rack, at a
second site behind a VPN — and still want the Config UI inside the HA
sidebar, gated by the HA login.

The obvious shortcut — pointing HA Ingress at a remote host — does not
exist: Ingress only proxies into an add-on container. And the ADR 0044
auth passthrough cannot be reused across a network boundary by design:
its trust chain requires the supervised environment, the Supervisor's
subnet (`172.30.32.0/23`) as TCP peer, and the `X-Ingress-Path` header.
A remote daemon sees none of these; trusting any of them from a
non-Supervisor peer would be a spoofable header check.

Multi-instance is a first-class requirement (mirroring the daemon's
multi-CCU stance, ADR 0002): one household may run several loom
daemons, and HA cannot install the same add-on twice.

## Decision

Ship a second add-on, **OpenCCU-Loom Remote** (slug
`openccu-loom-remote`), in the same add-on repository. It contains a
small, self-contained Go reverse proxy (`cmd/openccu-loom-remote`,
`internal/remoteproxy`) and **no daemon**.

1. **Instances are a list.** Add-on options carry
   `instances: [{name, url, token, tls_insecure}]`. With exactly one
   instance the proxy is fully transparent at `/`. With more than one,
   `/` serves an overview page with per-instance status tiles and each
   instance is mounted under the path prefix `/i/<name>/`. The SPA
   tolerates this because its build uses a relative asset base and it
   already runs behind the dynamic Ingress prefix today.
2. **Auth is token injection with login fallback.** When an instance
   has a `token`, the proxy injects `Authorization: Bearer <token>`
   into every upstream request that carries no credential of its own —
   HA admins (the Ingress panel keeps `panel_admin: true`) land in the
   UI without a second login. This extends the ADR 0044 philosophy
   across the network boundary: the HA admin gate stays the outer
   auth layer, and the explicit per-instance secret replaces the
   Supervisor-subnet trust that cannot exist remotely. Without a
   token, requests pass through untouched and the operator signs in on
   the remote login page; the proxy rewrites `Set-Cookie` paths onto
   the instance prefix so two instances' sessions cannot collide on
   the shared HA origin.
3. **The proxy stays dumb.** It knows the remote REST surface only as
   opaque paths plus two read-only endpoints for the status tiles
   (`/api/v1/health`, `/api/v1/info`). Authorization is entirely the
   remote daemon's job (ADR 0051): the rights of the injected token
   are decided where the token was minted. The proxy adds no second
   permission model, no config write path, no API translation — remote
   instances may be older or newer than the add-on.
4. **Transport.** Upstream URLs may be `http://` (the LAN default) or
   `https://`; certificates are verified unless the operator sets the
   per-instance `tls_insecure` flag for self-signed setups. WebSocket
   upgrades pass through; `X-Ingress-Path` is forwarded with the
   instance prefix appended so the remote daemon keeps generating
   correct ingress-aware redirects.
5. **Packaging.** The add-on ships from `packaging/ha-addon/
   openccu-loom-remote/`, follows the existing thin-image pattern
   (binary copied out of the release image), needs **no**
   `host_network`, and rides the normal release train — its
   `config.yaml` version is bumped in every release commit alongside
   the main add-on.

## Consequences

- Users who already added the repository see the new add-on in the
  store automatically; one more sidebar entry per HA installation, not
  per loom instance.
- The token lives in the Supervisor's add-on options store, like every
  other add-on secret (MQTT passwords etc.). Operators who reject that
  trade-off simply leave the token empty and use the login fallback.
- A dedicated API token per instance is the recommended setup: it is
  individually revocable and shows up as its own subject in the remote
  audit log.
- MQTT and Matter of the remote instance are out of scope by design —
  the add-on bridges the UI/REST/WS surface only.
