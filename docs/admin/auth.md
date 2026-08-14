# Authentication & Users

Enable and operate the authentication schemes OpenCCU-Loom ships
with — Basic, API tokens, browser sessions, OIDC, CCU-delegated login,
and HA Ingress passthrough — plus how roles, the first-admin bootstrap,
and CSRF behave.

!!! info "Who this page is for"
    Administrators wiring up access control for the daemon's REST API and
    web UI. For the higher-level posture and threat model, read the
    [security guide](../SECURITY.md).

## Roles

Authorization is coarse-grained and strictly nested:

| Role | Can do |
|---|---|
| `viewer` | Read-only access to everything its endpoints expose. |
| `operator` | Everything a viewer can, plus mutations: paramset writes, value writes, links, schedules, sysvar writes. |
| `admin` | Everything an operator can, plus dangerous operations: delete device, install mode, backups, config edits, user/token management, diagnostics. |

`admin` covers `operator`, which covers `viewer`. There is no finer
per-endpoint permission model.

!!! note "When role middleware is not wired"
    If a deployment does not wire the operator/admin role gates, every
    authenticated user passes — the daemon falls back to "any logged-in
    user is allowed" so an upgrade never locks anyone out. Wire the role
    gates to actually enforce the table above.

## The first admin (`/setup`)

A fresh daemon with no users defined cannot be administered until you
create the first account. The bootstrap UI exposes a single-shot setup
flow:

1. Browse to `:8119` and open `/setup`.
2. Submit a username, password, and password confirmation.
3. The account is created with the **`admin`** role and you are
   redirected to `/login`.

!!! warning "Setup runs exactly once"
    `POST /setup` refuses to run as soon as **any** user exists. It
    always creates an `admin`. After the first admin is in place, manage
    further users through the admin API or your configured user store —
    not through `/setup`.

### Closing the surface permanently

The onboarding endpoints are unauthenticated by necessity, and they
re-open by themselves whenever the daemon has no authentication source
at all — a wiped data volume or a restored blank database is enough.
Deployments that must never expose anonymous admin creation pin the
surface shut in the bootstrap tier of `config.yaml`:

```yaml
bootstrap:
  allow_first_run_setup: false
```

With that set, `GET /api/v1/setup/status` reports `required: false` and
`POST /api/v1/setup` answers `403` regardless of the users table.

!!! danger "This can lock you out, on purpose"
    If no authentication source is configured (no local user, no YAML
    user, no CCU-delegated login, no OIDC), the flag leaves no way into
    the daemon at all. The only way back is setting it to `true` in the
    config file and restarting. The daemon logs
    `setup.onboarding.dormant` at boot when it is in that state.

## Basic auth

Username/password over HTTP Basic, also backing the HTML login form.
Passwords are compared in constant time.

```yaml
north:
  rest:
    auth:
      basic_enabled: true
      users:
        alice: "s3cr3t-password"      # username -> password
        bob: "another-password"
```

The `users` map is a secret-classed config field, so its values are
encrypted at rest (see the [security guide](../SECURITY.md)). Clients
send `Authorization: Basic <base64(user:pass)>`; the realm advertised on
a `401` is `openccu-loom`.

## Browser sessions

A successful login (form `POST /login`, or the SPA's
`POST /api/v1/auth/login`) issues a server-side session and drops the
session cookie. Session resolution is always installed — it is the
SPA's core login mechanism and has no config gate.

- Cookie name: `openccu_loom_session`, `HttpOnly`, `SameSite=Lax`.
- Lifetime: 12 hours (server-side TTL; expired sessions are evicted on
  read).
- Logout (`POST /logout` or `POST /api/v1/auth/logout`) revokes the
  session and clears the cookie.
- The `Secure` flag is set when the daemon is told it sits behind TLS —
  pair this with `north.rest.csrf_secure: true` behind an HTTPS proxy.

!!! note "Sessions are persisted to SQLite"
    Sessions are saved to the `auth_sessions` table (migration `021`) and
    are rehydrated on boot, so a daemon restart does **not** log users
    out — sessions remain valid until their TTL expires. The in-memory
    store is only a fallback that engages when the database is
    unavailable at startup; in that degraded mode sessions do not
    survive a restart. See [ADR 0041](../adr/0041-persist-auth-sessions.md).

## API tokens (Bearer)

Tokens authenticate non-browser clients (CI, scripts, automation) via
`Authorization: Bearer <token>`. They are compared in constant time and
bypass CSRF (a bearer header is never auto-attached by a browser).

```yaml
north:
  rest:
    auth:
      bearer_enabled: true
```

### Managing tokens over REST

All token-management endpoints are **admin-only** and live under
`/api/v1/auth`. There are two generations of the token-admin surface —
**use v2** for anything new; v1 is deprecated but still served for
existing external API consumers.

