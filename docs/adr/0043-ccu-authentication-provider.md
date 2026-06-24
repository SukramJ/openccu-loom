# ADR 0043 — CCU as an authentication provider (delegate login to the CCU user database)

- **Status**: proposed
- **Date**: 2026-06-24
- **Related**:
  [ADR 0002 — multi-CCU first class](./0002-multi-ccu-first-class.md),
  [ADR 0041 — persist auth sessions](./0041-persist-auth-sessions.md),
  [SPECIFICATION.md](../../SPECIFICATION.md) §auth

## Context

The CCU ships its own user database with username/password credentials and
a coarse permission level (`UserLevel()`: `UPL_ADMIN=8`, `UPL_USER=2`,
`UPL_GUEST=1`, `UPL_NONE=0`). OpenCCU-Loom currently maintains a **separate**
user list: local SQLite users (seeded from `north.rest.auth.users`) plus an
in-memory fallback, with optional OIDC for SSO.

When OpenCCU-Loom runs **as an add-on on the CCU** (or anywhere with a
reachable CCU), operators must create and maintain a *second* set of
accounts that duplicates the ones they already manage on the CCU. That is
friction and a drift risk: a user offboarded on the CCU still has a working
Loom login, and vice versa.

We want an **optional** authentication provider that validates a login
against the **CCU's own** user database and derives the Loom role from the
CCU permission level — so operators can sign in with their existing CCU
accounts, exactly as they do in the CCU WebUI.

This is a *concept* ADR: it fixes the design so the implementation can be
reviewed against it. It deliberately reuses the existing pluggable auth
pipeline rather than inventing a parallel one.

### What the existing pipeline gives us

- **`auth.UserStore`** (`internal/auth/auth.go:55`) is the single seam:
  `AuthenticateBasic(ctx, username, password) (Identity, error)`. The
  SQLite and in-memory stores both implement it.
- **`auth.ChainedUserStore`** (`internal/auth/chain.go:16`) tries `Primary`
  then `Secondary`, but **only falls through on `ErrUnauthenticated`** — any
  other error (e.g. a network failure) short-circuits the whole chain to a
  401. This ordering rule is load-bearing for the design below.
- **Login issues a session once** (`handlers/auth.go:258` →
  `Sessions.Issue`, 12 h TTL, persisted via ADR 0041). After that, every
  request is resolved from the session cookie — the auth backend is **not**
  consulted again. So a CCU-backed login touches the CCU exactly **once**.
- **Roles** are `viewer < operator < admin` (`auth.go:33`); a store sets
  `Identity.Role` when it authenticates.

### What the CCU gives us

- **Credential check**: `Session.login(username, password)` against
  `/api/homematic.cgi` returns a non-empty session string on success, and
  the JSON-RPC error `501 "invalid credentials or too many sessions"` on
  failure. The daemon's JSON-RPC client already encodes this
  (`jsonrpc/client.go:303` → `ErrAuthFailure` on empty result).
- **Permission level**: there is **no** JSON-RPC method that returns a
  user's level. It must be read with a ReGa script:
  `dom.GetObject(ID_USERS).Get("<user>").UserLevel()` → `8|2|1|0`.
