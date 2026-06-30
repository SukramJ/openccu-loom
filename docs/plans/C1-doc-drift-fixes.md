# Implementation plan — C1: Documentation drift fixes

**Summary.** Two stale documentation statements mislead future readers
(and AI agents) about the code's real shape. Fix both. Pure
comment/doc edits — no logic change. Best bundled with B6 (which already
touches `internal/auth/`).

**Status.** Prioritised, not started. Effort: **XS**.

---

## 1. Current state (verified)

### Drift 1 — stale "reserved for future" on `SchemeSession`

`internal/auth/auth.go:25`:
```go
SchemeSession Scheme = "session" // reserved for future session auth
```
The comment is **false**: session-cookie auth is fully wired:
- `internal/auth/oidc/client.go:228` — login returns
  `auth.Identity{… Scheme: auth.SchemeSession …}`.
- `internal/auth/csrf.go:125` — `case SchemeSession:` (CSRF enforced for
  the session scheme).
- `internal/auth/session.go:185` — `id.Scheme = SchemeSession` on
  session validation.

### Drift 2 — `JsonCcuBackend` / CCU-Jack mention in CLAUDE.md

`CLAUDE.md:648-649` (Architecture Quick Reference, `internal/client/backends`):
```
- `internal/client/backends`: `CcuBackend` (XML-RPC + JSON-RPC, with
  `JsonCcuBackend` for CCU-Jack JSON-only mode), `CuxdBackend`
```
CCU-Jack was **dropped**; no `JsonCcuBackend` type exists:
- `internal/client/backends/doc.go:7` — "CCU-Jack ist gestrichen — der
  zugehörige Backend-Stub …".
- `SPECIFICATION.md` §2.2:149-150 — "No CCU-Jack / pull-only path. Every
  interface supports push callbacks; there is no JSON-RPC-only mode."
- `SPECIFICATION.md:471` — "CCU-Jack is dropped (no pull-only path)."

---

## 2. Design decisions

- Pure documentation edits; **no** behaviour change, **no** symbol
  rename.
- The `auth.go` comment is a `.go` line comment → subject to
  `TestDocPurity` (`tests/contract/`): must be **English** and free of
  the banned tracking tokens / German function-words. Keep it short and
  factual.
- `CLAUDE.md` is Markdown → subject to `TestMarkdownLinksValid` only (no
  links change here) and must stay English (it already is).

---

## 3. Implementation steps

1. **`internal/auth/auth.go:25`** — replace the stale comment:
   ```go
   SchemeSession Scheme = "session" // session-cookie auth, set by the session and OIDC login flow
   ```
   (Any equivalent factual English phrasing is fine; do not reintroduce
   "future"/"reserved".)

2. **`CLAUDE.md:648-651`** — drop the `JsonCcuBackend`/CCU-Jack clause:
   ```
   - `internal/client/backends`: `CcuBackend` (XML-RPC + JSON-RPC),
     `CuxdBackend` (BIN-RPC), `HomegearBackend` (XML-RPC; depth-parity
     with CCU is a post-0.1.0 milestone — see `SPECIFICATION.md` §2.2
     Non-Goals).
   ```
   (Optionally add a half-sentence that CCU-Jack is a non-goal, but the
   §2.2 reference already covers it — keeping it terse is preferable.)

3. **Optional adjacent finding (note, don't fix here):**
   `internal/client/backends/doc.go:7` carries a **German** code comment
   ("CCU-Jack ist gestrichen …"). Code comments should be English per
   CLAUDE.md; this predates the rule and is out of C1's scope, but worth
   a follow-up if `TestDocPurity` is ever tightened to flag it.

---

## 4. Config & API contract changes

None. No `cfg:` field, no `openapi.yaml`/`wsapi.json` change, no
`APIVersion` bump.

---

## 5. Tests

- No new test. `make test` must stay green — specifically `TestDocPurity`
  (the edited `auth.go` comment is English and token-free) and
  `TestMarkdownLinksValid` (no Markdown links touched). Rationale: this
  is a doc-accuracy fix, not a behavioural change, so there is no
  observable behaviour to assert; the existing purity/link guards are the
  safety net.

---

## 6. Project-rule checklist

- [ ] Edited comment is English, no banned DocPurity tokens.
- [ ] No logic / signature change; no new file (so no SPDX concern).
- [ ] `make test` green (`TestDocPurity`, `TestMarkdownLinksValid`).

---

## 7. Acceptance criteria

- `grep -n "reserved for future" internal/auth/auth.go` → no match.
- `grep -n "JsonCcuBackend" CLAUDE.md` → no match.
- `make test` green.

---

## 8. References

- CLAUDE.md → *Code Quality & Standards → Comments in code*
  (`TestDocPurity`), *Documents in markdown* (`TestMarkdownLinksValid`).
- Roadmap entry: *Housekeeping → Doc-drift fixes (bundle with the
  user/token view)*.
- Bundle with: B6 (user/token management view) — it also corrects the
  stale "planned future feature" comment at
  `internal/north/rest/handlers/auth.go:56` and touches `internal/auth/`.
