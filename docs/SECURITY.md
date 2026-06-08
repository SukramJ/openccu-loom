# Security

How OpenCCU-Loom protects the surfaces it exposes — the daemon's threat
model, authentication options, secret handling, and the limitations you
need a reverse proxy to cover.

!!! info "Who this page is for"
    Operators who run the daemon on a network and need to understand its
    security posture before exposing it beyond `localhost`.

## Threat model

The daemon bridges in-use Homematic devices to MQTT, REST/WebSocket, a
web UI and (optionally) a Matter fabric. The assets worth protecting and
how the code defends them:

| Asset | Threat | Mitigation in the code |
|---|---|---|
| CCU credentials (`centrals[].password`) | Disclosure through a config dump | The `password` field is marked `cfg:"secret"`; secret-classed values are encrypted at rest and never echoed back by the config read endpoints. |
| Config secrets at rest (OIDC client secret, MQTT password, token map, Matter DAC key, …) | Theft of the data directory | AES-256-GCM sealing with an `enc:v1:` prefix; master key from `OPENCCU_LOOM_SECRET_KEY` or an auto-generated `secret.key` (mode `0600`). See [Secrets at rest](#secrets-at-rest). |
| Session cookie | XSS / session theft | `openccu_loom_session` is `HttpOnly` + `SameSite=Lax`; `Secure` is set when the daemon is told it sits behind TLS. 12-hour server-side TTL; revoked on logout. |
| Mutating REST/UI requests | Cross-site request forgery | Double-submit CSRF token (cookie `openccu_loom_csrf` + `X-CSRF-Token` header / `_csrf` form field), enabled by default. See [CSRF](#csrf). |
| API tokens and passwords | Timing side-channel on comparison | `crypto/subtle.ConstantTimeCompare` for both bearer-token and Basic-password matching. |
| Bearer token storage | Token leak from the management API | The raw token is returned **only once at creation**; list/audit views expose just a stable ID and a six-character fingerprint. |
| ID token from the IdP | Forged / tampered token | RS256 signature verified against the IdP's JWKS; `issuer`, `audience`, and `exp` validated; PKCE (`S256`) protects the code exchange. See [OIDC signature verification](#oidc-signature-verification). |
| Transport in the clear | Eavesdropping on the LAN | The daemon serves plain HTTP; terminate TLS at a reverse proxy. See [TLS posture](#tls-posture). |
| Request floods | Resource exhaustion on REST | Optional per-identity rate limiter (off by default) plus request timeouts; production should also rate-limit at the proxy. See [Known limitations](#known-limitations). |

## Authentication flows

The daemon supports four ways to authenticate, configured under
`north.rest.auth`. Each can be enabled independently. Step-by-step setup
lives in the [authentication admin guide](admin/auth.md); this is the
summary.

| Scheme | How a client presents it | Backed by |
|---|---|---|
| Basic | `Authorization: Basic …` (or the HTML login form) | `north.rest.auth.users` map, constant-time password compare |
| Bearer / API token | `Authorization: Bearer <token>` | in-memory token store; token ID = first 16 hex of the token's SHA-256 |
| Session | `openccu_loom_session` cookie issued after a successful login | in-memory session store, 12-hour TTL |
| OIDC | browser redirect to the IdP, authorization-code + PKCE | `north.rest.auth.oidc.*` |

Roles are coarse: `admin` ⊇ `operator` ⊇ `viewer`. Mutating endpoints
gate on `operator`; dangerous endpoints (delete device, install mode,
backups, config edits, user/token management) gate on `admin`.

## Secrets at rest

Config fields tagged `cfg:"secret"` — CCU passwords, the MQTT password,
the OIDC client secret, the bearer-token map, and the Matter
commissioning passcode / DAC key — are encrypted before they are written
to the SQLite store.

- **Algorithm:** AES-256-GCM. Sealed values carry an `enc:v1:` prefix so
  the scheme can be versioned later.
- **Master key resolution (in order):**
    1. `OPENCCU_LOOM_SECRET_KEY` — a base64-encoded 32-byte key in the
       environment. Takes precedence when set.
    2. `<data_dir>/secret.key` — auto-generated on first boot with file
       mode `0600`.
- **Resilient fallback:** if no key can be resolved or created, the
  daemon logs a warning and stores secret values **in plaintext** rather
  than refusing to start. Encryption is a hardening layer, not a boot
  dependency.

!!! warning "A malformed `OPENCCU_LOOM_SECRET_KEY` is fatal"
    A present-but-invalid env key (wrong length or not base64) stops the
    daemon — that is an operator mistake worth surfacing loudly. A
    missing key only downgrades to the plaintext fallback.

!!! danger "The Matter operational private key is stored unencrypted"
    Per-fabric Matter operational credentials (NOC, ICAC, operational
    private key, IPK) are persisted unencrypted so commissioned
    controllers survive a daemon restart. Protecting the data directory
    is the operator's responsibility.

## CSRF

Browser-facing mutating requests are guarded by a double-submit token,
controlled by `north.rest.csrf_enabled` (default **true**).

- The daemon sets a JS-readable cookie `openccu_loom_csrf`. The SPA
  echoes it back in the `X-CSRF-Token` header; server-rendered forms
  submit it as the `_csrf` field.
- Safe methods (`GET`, `HEAD`, `OPTIONS`) pass through untouched.
- **Per-request credential schemes bypass CSRF on purpose:** requests
  authenticated with `Authorization: Basic` or `Authorization: Bearer`
  skip the token check, because browsers never auto-attach those headers
  cross-origin. CSRF defends ambient credentials (the session cookie)
  only. `curl` / CI / automation using a bearer token are therefore
  unaffected.
- `north.rest.csrf_secure` (default **false**) adds the `Secure` flag to
  the CSRF cookie — turn it on when the daemon is behind HTTPS.

## TLS posture

The REST/WebSocket server and the bootstrap UI server **serve plain
HTTP**. The daemon does not terminate TLS itself.

- Put a reverse proxy (Traefik, Caddy, nginx) in front for HTTPS, and
  set `north.rest.csrf_secure: true` so the CSRF cookie carries the
  `Secure` flag.
- South-bound to the CCU, set `centrals[].tls: true` to use HTTPS for
  XML-RPC / JSON-RPC. `centrals[].tls_insecure_skip_verify` disables
  certificate verification — use it only against a self-signed CCU on a
  trusted network.
- North-bound to MQTT, a `tls://` (or `mqtts://`) broker URL enables
  server-side TLS.

## OIDC signature verification

Both OIDC callback paths (the SPA REST callback and the bootstrap-UI
callback) verify the ID token before issuing a session:

- **Signature** — the RS256 signature is checked against the provider's
  JWKS (`internal/auth/oidc/jwks.go`), fetched from the `jwks_uri`
  advertised by the IdP's discovery document and cached with a freshness
  TTL. Only `RS256` is accepted; a token that is unsigned, uses another
  algorithm, or is signed by a key the IdP does not publish is rejected.
- **Claims** — the `issuer` is pinned to the discovered issuer, the
  `audience` must contain the configured `client_id`, and `exp` is
  enforced (with a small clock-skew leeway; `nbf` too when present).
- **Code exchange** — PKCE (`S256`) protects the authorization-code
  exchange.

!!! note "Transport trust still matters"
    Verification relies on reaching the IdP's JWKS over a trustworthy
    connection. Keep the `issuer` on TLS and treat the OIDC
    `redirect_url` as a same-origin value. A provider that does not
    advertise a `jwks_uri` cannot be verified, so logins against it are
    refused.

## Known limitations

- **No built-in REST rate limiting by default.** A per-identity limiter
  exists (`north.rest.rate_limit.enabled`) but ships off. The expected
  production position is a reverse proxy handling rate limiting and WAF
  duties in front of the daemon.
- **mTLS on MQTT is not exposed in YAML.** The MQTT adapter accepts a
  `*tls.Config` programmatically, but there are no config keys for
  client cert/key files yet. Server-side TLS via a `tls://` broker URL
  works.
- **In-memory sessions and tokens.** The default Basic/session/token
  stores live in memory; a restart invalidates active sessions.

## Verify before a release

The security-relevant release gate (lint with `gosec`, `go vet`,
`-race`, license/CGo scans, contract and integration tests) lives in the
developer testing guide rather than here, so the operator-facing posture
and the contributor checklist do not drift apart. See the developer
testing documentation under `docs/developer/testing.md`.