- **Session pressure**: the CCU caps concurrent sessions ("too many
  sessions" shares the 501 error). Validation logins must be released
  (`Session.logout`) immediately.

## Decision

Add an **optional CCU authentication provider** as a new `auth.UserStore`
that plugs into the existing chain. It does two CCU round-trips on login,
nothing afterwards.

### 1. Credential validation — a *transient* login

The provider opens a **short-lived, dedicated** JSON-RPC session with the
end-user's credentials and immediately logs it out:

```
Session.login(user, pass)  → non-empty sid ⇒ valid ; 501 ⇒ invalid
Session.logout(sid)        → release the CCU session slot
```

It **must not** reuse the daemon's service-account `jsonrpc.Client`: that
client holds the daemon's own session and is renewed/used for all CCU I/O.
Logging in as the end-user on it would clobber the service session. The
provider therefore uses a separate transient connection (or a dedicated
"validate credentials" call that opens and closes its own session).

### 2. Role derivation — via the privileged service session

After a valid credential check, the provider reads the user's level with a
new ReGa script `get_user_level.fn`, run through the daemon's **existing
privileged runner** (the service account is admin and may read `ID_USERS`):

```
!# Params: ##username##   Output: the user's UserLevel as an integer (or -1)
object oUser = dom.GetObject(ID_USERS).Get("##username##");
if (oUser) { Write(oUser.UserLevel()); } else { Write(-1); }
```

It maps the level to a Loom role (configurable; defaults below):

| CCU `UserLevel()` | Constant    | Loom role  | Rationale |
|---|---|---|---|
| 8 | `UPL_ADMIN` | **admin**    | Full WebUI admin (`session_requestisvalid 8`) |
| 2 | `UPL_USER`  | **operator** | Normal actions (`session_requestisvalid 0`) |
| 1 | `UPL_GUEST` | **viewer**   | Read-only |
| 0 / -1 | `UPL_NONE` / unknown | **deny** | No access; fail closed |

### 3. Chain placement — local first, CCU last

The chain is ordered **local stores first, CCU last**:

```
ChainedUserStore{
  Primary:   sqUsers,                                   // local SQLite
  Secondary: ChainedUserStore{Primary: ccuStore, Secondary: memUsers},
}
```

Two reasons, both following directly from the chain semantics:

1. **Break-glass safety.** A local admin must keep working when the CCU is
   down or auth-misconfigured. Trying local users first guarantees that.
2. **No accidental short-circuit.** Because the chain stops on any
   non-`ErrUnauthenticated` error, the CCU store **maps every transient
   failure** (CCU unreachable, timeout, 503) **to `ErrUnauthenticated`**,
   never a raw error. Effect: a CCU outage makes CCU-only logins fail
   ("invalid credentials, try again") but never blocks the chain or local
   users. The store logs the real cause at WARN for diagnosis.

A username that exists both locally and on the CCU resolves to the **local**
account (tried first) — an intentional override path.

### 4. Issue our own session

On success the provider returns
`Identity{Subject: user, Scheme: SchemeBasic, Role: mapped}`. The login
handler issues a normal Loom session (ADR 0041). The CCU is **not** touched
again for the session's lifetime — no per-request CCU dependency, no extra
CCU session held open.

### 5. Configuration

A new `CCUAuthConfig` block under `AuthConfig`, modelled on `OIDCConfig`.
It carries **no secret** — the credentials come from the login form:

```yaml
north:
  rest:
    auth:
      ccu:
        enabled: false            # opt-in
        central: ""               # CentralConfig name to authenticate against
                                  # (empty ⇒ the first configured central)
        min_user_level: 1         # reject users below this UPL (0 = UPL_NONE always denied)
        role_mapping:             # CCU UserLevel → Loom role (override defaults)
          "8": admin
          "2": operator
          "1": viewer
```

There is **no reliable "running on the CCU" indicator** in the daemon
(`detectSupervisedRestart` only detects *a* supervisor, not *which host*),
so the provider is **config-driven**: the operator opts in and names the
central. The add-on case is just the most common deployment, not a
detected mode.

### 6. Capability + UI

Expose a capability token `auth.ccu.v1` via `GET /info` (alongside the
existing `auth.oidc` / supervised-restart tokens). The SPA login form needs
**no new field** — the credential shape is identical — but may show a hint
("sign in with your CCU account") when the capability is present.

### Architecture seam

The `*jsonrpc.Client` is not stored on `central.Unit` today; it lives in the
hub-wiring closures. Rather than leak the client, add one narrow method on
the central/unit layer and depend on it from the auth layer through a small
port, keeping `internal/auth` decoupled from `internal/central`:

```go
// satisfied by an adapter over central.Registry
type CCUAuthenticator interface {
    ValidateCredentials(ctx context.Context, central, user, pass string) error // transient login
    UserLevel(ctx context.Context, central, user string) (int, error)          // privileged ReGa read
}
```

`internal/auth/ccuauth.Store` implements `auth.UserStore` on top of this
port; the production adapter resolves the central by name (the same
`registry.List()` pattern `RoomFunctionAdminDomain` uses) and runs the
transient login + the `get_user_level.fn` script.

## Security considerations

- **ReGa injection.** `##username##` is untrusted input substituted into a
  ReGa script. The provider **must sanitise** the username before the
  `UserLevel` lookup — restrict to the CCU's legal username charset
  (alphanumerics + a small allow-list) and reject anything else as
  `ErrUnauthenticated`. (The credential check via `Session.login` carries
  the username as a JSON parameter, not as script text, so it is not an
  injection vector; the ReGa read is.)
- **CCU session exhaustion.** Every validation login consumes a CCU session
  slot until logout. The provider logs out immediately and **bounds the
  concurrency** of in-flight validations (small semaphore) so a login storm
  cannot exhaust CCU sessions or trip the shared "too many sessions" 501.
- **Brute force.** Failed CCU logins should be rate-limited on the Loom side
  (reuse the existing REST rate limiter for `/auth/login`) so the daemon
  does not turn into a brute-force amplifier against the CCU, which has its
  own lockout behaviour.
- **Transport.** The user password is forwarded to the CCU over the
  central's configured transport. It inherits that central's TLS settings;
  document that CCU auth over plain HTTP exposes credentials on the wire.
- **No password storage.** The daemon never persists CCU passwords — it
  validates, derives a role, issues its own session, and forgets the
  password.
- **Fail closed.** Unknown user, `UPL_NONE`, or a `UserLevel` read that
  fails after a *successful* credential check ⇒ deny (do not fall back to a
  default role).

## Alternatives considered

- **Reverse-proxy / header trust** (CCU terminates auth, injects a header):
  rejected — couples Loom to a specific proxy topology and trusts an
  unauthenticated header on the LAN.
- **Mirror the CCU user table into SQLite** on a schedule: rejected —
  passwords are not extractable, and it reintroduces the drift we set out to
  remove.
- **Make the CCU store the chain Primary**: rejected for the default — a CCU
  outage would block local break-glass admins. (Could be a future opt-in
  `ccu_primary: true` for deployments that want CCU-only auth, but not the
  default.)
- **A dedicated `Scheme: ccu`** instead of reusing `SchemeBasic`: deferred —
  the wire/credential shape is identical to Basic; a distinct scheme buys
  only nicer audit labelling, which can be added later without a redesign.

## Consequences

- **Positive**: operators reuse existing CCU accounts; offboarding on the
  CCU immediately blocks new Loom logins (existing sessions still run until
  TTL — documented); zero new credential surface; the add-on deployment
  "just works" after one config flag.
- **Negative / cost**: a new ReGa script + enum; a new config block; a small
  central-layer method + auth-layer store + chain rewiring; the username
  sanitisation and concurrency bound must be implemented carefully.
- **Testing**: unit tests for level→role mapping, username sanitisation, and
  the transient-failure→`ErrUnauthenticated` rule; a contract test pinning
  the chain order (local before CCU); integration coverage is limited
  because `godevccu` does not model per-user `Session.login`/`UserLevel` —
  note that gap and cover the wire shape with a fake `CCUAuthenticator`.

## Implementation sketch (for the follow-up PR)

1. `internal/config/config.go`: `CCUAuthConfig` in `AuthConfig` (+ schema
   tags, `example.config.yaml` docs).
2. `internal/client/rega/scripts/get_user_level.fn` + a `RegaScript`
   enum entry.
3. Central seam: `ValidateCredentials` (transient login) + `UserLevel`
   (privileged ReGa read) reachable per central; a registry-backed adapter
   implementing the `CCUAuthenticator` port.
4. `internal/auth/ccuauth/store.go`: `Store` implementing `auth.UserStore`
   (sanitise → validate → level → map → `Identity`), transient-failure →
   `ErrUnauthenticated`, concurrency bound.
5. Wire into the chain **local-first, CCU-last** in `daemon_rest.go` for
   both `authMw` and `restAuth.LoginUsers`, gated on `auth.ccu.enabled`.
6. Capability `auth.ccu.v1` in `/info`; optional SPA login hint.
7. Audit: record CCU-backed logins distinctly so operators can see which
   accounts came from the CCU.

## Resolved decisions

These were settled during the design review (2026-06-24):

- **Session lifecycle on CCU user removal**: *blocked at next login, runs
  until TTL*. No periodic re-check or revocation — that would defeat the
  "touch the CCU only at login" property. A user deleted on the CCU cannot
  obtain a new Loom session; an in-flight session expires within the 12 h
  TTL. Documented as a deliberate trade-off.
- **Chain order**: *local first, CCU last* (as in §3) — break-glass safety
  wins over immediate CCU-side revocation. CCU-as-Primary stays a rejected
  default (see Alternatives); it may return later as an explicit opt-in but
  is out of scope here.
- **Role mapping**: the *8→admin, 2→operator, 1→viewer* default (the
  `UserLevel` lookup path), overridable via `role_mapping`. The "all users →
  operator" shortcut (which would skip the ReGa lookup entirely) is **not**
  the default but remains a documented config-time choice for deployments
  that prefer it.
- **Multi-CCU scope**: a *single configured `central`* is the user source
  (empty ⇒ first central). Authenticating against several CCUs at once is
  out of scope; revisit only if operators ask for it.
