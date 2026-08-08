# ADR 0044 — Single-port onboarding and HA Ingress auth passthrough

- **Status**: Accepted
- **Date**: 2026-06-25
- **Related**:
  [ADR 0041 — persist auth sessions](./0041-persist-auth-sessions.md),
  [ADR 0043 — CCU authentication provider](./0043-ccu-authentication-provider.md),
  [SPECIFICATION.md](https://github.com/SukramJ/openccu-loom/blob/main/SPECIFICATION.md) §4.1

## Context

Up to 0.13.0 the daemon ran **two separate HTTP listeners**:

- `:8080` — REST API and Svelte SPA.
- `:8081` — HTMX bootstrap surface (login form, first-run `/setup` wizard,
  OIDC callback handler, server-rendered `/health` and `/about`).

The split was introduced to keep pre-auth flows (login, setup) reachable even
when the SPA bundle was unavailable. In practice it created two friction
points:

1. **Docker users** and **reverse-proxy operators** had to open and forward
   two ports. The Home Assistant add-on (ADR packaging, 0.2.0) routes exactly
   one port through HA Ingress (`:8080`). With the bootstrap surface on a
   separate port, the first-run `/setup` wizard was unreachable through
   Ingress — operators had to SSH into the host, set up a user via the CLI,
   and only then open the panel.

2. **HA Ingress** does not forward arbitrary ports. The add-on's Ingress panel
   has always pointed at `:8080`. The second listener existed outside that
   path permanently.

Separately, the HA supervised add-on has a well-defined security boundary: the
HA Supervisor gate keeps Ingress restricted to HA admins (`panel_admin: true`
in the add-on's `config.yaml`), but the daemon received those requests
unauthenticated — operators still had to create a local Loom account or
configure OIDC before Ingress was useful. For a supervised deployment where the
HA admin is the natural Loom admin, requiring a second credential is friction
with no security benefit.

## Decision

### 1. Fold the HTMX bootstrap surface onto the REST listener

The HTMX bootstrap routes (`/login`, `/logout`, `/setup`, `/about`,
`/auth/oidc/callback`, `/health`) are served on the **same listener** as
the REST API and the SPA embed (`north.rest.listen`, default `:8080`). The
separate `:8081` listener is removed.

The `north.ui.listen` config key is **deprecated** (silently ignored;
the daemon logs a startup warning when it is present). Operators who set
it in their `config.yaml` see a one-line warning and can remove the key
at their convenience — there is no breakage.

**First-run redirect**: when no admin user exists in the database, a
`GET /` (the SPA entrypoint) returns a `302` redirect to `/setup`. The
setup wizard completes, the redirect target becomes the SPA, and
subsequent visits reach the normal app. This is how the wizard becomes
reachable through a single port — and therefore through HA Ingress.

### 2. Opt-in HA Ingress auth passthrough

A new config block `north.rest.auth.ha_ingress` enables the daemon to accept
a request forwarded by the HA Supervisor as an authenticated admin, without
requiring the operator to present a local credential.

#### Trust chain (three layers, all required)

A request qualifies for Ingress passthrough only when **all** of the
following hold:

1. **`ha_ingress.enabled: true`** — explicit operator opt-in; default `false`.
2. **Supervised build/env** — the daemon was started with
   `OPENCCU_LOOM_SUPERVISOR` set, or was compiled with the supervised add-on
   build stamp. Prevents the feature from activating on a bare Linux install
   that happens to have the config key set.
3. **RemoteAddr ∈ `trusted_proxy_cidr`** — the TCP peer address (never
   `X-Forwarded-For`) must fall within the configured CIDR block. Default is
   `172.30.32.0/23`, the well-known HA Supervisor subnet. The `X-Forwarded-For`
   header is deliberately not consulted — spoofing it is trivial; the TCP peer
   address is not.
4. **`X-Ingress-Path` header present** — the HA Supervisor injects this header
   on every Ingress-proxied request. Its presence is a reliable signal that the
   request passed through the Supervisor's auth gate.

#### Why the trust chain is sufficient

The HA add-on's `config.yaml` carries `panel_admin: true`. The HA Supervisor
enforces this flag: only HA admins can open the add-on's Ingress panel. The
Supervisor does **not** forward unauthenticated requests; it verifies the
caller's HA session before proxying. Because OpenCCU-Loom cannot verify the
HA session itself, it relies on the Supervisor as the outer gate — and trusts
only connections that arrive from the Supervisor's known subnet over the
TCP-peer check, not a forgeable header.

This chain mirrors how other supervised HA add-ons handle admin-level Ingress
access (e.g. the Grafana add-on's `authproxy` mode). It does **not** apply to
direct connections on `:8080` from other hosts, because those do not arrive
from the Supervisor subnet.

#### Credential priority

A request that carries a valid Bearer token, session cookie, or Basic
credentials is authenticated by the normal pipeline **first**. Ingress
passthrough is a **fallback** invoked only when no other credential is present.
This means:
- An operator who has a local Loom session can revoke Ingress access for
  themselves by using their local session instead.
- A misconfigured Ingress header on a direct-port request (wrong subnet) is
  simply ignored.

#### Audit identity

Passthrough sessions are recorded with `subject: "ha-ingress"` and
`scheme: "ingress"` in the audit log, making them distinguishable from normal
local logins.

### 3. Configuration

```yaml
north:
  rest:
    auth:
      # HA Ingress auth passthrough (supervised add-on only; ADR 0044).
      # ha_ingress:
      #   enabled: false          # opt-in; default false
      #   trusted_proxy_cidr: "172.30.32.0/23"  # HA Supervisor subnet
      #   role: admin             # granted role: admin | operator | viewer

  # DEPRECATED since 0.14.0 — the bootstrap surface (login, /setup,
  # OIDC callback, /health, /about) is now served on the REST listener.
  # north.ui.listen is silently ignored; a startup warning is emitted.
  # ui:
  #   listen: ":8081"
```

## Security considerations

- **Default off.** `ha_ingress.enabled` is `false` by default. A new
  installation or an upgrade from 0.13.0 is unaffected unless the operator
  explicitly opts in.
- **Supervised-only guard.** The three-layer check (enabled + supervised env +
  subnet + header) ensures the feature cannot be triggered on a bare Docker or
  binary deployment, even if someone copies an add-on `config.yaml` verbatim.
- **`panel_admin: true` dependency.** The security model depends on the HA
  add-on's `config.yaml` carrying `panel_admin: true` to restrict Ingress to
  HA admins. If an operator removes this flag, any HA user (including
  non-admins) could reach the add-on panel — and, with Ingress passthrough
  enabled, would be granted the configured Loom `role`. This risk must be
  documented in the add-on `config.yaml` and the user guide.
- **No `X-Forwarded-For` trust.** `X-Forwarded-For` is attacker-controlled.
  Only the TCP peer address is checked.
- **Single-port change is a net security improvement.** Removing the separate
  listener eliminates a second bind surface with different auth semantics that
  could be overlooked during firewall configuration.

## Alternatives considered

- **Keep `:8081` as a deprecated alias for `:8080`**: adds maintenance cost
  for no user benefit; rejected.
- **Trust `X-Ingress-Path` alone**: trivial to forge from the LAN; rejected.
- **Trust `X-Forwarded-For` for subnet check**: attacker-controlled; rejected.
- **Read HA session token from Ingress request and validate against HA API**:
  requires an outbound call to the Supervisor API on every request, adds
  latency, and creates a dependency on Supervisor uptime for every page load;
  rejected.

## Consequences

- **Positive**: onboarding works end-to-end through one port and through HA
  Ingress; Docker users open one port; the add-on's "Open" button reaches the
  setup wizard on a fresh install without SSH access.
- **Positive**: operators of the supervised add-on get frictionless Ingress
  access once the opt-in flag is set; no second credential required.
- **Negative / migration cost**: `north.ui.listen` in existing configs emits
  a startup warning. This is deliberate — it is a soft deprecation, not a hard
  error. Operators can remove the key on their own schedule.
- **Testing**: existing HTMX surface tests need their base URL updated from
  `:8081` to `:8080`; the `ha_ingress` path needs unit tests covering each
  layer of the trust chain (disabled, wrong subnet, missing header, creds-win),
  and a contract test pinning that `X-Forwarded-For` is never used for the
  subnet check.

## Update (0.14.3): passthrough default-on in the HA add-on

The original decision shipped `ha_ingress.enabled` as a plain `false` default
(opt-in everywhere). In practice the HA add-on has no local admin and CCU auth
is off there (the HA add-on binary is not stamped `AddonBuild=true`), so every
add-on user hit the first-run `/setup` wizard through Ingress instead of simply
landing in the app — the friction this ADR set out to remove.

`ha_ingress.enabled` is therefore now **tri-state** (`*bool`): unset defaults to
the supervised stamp — **on** in the HA add-on (which pins `panel_admin: true`,
so Ingress is admin-only), **off** in a plain build / Docker image. An explicit
`true`/`false` still overrides. The trust chain is unchanged (supervised + real
`RemoteAddr` in the subnet + `X-Ingress-Path`; a real token/session still wins),
so it remains inert outside genuine Supervisor traffic — including the
CCU/RaspberryMatic add-on, which is supervised but not reached through Ingress.
Net effect: opening the add-on via the HA panel logs the operator straight in as
admin, with no setup or login page, and the first-run redirect never triggers.
