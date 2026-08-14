# Implementation plan — canonicalising the OIDC login subject

**Status:** executed in 0.59.1. `preferred_username` is folded, `sub` passes
through byte-for-byte, and the open decision inside the plan was settled by
scoping the identity: a federated principal now carries `auth.SchemeOIDC` and
is no longer reachable by the subject-keyed controls over local accounts.
What deliberately did **not** change: sessions issued before the upgrade keep
their old spelling and their local treatment until they expire, API-token
purges still key on the subject string alone, and the larger `iss` + `sub`
re-keying named under [Risks](#risks) was considered and not taken. See
[What shipped](#what-shipped).
**Audience:** a fresh agent with no access to the review conversation.
Everything needed is inline.

## Why this exists

0.59.1 fixed a real security defect for local accounts: `AuthenticateBasic`
returned the caller's own spelling as the session subject while user rows are
keyed lower-case, so `RevokeBySubject` missed the session. A password reset
issued after a credential leak answered 204 and left the stolen cookie working
until it expired on its own.

The fix landed at the source — both user stores now return the canonical
subject they matched on. **The OIDC path was left untouched**, and the reason
given at the time was that an OIDC `sub` is an opaque identifier that may be
case-sensitive, so folding it could merge two distinct principals.

That reason is correct about `sub` and beside the point about what the code
actually uses. `Client.IdentityFrom` (`internal/auth/oidc/client.go`) read:

```go
subject := claims.PreferredUser
if subject == "" {
    subject = claims.Subject
}
```

So the session subject is normally **`preferred_username`**, and only falls
back to `sub` when the provider omits it. Those two claims have opposite
properties:

- `sub` — per OpenID Connect Core §2, opaque, case-sensitive, stable per
  issuer. Folding it is genuinely unsafe: two distinct principals may differ
  only in case.
- `preferred_username` — per the same spec, explicitly **not** guaranteed
  stable or unique, and in practice an email-like handle. Azure AD, Keycloak
  and Okta all emit whatever casing the user typed or the directory stores.
  The same human logging in twice can produce `Markus@example.com` and
  `markus@example.com`.

## The defect

Two sessions for the same person can carry two different `Identity.Subject`
values. Every subject-keyed control then covers only one of them:

- `SessionStore.revokeBySubject` — an admin revoking a compromised OIDC login
  evicts one session and silently leaves the other.
- `TokenStore.DeleteBySubject` — same gap for API tokens minted for that
  subject.
- The audit trail attributes one person's actions to two identities.
- Per-user state keyed on the subject (favourites, start page) splits in two.

This is the same defect class 0.59.1 closed for local accounts, on a path that
was not part of that change.

## What was planned

**Fold `preferred_username`, never fold `sub`.** Concretely, in
`Client.IdentityFrom`:

1. When the subject comes from `claims.PreferredUser`, canonicalise it with
   the same helper the local stores use (`auth.CanonicalSubject`, added in
   0.59.1) so there is exactly one spelling of the rule in the codebase.
2. When it falls back to `claims.Subject`, pass it through **unchanged**, and
   say why in a comment at that line so the asymmetry is not "cleaned up"
   later.
3. Decide what happens when an installation has both a local account `markus`
   and an OIDC principal whose `preferred_username` folds to `markus`. Today
   they collide in the session store and in `RevokeBySubject`. Either scope the
   identity by scheme (the `Identity` already carries `Scheme`, so revocation
   and lookup can compare the pair) or document that the collision is intended
   and the two are the same person. **Do not leave this undecided** — it is the
   one part of this change that alters behaviour beyond casing.

## What shipped

Steps 1 and 2 verbatim: `IdentityFrom` folds `claims.PreferredUser` through
`auth.CanonicalSubject`, and the `claims.Subject` fallback is passed through
byte-for-byte with the reason for the asymmetry at that line
(`TestIdentityFromFoldsPreferredUsername`,
`TestIdentityFromKeepsSubjectClaimVerbatim`, plus
`TestOIDCLoginReportsCanonicalSubject` in `tests/contract/`).

Step 3 was decided in favour of **scoping by scheme**, because folding alone
would have made a federated login and a local account of the same name one
principal — a worse defect than the one being fixed, since the external
provider owns those credentials and the daemon owns none of them. Concretely:

- `auth.SchemeOIDC` exists (a value the REST `Identity` schema had always
  declared while the daemon emitted `session`), and `Scheme.Federated()` is the
  single predicate everything else asks.
- `SessionMiddleware` no longer overwrites the stored scheme for a federated
  session, so the distinction survives the cookie round trip.
- `SessionStore.revokeBySubject` skips federated sessions, so a local password
  reset, role change or account deletion no longer evicts them.
- `PATCH /auth/me/password` refuses a federated caller, who would otherwise
  rewrite the local account's password.
- CSRF protection follows the cookie, not the scheme, and covers the new value
  unchanged (`internal/auth/csrf_test.go`).

## What did not, and why

- **Sessions issued before the upgrade.** They carry the unfolded spelling and
  the `session` scheme, so they are still treated as local and are still
  reachable only by their original spelling. They were not invalidated on
  upgrade: the gap closes for every session minted afterwards and the old ones
  expire within the session TTL. The CHANGELOG states this.
- **`TokenStore.DeleteBySubject` still keys on the subject alone.** The token
  stores fold the subject on `Put` and on delete, so the casing half of the
  defect is closed for tokens too — but a token carries no scheme, so an API
  token minted for a federated subject is still purged when a local account of
  the same name is deleted. Tokens are administrator-minted and administrator-
  scoped, so the blast radius is an admin removing an account they created the
  token under; closing it properly means giving a token an issuer-scoped
  subject, which is the same re-keying as the item below.
- **Re-keying the identity on `iss` + `sub`.** Named in the plan as the
  architecturally cleaner answer and not taken. It changes the subject of every
  existing federated session and every audit row that references one, and it
  makes the subject unreadable in the UI — `preferred_username` would become a
  display name carried separately. Worth revisiting only together with a
  migration for the audit trail; the scheme-scoped fix covers the reported
  defect without it.

## Risks

- Existing OIDC sessions carry the unfolded spelling. After the change,
  `RevokeBySubject` with the folded spelling will not match them either — the
  gap closes for new sessions and persists for sessions issued before the
  upgrade. Either accept that (they expire within the session TTL) or
  invalidate OIDC sessions once on upgrade. State the choice in the CHANGELOG.
- An IdP that genuinely distinguishes two users by `preferred_username` casing
  alone would merge them. This is possible but pathological — the spec already
  warns that the claim is neither stable nor unique, which is why nothing
  should be keyed on it without the issuer. If the installation base makes this
  a real concern, the alternative is to key the identity on `iss` + `sub` and
  treat `preferred_username` as a display name only. That is the
  architecturally cleaner answer and a larger change; it is worth considering
  before doing the narrow fix.

## Tests

Written before the scheme decision, which **inverted the second bullet**: a
federated session is deliberately out of `RevokeBySubject`'s reach, so the
shipped assertion is the fourth bullet instead. The others hold as written.

- `IdentityFrom` with `preferred_username: "Markus@Example.COM"` returns the
  folded subject; with only `sub: "AbC123"` returns `AbC123` byte-for-byte.
- ~~A session issued through the OIDC callback with mixed-case
  `preferred_username` is evicted by `RevokeBySubject` using the folded
  spelling.~~ Inverted: it is not evicted at all, because revoking by subject
  is a control over local accounts.
- The same for `TokenStore.DeleteBySubject` — which still folds on both ends
  and still has no scheme to scope by.
- If scheme-scoping is chosen: a local account and an OIDC principal that fold
  to the same string do not revoke each other.
