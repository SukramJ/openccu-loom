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

## 2. Emit the `role` claim (most important step)

OpenCCU-Loom maps the operator to one of three roles — `viewer`, `operator`,
`admin` — from a **single-valued, top-level ID-token claim named exactly
`role`**:

| `role` claim value | OpenCCU-Loom role |
|---|---|
| `admin` or `administrator` | `admin` |
| `operator` | `operator` |
| anything else / absent | `viewer` |

!!! danger "Keycloak's built-in role mappers do NOT work here"
    The standard *User Realm Role* / *User Client Role* mappers emit a
    **multivalued array** (`roles` / `realm_access.roles`). OpenCCU-Loom
    **ignores** those — it reads only a single string claim called `role`.
    You must add a mapper that produces exactly that.

The simplest per-user approach is a **User Attribute** mapper backed by a user
attribute:

1. **Clients → openccu-loom → Client scopes →** the dedicated scope
   `openccu-loom-dedicated` **→ Add mapper → By configuration → User Attribute**.
2. Configure it:

    | Mapper field | Value |
    |---|---|
    | Name | `loom-role` |
    | User Attribute | `loom_role` |
    | Token Claim Name | `role` |
    | Claim JSON Type | `String` |
    | Add to ID token | **On** |
    | Add to userinfo | On |
    | Multivalued | **Off** |

3. For each user, set the attribute under **Users → _user_ → Attributes**:
   `loom_role = admin` (or `operator` / `viewer`).

!!! note "Group-based assignment"
    To assign roles by group instead of per user, add the same `loom_role`
    attribute at the **group** level (Groups → _group_ → Attributes) and enable
    *"Aggregate attribute values"* is not required for a single value — group
    membership makes the attribute resolve for the member. Keep the mapper
    single-valued and named `role`.

**Add to ID token** must stay **On**: OpenCCU-Loom reads claims from the ID
token it verifies, not from the userinfo endpoint.

## 3. Scopes

OpenCCU-Loom always requests exactly `openid profile email` (this is fixed and
not configurable). Keycloak's default `profile` and `email` client scopes
already supply `preferred_username`, `name`, and `email`, so no extra scope
setup is needed. The session subject is `preferred_username` when present,
otherwise the `sub` claim.

## 4. Configure OpenCCU-Loom

Fill the `north.rest.auth.oidc` block:

```yaml
north:
  rest:
    auth:
      oidc:
        enabled: true
        # Realm issuer URL exactly as Keycloak advertises it. OpenCCU-Loom
        # discovers the endpoints from <issuer>/.well-known/openid-configuration
        # at startup and checks the token's `iss` against it.
        issuer: "https://keycloak.example.com/realms/home"
        client_id: "openccu-loom"
        # From the Keycloak Credentials tab. Leave empty for a public client.
        client_secret: "…"
        # Must equal the Keycloak Valid Redirect URI character for character.
        redirect_url: "https://loom.example/api/v1/auth/oidc/callback"
        # Leave at the default "role" — see the note at the end.
        role_claim: "role"
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
| Login succeeds but the user is always **viewer** | The `role` claim is missing from the **ID token**. Verify the mapper (Token Claim Name `role`, *Add to ID token* On, *Multivalued* Off) and that the user's `loom_role` attribute is set. The built-in `roles` array is ignored. |
| Login refused after the Keycloak redirect | `issuer` mismatch — it must equal the realm issuer exactly (no extra/missing trailing slash). Compare against `https://keycloak.example.com/realms/home/.well-known/openid-configuration`'s `issuer` field. |
| Works locally, fails through HA | The static `redirect_url` cannot match the ephemeral Ingress path. Expose OpenCCU-Loom via a reverse proxy at a stable URL and register that callback. |

!!! warning "`role_claim` is currently inert"
    The `role_claim` config field is stored but **not yet consumed** by the
    daemon — the role is always read from the top-level `role` claim regardless
    of this value. Keep it at `role` and name your Keycloak claim `role`. (The
    generic [auth doc](auth.md#oidc-single-sign-on) and
    `example.config.full.yaml` mention `role_claim` mapping; treat that as
    aspirational until the field is wired up.)
