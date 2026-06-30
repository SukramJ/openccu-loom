# Implementation plan — B6: User & token management view (SPA-only)

## 1. Summary & status

**Status**: prioritised, not started. **Frontend-only.**

Add a SPA admin view that lets an operator manage Basic-auth users and
API tokens from the browser instead of editing `config.yaml`. The
**entire backend already exists and is wired** — SQLite-backed CRUD
handlers, REST routes, OpenAPI documentation, the typed API-client
methods, and the shared `DataTable` component. The only missing piece is
the Svelte route(s) plus navigation, i18n strings, and one stale-comment
fix. No backend code, no OpenAPI change, no `APIVersion` bump.

> Verified 2026-06-30 against the current tree. Every claim below was
> checked in code; paths and line numbers are accurate at that commit and
> should be re-confirmed (they may have shifted) before editing.

## 2. Current state (verified)

### Backend — complete

V2 SQLite-backed CRUD, all `admin`-gated, mounted in
`internal/north/rest/router.go` (~lines 860–869):

```
if d.UserAdmin != nil {
    GET    /users                       handlers.ListUsersV2(d.UserAdmin)
    POST   /users                       handlers.CreateUser(d.UserAdmin, d.AuditRecorder)
    PATCH  /users/{subject}             handlers.UpdateUser(d.UserAdmin, d.AuditRecorder)
    DELETE /users/{subject}             handlers.DeleteUser(d.UserAdmin, d.AuditRecorder)
}
if d.TokenAdmin != nil {
    GET    /auth/tokens/v2              handlers.ListTokensV2(d.TokenAdmin)
    POST   /auth/tokens/v2              handlers.CreateTokenAdmin(d.TokenAdmin, d.AuditRecorder)
    DELETE /auth/tokens/v2/{fingerprint} handlers.DeleteTokenAdmin(d.TokenAdmin, d.AuditRecorder)
}
```

- Handlers: `internal/north/rest/handlers/admin_users.go`
  (`UserAdminService` interface with `Put(ctx, subject, password, role)`
  upsert + `Delete(ctx, subject)`; `CreateUser`, `UpdateUser`,
  `DeleteUser`, `ListUsersV2`, `validRole`) and
  `internal/north/rest/handlers/admin_tokens.go` (`TokenAdminService`
  with `Delete`; `CreateTokenAdmin`, `DeleteTokenAdmin`, `ListTokensV2`).
- Self-service password change: `internal/north/rest/handlers/auth_me_password.go`
  (`SelfPasswordService`) — a separate, already-shipped surface.
- **OpenAPI already documents** `/users`, `/users/{subject}`,
  `/auth/tokens/v2`, `/auth/tokens/v2/{fingerprint}` (`assets/openapi.yaml`
  ~lines 3891–4060). No spec change required.
- All routes are conditional on the `UserAdmin` / `TokenAdmin` DI deps
  being non-nil. **Confirm these are wired in `cmd/openccu-loom/`**
  (search for `UserAdmin:` / `TokenAdmin:` in the REST-deps assembly). If
  a deployment leaves them nil, the routes are absent and the view must
  degrade to the legacy read-only `/auth/users` path — see §3.

### API client — complete

`assets/ui/src/lib/api/client.ts` already exposes (verified ~lines
1137–1171):

- `listUsersV2()` → `UserSummaryV2[]`
- `createUser({ username, password, role })`
- `updateUser(subject, { password?, role? })`
- `deleteUser(subject)`
- `listTokensV2()` → `TokenSummaryV2[]`
- `createTokenV2({ subject, role })`
- `deleteTokenV2(fingerprint)`

Types `UserSummaryV2`, `TokenSummaryV2` are defined in the same file
(~lines 1319–1330). Legacy read-only `listUsers()` / `listTokens()` also
exist (~lines 595–599). CSRF is handled centrally in `request()`
(`X-CSRF-Token`), so no per-call CSRF work is needed.

### Shared UI — complete

- `assets/ui/src/lib/components/ui/DataTable.svelte` (+ `data-table.ts`,
  `DataTable.test.ts`) — sortable/searchable table used across all list
  views.
