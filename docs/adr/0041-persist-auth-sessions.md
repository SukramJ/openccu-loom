# ADR 0041 — Persist auth sessions in SQLite as a save-through cache

- **Status**: accepted
- **Date**: 2026-06-19
- **Related**:
  [ADR 0019 — persistent VALUES cache](./0019-persistent-values-cache.md),
  [ADR 0027 — encrypt config secrets at rest](./0027-encrypt-config-secrets-at-rest.md),
  [ADR 0040 — embedded SQLite measurement history](./0040-measurement-history.md),
  [SECURITY.md](../SECURITY.md)

## Context

A browser authenticates to OpenCCU-Loom by logging in once and then
carrying an `openccu_loom_session` cookie (12-hour server-side TTL). The
session record that the cookie points at lived **only in memory**: the
`auth.SessionStore` was a plain `map[string]*Session` behind a mutex.
That made a daemon restart user-hostile — every active browser session
was silently invalidated, so every operator and every SPA tab had to log
in again after an update, a crash, or a supervised restart.

This was already inconsistent with the rest of the auth surface. Users
are persisted (the SQLite `users` table). Dynamically created **API
tokens** are persisted as bcrypt-style hashes (the `tokens` table, fronted
by the chained token store with the SQLite store as the primary). Sessions
were the one credential class that did not survive a restart.

The fix has to respect the project's hard constraints: single static
binary, `CGO_ENABLED=0`, pure-Go SQLite already in tree, multi-CCU
neutrality (sessions are daemon-global, not per-CCU), and the
"degrade, don't crash" posture the boot path takes everywhere else.

## Decision

Make sessions durable in SQLite as a **save-through cache**: the
in-memory map remains the hot read/write path within a running process,
and SQLite is the durability layer that lets the map be rebuilt after a
restart.

Three moving parts:

1. **A `SessionPersistence` port** in `internal/auth` with four methods —
   `SaveSession`, `DeleteSession`, `LoadActiveSessions(now)`,
   `DeleteExpiredSessions(now)`. The interface lives in the consumer
   package (standard Go convention); the SQLite implementation
   (`AuthSessionStore`) lives in `internal/store/sqlite/auth_sessions.go`
   beside the existing token/user stores.

2. **`NewPersistentSessionStore(persist, logger)`** that hydrates the
   in-memory map from `LoadActiveSessions` on boot, then mirrors each
   `Issue` (save) and `Revoke` / expired-`Lookup` eviction (delete) to
   the store **best-effort**. The legacy `NewSessionStore()` constructor
   is unchanged (persist == nil), so the no-DB path (tests, dev-loopback)
   keeps the pure in-memory behaviour.

3. **A periodic purge** — an hourly background ticker calls
   `PurgeExpired`, which evicts expired sessions from memory and runs a
   single indexed `DELETE FROM auth_sessions WHERE expires_unix <= now`.
   It is a backstop for sessions that are never looked up again before
   they expire; lazy eviction on `Lookup` already handles the common case.

The new `auth_sessions` table (migration `021`) stores the flat
`Identity` as columns plus the session id and two Unix-second timestamps,
with an index on `expires_unix` to back both the boot-time active load
and the purge sweep. The table lives in the existing config/session DB —
unlike the append-heavy measurement history (ADR 0040), session writes
are low-volume and event-driven (one row per login / logout), so they do
not warrant a separate WAL handle.

## Consequences

### Positive

- A daemon restart — update, crash, supervised restart — no longer logs
  active browsers out. Operators and SPA tabs keep their session.
- The auth surface is now uniform: users, tokens, and sessions all
  survive a restart.
- No new dependency, no CGo, no copyleft — pure-Go SQLite already in tree.
- The no-DB path is untouched: tests and dev-loopback keep the fast
  in-memory store with no behavioural change.

### Negative

- **Persistence at `Issue` is best-effort, not transactional.** If the DB
  write fails at login time, the login still succeeds (the in-memory copy
  works this run) but that one session is not durable across a restart.
  This is the deliberate trade-off: a transient disk hiccup must never
  block a login. The failure is logged, and the next login re-attempts.
- One more table in the config DB and one more background goroutine. The
  blast radius is small (low write volume, single indexed delete), but it
  is more moving parts than "memory only".
- Session ids are stored verbatim (they are random 256-bit bearer
  secrets, like the cookie value). Theft of the data directory exposes
  them for their remaining TTL — the same exposure the in-memory store
  had against a heap dump, now also against the on-disk file. Session
  hijacking via stolen cookie was already in the threat model
  (`HttpOnly` + `SameSite=Lax` + `Secure`-behind-TLS); this does not
  widen it materially, and the 12-hour TTL plus the purge sweep bound it.