| Method & path | Purpose |
|---|---|
| `GET /api/v1/auth/tokens/v2` | List tokens with full fingerprint + `created_at`/`last_seen_at`/`expires_at`. |
| `POST /api/v1/auth/tokens/v2` | Mint a new token; optional `expires_in_days`. |
| `DELETE /api/v1/auth/tokens/v2/{fingerprint}` | Revoke a token by its fingerprint. |
| `GET /api/v1/auth/users` | List configured usernames + roles (admin-only). |

!!! warning "v1 (`/api/v1/auth/tokens`) is deprecated"
    `GET`/`POST /api/v1/auth/tokens` are marked `deprecated: true` in
    the OpenAPI spec. They remain served — `DELETE
    /api/v1/auth/tokens/{id}` still revokes tokens created through the
    v1 path — but the in-tree Svelte SPA (`AccessControl.svelte`)
    talks to v2 exclusively, and v1's elided fingerprint + lack of
    expiry support make it strictly less useful. New integrations
    should use v2.

Create a token by posting a subject and role (optionally with a
lifetime):

```bash
curl -u admin:… -X POST https://loom.example/api/v1/auth/tokens/v2 \
  -H 'Content-Type: application/json' \
  -d '{"subject":"ci-runner","role":"operator","expires_in_days":90}'
```

The response carries the raw token **once**:

```json
{
  "token": "rX3…urlsafe-base64…",
  "fingerprint": "…abc123",
  "expires_at": "2026-10-05T00:00:00Z"
}
```

!!! warning "Store the token immediately"
    The raw token is returned only at creation. Subsequent list and
    audit views show only the fingerprint — the daemon cannot reissue
    the secret. The token is 32 random bytes as URL-safe base64
    (~43 characters). Its fingerprint (sha256-derived) is the `{fingerprint}`
    path segment for revocation. `expires_in_days` is optional; omitted
    or non-positive creates a token that never expires.

Valid roles are `viewer`, `operator`, `admin`; anything else is
rejected with `422`.

## OIDC (single sign-on)

OIDC uses the authorization-code flow with PKCE (`S256`). The browser is
redirected to your identity provider; on return the daemon issues the
same session cookie as a Basic login.

```yaml
north:
  rest:
    auth:
      oidc:
        enabled: true
        issuer: "https://idp.example.com/realms/home"
        client_id: "openccu-loom"
        client_secret: "…"          # optional for public clients
        redirect_url: "https://loom.example/api/v1/auth/oidc/callback"
        role_claim: "realm_access.roles"  # string, array, or nested path; "role" when empty
```

Step by step:

1. Register OpenCCU-Loom as a confidential (or public) client at your
   IdP. Set its redirect URI to the daemon's
   `…/auth/oidc/callback` URL, reachable through your reverse proxy.
2. Fill the `oidc` block above. The daemon discovers the authorization,
   token, and JWKS endpoints from
   `<issuer>/.well-known/openid-configuration` at startup. Default scopes
   are `openid profile email`. The issuer must be **https** (plain http is
   allowed only on localhost), and its discovery `issuer` must match the
   configured value.
3. The SPA login page offers a "Login with OIDC" entry that drives
   `GET /api/v1/auth/oidc/start` → IdP → `…/auth/oidc/callback`.
4. Role mapping reads the claim named by `role_claim` (default `role`),
   which may be a string, a string array, or a dotted path into a nested
   object (e.g. `realm_access.roles`): `admin` / `administrator` → `admin`,
   `operator` → `operator`, everything else → `viewer`. When the claim
   carries several roles the highest one wins. The session subject is
   `preferred_username` when present, otherwise the `sub` claim.

    !!! note
        For a provider-specific walkthrough — client, redirect URI, and
        mapping Keycloak realm roles / groups into the role claim — see
        [Keycloak (OIDC)](keycloak-oidc.md).

