# Keycloak as an OIDC provider

This guide sets up [Keycloak](https://www.keycloak.org/) as the single
sign-on provider for OpenCCU-Loom. It is a concrete, provider-specific
companion to the generic [OIDC section of Authentication &
Users](auth.md#oidc-single-sign-on) — read that first for the config-field
reference.

OpenCCU-Loom drives the **authorization-code flow with PKCE (`S256`)** and
verifies the returned ID token against the provider's JWKS. On success it
issues the same session cookie a Basic login would.

!!! warning "OIDC needs a stable, externally reachable callback URL"
    The `redirect_url` you configure is sent to Keycloak **verbatim** — it is
    **not** derived from the request and **not** Home-Assistant-Ingress-aware.
    Behind the HA add-on's Ingress the proxy path (`/api/hassio_ingress/<token>/…`)
    changes every session, so it can never match a static redirect URI. Use
    OIDC when OpenCCU-Loom is reachable at a **stable URL through a reverse
    proxy** (e.g. `https://loom.example`). The HA add-on's zero-config login is
    a *different* mechanism (Ingress auth passthrough), not OIDC.

## Prerequisites

- A running Keycloak realm (this guide uses a realm named `home`).
- OpenCCU-Loom reachable at a stable HTTPS URL, e.g. `https://loom.example`,
  so its callback `https://loom.example/api/v1/auth/oidc/callback` is
  reachable by the browser.

## 1. Create the Keycloak client

In the target realm, **Clients → Create client**:

| Field | Value |
|---|---|
| Client type | **OpenID Connect** |
| Client ID | `openccu-loom` (this becomes `client_id`) |
| Name | OpenCCU-Loom |

On the next screen (**Capability config**):

- **Client authentication**: `On` for a *confidential* client (Keycloak issues
  a client secret) **or** `Off` for a *public* client (PKCE only). OpenCCU-Loom
  supports both — it sends the client secret only when one is configured.
- **Authentication flow**: enable **Standard flow** (authorization code).
  Leave *Direct access grants* / *Implicit* / *Service accounts* off.

Then open the client's **Settings** and set:

- **Valid redirect URIs**: exactly your callback URL —
  `https://loom.example/api/v1/auth/oidc/callback`. It must match the
  `redirect_url` in the OpenCCU-Loom config **character for character**
  (scheme, host, path).
- **Web origins**: `https://loom.example` (or `+` to derive it from the
  redirect URIs).

On the **Advanced** tab:

- **Proof Key for Code Exchange Code Challenge Method**: `S256`.
  OpenCCU-Loom always sends `S256`; some Keycloak versions reject the request
  if the client is not set to expect it.

If you chose a confidential client, copy the secret from the **Credentials**
tab — it becomes `client_secret` below.

## 2. Map Keycloak roles to OpenCCU-Loom roles (most important step)

OpenCCU-Loom grants one of three roles — `viewer`, `operator`, `admin` — from
the ID-token claim named by `role_claim` (default `role`). The claim may be a
plain **string**, a **string array**, or a **dotted path into a nested object**,
so Keycloak's standard role and group claims work directly. When the claim
carries several names, the **highest** role wins:

| claim value(s) contain | OpenCCU-Loom role |
|---|---|
| `admin` or `administrator` | `admin` |
| `operator` (and no admin) | `operator` |
| anything else / absent | `viewer` |

### Recommended: Keycloak realm roles (`realm_access.roles`)

1. **Realm roles → Create role** for the roles you use (`admin`, `operator`,
   `viewer`) and assign them to users or groups.
2. Put them in the **ID token**: **Clients → openccu-loom → Client scopes →**
   the dedicated scope `openccu-loom-dedicated` **→ Add mapper → By
   configuration → User Realm Role**, with **Token Claim Name**
   `realm_access.roles`, **Add to ID token** On, **Multivalued** On. (Keycloak
   already exposes realm roles at `realm_access.roles`, but by default only on
   the *access* token; OpenCCU-Loom reads the *ID* token, so this mapper adds
   them there.)
3. Set `role_claim: "realm_access.roles"` in the OpenCCU-Loom config (§4).

Groups work the same way: map a group claim (e.g. `groups`) into the ID token,
name the groups `admin` / `operator` / `viewer`, and point `role_claim` at that
claim.

### Alternative: a single `role` string (default)

For a simple per-user setup, leave `role_claim` at its default `role` and emit a
single-valued `role` claim with a **User Attribute** mapper — User Attribute
`loom_role` → **Token Claim Name** `role`, **Claim JSON Type** String, **Add to
ID token** On, **Multivalued** Off — then set each user's `loom_role` attribute
to `admin` / `operator` / `viewer` under **Users → _user_ → Attributes**.

!!! note "Read from the ID token"
    Whichever mapper you choose, **Add to ID token** must be **On** —
    OpenCCU-Loom reads claims from the ID token it verifies, not from the
    userinfo endpoint.

## 3. Scopes

OpenCCU-Loom always requests exactly `openid profile email` (this is fixed and
not configurable). Keycloak's default `profile` and `email` client scopes
already supply `preferred_username`, `name`, and `email`, so no extra scope
setup is needed. The session subject is `preferred_username` when present
(trimmed and lower-cased, so a realm that stores `Frank` and a login typed as
`frank` end up as one principal), otherwise the `sub` claim verbatim — that
one is opaque and case-sensitive. A Keycloak principal stays separate from a
local OpenCCU-Loom account of the same name; see
[Authentication](auth.md#oidc-single-sign-on).

## 4. Configure OpenCCU-Loom

Fill the `north.rest.auth.oidc` block:

```yaml
north:
  rest:
    auth:
      oidc:
        enabled: true
        # Realm issuer URL exactly as Keycloak advertises it. Must be https
        # (plain http is allowed only on localhost). OpenCCU-Loom discovers the
        # endpoints from <issuer>/.well-known/openid-configuration at startup,
        # checks the metadata issuer equals this value, and pins the token iss.
        issuer: "https://keycloak.example.com/realms/home"
        client_id: "openccu-loom"
        # From the Keycloak Credentials tab. Leave empty for a public client.
        client_secret: "…"
        # Must equal the Keycloak Valid Redirect URI character for character.
        redirect_url: "https://loom.example/api/v1/auth/oidc/callback"
        # ID-token claim carrying the role(s) — see step 2. "realm_access.roles"
        # for Keycloak realm roles (recommended), a group claim like "groups",
        # or "role" for a single-valued string claim. Nested dotted paths and
        # string arrays are both supported.
        role_claim: "realm_access.roles"
```

`client_secret` is a masked secret: `GET /api/v1/config` returns it as `***`,
and it can be supplied out-of-band via the environment variable
`OPENCCU_LOOM_OIDC_CLIENT_SECRET`.

## 5. Log in

The SPA login page shows a **"Login with OIDC"** entry once `enabled: true`. It
drives:

```
GET /api/v1/auth/oidc/start  →  Keycloak login  →  GET /api/v1/auth/oidc/callback
```

The callback verifies the ID token (RS256 signature against the discovered
JWKS, `issuer`/`audience`/`expiry`/`nonce`, plus the PKCE code verifier), issues
the `openccu_loom_session` cookie, and redirects to the SPA. See
[OIDC signature verification](../SECURITY.md#oidc-signature-verification) for the
validation details.

!!! note "Logout is local only"
    OpenCCU-Loom's logout revokes only its own session cookie. There is **no**
    RP-initiated logout / `end_session_endpoint` call, so the Keycloak SSO
    session is left intact — a subsequent "Login with OIDC" may sign the user
    straight back in without a Keycloak prompt. To end the Keycloak session,
    log out in Keycloak (or the account console) as well.

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| Keycloak shows *"Invalid parameter: redirect_uri"* | The **Valid redirect URI** does not match `redirect_url` exactly. Compare scheme, host, and the full `/api/v1/auth/oidc/callback` path. |
| Login succeeds but the user is always **viewer** | The claim named by `role_claim` is missing from the **ID token**, or the role name does not match. Confirm the mapper has **Add to ID token** On, that `role_claim` points at the claim you actually emit (`realm_access.roles` / `groups` / `role`), and that the value is `admin` / `operator` / `viewer`. |
| OIDC does not start / a discovery warning is logged | The issuer or a discovered endpoint is plain `http` on a non-loopback host. OpenCCU-Loom requires https (loopback excepted) so the flow never runs in cleartext — use an https issuer. |
| Login refused after the Keycloak redirect | `issuer` mismatch — the configured `issuer` must equal the realm issuer exactly (no extra/missing trailing slash) and the discovery document's own `issuer`. Compare against `https://keycloak.example.com/realms/home/.well-known/openid-configuration`'s `issuer` field. |
| Works locally, fails through HA | The static `redirect_url` cannot match the ephemeral Ingress path. Expose OpenCCU-Loom via a reverse proxy at a stable URL and register that callback. |
