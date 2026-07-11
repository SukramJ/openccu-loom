# ADR 0051 — North-bound authorization model and backup-at-rest sealing

- **Status**: Accepted
- **Date**: 2026-07-11
- **Related**:
  [ADR 0027 — encrypt config secrets at rest](./0027-encrypt-config-secrets-at-rest.md),
  [ADR 0041 — persist auth sessions in SQLite](./0041-persist-auth-sessions.md),
  [ADR 0043 — CCU as an authentication provider](./0043-ccu-authentication-provider.md),
  [ADR 0044 — single-port onboarding and HA Ingress auth](./0044-single-port-onboarding-and-ha-ingress-auth.md)

## Context

The north-bound surface (REST + WebSocket + SPA) authenticates callers through
several schemes — Basic, API tokens, browser sessions, OIDC, and CCU-delegated
login (ADR 0043) — all of which resolve to a single `auth.Identity`
(`internal/auth`). *Authentication* (who is the caller) was well-defined;
*authorization* (what may that caller do) was not uniformly enforced.

A code-vs-code security audit of the REST/WebSocket/auth surface (landed in the
0.25.0 security work) found the concrete gaps this ADR records the decisions
for:

- **WebSocket writes were not role-gated.** The command dispatch dropped the
  caller identity, so a read-only *viewer* could invoke operator/admin
  state-changing commands (`paramset.put`, `device.install_mode`,
  `backup.trigger`, …) over the socket — while the equivalent REST routes
  correctly required a higher role. The dropped identity also collapsed the
  per-command rate-limiter and `system.user_permissions` to "anonymous".
- **A credential change did not take effect immediately.** A password reset,
  role change, or user deletion left the subject's existing sessions valid for
  the full session TTL, and (on deletion) left its API tokens usable.
- **Backups leaked live secrets at rest.** The `backup create` archive wrote
  the unencrypted SQLite DB world-readable (`0644`), exposing live session
  tokens, Matter PSKs, and CCU passwords in a copied tarball. ADR 0027 sealed
  *config secrets* inside the DB, but the DB itself, once dumped into a backup,
  was plaintext on disk.

## Decision

**Adopt one role-based authorization model applied uniformly across every
north-bound write surface, make credential changes revoke sessions
immediately, and seal the whole backup archive at rest with AES-256-GCM.**

### 1. Three-role model with monotonic containment

`auth.Role` has exactly three values (`internal/auth/auth.go`):

- `RoleViewer` — read-only.
- `RoleOperator` — every real device / config / schedule / link mutation.
- `RoleAdmin` — daemon-administrative actions (backups, cache invalidation,
  user management).

Roles are strictly nested: `Identity.HasRole(want)` grants Admin everything,
Operator covers Operator+Viewer, and Viewer covers only Viewer. There is no
per-endpoint permission matrix — the three levels are sufficient and keep the
privilege boundary auditable.

### 2. `MinRole` gating unified across REST and WebSocket

- **REST** wraps write routes in `.With(op)` / `.With(admin)`; reads are open
  to any authenticated identity.
- **WebSocket** enforces the *same* minimum role for the *same* action via a
  single `writeCommandRoles` table (`internal/north/rest/ws/commands.go`): any
  command absent from the table is read-only (viewer); listed commands demand
  Operator or Admin. Keeping the policy in one table — rather than tagging ~90
  registration sites — makes the boundary auditable in one place, and
  `TestWriteCommandRolesAreRegistered` pins every entry to a real command so a
  typo cannot silently open a write. The caller identity is threaded through
  the dispatch context so the rate-limiter and `system.user_permissions` see
  the real subject, not "anonymous".

The invariant: **a WebSocket command and its REST equivalent require the same
minimum role.** Neither surface is a privilege-escalation bypass of the other.

### 3. Credential changes revoke sessions immediately

A password change, role change, or user deletion revokes that subject's other
server-side sessions at once (`SessionRevoker.RevokeBySubject` /
`RevokeBySubjectExcept`, satisfied by the persistent session store of ADR 0041)
instead of letting a stale or stolen session live out the 12 h TTL. User
deletion additionally purges the subject's API tokens (`TokenPurger`). The
acting session may be kept (`…Except`) so an admin changing their own password
is not logged out.

### 4. Whole-archive backup sealing at rest

The `backup create` archive is sealed with **AES-256-GCM** using the data-dir
master key, in a **versioned container**, and written `0600`:

- Sealing is streamed frame-by-frame (`internal/secret` `NewEncryptWriter` /
  `NewDecryptReader`) so memory stays bounded for multi-hundred-MB archives;
  each frame carries its own GCM nonce and the frame counter + final-flag are
  authenticated, so a dropped, reordered, or truncated frame fails
  authentication rather than yielding a partial archive.
- The **at-rest master key is excluded from the backup**, so a stolen tarball
  carries ciphertext but not the key. Restoring onto a fresh host therefore
  requires `OPENCCU_LOOM_SECRET_KEY` (or copying `secret.key` out of band).
- **Legacy plaintext archives are auto-detected and still restorable**; if no
  master key is available at create time the tool warns loudly rather than
  silently writing plaintext.
- `backup restore` is hardened alongside: a Zip-Slip guard, decompression-bomb
  bounds, a schema-compat check (a newer-daemon backup is refused without
  `--force`), and all-or-nothing atomic staging.

This extends ADR 0027 (which sealed config-secret leaves *inside* the DB) to
the **whole exported archive**: the two together mean neither the live DB nor a
backup of it exposes secrets on disk.

## Alternatives considered

- **A fine-grained per-permission ACL model.** Rejected: three nested roles
  cover every north-bound action cleanly; a permission matrix adds
  configuration surface and audit cost without a real use case.
- **Tag each WS command site with its role inline.** Rejected: ~90 scattered
  tags are unauditable and easy to forget; one central `writeCommandRoles`
  table with a completeness test is the structural lock.
- **Rely on the session TTL to expire compromised credentials.** Rejected: a
  12 h window after a known password reset or user deletion is an
  unacceptable exposure; immediate revocation is required.
- **Encrypt only the sensitive DB tables in a backup, not the whole archive.**
  Rejected: the archive also carries filesystem state (Matter PSKs, secret
  material) and partial encryption is error-prone; sealing the whole container
  is simpler and leaves no plaintext gap.

## Consequences

- One authorization model governs REST and WebSocket writes; a viewer can no
  longer escalate via the socket, and the two surfaces cannot drift apart
  (guarded by the command-role completeness + viewer-rejection tests).
- Credential changes are effective immediately; a stolen or stale session dies
  on password/role change or deletion instead of lingering for the TTL.
- Backup archives are ciphertext-at-rest (`0600`), keyed by the data-dir master
  key which is *not* in the archive — a stolen backup no longer yields live
  tokens, PSKs, or CCU passwords. *Operator cost:* restoring an encrypted
  archive onto a fresh host requires the original `OPENCCU_LOOM_SECRET_KEY`.
- Legacy plaintext archives remain restorable, so the change is
  backwards-compatible for existing backups.