- Operating-concept primitives all present in
  `assets/ui/src/lib/components/ui/`: `Button`, `Input`, `Select`,
  `Card`, `Badge`, `LoadingState`, `EmptyState`, `ErrorState`,
  `ConfirmDialog`, `Toaster` + `toastStore` + `confirmStore`.

### What is missing

- No `Users` / `Tokens` route under `assets/ui/src/routes/` (confirmed —
  the only auth-adjacent routes are `Login.svelte`, `Setup.svelte`).
- No navigation entry for it.
- The stale comment at `internal/north/rest/handlers/auth.go` (~line 56,
  on `ListUsers`): *"Adding/removing users in this release happens via
  config.yaml; live edits are a planned future feature."* — now false
  (the V2 CRUD is the live-edit path).

## 3. Design decisions

- **One route, two sections.** A single `Settings → Access` /
  `routes/AccessControl.svelte` route with two `DataTable` cards (Users,
  API tokens) keeps navigation lean. (Alternative: two routes. Pick one;
  the single-route form is recommended — fewer nav entries, both are
  admin-only.)
- **Drive the V2 endpoints**, not the legacy read-only ones. Use
  `listUsersV2` / `createUser` / `updateUser` / `deleteUser` and
  `listTokensV2` / `createTokenV2` / `deleteTokenV2`.
- **Admin-gate the route in the SPA** the same way other admin routes
  gate (check the auth store role; see how `Diagnostics`/`AuditLog`
  guard). The backend is already `admin`-gated, so this is UX, not
  security.
- **Token secret is shown once.** `createTokenV2` returns the plaintext
  token once (see `createTokenResponse` in `admin_tokens.go`); render it
  in a copy-once dialog and never re-fetch it (the list only returns
  fingerprints/metadata).
- **Roles** come from `auth.Role` (`validRole` in `admin_users.go`).
  Confirm the exact accepted set (e.g. `admin`, `user`) in
  `internal/auth/` and render a `Select`, not a free text field.
- **Graceful degradation.** If `listUsersV2()` returns 404 (deps nil),
  fall back to the read-only `listUsers()` view and disable mutation
  controls, surfacing an `EmptyState`/note that live editing needs the
  user store enabled. (Optional but cheap; matches the existing
  nil-provider patterns.)
- **Self-password** is out of scope for this view (it is a per-account
  action, not admin). If desired, wire `auth_me_password.go` into the
  existing `Settings`/account area as a separate small task.

## 4. Implementation steps

1. **Create the route** `assets/ui/src/routes/AccessControl.svelte`:
   - On mount, `Promise.all([api.listUsersV2(), api.listTokensV2()])`
     wrapped in `LoadingState` / `ErrorState` (retry) / `EmptyState`.
   - **Users card**: `DataTable` columns username + role + actions.
     - "Add user" → dialog with `Input` (username), `Input type=password`
       (password), `Select` (role) → `api.createUser(...)` →
       `toastStore.success` and reload; errors → `toastStore.error`.
     - Row edit → dialog calling `api.updateUser(subject, {...})`.
     - Row delete → `confirmStore.ask({ destructive: true, ... })` →
       `api.deleteUser(subject)`.
   - **Tokens card**: `DataTable` columns subject/role/fingerprint/
     created + actions.
     - "Create token" → dialog (`Select` subject? or free `Input`,
       `Select` role) → `api.createTokenV2(...)`; on success show the
       returned plaintext token in a copy-once dialog.
     - Row delete → confirm → `api.deleteTokenV2(fingerprint)`.
2. **Register the route** in `assets/ui/src/App.svelte` (hash-router;
   follow the lazy-loaded pattern used by `Diagnostics`/`Logs` if you
   want code-splitting).
3. **Add the nav entry** in
   `assets/ui/src/lib/components/ui/Sidebar.svelte`, admin-gated like the
   other admin links.
4. **Add i18n keys** (EN + DE) in `assets/ui/src/lib/i18n.ts` for every
   visible string (titles, column headers, button labels, dialog copy,
   toast messages, confirm text). No machine-humanised fallbacks.
