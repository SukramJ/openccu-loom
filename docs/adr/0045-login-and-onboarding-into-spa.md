# ADR 0045 — Login and first-run onboarding move into the SPA

- **Status**: Accepted
- **Date**: 2026-06-29
- **Related**:
  [ADR 0044 — single-port onboarding and HA Ingress auth](./0044-single-port-onboarding-and-ha-ingress-auth.md),
  [ADR 0043 — CCU authentication provider](./0043-ccu-authentication-provider.md),
  [ADR 0041 — persist auth sessions](./0041-persist-auth-sessions.md)

## Context

Since ADR 0044 the daemon serves everything through one listener (`:8080`),
but it still carried **two parallel onboarding surfaces**:

- a server-rendered, no-JS surface in `internal/north/ui` — a form login
  (`/login`), logout, an OIDC PKCE start/callback (`/login/oidc/*`), and a
  four-step first-run wizard (`/setup`, `/setup/{admin,locale,ccu,mqtt}`)
  backed by a server-side session store; and
- the Svelte SPA at `/app/`, which already implemented its own login view
  (`POST /api/v1/auth/login`), logout, OIDC initiation
  (`/api/v1/auth/oidc/start`), and session probe (`/api/v1/auth/me`).

Login and OIDC were therefore **fully duplicated**: the SPA is the surface
operators actually reach (root `/` redirects to `/app/`), so the
server-rendered `/login` was dead weight kept only by inertia. The one
genuinely server-bound concern — diagnosing a broken SPA — needs nothing more
than a no-JS `/health` and `/about`. The OIDC callback must land on a server
endpoint, but that already exists as the REST endpoint
`/api/v1/auth/oidc/callback`; it needs no HTML template surface.

First-run setup was the only interactive flow still living server-side. Keeping
it there meant maintaining a second set of forms, a second i18n surface, a
session store, and a CSRF path that the SPA's design system never touched.

## Decision

Collapse onboarding onto a single surface — the SPA — and keep only a minimal
no-JS diagnostic remnant server-side.

1. **Remove** the server-rendered `/login`, `/logout`, `/login/oidc/*`, and the
   `/setup*` wizard, plus their templates and the wizard session store.
   `internal/north/ui` shrinks to `/`, `/health`, `/about`, and `/ui/assets`.

2. **Add** an atomic first-run REST API:
   - `GET /api/v1/setup/status` → `{ "required": bool }`, probed by the SPA on
     boot to choose between the wizard and the login screen.
   - `POST /api/v1/setup` — persists the admin user, locale preference, optional
     CCU, and optional MQTT broker in one request. Unauthenticated by necessity
     (no admin exists yet) but **hard-gated**: it returns 409 once any
     authentication source exists, so a second admin can never be registered
     this way. This is the same single-shot guarantee the old wizard enforced.

3. **The SPA owns the wizard.** A four-step `Setup.svelte` (admin → locale →
   ccu → mqtt) keeps its state client-side and finalizes with one POST, then
   sends the operator to the login screen to sign in with the new account.

4. **Preserve the login brute-force speed-bump.** The old wizard's per-IP login
   limiter moves to a REST middleware (`LoginRateLimiter`) wrapping
   `POST /api/v1/auth/login`. The per-identity REST rate limiter does not cover
   this: it keys on a resolved identity (absent before login) and pools all
   anonymous traffic into one bucket, so it is a global throttle, not
   per-source brute-force protection.

## Consequences

- **Positive**: one login truth (the SPA), no duplicated OIDC/login code, a
  smaller server-rendered surface, and a setup flow that reuses the SPA design
  system (toasts, shared primitives, i18n, dark mode).
- **Trade-off**: first-run setup now requires the SPA bundle to load. This is
  acceptable — the bundle is embedded in the release binary via `go:embed`, so
  it is always present; the no-JS `/health`/`/about` remain as the SPA-down
  diagnostic anchor.
- The OIDC callback stays a server endpoint (it must), but only as a REST
  endpoint with no HTML template.
- `APIVersion` bumps to 2.7.0 (capability addition: the setup endpoints).
