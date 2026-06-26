# Authentication & Users

Enable and operate the four authentication schemes OpenCCU-Loom ships
with — Basic, API tokens, browser sessions, and OIDC — plus how roles,
the first-admin bootstrap, and CSRF behave.

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

1. Browse to `:8080` and open `/setup`.
2. Submit a username, password, and password confirmation.
3. The account is created with the **`admin`** role and you are
   redirected to `/login`.

!!! warning "Setup runs exactly once"
    `POST /setup` refuses to run as soon as **any** user exists. It
    always creates an `admin`. After the first admin is in place, manage
    further users through the admin API or your configured user store —
    not through `/setup`.

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
session cookie:

```yaml
north:
  rest:
    auth:
      session_enabled: true
```

- Cookie name: `openccu_loom_session`, `HttpOnly`, `SameSite=Lax`.
- Lifetime: 12 hours (server-side TTL; expired sessions are evicted on
  read).
- Logout (`POST /logout` or `POST /api/v1/auth/logout`) revokes the
  session and clears the cookie.
- The `Secure` flag is set when the daemon is told it sits behind TLS —
  pair this with `north.rest.csrf_secure: true` behind an HTTPS proxy.

!!! note "Sessions are in-memory"
    The default session store does not survive a daemon restart; users
    re-authenticate after a restart.

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
`/api/v1/auth`:

| Method & path | Purpose |
|---|---|
| `GET /api/v1/auth/tokens` | List tokens (ID, fingerprint, subject, role — never the secret). |
| `POST /api/v1/auth/tokens` | Mint a new token. |
| `DELETE /api/v1/auth/tokens/{id}` | Revoke a token by its stable ID. |
| `GET /api/v1/auth/users` | List configured usernames + roles (admin-only). |

Create a token by posting a subject and role:

```bash
curl -u admin:… -X POST https://loom.example/api/v1/auth/tokens \
  -H 'Content-Type: application/json' \
  -d '{"subject":"ci-runner","role":"operator"}'
```

The response carries the raw token **once**:

```json
{
  "id": "9f2b1c0a4d5e6f70",
  "token": "rX3…urlsafe-base64…",
  "fingerprint": "…abc123",
  "subject": "ci-runner",
  "role": "operator"
}
```

!!! warning "Store the token immediately"
    The raw token is returned only at creation. Subsequent list and
    audit views show only the ID and a six-character fingerprint — the
    daemon cannot reissue the secret. The token is 32 random bytes as
    URL-safe base64 (~43 characters). Its ID is the first 16 hex
    characters of its SHA-256, used as the `{id}` path segment for
    revocation.

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
        role_claim: "role"          # defaults to "role" when empty
```

Step by step:

1. Register OpenCCU-Loom as a confidential (or public) client at your
   IdP. Set its redirect URI to the daemon's
   `…/auth/oidc/callback` URL, reachable through your reverse proxy.
2. Fill the `oidc` block above. The daemon discovers the authorization,
   token, and JWKS endpoints from
   `<issuer>/.well-known/openid-configuration` at startup. Default scopes
   are `openid profile email`.
3. The SPA login page offers a "Login with OIDC" entry that drives
   `GET /api/v1/auth/oidc/start` → IdP → `…/auth/oidc/callback`.
4. Role mapping reads the `role_claim` claim: `admin` /
   `administrator` → `admin`, `operator` → `operator`, everything else
   → `viewer`. The session subject is `preferred_username` when present,
   otherwise the `sub` claim.

!!! note "How the ID token is validated"
    The callback verifies the ID token's RS256 signature against the
    provider's JWKS (discovered from `jwks_uri` and cached), and checks
    that the `issuer` matches, the `audience` contains your `client_id`,
    and the token has not expired. PKCE protects the code exchange. A
    provider that advertises no `jwks_uri` cannot be verified, so logins
    against it are refused. See
    [OIDC signature verification](../SECURITY.md#oidc-signature-verification)
    for details.

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