5. **Fix the stale comment** in
   `internal/north/rest/handlers/auth.go` (~line 56): replace the
   "planned future feature" wording with a note that the live-edit path
   is the `UserAdmin`-backed `/users` CRUD and that `ListUsers` here is
   the legacy read-only fallback. (Bundle the `auth.go:25`
   `// reserved for future session auth` correction from roadmap item C1
   in the same edit — session auth is wired via `oidc/client.go`,
   `csrf.go`, `session.go`.)
6. **Confirm DI wiring** (read-only check): verify `UserAdmin` /
   `TokenAdmin` are populated in `cmd/openccu-loom/` REST deps. If not,
   either wire them or document the degraded path from §3.

## 5. Config / API / i18n changes

- **Config**: none. No new `cfg:`-tagged fields → no `config.field.*` /
  `config.help.*` entries, no `TestConfigFieldsHaveLabelsAndHelp` impact.
- **API contract**: none. Endpoints + DTOs already in `assets/openapi.yaml`
  and `wsapi.json` is untouched → **no `make export-schemas`, no
  `APIVersion` bump**. (If you discover a response field the UI needs
  that is not yet documented, that *would* trigger the API-contract
  checklist — but the V2 user/token shapes are already complete.)
- **i18n**: add view strings in both `EN` and `DE` catalogues of
  `assets/ui/src/lib/i18n.ts`.

## 6. Tests

- **Playwright e2e + visual regression** (`assets/ui/tests/e2e/`):
  add a spec that mocks `/api/v1/users` and `/api/v1/auth/tokens/v2`
  (GET/POST/PATCH/DELETE) in `tests/e2e/helpers/mock-api.ts`, then
  asserts: list renders, create flow shows a success toast, delete opens
  the confirm dialog, and the token-create dialog shows the one-time
  secret. Capture **light and dark** screenshot baselines for the new
  route (`*-chromium-linux.png` committed for CI; macOS `-darwin` may
  coexist locally). Refresh with `npm run e2e:update`.
- **Vitest component test** (`AccessControl.test.ts` or similar): cover
  the role-select options, the disabled/degraded state when the list
  endpoint 404s, and the copy-once token dialog logic.
- Name test files after the unit (`AccessControl.test.ts`), never
  `*_coverageN`.

## 7. Project-rule checklist

- SPA operating concept: `LoadingState` / `EmptyState` / `ErrorState`
  (never bare `<p>`); `toastStore.success/error` for every action result
  (no silent aborts); `confirmStore.ask({ destructive: true })` for
  deletes; `Button` / `Input` / `Select` / `Card` / `Badge` over raw
  elements; every colour utility carries a `dark:` variant or uses
  `--ha-*` tokens.
- All strings localized via `t(...)` in EN + DE.
- The one Go file touched (`auth.go`) keeps its SPDX header; the comment
  fix must satisfy `TestDocPurity` (no audit tags / wave numbers / dates).
- Admin gating in the SPA mirrors existing admin routes.

## 8. Acceptance criteria

- An admin can list, create, edit (role/password), and delete users from
  the browser; changes persist (visible after reload) and are audited
  (the handlers already call `d.AuditRecorder`).
- An admin can list, create (with a one-time secret reveal), and delete
  API tokens.
- A newly created token authenticates a subsequent API call; a deleted
  token is rejected.
- Non-admin users do not see the nav entry and are refused by the backend.
- `make test` (Go) and `cd assets/ui && npm run test && npm run e2e` are
  green; the stale `auth.go` comment is gone.

## 9. Effort

**M (UI-only).** ~1 route + dialogs + nav + i18n + e2e/vitest. No backend
work beyond the comment fix.

## 10. References

- `CLAUDE.md` → *Architecture Quick Reference → SPA operating concept*;
  *Testing Guidelines → SPA browser-e2e*; *Common Tasks → Add a
  translation key*; *Critical Rules → every config field needs a
  label…* (not triggered here — no new config).
- Roadmap entries: B6 (this) and C1 (the bundled comment fixes) in
  [`docs/roadmap.md`](../roadmap.md).
- Auth model: `docs/admin/auth.md`.