!!! note "How the ID token is validated"
    The callback verifies the ID token's RS256 signature against the
    provider's JWKS (discovered from `jwks_uri` and cached), and checks
    that the `issuer` matches, the `audience` contains your `client_id`,
    and the token has not expired. PKCE protects the code exchange. A
    provider that advertises no `jwks_uri` cannot be verified, so logins
    against it are refused. See
    [OIDC signature verification](../SECURITY.md#oidc-signature-verification)
    for details.

## CCU-delegated login

Instead of maintaining a separate Loom account, operators can sign in
with their existing CCU username/password; the daemon validates the
credentials against the CCU's own user database and derives the Loom
role from the CCU's `UserLevel`. See
[ADR 0043](../adr/0043-ccu-authentication-provider.md) for the full
design.

```yaml
north:
  rest:
    auth:
      ccu:
        enabled: ~                # tri-state; unset = build-stamp default
        primary: ~                # tri-state; unset = true (CCU tried first)
        central: ""                # central to authenticate against (empty = first configured)
        min_user_level: 1          # reject CCU UserLevel below this (0 = UPL_NONE always denied)
        role_mapping:               # override the default UserLevel -> role map
          "8": admin
          "2": operator
          "1": viewer
```

Mechanics:

1. The daemon opens a short-lived, dedicated CCU session with the
   submitted credentials (`Session.login`) and logs it out immediately
   — a validation login never reuses the daemon's own service session.
2. On success it reads the user's `UserLevel()` via a privileged ReGa
   script and maps it to a Loom role. Default mapping:

   | CCU `UserLevel()` | Loom role |
   |---|---|
   | `8` (`UPL_ADMIN`) | `admin` |
   | `2` (`UPL_USER`) | `operator` |
   | `1` (`UPL_GUEST`) | `viewer` |
   | `0` (`UPL_NONE`) or unknown | denied |

3. A successful check issues a normal Loom session (the same
   persisted-session mechanism as any other scheme) — the CCU is not
   consulted again for the lifetime of that session.

**Break-glass fallback.** The auth chain always keeps a local
break-glass path: when `primary` is true (the default once CCU login
is enabled), the CCU store is tried first but any failure — wrong
credentials *or* the CCU being unreachable — is mapped to
"unauthenticated", so the chain falls through to local SQLite users
and, ultimately, the in-memory store. A CCU outage therefore blocks
only CCU-backed logins, never local admins. Set `primary: false` to
flip the order (local users tried first, CCU last).

**Enabled by default in the CCU add-on.** `enabled` is tri-state: left
unset, it resolves to the build's add-on stamp — **on** in the CCU
add-on, **off** in a plain binary or Docker build. An explicit
`true`/`false` always overrides the build default.

**Requires a configured central.** The scheme authenticates against a
CCU's user database, so it can only sign anyone in once at least one
central exists. Until then it does not count as an available
authentication source and the [first-run setup](../user-guide.md#first-run-setup)
wizard stays reachable — otherwise a fresh add-on install would be
locked out: no wizard, and every CCU login rejected for want of a
central to ask.

`central: ""` picks the first configured central, resolved the same way
the rest of the daemon resolves centrals: the SQLite `centrals` table
wins whenever it holds any row (this is what makes a central adopted at
runtime authenticatable without a restart); an empty table means the
`centrals:` block of `config.yaml` is authoritative. A named central
that is unknown or disabled in the authoritative tier fails closed.

## HA Ingress passthrough

Supervised Home Assistant add-on deployments can let the HA Supervisor
vouch for a request instead of requiring a second local credential.
See [ADR 0044](../adr/0044-single-port-onboarding-and-ha-ingress-auth.md)
for the full trust-chain rationale.

```yaml
north:
  rest:
    auth:
      ha_ingress:
        enabled: ~                        # tri-state; unset = supervised build-stamp default
        trusted_proxy_cidr: "172.30.32.0/23"  # HA Supervisor subnet
        role: admin                        # role granted to a trusted Ingress request
```

A request qualifies for passthrough only when **all** of the following
hold: the daemon is a supervised build (`OPENCCU_LOOM_SUPERVISOR` /
the add-on build stamp), the TCP peer address (never
`X-Forwarded-For`, which is trivially spoofable) falls inside
`trusted_proxy_cidr`, and the HA Supervisor's `X-Ingress-Path` header
is present. A genuine Bearer token, session cookie, or Basic
credential on the same request always wins — passthrough is
evaluated only as a fallback when no other credential is supplied.

This is an admin-level bypass gated on the add-on's `config.yaml`
carrying `panel_admin: true` (only HA admins can open the Ingress
panel). `enabled` defaults to the supervised build stamp — **on** in
the HA add-on, **off** everywhere else — and an explicit `true`/`false`
overrides it. Passthrough sessions are recorded in the audit log with
`subject: "ha-ingress"` and `scheme: "ingress"` so they are
distinguishable from local logins.

## CSRF for browser vs API clients

`north.rest.csrf_enabled` (default **true**) installs a double-submit
guard on mutating requests:

- **Browser/SPA (session cookie):** the daemon sets a JS-readable
  `openccu_loom_csrf` cookie; the client must echo it back in the
  `X-CSRF-Token` header (or `_csrf` form field) on every `POST`/`PUT`/
  `PATCH`/`DELETE`. A missing or mismatched token returns `403`.
- **API clients (Basic / Bearer):** exempt. These schemes carry a
  per-request `Authorization` header that browsers never auto-include
  cross-origin, so no CSRF token is needed. `curl` and automation work
  unchanged.
- **Safe methods** (`GET`/`HEAD`/`OPTIONS`): always pass through.

Set `north.rest.csrf_secure: true` when serving over HTTPS so the CSRF
cookie carries the `Secure` flag.

## Related

- [Security guide](../SECURITY.md) — threat model, secrets at rest, TLS.
- Configuration reference — the full `north.rest.auth` schema lives in
  the admin configuration guide (`docs/admin/configuration.md`).
- [ADR 0041 — persist auth sessions](../adr/0041-persist-auth-sessions.md).
- [ADR 0043 — CCU authentication provider](../adr/0043-ccu-authentication-provider.md).
- [ADR 0044 — single-port onboarding and HA Ingress auth](../adr/0044-single-port-onboarding-and-ha-ingress-auth.md).
